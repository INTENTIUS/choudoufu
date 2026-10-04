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

	"github.com/zclconf/go-cty/cty"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
)

// fieldStubSweeper is a [stubSweeper] that also answers
// [kubesweep.FieldManagedLister], from fixed objects per kind.
type fieldStubSweeper struct {
	*stubSweeper
	managed map[string][]kubesweep.FieldManagedObject // by kind
	asked   []string
}

func (s *fieldStubSweeper) ListFieldManaged(_ context.Context, k kubesweep.Kind, manager string) ([]kubesweep.FieldManagedObject, error) {
	s.asked = append(s.asked, k.Kind+" "+manager)
	return s.managed[k.Kind], nil
}

// The six types' shapes as FieldGranularTypeOf reads them off
// hashicorp/kubernetes 3.2.1.
var testFieldGranularTypes = []FieldGranularType{
	{TypeName: "kubernetes_annotations", NamesKind: true, Maps: []string{"annotations", "template_annotations"}},
	{TypeName: "kubernetes_config_map_v1_data", FixedAPIVersion: "v1", FixedKind: "ConfigMap", Maps: []string{"data"}},
	{TypeName: "kubernetes_env", NamesKind: true, Env: true},
	{TypeName: "kubernetes_labels", NamesKind: true, Maps: []string{"labels"}},
	{TypeName: "kubernetes_node_taint", FixedAPIVersion: "v1", FixedKind: "Node", Taint: true},
	{TypeName: "kubernetes_secret_v1_data", FixedAPIVersion: "v1", FixedKind: "Secret", Maps: []string{"data"}},
}

func managedObj(apiVersion, kind, namespace, name, fields string) kubesweep.FieldManagedObject {
	return kubesweep.FieldManagedObject{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name, Fields: []byte(fields)}
}

func fieldSweepFixture() (*fieldStubSweeper, []kubesweep.Kind) {
	cm := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", APIVersion: "v1", Namespaced: true, TypeNames: []string{"kubernetes_config_map_v1"}}
	deploy := kubesweep.Kind{GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, Kind: "Deployment", APIVersion: "apps/v1", Namespaced: true, TypeNames: []string{"kubernetes_deployment_v1"}}
	node := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, Kind: "Node", APIVersion: "v1", TypeNames: []string{"kubernetes_manifest"}, Manifest: true}
	kinds := []kubesweep.Kind{cm, deploy, node}
	s := &fieldStubSweeper{
		stubSweeper: &stubSweeper{kinds: kinds},
		managed: map[string][]kubesweep.FieldManagedObject{
			"ConfigMap": {
				// A deleted kubernetes_labels block's label.
				managedObj("v1", "ConfigMap", "ns", "labelled", `{"f:metadata":{"f:labels":{"f:team":{}}}}`),
				// A deleted kubernetes_config_map_v1_data block's keys.
				managedObj("v1", "ConfigMap", "ns", "data", `{"f:data":{"f:a":{},"f:b":{}}}`),
				// A labels block that became an annotations block on the
				// same object: the declared block accounts for the object.
				managedObj("v1", "ConfigMap", "ns", "kept", `{"f:metadata":{"f:labels":{"f:old":{}}}}`),
			},
			"Deployment": {
				managedObj("apps/v1", "Deployment", "ns", "web", `{"f:spec":{"f:template":{"f:spec":{"f:containers":{"k:{\"name\":\"app\"}":{".":{},"f:env":{"k:{\"name\":\"LOG\"}":{".":{},"f:name":{},"f:value":{}}}}}}}}}`),
				// The manager owns a field no field-granular type writes.
				managedObj("apps/v1", "Deployment", "ns", "odd", `{"f:spec":{"f:replicas":{}}}`),
			},
			"Node": {
				managedObj("v1", "Node", "", "worker-1", `{"f:spec":{"f:taints":{}}}`),
			},
		},
	}
	return s, kinds
}

// TestFieldGranularSweepFilesEachOrphanUnderTheTypeThatWroteIt (GitHub
// issue #1863): every object the estate's field manager owns fields of
// and no block names becomes an orphan of the one field-granular type
// whose fields they are, at a synthetic address classifyOrphans can turn
// back into an instance, with the import id the type's identity renders
// and the identity values its stub needs; a declared block on the object
// accounts for it whatever its type; fields no single type explains are
// warned about and never proposed.
func TestFieldGranularSweepFilesEachOrphanUnderTheTypeThatWroteIt(t *testing.T) {
	s, kinds := fieldSweepFixture()
	leg := KubernetesSweep{Client: s, FieldGranular: testFieldGranularTypes}
	req := Request{
		Estate: "e",
		Resolutions: []identity.Resolution{
			{Addr: k8sInstance(t, "kubernetes_annotations", "kept"), Class: identity.ClassConcrete, ImportID: "apiVersion=v1,kind=ConfigMap,namespace=ns,name=kept"},
		},
	}
	res := &Result{}
	diags := leg.sweepFieldGranular(context.Background(), req, kinds, res)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	type got struct{ addr, typeName, importID string }
	var orphans []got
	byType := map[string]OwnedResource{}
	for _, o := range res.Orphans {
		addr, ok := UnescapeAddress(o.Normalized)
		if !ok {
			t.Fatalf("orphan %+v does not unescape to an address", o)
		}
		if addr.Resource.Resource.Type != o.TypeName {
			t.Errorf("orphan %s is filed as %s; classifyOrphans withholds a marker naming another type", addr, o.TypeName)
		}
		if o.Marker != "e" || !o.Swept {
			t.Errorf("orphan %+v: want marker e and swept", o)
		}
		orphans = append(orphans, got{addr.String(), o.TypeName, o.ImportID})
		byType[o.TypeName] = o
	}
	want := []got{
		{"kubernetes_labels.orphan_configmap_ns_labelled", "kubernetes_labels", "apiVersion=v1,kind=ConfigMap,namespace=ns,name=labelled"},
		{"kubernetes_config_map_v1_data.orphan_ns_data", "kubernetes_config_map_v1_data", "ns/data"},
		{"kubernetes_env.orphan_deployment_ns_web", "kubernetes_env", "apiVersion=apps/v1,kind=Deployment,namespace=ns,name=web"},
		{"kubernetes_node_taint.orphan_worker-1", "kubernetes_node_taint", "worker-1"},
	}
	if !reflect.DeepEqual(orphans, want) {
		t.Errorf("orphans =\n %v\nwant\n %v", orphans, want)
	}

	if v := byType["kubernetes_env"].IdentityValues; v["container"] != "app" || v["kind"] != "Deployment" || v["api_version"] != "apps/v1" || v["namespace"] != "ns" || v["name"] != "web" {
		t.Errorf("env orphan's identity values = %v; the stub needs the container, kind, apiVersion, namespace and name", v)
	}
	if v := byType["kubernetes_config_map_v1_data"].IdentityValues; v["name"] != "data" || v["namespace"] != "ns" || v["kind"] != "" {
		t.Errorf("data orphan's identity values = %v; a fixed-kind type names no kind", v)
	}

	var warned string
	for _, d := range diags {
		if d.Description().Summary == SummaryFieldGranularOrphanUnclassified {
			warned = d.Description().Detail
		}
	}
	if !strings.Contains(warned, "Deployment ns/odd") {
		t.Errorf("no warning names the object whose owned fields no type explains; got %q", warned)
	}
	if strings.Contains(warned, "kept") {
		t.Errorf("the object a declared block names was reported: %q", warned)
	}

	for _, a := range s.asked {
		if !strings.HasSuffix(a, " choudoufu:e") {
			t.Errorf("listed under %q; the estate's own manager is choudoufu:e", a)
		}
	}
	covered := map[string]bool{}
	for _, c := range res.SweepCovered {
		covered[c] = true
	}
	for _, ft := range testFieldGranularTypes {
		if !covered[ft.TypeName] {
			t.Errorf("%s not reported as swept", ft.TypeName)
		}
	}
}

// TestFieldGranularSweepDefersWhileABlockCannotBeNamed: a field-granular
// block whose object is not known yet may be the one that owns any of the
// fields found, so nothing is proposed and the run says why.
func TestFieldGranularSweepDefersWhileABlockCannotBeNamed(t *testing.T) {
	s, kinds := fieldSweepFixture()
	leg := KubernetesSweep{Client: s, FieldGranular: testFieldGranularTypes}
	req := Request{
		Estate: "e",
		Resolutions: []identity.Resolution{
			{Addr: k8sInstance(t, "kubernetes_labels", "later"), Class: identity.ClassNeedsDiscovery},
		},
	}
	res := &Result{}
	diags := leg.sweepFieldGranular(context.Background(), req, kinds, res)
	if len(res.Orphans) != 0 {
		t.Errorf("orphans = %v, want none while kubernetes_labels.later's object is unknown", res.Orphans)
	}
	found := false
	for _, d := range diags {
		if d.Description().Summary == SummaryFieldGranularOrphansPending && strings.Contains(d.Description().Detail, "kubernetes_labels.later") {
			found = true
		}
	}
	if !found {
		t.Errorf("no deferral warning naming the unresolved block: %v", diags.ErrWithWarnings())
	}
}

// TestFieldGranularSweepNeedsTheListerAndAnEstate: a sweeper that cannot
// list by field manager, or a run with no estate, asks nothing and files
// nothing.
func TestFieldGranularSweepNeedsTheListerAndAnEstate(t *testing.T) {
	s, kinds := fieldSweepFixture()
	res := &Result{}
	KubernetesSweep{Client: s.stubSweeper, FieldGranular: testFieldGranularTypes}.sweepFieldGranular(context.Background(), Request{Estate: "e"}, kinds, res)
	KubernetesSweep{Client: s, FieldGranular: testFieldGranularTypes}.sweepFieldGranular(context.Background(), Request{}, kinds, res)
	if len(res.Orphans) != 0 || len(s.asked) != 0 {
		t.Errorf("orphans %v, lists %v; want none", res.Orphans, s.asked)
	}
}

// TestFieldGranularImportIDRoundTrips: the id the sweep renders parses
// back to the same object, which is the join a declared block meets an
// object on.
func TestFieldGranularImportIDRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		t                          FieldGranularType
		apiVersion, kind, ns, name string
	}{
		{testFieldGranularTypes[3], "apps/v1", "Deployment", "ns", "web"},
		{testFieldGranularTypes[3], "v1", "Namespace", "", "team"},
		{testFieldGranularTypes[1], "v1", "ConfigMap", "ns", "cfg"},
		{testFieldGranularTypes[4], "v1", "Node", "", "worker-1"},
	} {
		id := FieldGranularImportID(tc.t, tc.apiVersion, tc.kind, tc.ns, tc.name)
		a, k, ns, n, ok := parseFieldGranularImportID(tc.t, id)
		if !ok || a != tc.apiVersion || k != tc.kind || ns != tc.ns || n != tc.name {
			t.Errorf("%s: %q parsed to %q %q %q %q %v", tc.t.TypeName, id, a, k, ns, n, ok)
		}
	}
}

// TestFieldGranularTypeOfReadsTheSchema: the shape is read off the schema,
// and a schema that is not the shape is not one.
func TestFieldGranularTypeOfReadsTheSchema(t *testing.T) {
	block := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"field_manager": {Type: cty.String, Optional: true},
			"force":         {Type: cty.Bool, Optional: true},
			"data":          {Type: cty.Map(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {Nesting: configschema.NestingList, MaxItems: 1, Block: configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"name":      {Type: cty.String, Required: true},
					"namespace": {Type: cty.String, Optional: true},
				},
			}},
		},
	}
	got, ok := FieldGranularTypeOf("kubernetes_config_map_v1_data", block)
	want := FieldGranularType{TypeName: "kubernetes_config_map_v1_data", FixedAPIVersion: "v1", FixedKind: "ConfigMap", Maps: []string{"data"}}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("FieldGranularTypeOf = %+v, %v; want %+v", got, ok, want)
	}
	delete(block.Attributes, "force")
	if _, ok := FieldGranularTypeOf("kubernetes_config_map_v1_data", block); ok {
		t.Error("a schema without force is not the field-granular shape")
	}
}
