// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"reflect"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// objectMetaSchema is the provider's object-metadata shape at
// hashicorp/kubernetes 3.2.1, with or without a namespace, wrapped in a
// resource block that carries nothing else identity-bearing.
func objectMetaSchema(namespaced bool) providers.Schema {
	attrs := map[string]*configschema.Attribute{
		"name":             {Type: cty.String, Optional: true, Computed: true},
		"generation":       {Type: cty.Number, Computed: true},
		"labels":           {Type: cty.Map(cty.String), Optional: true},
		"annotations":      {Type: cty.Map(cty.String), Optional: true},
		"generate_name":    {Type: cty.String, Optional: true},
		"resource_version": {Type: cty.String, Computed: true},
		"uid":              {Type: cty.String, Computed: true},
	}
	if namespaced {
		attrs["namespace"] = &configschema.Attribute{Type: cty.String, Optional: true}
	}
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Optional: true, Computed: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"metadata": {Block: configschema.Block{Attributes: attrs}, Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"api_version": {Type: cty.String, Required: true},
				"kind":        {Type: cty.String, Required: true},
				"name":        {Type: cty.String, Required: true},
			},
		},
		IdentitySchemaVersion: 1,
	}
}

// TestObjectMetaRuleReproducesTheRatifiedRows is what keeps #326's four
// hand rows honest now that a rule states the convention: for each, the
// rule's entry carries the same components (block, argument, separator,
// identity attribute) and the same import syntax the row does. A row that
// drifts from the rule, or a rule that drifts from the rows, fails here.
func TestObjectMetaRuleReproducesTheRatifiedRows(t *testing.T) {
	for typeName, namespaced := range map[string]bool{
		"kubernetes_config_map":           true,
		"kubernetes_namespace":            false,
		"kubernetes_cluster_role_binding": false,
		"kubernetes_storage_class":        false,
	} {
		row, ok := LookupType(typeName)
		if !ok {
			t.Fatalf("%s has no ratified row; this test is checking nothing for it", typeName)
		}
		if !row.NonAWSProvider {
			t.Errorf("%s's row is not marked NonAWSProvider", typeName)
		}
		got, ok := synthesizeMetadataIdentity(typeName, objectMetaSchema(namespaced))
		if !ok {
			t.Fatalf("%s: the object-metadata rule refused a schema of its own shape", typeName)
		}
		if !reflect.DeepEqual(got.Components, row.Components) {
			t.Errorf("%s: rule components\n got %+v\nwant %+v (the ratified row)", typeName, got.Components, row.Components)
		}
		if got.ImportSyntax != row.ImportSyntax {
			t.Errorf("%s: rule import syntax %q, row %q", typeName, got.ImportSyntax, row.ImportSyntax)
		}
		// And the resolver prefers the rule's entry over the row when the
		// two agree, the same schema-first precedence every AWS row gets.
		schemas := map[string]providers.Schema{typeName: objectMetaSchema(namespaced)}
		synthesized, ok := SynthesizeTypeIdentity(typeName, schemas, nil)
		if !ok {
			t.Fatalf("%s: SynthesizeTypeIdentity refused", typeName)
		}
		if !schemaReproducesRow(row, synthesized) {
			t.Errorf("%s: the rule's entry does not reproduce the row by schemaReproducesRow's own comparison", typeName)
		}
	}
}

func TestObjectMetaShape(t *testing.T) {
	if ns, ok := ObjectMetaShape(objectMetaSchema(true).Block); !ok || !ns {
		t.Errorf("namespaced shape: ok=%v namespaced=%v", ok, ns)
	}
	if ns, ok := ObjectMetaShape(objectMetaSchema(false).Block); !ok || ns {
		t.Errorf("cluster-scoped shape: ok=%v namespaced=%v", ok, ns)
	}
	for name, mutate := range map[string]func(*configschema.Block){
		"no metadata":        func(b *configschema.Block) { delete(b.BlockTypes, "metadata") },
		"metadata is a set":  func(b *configschema.Block) { b.BlockTypes["metadata"].Nesting = configschema.NestingSet },
		"metadata unbounded": func(b *configschema.Block) { b.BlockTypes["metadata"].MaxItems = 0 },
		"no uid":             func(b *configschema.Block) { delete(b.BlockTypes["metadata"].Block.Attributes, "uid") },
		"no labels":          func(b *configschema.Block) { delete(b.BlockTypes["metadata"].Block.Attributes, "labels") },
		"name computed only": func(b *configschema.Block) {
			b.BlockTypes["metadata"].Block.Attributes["name"] = &configschema.Attribute{Type: cty.String, Computed: true}
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := objectMetaSchema(true).Block
			mutate(b)
			if _, ok := ObjectMetaShape(b); ok {
				t.Errorf("%s: reported as object metadata", name)
			}
		})
	}
	// An AWS-shaped block with a top-level tags map is not this.
	aws := &configschema.Block{Attributes: map[string]*configschema.Attribute{"tags": {Type: cty.Map(cty.String), Optional: true}}}
	if _, ok := ObjectMetaShape(aws); ok {
		t.Error("an AWS tags map was reported as object metadata")
	}
	if _, ok := ObjectMetaShape(nil); ok {
		t.Error("nil block reported as object metadata")
	}
}

// TestObjectMetaRuleAdmitsAnUnratifiedType: a type with no row resolves
// through the rule alone, namespaced and cluster-scoped, and never claims
// an "id" identity attribute the schema does not carry.
func TestObjectMetaRuleAdmitsAnUnratifiedType(t *testing.T) {
	if _, has := LookupType("kubernetes_service_account"); has {
		t.Fatal("kubernetes_service_account gained a ratified row; pick another type for this test")
	}
	ti, ok := SynthesizeTypeIdentity("kubernetes_service_account", map[string]providers.Schema{"kubernetes_service_account": objectMetaSchema(true)}, nil)
	if !ok || ti.ImportSyntax != "NAMESPACE/NAME" || len(ti.Components) != 3 {
		t.Fatalf("kubernetes_service_account: %+v, %v", ti, ok)
	}
	ti, ok = SynthesizeTypeIdentity("kubernetes_cluster_role", map[string]providers.Schema{"kubernetes_cluster_role": objectMetaSchema(false)}, nil)
	if !ok || ti.ImportSyntax != "NAME" || len(ti.Components) != 1 {
		t.Fatalf("kubernetes_cluster_role: %+v, %v", ti, ok)
	}
	if len(ti.IdentityAttrs) != 0 {
		t.Errorf("the rule claimed identity attributes %v; the rows it reproduces claim none", ti.IdentityAttrs)
	}
}

func TestObjectMetaTraversal(t *testing.T) {
	// Built the way addrs.ParseRef leaves ref.Remaining: the steps past
	// the resource instance.
	for name, tc := range map[string]struct {
		rest hcl.Traversal
		want string
		ok   bool
	}{
		"metadata[0].name":      {hcl.Traversal{hcl.TraverseAttr{Name: "metadata"}, hcl.TraverseIndex{Key: cty.NumberIntVal(0)}, hcl.TraverseAttr{Name: "name"}}, "name", true},
		"metadata[0].namespace": {hcl.Traversal{hcl.TraverseAttr{Name: "metadata"}, hcl.TraverseIndex{Key: cty.NumberIntVal(0)}, hcl.TraverseAttr{Name: "namespace"}}, "namespace", true},
		"metadata[1].name":      {hcl.Traversal{hcl.TraverseAttr{Name: "metadata"}, hcl.TraverseIndex{Key: cty.NumberIntVal(1)}, hcl.TraverseAttr{Name: "name"}}, "", false},
		"metadata[0].uid":       {hcl.Traversal{hcl.TraverseAttr{Name: "metadata"}, hcl.TraverseIndex{Key: cty.NumberIntVal(0)}, hcl.TraverseAttr{Name: "uid"}}, "", false},
		"spec[0].name":          {hcl.Traversal{hcl.TraverseAttr{Name: "spec"}, hcl.TraverseIndex{Key: cty.NumberIntVal(0)}, hcl.TraverseAttr{Name: "name"}}, "", false},
		"a single attribute":    {hcl.Traversal{hcl.TraverseAttr{Name: "name"}}, "", false},
	} {
		got, ok := objectMetaTraversal(tc.rest)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: objectMetaTraversal = %q, %v; want %q, %v", name, got, ok, tc.want, tc.ok)
		}
	}
}
