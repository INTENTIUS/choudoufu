// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestReleasedFieldGranularWriteIsNoOrphan is corpus-govuk-cluster-
// services' day2_replace (#1885): after day2_remove released the
// StorageClass annotation, every later plan still reported
// kubernetes_annotations.orphan_storageclass_standard [ABSENT].
//
// hashicorp/kubernetes 3.2.1's Delete of a field-granular type is a
// server-side apply of an EMPTY map (or env list) under the block's
// manager. That drops every key the manager owned, but the manager's Apply
// entry stays, owning the empty map itself: {"f:metadata":{"f:annotations":{}}}.
// labels, annotations, data and a container's env are granular - a key or
// an item is the field - so owning the empty container owns no field of
// the type's surface, and the object is no orphan. A Node's taints are an
// atomic list, owned as one leaf with no members whether it holds taints or
// not, so that case is unchanged here.
func TestReleasedFieldGranularWriteIsNoOrphan(t *testing.T) {
	annotations := FieldGranularType{TypeName: "kubernetes_annotations", NamesKind: true, Maps: []string{"annotations", "template_annotations"}}
	env := FieldGranularType{TypeName: "kubernetes_env", NamesKind: true, Env: true}
	data := FieldGranularType{TypeName: "kubernetes_config_map_v1_data", FixedAPIVersion: "v1", FixedKind: "ConfigMap", Maps: []string{"data"}}
	taint := FieldGranularType{TypeName: "kubernetes_node_taint", FixedAPIVersion: "v1", FixedKind: "Node", Taint: true}
	types := []FieldGranularType{annotations, env, data, taint}

	for _, c := range []struct {
		name, apiVersion, kind, fields string
		want                           string
	}{
		{"released annotations", "storage.k8s.io/v1", "StorageClass", `{"f:metadata":{"f:annotations":{}}}`, ""},
		{"held annotation", "storage.k8s.io/v1", "StorageClass", `{"f:metadata":{"f:annotations":{"f:storageclass.kubernetes.io/is-default-class":{}}}}`, "kubernetes_annotations"},
		{"released data", "v1", "ConfigMap", `{"f:data":{}}`, ""},
		{"held data", "v1", "ConfigMap", `{"f:data":{"f:k":{}}}`, "kubernetes_config_map_v1_data"},
		{"released env", "apps/v1", "Deployment", `{"f:spec":{"f:template":{"f:spec":{"f:containers":{"k:{\"name\":\"web\"}":{".":{},"f:name":{},"f:env":{}}}}}}}`, ""},
		{"held env", "apps/v1", "Deployment", `{"f:spec":{"f:template":{"f:spec":{"f:containers":{"k:{\"name\":\"web\"}":{".":{},"f:name":{},"f:env":{"k:{\"name\":\"A\"}":{".":{},"f:name":{},"f:value":{}}}}}}}}}`, "kubernetes_env"},
		{"atomic taints", "v1", "Node", `{"f:spec":{"f:taints":{}}}`, "kubernetes_node_taint"},
	} {
		got := matchFieldGranular(types, c.apiVersion, c.kind, []byte(c.fields))
		switch {
		case c.want == "" && len(got) != 0:
			t.Errorf("%s: matched %s; a manager owning only an empty container owns no field", c.name, got[0].t.TypeName)
		case c.want != "" && (len(got) != 1 || got[0].t.TypeName != c.want):
			t.Errorf("%s: matched %v, want exactly %s", c.name, got, c.want)
		}
	}
}

// TestFieldGranularSweepNamesUnheldDeclaredInstances (#1885): a declared
// field-granular instance whose object's kind the sweep listed, and on
// which the estate's manager owns no field of the instance's type -
// released (only the empty container left) or never written - is named in
// Result.FieldGranularUnheld, so the projection plans its create without a
// read. One the manager holds is not, and neither is one whose kind was
// not listed.
func TestFieldGranularSweepNamesUnheldDeclaredInstances(t *testing.T) {
	s, kinds := fieldSweepFixture()
	s.managed["ConfigMap"] = append(s.managed["ConfigMap"],
		managedObj("v1", "ConfigMap", "ns", "released", `{"f:metadata":{"f:labels":{}}}`))
	s.managed["Deployment"] = append(s.managed["Deployment"],
		managedObj("apps/v1", "Deployment", "ns", "gone-env", `{"f:spec":{"f:template":{"f:spec":{"f:containers":{"k:{\"name\":\"web\"}":{".":{},"f:name":{},"f:env":{}}}}}}}`))
	leg := KubernetesSweep{Client: s, FieldGranular: testFieldGranularTypes}
	held := k8sInstance(t, "kubernetes_labels", "held")
	released := k8sInstance(t, "kubernetes_labels", "released")
	never := k8sInstance(t, "kubernetes_labels", "never")
	goneEnv := k8sInstance(t, "kubernetes_env", "goneenv")
	heldEnv := k8sInstance(t, "kubernetes_env", "heldenv")
	unlisted := k8sInstance(t, "kubernetes_labels", "unlisted")
	req := Request{
		Estate: "e",
		Resolutions: []identity.Resolution{
			{Addr: held, Class: identity.ClassConcrete, ImportID: "apiVersion=v1,kind=ConfigMap,namespace=ns,name=labelled"},
			{Addr: released, Class: identity.ClassConcrete, ImportID: "apiVersion=v1,kind=ConfigMap,namespace=ns,name=released"},
			{Addr: never, Class: identity.ClassConcrete, ImportID: "apiVersion=v1,kind=ConfigMap,namespace=ns,name=absent"},
			{Addr: goneEnv, Class: identity.ClassConcrete, ImportID: "apiVersion=apps/v1,kind=Deployment,namespace=ns,name=gone-env"},
			{Addr: heldEnv, Class: identity.ClassConcrete, ImportID: "apiVersion=apps/v1,kind=Deployment,namespace=ns,name=web"},
			{Addr: unlisted, Class: identity.ClassConcrete, ImportID: "apiVersion=v1,kind=Namespace,name=ns"},
		},
	}
	res := &Result{}
	if diags := leg.sweepFieldGranular(context.Background(), req, kinds, res); diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	want := map[string]bool{released.String(): true, never.String(): true, goneEnv.String(): true}
	if !reflect.DeepEqual(res.FieldGranularUnheld, want) {
		t.Errorf("FieldGranularUnheld = %v, want %v", res.FieldGranularUnheld, want)
	}
}

// TestFieldGranularSweepNamesMissingObjects (#1885, ruled 2026-10-04): a
// declared field-granular instance whose object the listing did not
// return does not exist; it is in FieldGranularMissing, named by its
// object, and FieldGranularMissingRefusals refuses it by name. An object
// listed with no field of the manager's exists: unheld, not missing, and
// never an orphan.
func TestFieldGranularSweepNamesMissingObjects(t *testing.T) {
	s, kinds := fieldSweepFixture()
	s.managed["Deployment"] = append(s.managed["Deployment"],
		kubesweep.FieldManagedObject{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "ns", Name: "bare"},
		// Listed, undeclared, and the manager owns nothing of it.
		kubesweep.FieldManagedObject{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "ns", Name: "unrelated"})
	leg := KubernetesSweep{Client: s, FieldGranular: testFieldGranularTypes}
	gone := k8sInstance(t, "kubernetes_env", "gone")
	bare := k8sInstance(t, "kubernetes_env", "bare")
	req := Request{
		Estate: "e",
		Resolutions: []identity.Resolution{
			{Addr: gone, Class: identity.ClassConcrete, ImportID: "apiVersion=apps/v1,kind=Deployment,namespace=shared-objs,name=web"},
			{Addr: bare, Class: identity.ClassConcrete, ImportID: "apiVersion=apps/v1,kind=Deployment,namespace=ns,name=bare"},
		},
	}
	res := &Result{}
	sweepDiags := leg.sweepFieldGranular(context.Background(), req, kinds, res)
	if sweepDiags.HasErrors() {
		t.Fatal(sweepDiags.Err())
	}
	for _, d := range sweepDiags {
		if strings.Contains(d.Description().Detail, "ns/unrelated") || strings.Contains(d.Description().Detail, "ns/bare") {
			t.Errorf("an object the manager owns nothing of was reported: %s: %s", d.Description().Summary, d.Description().Detail)
		}
	}
	if want := map[string]string{gone.String(): "Deployment shared-objs/web"}; !reflect.DeepEqual(res.FieldGranularMissing, want) {
		t.Errorf("FieldGranularMissing = %v, want %v", res.FieldGranularMissing, want)
	}
	if !res.FieldGranularUnheld[gone.String()] || !res.FieldGranularUnheld[bare.String()] {
		t.Errorf("FieldGranularUnheld = %v; both are absent without a read", res.FieldGranularUnheld)
	}
	for _, o := range res.Orphans {
		if strings.Contains(o.ImportID, "name=bare") || strings.Contains(o.ImportID, "name=unrelated") {
			t.Errorf("an object the manager owns nothing of was filed as an orphan: %+v", o)
		}
	}
	create := FieldGranularWrite{Addr: gone, Object: kubesweep.ObjectRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shared-objs", Name: "web"}, Create: true}
	diags := FieldGranularMissingRefusals(res.FieldGranularMissing, []FieldGranularWrite{create}, nil)
	if len(diags) != 1 || diags[0].Severity() != tfdiags.Error || diags[0].Description().Summary != SummaryFieldGranularTargetMissing {
		t.Fatalf("refusals = %v, want one %q error", diags, SummaryFieldGranularTargetMissing)
	}
	for name, planned := range map[string][]PlannedObject{
		"the plan creates it":          {{Kind: "Deployment", Namespace: "shared-objs", Name: "web", Known: true}},
		"a Deployment of unknown name": {{Kind: "Deployment"}},
	} {
		if d := FieldGranularMissingRefusals(res.FieldGranularMissing, []FieldGranularWrite{create}, planned); len(d) != 0 {
			t.Errorf("%s: refused anyway: %v", name, d)
		}
	}
	if d := FieldGranularMissingRefusals(res.FieldGranularMissing, []FieldGranularWrite{create}, []PlannedObject{{Kind: "ConfigMap"}}); len(d) != 1 {
		t.Errorf("an unknown object of another kind suppressed the refusal: %v", d)
	}
	detail := diags[0].Description().Detail
	for _, want := range []string{gone.String(), "Deployment shared-objs/web", "the object this block patches does not exist", "Recreate it, or remove the block"} {
		if !strings.Contains(strings.ToLower(detail), strings.ToLower(want)) {
			t.Errorf("refusal detail lacks %q:\n%s", want, detail)
		}
	}
}
