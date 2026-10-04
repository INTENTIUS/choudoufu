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

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The field-manager leg of the Kubernetes sweep (GitHub issue #1863, the
// first of PR #1828's three follow-ups to #1191).
//
// A field-granular block - kubernetes_labels, kubernetes_annotations,
// kubernetes_env, kubernetes_config_map_v1_data, kubernetes_secret_v1_data,
// kubernetes_node_taint ([substrate.FieldGranularShape]) - writes fields of
// an object it does not own, and its ownership marker is the field manager
// the write is made under, "choudoufu:<estate>". Nothing on the object
// names the estate, so the label-selected list [KubernetesSweep] makes
// never sees these fields, and a block deleted from configuration left its
// fields on the object where stock removes them: stock's Delete of each
// type is a server-side apply of an empty map (or env list, or taint list)
// under the block's field manager, which releases every field the manager
// owned and deletes each one no other manager also owns.
//
// So this leg lists objects without a selector and reads which fields the
// estate's manager holds on each ([kubesweep.FieldManagedLister]). That is
// one unselected list per kind, so it runs only for an estate that has
// field-granular instances - in its configuration, or in its record store,
// which write-back keeps for every applied instance and so still knows one
// whose block was dropped - and only over the kinds those types patch:
// ConfigMap, Secret and Node, the workload kinds kubernetes_env writes
// into, and the kinds the configuration's and the records' labels and
// annotations blocks name. An estate with none lists nothing. An object whose fields the manager
// owns and that no field-granular block of the configuration names is an
// orphan, filed the way the label leg files one, at a synthetic address,
// so [classifyOrphans] and [applyOrphanPolicy] decide its removal with no
// rule of their own. The projection then reads it under the estate's
// manager ([projection]'s fieldGranularSeed) and the plan engine proposes
// a destroy, which the provider carries out exactly as stock's would.
//
// The join is on the OBJECT, never on the type: two field-granular blocks
// of one estate on one object are refused ([SameObjectFieldWrites]), so a
// configured block naming the object accounts for every field the manager
// holds there - a block changed from kubernetes_labels to
// kubernetes_annotations on the same object is not a removal of the
// labels block, because the annotations block's next apply under the same
// manager is what drops the labels, and a destroy planned beside it would
// apply under that manager too and erase the annotations it just wrote.
//
// What decides the TYPE an orphan is filed under is what the manager owns,
// read against each field-granular type's own schema: the maps it writes,
// the env it writes, the taints it writes, and for the three that name no
// kind, the one kind they patch. An object on which that answer is not
// exactly one type - the manager owns fields no field-granular type
// writes, or the fields of two - is reported and never proposed: a destroy
// is a write under the estate's manager, and one planned for the wrong
// type would release the wrong fields.

// SummaryFieldGranularOrphanUnclassified is the warning for an object this
// estate's field manager owns fields of, that no block names, and whose
// fields no single field-granular type explains. Nothing is proposed.
const SummaryFieldGranularOrphanUnclassified = "Fields owned by this estate's field manager not proposed for removal"

// SummaryFieldGranularOrphansPending is the warning for field-granular
// orphans held back because a field-granular block's object cannot be
// named yet, so any of them may still be that block's.
const SummaryFieldGranularOrphansPending = "Field-granular removals deferred"

// SummaryFieldHandoverFailed is the warning internal/command raises when
// a planned stock hand-over's ownership move fails before the apply
// (GitHub issue #1863's second follow-up); the apply still runs and the
// estate's write then shares the fields with "Terraform".
const SummaryFieldHandoverFailed = "Field hand-over from Terraform failed"

// FieldGranularType is one provider resource type of the field-granular
// shape, read off its schema: which object it patches and which fields it
// writes there.
type FieldGranularType struct {
	TypeName string
	// NamesKind is [substrate.FieldGranularNamesKind]: the configuration
	// names the patched object's apiVersion and kind. False for the types
	// that patch one fixed kind, FixedAPIVersion and FixedKind.
	NamesKind       bool
	FixedAPIVersion string
	FixedKind       string
	// Maps are the written map attributes the schema has, in
	// [kubesweep.FieldGranularMapAttrs] order.
	Maps []string
	// Env and Taint report the written nested blocks.
	Env, Taint bool
}

// FieldGranularTypeOf reads typeName's schema as a [FieldGranularType], or
// ok false when the schema is not the shape or names nothing it writes.
func FieldGranularTypeOf(typeName string, block *configschema.Block) (FieldGranularType, bool) {
	if _, ok := substrate.FieldGranularShape(block); !ok {
		return FieldGranularType{}, false
	}
	t := FieldGranularType{TypeName: typeName, NamesKind: substrate.FieldGranularNamesKind(block)}
	if !t.NamesKind {
		t.FixedAPIVersion, t.FixedKind = kubesweep.FieldGranularFixedKind(typeName)
		if t.FixedKind == "" {
			return FieldGranularType{}, false
		}
	}
	for _, name := range kubesweep.FieldGranularMapAttrs {
		if a, ok := block.Attributes[name]; ok && a != nil && a.Type.IsMapType() {
			t.Maps = append(t.Maps, name)
		}
	}
	_, t.Env = block.BlockTypes["env"]
	_, t.Taint = block.BlockTypes["taint"]
	if len(t.Maps) == 0 && !t.Env && !t.Taint {
		return FieldGranularType{}, false
	}
	return t, true
}

// fieldGranularObjectKey is the join between a listed object and a
// configured block: group, kind, namespace and name. The version is left
// out, because a block naming apps/v1 and a listing at the group's
// preferred version name one object.
func fieldGranularObjectKey(apiVersion, kind, namespace, name string) string {
	group := ""
	if i := strings.LastIndex(apiVersion, "/"); i >= 0 {
		group = apiVersion[:i]
	}
	return group + "|" + kind + "|" + namespace + "|" + name
}

// FieldGranularImportID renders the import id t's identity renders for
// the object (substrate's fieldGranularIdentity): the
// [substrate.FieldGranularImportSyntax] for a type that names its kind,
// NAMESPACE/NAME or NAME for one that patches a fixed kind.
func FieldGranularImportID(t FieldGranularType, apiVersion, kind, namespace, name string) string {
	if !t.NamesKind {
		return kubesweep.NaturalKey(namespace, name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion=%s,kind=%s,", apiVersion, kind)
	if namespace != "" {
		fmt.Fprintf(&b, "namespace=%s,", namespace)
	}
	fmt.Fprintf(&b, "name=%s", name)
	return b.String()
}

// parseFieldGranularImportID reads a concrete resolution's import id back
// as the object it names. ok is false for an id the type's syntax does
// not parse.
func parseFieldGranularImportID(t FieldGranularType, id string) (apiVersion, kind, namespace, name string, ok bool) {
	if !t.NamesKind {
		namespace, name = "", id
		if i := strings.Index(id, "/"); i >= 0 {
			namespace, name = id[:i], id[i+1:]
		}
		return t.FixedAPIVersion, t.FixedKind, namespace, name, name != ""
	}
	for _, part := range strings.Split(id, ",") {
		k, v, found := strings.Cut(part, "=")
		if !found {
			return "", "", "", "", false
		}
		switch k {
		case "apiVersion":
			apiVersion = v
		case "kind":
			kind = v
		case "namespace":
			namespace = v
		case "name":
			name = v
		}
	}
	return apiVersion, kind, namespace, name, apiVersion != "" && kind != "" && name != ""
}

// fieldGranularEnvKinds are the workload kinds hashicorp/kubernetes'
// kubernetes_env patches a container's env in, by API group: the kinds
// whose pod spec [kubesweep.PodSpecRoot] reads.
var fieldGranularEnvKinds = []string{
	"|Pod", "|ReplicationController",
	"apps|Deployment", "apps|StatefulSet", "apps|DaemonSet", "apps|ReplicaSet",
	"batch|Job", "batch|CronJob",
}

// fieldGranularKindKey is a kind's group|Kind, the key the scan's kind
// filter is on.
func fieldGranularKindKey(apiVersion, kind string) string {
	group := ""
	if i := strings.LastIndex(apiVersion, "/"); i >= 0 {
		group = apiVersion[:i]
	}
	return group + "|" + kind
}

// fieldGranularKinds is the kinds the field-granular types patch whatever
// a configuration says: each fixed-kind type's kind, and the env type's
// workload kinds. The kinds labels and annotations name come from the
// configuration and the records, and the caller adds them.
func fieldGranularKinds(types []FieldGranularType) map[string]bool {
	out := map[string]bool{}
	for _, t := range types {
		if !t.NamesKind {
			out[fieldGranularKindKey(t.FixedAPIVersion, t.FixedKind)] = true
		}
		if t.Env {
			for _, k := range fieldGranularEnvKinds {
				out[k] = true
			}
		}
	}
	return out
}

// fieldGranularRecords is every field-granular instance the estate's record
// store knows of, read by key so that an estate with none reads nothing
// but the key listing. A store that cannot be listed is a gap for every
// field-granular type, never grounds to assume there is nothing.
func (leg KubernetesSweep) fieldGranularRecords(ctx context.Context, req Request, byType map[string]FieldGranularType, res *Result) ([]projection.FieldGranularRecord, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if req.HintStore == nil {
		return nil, diags
	}
	prefix := req.KeyPrefix
	if prefix == "" {
		prefix = projection.RecordKeyPrefix(req.Estate)
	}
	types := make(map[string]bool, len(byType))
	for name := range byType {
		types[name] = true
	}
	recs, err := projection.NewRecordEnvelopeStore(req.HintStore, prefix).ListFieldGranular(ctx, types)
	if err != nil {
		for _, t := range leg.FieldGranular {
			res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: t.TypeName, Reason: SweepGapListFailed,
				Detail: fmt.Sprintf("listing the record store for field-granular instances failed: %s", err)})
		}
		return nil, diags
	}
	return recs, diags
}

// fieldGranularMatch is one type's reading of what a manager owns on one
// object: whether it explains any of it, and the identity values the
// orphan's import stub needs beyond the object's name.
type fieldGranularMatch struct {
	t      FieldGranularType
	values map[string]string
	// ambiguous names why the type matched but cannot be filed: env owned
	// in more than one container.
	ambiguous string
}

// matchFieldGranular is every type in types that explains part of what
// fields - one manager's FieldsV1 on an object of kind at apiVersion -
// owns.
func matchFieldGranular(types []FieldGranularType, apiVersion, kind string, fields []byte) []fieldGranularMatch {
	var out []fieldGranularMatch
	for _, t := range types {
		if !t.NamesKind && (t.FixedKind != kind || t.FixedAPIVersion != apiVersion) {
			continue
		}
		m := fieldGranularMatch{t: t, values: map[string]string{}}
		owns := false
		for _, attr := range t.Maps {
			root, _ := kubesweep.FieldGranularMapRoot(attr)
			if members, atomic, found := kubesweep.OwnedAt(fields, root); found && (len(members) > 0 || atomic) {
				owns = true
			}
		}
		if t.Taint {
			if members, atomic, found := kubesweep.OwnedAt(fields, kubesweep.TaintsRoot); found && (len(members) > 0 || atomic) {
				owns = true
			}
		}
		if t.Env {
			names, initNames := kubesweep.EnvContainers(fields, kind)
			switch {
			case len(names)+len(initNames) > 1:
				owns = true
				m.ambiguous = fmt.Sprintf("the field manager owns env in %d containers, and one %s block writes one container's", len(names)+len(initNames), t.TypeName)
			case len(names) == 1:
				owns = true
				m.values["container"] = names[0]
			case len(initNames) == 1:
				owns = true
				m.values["init_container"] = initNames[0]
			}
		}
		if owns {
			out = append(out, m)
		}
	}
	return out
}

// sweepFieldGranular is the leg; see this file's doc comment. kinds is the
// label leg's own listing of what the cluster serves, so this costs no
// second API discovery.
func (leg KubernetesSweep) sweepFieldGranular(ctx context.Context, req Request, kinds []kubesweep.Kind, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if len(leg.FieldGranular) == 0 || req.Estate == "" || markers.ValidFieldManagerEstate(req.Estate) != "" {
		return diags
	}
	lister, ok := leg.Client.(kubesweep.FieldManagedLister)
	if !ok {
		return diags
	}
	manager := markers.FieldManagerFor(req.Estate)

	byType := map[string]FieldGranularType{}
	for _, t := range leg.FieldGranular {
		byType[t.TypeName] = t
	}
	declared := map[string]bool{}
	var pending []string
	declaredCount := map[string]int{}
	for _, r := range req.Resolutions {
		t, ok := byType[r.Addr.Resource.Resource.Type]
		if !ok || r.Undeclared {
			continue
		}
		if r.Class != identity.ClassConcrete || r.ImportID == "" {
			pending = append(pending, r.Addr.String())
			continue
		}
		apiVersion, kind, namespace, name, ok := parseFieldGranularImportID(t, r.ImportID)
		if !ok {
			pending = append(pending, r.Addr.String())
			continue
		}
		declared[fieldGranularObjectKey(apiVersion, kind, namespace, name)] = true
		declaredCount[t.TypeName]++
	}

	// Gate and scope (GitHub issue #1863's follow-up ruling): the scan is
	// one unselected list per kind, so it runs only for an estate that has
	// field-granular instances - declared now, or recorded by an earlier
	// apply or migration, which is how an instance whose block the
	// configuration dropped is still known - and only over the kinds
	// those types patch. An estate with none pays nothing.
	recorded, recDiags := leg.fieldGranularRecords(ctx, req, byType, res)
	diags = diags.Append(recDiags)
	declaredAny := len(declared) > 0 || len(pending) > 0
	if !declaredAny && len(recorded) == 0 {
		return diags
	}
	wantKinds := fieldGranularKinds(leg.FieldGranular)
	for key := range declared {
		parts := strings.SplitN(key, "|", 3)
		wantKinds[parts[0]+"|"+parts[1]] = true
	}
	for _, r := range recorded {
		wantKinds[fieldGranularKindKey(r.APIVersion, r.Kind)] = true
	}

	type found struct {
		obj   kubesweep.FieldManagedObject
		match fieldGranularMatch
	}
	var orphans []found
	var unclassified []string
	listedAll := true
	seen := map[string]bool{}
	for _, k := range kinds {
		if seen[k.GVR.String()] || !wantKinds[k.GVR.Group+"|"+k.Kind] {
			continue
		}
		seen[k.GVR.String()] = true
		objs, err := lister.ListFieldManaged(ctx, k, manager)
		if err != nil {
			listedAll = false
			for _, t := range leg.FieldGranular {
				if !t.NamesKind && t.FixedKind != k.Kind {
					continue
				}
				gap := SweepGap{TypeName: t.TypeName, Reason: SweepGapListFailed,
					Detail: fmt.Sprintf("listing %s across all namespaces for fields owned by field manager %q failed: %s", k.GVR.String(), manager, err)}
				if detail, denied := kubesweep.Forbidden(err); denied {
					sweepGapKubeDenied(res, gap, k.Kind, detail, err)
					continue
				}
				res.SweepGaps = append(res.SweepGaps, gap)
			}
			continue
		}
		for _, o := range objs {
			if declared[fieldGranularObjectKey(o.APIVersion, o.Kind, o.Namespace, o.Name)] {
				continue
			}
			matches := matchFieldGranular(leg.FieldGranular, o.APIVersion, o.Kind, o.Fields)
			what := o.Kind + " " + kubesweep.NaturalKey(o.Namespace, o.Name)
			switch {
			case len(matches) == 0:
				unclassified = append(unclassified, fmt.Sprintf("%s: the field manager owns fields none of the field-granular types writes", what))
			case len(matches) > 1:
				names := make([]string, len(matches))
				for i, m := range matches {
					names[i] = m.t.TypeName
				}
				unclassified = append(unclassified, fmt.Sprintf("%s: the field manager owns fields of %s, and one object holds one field-granular block per estate", what, strings.Join(names, " and ")))
			case matches[0].ambiguous != "":
				unclassified = append(unclassified, fmt.Sprintf("%s: %s", what, matches[0].ambiguous))
			default:
				orphans = append(orphans, found{obj: o, match: matches[0]})
			}
		}
	}

	if listedAll {
		for _, t := range leg.FieldGranular {
			res.Scans = append(res.Scans, TypeScan{
				TypeName:  t.TypeName,
				Filtering: FilterClientSide,
				Scope:     ScopeEstate,
				Sweep:     true,
				Source:    SourceKubernetes,
				Declared:  declaredCount[t.TypeName],
			})
			res.SweepCovered = append(res.SweepCovered, t.TypeName)
		}
	}

	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryFieldGranularOrphanUnclassified,
			fmt.Sprintf("No block of this configuration names these objects, and this estate's field manager %q owns fields on them that this run cannot attribute to exactly one field-granular resource type, so no removal is proposed for them:\n\n  - %s\n\nA removal is an apply under %q, and one planned for the wrong type would release the wrong fields. Declare the block that wrote them again and remove it in a run of its own, or release the fields by hand.",
				manager, strings.Join(unclassified, "\n  - "), manager)))
	}
	if len(orphans) == 0 {
		return diags
	}
	if len(pending) > 0 {
		sort.Strings(pending)
		names := make([]string, len(orphans))
		for i, o := range orphans {
			names[i] = o.match.t.TypeName + " on " + o.obj.Kind + " " + kubesweep.NaturalKey(o.obj.Namespace, o.obj.Name)
		}
		sort.Strings(names)
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryFieldGranularOrphansPending,
			fmt.Sprintf("This estate's field manager %q owns fields no configured block names (%s), and %s cannot yet name the object it patches, so any of those fields may be its. No removal is proposed this run; once every field-granular block's object is known, the next plan proposes it.",
				manager, strings.Join(names, "; "), strings.Join(pending, ", "))))
		return diags
	}

	for _, f := range orphans {
		o, t := f.obj, f.match.t
		name := kubesweep.OrphanResourceName(o.Namespace, o.Name)
		if t.NamesKind {
			name = kubesweep.ManifestOrphanResourceName(o.Kind, o.Namespace, o.Name)
		}
		addr := addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: t.TypeName,
			Name: name,
		}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
		values := map[string]string{"name": o.Name}
		if o.Namespace != "" {
			values["namespace"] = o.Namespace
		}
		if t.NamesKind {
			values["api_version"] = o.APIVersion
			values["kind"] = o.Kind
		}
		for k, v := range f.match.values {
			values[k] = v
		}
		res.Orphans = append(res.Orphans, OwnedResource{
			TypeName:       t.TypeName,
			ImportID:       FieldGranularImportID(t, o.APIVersion, o.Kind, o.Namespace, o.Name),
			IdentityValues: values,
			Marker:         req.Estate,
			Normalized:     markers.EscapeAddress(addr.String()),
			DisplayName:    fmt.Sprintf("fields of %s %s owned by %s", o.Kind, kubesweep.NaturalKey(o.Namespace, o.Name), manager),
			Resource:       cty.NilVal,
			Swept:          true,
		})
	}
	return diags
}
