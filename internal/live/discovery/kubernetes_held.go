// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The post-apply look at what this run's deletes left behind (GitHub issue
// #1184, shape ruled 2026-09-21).
//
// A finalizer turns DELETE into a request: the API server sets
// metadata.deletionTimestamp, answers 200, and the object stays until the
// finalizer clears. A provider delete that does not wait for the object to
// be gone therefore reports "Destruction complete" over an object that is
// still in the cluster, and the run counts it destroyed. Stock prints the
// same two lines, so they stay exactly as they are; what this adds is the
// one thing stock cannot know and the sweep can, because a terminating
// object still carries the estate's label: one list per kind that had
// deletes, by the sweep's own selector, and one warning naming every
// object this run deleted that came back with a deletionTimestamp.
//
// It is a Kubernetes notion and stays one. "The delete was accepted and the
// object is still listed, terminating" is a state the Kubernetes API has a
// field for; an AWS delete has no counterpart the tagging sweep could read,
// and nothing here calls AWS. A run with no Kubernetes delete makes no
// request at all.

// SummaryKubernetesDeleteHeld is the warning's summary. A warning and never
// an error: the apply did what it was asked, the exit code is the apply's,
// and the next plan proposes the same destroy again by itself.
const SummaryKubernetesDeleteHeld = "Delete accepted, object not gone"

// DeletedKubernetesObject is one object this run's plan scheduled a delete
// of, named the way the sweep names a declared object: the type its address
// is filed under, and the import id its resolution carried.
type DeletedKubernetesObject struct {
	Addr     addrs.AbsResourceInstance
	TypeName string
	ImportID string
}

// HeldKubernetesDelete is one of those objects the post-apply list still
// found, carrying a deletionTimestamp.
type HeldKubernetesDelete struct {
	Addr addrs.AbsResourceInstance
	Kind string
	// Group is the API group the object was listed in, empty for the core
	// group: what makes a custom resource's kind resolvable by name.
	Group             string
	Namespace         string
	Name              string
	DeletionTimestamp string
	Finalizers        []string
}

// heldWant is one deleted object as a listing can be asked for it.
type heldWant struct {
	addr addrs.AbsResourceInstance
	kind string
	// apiVersion is set for an object deleted through the manifest type,
	// whose import id names it; a built-in type's kind is served by the
	// group the Kubernetes API registers it under, which [kubesweep.Kind]
	// already says by Manifest being false.
	apiVersion string
	key        string
}

func (w heldWant) listedBy(k kubesweep.Kind) bool {
	if k.Kind != w.kind {
		return false
	}
	if w.apiVersion == "" {
		return !k.Manifest
	}
	want, err := schema.ParseGroupVersion(w.apiVersion)
	if err != nil {
		return false
	}
	return k.GVR.Group == want.Group
}

// HeldKubernetesDeletes lists, once per kind that had deletes, the estate's
// objects of that kind, and returns every deleted object that is still
// there with a deletionTimestamp. typeNames and manifestType are the
// universe the sweep was given ([Request.KubernetesTypes],
// [Request.KubernetesManifestType]); the kind join is the sweep's own
// ([kubesweep.KindOfType] for a built-in type, the manifest import id for
// the manifest type), so no type is named here.
//
// An object the list still finds WITHOUT a deletionTimestamp is not
// reported: nothing has asked it to go, which is an apply that did not
// reach it, and the apply's own diagnostics are what say so.
//
// Nothing deleted, or nothing deleted that the join can name, returns
// before the sweeper is asked anything. An error is a cluster that could
// not answer; the caller decides what that is worth, and held is whatever
// the kinds that did answer showed.
func HeldKubernetesDeletes(ctx context.Context, sweeper kubesweep.Sweeper, typeNames []string, manifestType, estate string, deleted []DeletedKubernetesObject) (held []HeldKubernetesDelete, err error) {
	if sweeper == nil || len(deleted) == 0 {
		return nil, nil
	}
	var wants []heldWant
	for _, d := range deleted {
		if manifestType != "" && d.TypeName == manifestType {
			apiVersion, kind, namespace, name, ok := kubesweep.ParseManifestImportID(d.ImportID)
			if !ok {
				continue
			}
			wants = append(wants, heldWant{addr: d.Addr, kind: kind, apiVersion: apiVersion, key: kubesweep.NaturalKey(namespace, name)})
			continue
		}
		kind, _, ok := kubesweep.KindOfType(d.TypeName)
		if !ok || d.ImportID == "" {
			continue
		}
		wants = append(wants, heldWant{addr: d.Addr, kind: kind, key: d.ImportID})
	}
	if len(wants) == 0 {
		return nil, nil
	}

	kinds, _, kindsErr := sweeper.Kinds(ctx, typeNames, manifestType)
	if kindsErr != nil {
		return nil, fmt.Errorf("API discovery: %w", kindsErr)
	}
	var errs []string
	for _, k := range kinds {
		byKey := map[string]heldWant{}
		for _, w := range wants {
			if w.listedBy(k) {
				byKey[w.key] = w
			}
		}
		if len(byKey) == 0 {
			continue
		}
		objects, _, listErr := sweeper.List(ctx, k, markers.TagEstate, estate)
		if listErr != nil {
			errs = append(errs, fmt.Sprintf("listing %s: %s", k.GVR.String(), listErr))
			continue
		}
		for _, o := range objects {
			w, deletedHere := byKey[kubesweep.NaturalKey(o.Namespace, o.Name)]
			if !deletedHere || o.DeletionTimestamp == "" {
				continue
			}
			held = append(held, HeldKubernetesDelete{
				Addr:              w.addr,
				Kind:              k.Kind,
				Group:             k.GVR.Group,
				Namespace:         o.Namespace,
				Name:              o.Name,
				DeletionTimestamp: o.DeletionTimestamp,
				Finalizers:        append([]string(nil), o.Finalizers...),
			})
		}
	}
	sort.SliceStable(held, func(i, j int) bool {
		if held[i].Kind != held[j].Kind {
			return held[i].Kind < held[j].Kind
		}
		return kubesweep.NaturalKey(held[i].Namespace, held[i].Name) < kubesweep.NaturalKey(held[j].Namespace, held[j].Name)
	})
	if len(errs) > 0 {
		err = fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return held, err
}

// HeldKubernetesDeletesDiag is the one warning for a run, naming every held
// object. Nil for none.
//
// The wording is the maintainer's (2026-09-21): what was found, by name,
// and the one command. The closing is chosen by whether ANY named object
// has finalizers: if one does, it speaks of finalizers and gives the
// command for the first named object that has some, since that is the
// object the command has something to show for; if none does, every object
// is one the server is still finishing, and there is no command to give.
//
// The command line is indented so the diagnostic renderer leaves it whole:
// it word-wraps any detail line that does not begin with a space, and a
// wrapped command does not paste.
func HeldKubernetesDeletesDiag(held []HeldKubernetesDelete) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if len(held) == 0 {
		return diags
	}
	one := len(held) == 1
	var b strings.Builder
	if one {
		b.WriteString("The API server accepted the delete of 1 object and it is still in the cluster, terminating:\n\n")
	} else {
		fmt.Fprintf(&b, "The API server accepted the delete of %d objects and they are still in the cluster, terminating:\n\n", len(held))
	}
	var show *HeldKubernetesDelete
	for i, h := range held {
		fmt.Fprintf(&b, "  - %s %s (%s), ", h.Kind, kubesweep.NaturalKey(h.Namespace, h.Name), h.Addr)
		if len(h.Finalizers) == 0 {
			b.WriteString("no finalizers: the server has not finished the delete yet\n")
			continue
		}
		fmt.Fprintf(&b, "finalizers: %s\n", strings.Join(h.Finalizers, ", "))
		if show == nil {
			show = &held[i]
		}
	}
	b.WriteString("\n")
	switch {
	case show == nil && one:
		b.WriteString("It stays until the server finishes the delete, and the next plan will propose destroying it again.")
	case show == nil:
		b.WriteString("They stay until the server finishes the delete, and the next plan will propose destroying them again.")
	case one:
		b.WriteString("It stays until those finalizers are removed, and the next plan will propose destroying it again. To see what holds it:\n")
	default:
		b.WriteString("They stay until their finalizers are removed, and the next plan will propose destroying them again. To see what holds one:\n")
	}
	if show != nil {
		b.WriteString("  " + heldFinalizersCommand(*show))
	}
	return diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryKubernetesDeleteHeld, b.String()))
}

// heldFinalizersCommand is the kubectl read of one held object's
// finalizers, built from the listed object's own fields: the kind lowered,
// qualified by its API group when it has one so that a custom resource
// resolves, and -n only for a namespaced object.
func heldFinalizersCommand(h HeldKubernetesDelete) string {
	resource := strings.ToLower(h.Kind)
	if h.Group != "" {
		resource += "." + h.Group
	}
	cmd := "kubectl get " + resource + " " + h.Name
	if h.Namespace != "" {
		cmd += " -n " + h.Namespace
	}
	return cmd + " -o jsonpath='{.metadata.finalizers}'"
}
