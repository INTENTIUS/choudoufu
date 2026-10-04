// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// clusterScopedFieldGranularSchema is a field-granular type's schema in
// the shape hashicorp/kubernetes serves it: metadata.namespace optional on
// every type but kubernetes_node_taint, which has none.
func clusterScopedFieldGranularSchema(namesKind, namespaced bool, fields map[string]*configschema.Attribute) providers.Schema {
	meta := map[string]*configschema.Attribute{"name": {Type: cty.String, Required: true}}
	if namespaced {
		meta["namespace"] = &configschema.Attribute{Type: cty.String, Optional: true}
	}
	attrs := map[string]*configschema.Attribute{
		"field_manager": {Type: cty.String, Optional: true},
		"force":         {Type: cty.Bool, Optional: true},
		"id":            {Type: cty.String, Optional: true, Computed: true},
	}
	if namesKind {
		attrs["api_version"] = &configschema.Attribute{Type: cty.String, Required: true}
		attrs["kind"] = &configschema.Attribute{Type: cty.String, Required: true}
	}
	for k, v := range fields {
		attrs[k] = v
	}
	return providers.Schema{Block: &configschema.Block{
		Attributes: attrs,
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1, Block: configschema.Block{Attributes: meta}},
		},
	}}
}

// TestFieldGranularClusterScopedRecordRoundTrips (GitHub epic #1885):
// a field-granular block on a cluster-scoped object - kubernetes_labels on
// the "default" Namespace, kubernetes_annotations on a StorageClass - is
// stored by an SDKv2 provider with metadata.namespace = "" rather than
// null, and the record a migration wrote from that state carried an empty
// "namespace" component that every later read refused ("carries an empty
// \"namespace\" component"). The namespace segment is OmitIfAbsent, so an
// empty one is the cluster-scoped object's absent namespace: the record
// must leave it out, and what is written must read back.
func TestFieldGranularClusterScopedRecordRoundTrips(t *testing.T) {
	stringMap := &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
	cases := []struct {
		typeName  string
		schema    providers.Schema
		obj       cty.Value
		namespace string
	}{
		{
			typeName: "kubernetes_labels",
			schema:   clusterScopedFieldGranularSchema(true, true, map[string]*configschema.Attribute{"labels": stringMap}),
			obj: cty.ObjectVal(map[string]cty.Value{
				"api_version":   cty.StringVal("v1"),
				"kind":          cty.StringVal("Namespace"),
				"labels":        cty.MapVal(map[string]cty.Value{"team": cty.StringVal("platform")}),
				"field_manager": cty.StringVal("Terraform"),
				"force":         cty.NullVal(cty.Bool),
				"id":            cty.StringVal("apiVersion=v1,kind=Namespace,name=default"),
				"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
					"name":      cty.StringVal("default"),
					"namespace": cty.StringVal(""),
				})}),
			}),
		},
		{
			typeName: "kubernetes_annotations",
			schema:   clusterScopedFieldGranularSchema(true, true, map[string]*configschema.Attribute{"annotations": stringMap, "template_annotations": stringMap}),
			obj: cty.ObjectVal(map[string]cty.Value{
				"api_version":          cty.StringVal("storage.k8s.io/v1"),
				"kind":                 cty.StringVal("StorageClass"),
				"annotations":          cty.MapVal(map[string]cty.Value{"storageclass.kubernetes.io/is-default-class": cty.StringVal("false")}),
				"template_annotations": cty.NullVal(cty.Map(cty.String)),
				"field_manager":        cty.StringVal("Terraform"),
				"force":                cty.NullVal(cty.Bool),
				"id":                   cty.StringVal("apiVersion=storage.k8s.io/v1,kind=StorageClass,name=standard"),
				"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
					"name":      cty.StringVal("standard"),
					"namespace": cty.StringVal(""),
				})}),
			}),
		},
		{
			// The namespaced case is unchanged: the namespace is recorded.
			typeName: "kubernetes_labels",
			schema:   clusterScopedFieldGranularSchema(true, true, map[string]*configschema.Attribute{"labels": stringMap}),
			obj: cty.ObjectVal(map[string]cty.Value{
				"api_version":   cty.StringVal("v1"),
				"kind":          cty.StringVal("ConfigMap"),
				"labels":        cty.MapVal(map[string]cty.Value{"team": cty.StringVal("platform")}),
				"field_manager": cty.StringVal("Terraform"),
				"force":         cty.NullVal(cty.Bool),
				"id":            cty.StringVal("apiVersion=v1,kind=ConfigMap,namespace=apps,name=settings"),
				"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
					"name":      cty.StringVal("settings"),
					"namespace": cty.StringVal("apps"),
				})}),
			}),
			namespace: "apps",
		},
		{
			typeName: "kubernetes_node_taint",
			schema: clusterScopedFieldGranularSchema(false, false, map[string]*configschema.Attribute{
				"taint": {Type: cty.List(cty.Object(map[string]cty.Type{"effect": cty.String, "key": cty.String, "value": cty.String})), Optional: true},
			}),
			obj: cty.ObjectVal(map[string]cty.Value{
				"taint": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
					"effect": cty.StringVal("NoSchedule"), "key": cty.StringVal("dedicated"), "value": cty.StringVal("gpu"),
				})}),
				"field_manager": cty.StringVal("Terraform"),
				"force":         cty.NullVal(cty.Bool),
				"id":            cty.StringVal("node-1"),
				"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
					"name": cty.StringVal("node-1"),
				})}),
			}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.typeName+"/"+tc.obj.GetAttr("id").AsString(), func(t *testing.T) {
			rec, ok := LocatedRecordFrom(tc.typeName, tc.schema, tc.obj)
			if !ok {
				t.Fatalf("no record derived")
			}
			for name, v := range rec.Components {
				if v == "" {
					t.Errorf("record carries an empty %q component: %#v", name, rec)
				}
			}
			if got := rec.Components["namespace"]; got != tc.namespace {
				t.Errorf("namespace component %q, want %q", got, tc.namespace)
			}

			ctx := context.Background()
			store := NewRecordEnvelopeStore(newTestLocalStore(t), "records/est")
			addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: tc.typeName, Name: "x"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
			provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
			if _, err := SeedLocatedForInstance(ctx, store, addr, provider, rec); err != nil {
				t.Fatalf("seeding: %s", err)
			}
			got, _, _, found, err := store.GetIdentity(ctx, addr)
			if err != nil {
				t.Fatalf("reading the record back: %s", err)
			}
			if !found || !componentsEqual(got.Components, rec.Components) || got.ImportID != rec.ImportID {
				t.Errorf("read back %#v (found=%v), wrote %#v", got, found, rec)
			}
		})
	}
}
