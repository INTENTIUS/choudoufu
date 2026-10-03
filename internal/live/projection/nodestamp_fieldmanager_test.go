// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// labelsPatchSchema is hashicorp/kubernetes 3.2.1's kubernetes_labels, the
// field-granular shape (GitHub issue #1191).
func labelsPatchSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"api_version":   {Type: cty.String, Required: true},
			"kind":          {Type: cty.String, Required: true},
			"labels":        {Type: cty.Map(cty.String), Required: true},
			"field_manager": {Type: cty.String, Optional: true},
			"force":         {Type: cty.Bool, Optional: true},
			"id":            {Type: cty.String, Optional: true, Computed: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1, Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
				"name":      {Type: cty.String, Required: true},
				"namespace": {Type: cty.String, Optional: true},
			}}},
		},
	}}
}

func labelsPatchConfig(fieldManager cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"api_version":   cty.StringVal("v1"),
		"kind":          cty.StringVal("ConfigMap"),
		"labels":        cty.MapVal(map[string]cty.Value{"owner": cty.StringVal("a")}),
		"field_manager": fieldManager,
		"force":         cty.NullVal(cty.Bool),
		"id":            cty.NullVal(cty.String),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal("shared"),
			"namespace": cty.StringVal("ns"),
		})}),
	})
}

// TestFieldManagerStampWritesTheEstatesManager: the node stamp sets an
// undeclared field_manager to choudoufu:<estate>, leaves the estate's own
// name as written, and refuses any other name - another estate's naming
// that estate - rather than write under it.
func TestFieldManagerStampWritesTheEstatesManager(t *testing.T) {
	n := &NodeResolver{Estate: "alpha"}
	addr := mustAddr(t, "kubernetes_labels.owner")
	schema := labelsPatchSchema()
	if !fieldGranularOwned("kubernetes", schema) {
		t.Fatal("kubernetes_labels' schema is not the field-granular shape")
	}
	if fieldGranularOwned("aws", schema) {
		t.Error("the field-granular stamp claims a schema on another provider family")
	}

	got, diags := n.adjustConfigValue(t.Context(), addr, labelsPatchConfig(cty.NullVal(cty.String)), schema, true)
	if diags.HasErrors() {
		t.Fatalf("stamp: %s", diags.Err())
	}
	if fm := got.GetAttr("field_manager"); fm.IsNull() || fm.AsString() != "choudoufu:alpha" {
		t.Errorf("field_manager = %#v, want choudoufu:alpha", fm)
	}
	if !got.GetAttr("labels").RawEquals(labelsPatchConfig(cty.NullVal(cty.String)).GetAttr("labels")) {
		t.Error("the stamp changed the labels the block writes; the estate label belongs to no field-granular write")
	}

	same := labelsPatchConfig(cty.StringVal("choudoufu:alpha"))
	got, diags = n.adjustConfigValue(t.Context(), addr, same, schema, false)
	if diags.HasErrors() || !got.RawEquals(same) {
		t.Errorf("the estate's own manager, declared, was not left as written: %v", diags.Err())
	}

	for declared, want := range map[string]string{
		"Terraform":       `declares field_manager = "Terraform" and this run writes under "choudoufu:alpha"`,
		"choudoufu:gamma": `the estate "gamma"'s field manager, and this run is the estate "alpha"`,
	} {
		_, diags := n.adjustConfigValue(t.Context(), addr, labelsPatchConfig(cty.StringVal(declared)), schema, false)
		if !diags.HasErrors() {
			t.Errorf("field_manager = %q was not refused", declared)
			continue
		}
		desc := diags[0].Description()
		if desc.Summary != SummaryMarkerConflict || !strings.Contains(desc.Detail, want) {
			t.Errorf("field_manager = %q: %s: %s\nwant the marker conflict saying %q", declared, desc.Summary, desc.Detail, want)
		}
	}

	_, diags = n.adjustConfigValue(t.Context(), addr, labelsPatchConfig(cty.UnknownVal(cty.String)), schema, false)
	if !diags.HasErrors() || diags[0].Description().Summary != SummaryFieldManagerUnresolved {
		t.Errorf("an unknown field_manager was not refused by name: %v", diags.Err())
	}

	long := &NodeResolver{Estate: strings.Repeat("e", markers.FieldManagerMaxLen)}
	_, diags = long.adjustConfigValue(t.Context(), addr, labelsPatchConfig(cty.NullVal(cty.String)), schema, false)
	if !diags.HasErrors() || diags[0].Description().Summary != SummaryFieldManagerNotCarried {
		t.Errorf("an estate too long for a field manager name was not refused by name: %v", diags.Err())
	}

	untag := &NodeResolver{Estate: "alpha", PolicyUntag: map[string]string{addr.String(): markers.TagEstate}}
	in := labelsPatchConfig(cty.NullVal(cty.String))
	got, diags = untag.adjustConfigValue(t.Context(), addr, in, schema, false)
	if diags.HasErrors() || !got.RawEquals(in) {
		t.Errorf("an untag of tofu-estate still stamped the field manager: %#v", got.GetAttr("field_manager"))
	}
}

// TestFieldGranularSeedReadsUnderTheEstatesManager: the prior handed to the
// provider's read names this estate's manager and none of the written
// fields, so the read returns what the manager owns and nothing a
// configured key would otherwise keep.
func TestFieldGranularSeedReadsUnderTheEstatesManager(t *testing.T) {
	seed := map[string]cty.Value{
		"labels":        cty.MapVal(map[string]cty.Value{"owner": cty.StringVal("a")}),
		"api_version":   cty.StringVal("v1"),
		"field_manager": cty.StringVal("choudoufu:alpha"),
	}
	got := fieldGranularSeed(seed, "alpha")
	if _, ok := got["labels"]; ok {
		t.Error("the written labels are still in the prior")
	}
	if got["field_manager"].AsString() != "choudoufu:alpha" || !got["api_version"].RawEquals(cty.StringVal("v1")) {
		t.Errorf("seed = %#v", got)
	}
	if _, ok := seed["labels"]; !ok {
		t.Error("the caller's seed map was mutated")
	}
	if got := fieldGranularSeed(seed, ""); len(got) != len(seed) {
		t.Error("no estate still rewrote the seed")
	}
}

// TestFieldGranularStubCarriesMetadataAndID: the stub for a type with no
// Importer holds the patched object's name and namespace in its metadata
// block and the rendered import id, which the SDK needs non-empty and
// kubernetes_env's Read parses.
func TestFieldGranularStubCarriesMetadataAndID(t *testing.T) {
	values := map[string]string{"api_version": "v1", "kind": "ConfigMap", "namespace": "ns", "name": "shared"}
	id := "apiVersion=v1,kind=ConfigMap,namespace=ns,name=shared"
	stub, ok := fieldGranularStub(labelsPatchSchema(), values, id)
	if !ok {
		t.Fatal("no stub")
	}
	if errs := stub.Type().TestConformance(labelsPatchSchema().Block.ImpliedType()); len(errs) > 0 {
		t.Fatalf("the stub does not conform to the schema: %v", errs)
	}
	meta := stub.GetAttr("metadata").Index(cty.NumberIntVal(0))
	if meta.GetAttr("name").AsString() != "shared" || meta.GetAttr("namespace").AsString() != "ns" {
		t.Errorf("metadata = %#v", meta)
	}
	if stub.GetAttr("id").AsString() != id || stub.GetAttr("kind").AsString() != "ConfigMap" {
		t.Errorf("id = %#v, kind = %#v", stub.GetAttr("id"), stub.GetAttr("kind"))
	}
	if _, ok := fieldGranularStub(labelsPatchSchema(), map[string]string{"namespace": "ns"}, id); ok {
		t.Error("a stub was built with no name to place")
	}
}

// TestFieldGranularHoldsFields: present exactly when the read came back
// holding a written field; an unknown value is never called absent.
func TestFieldGranularHoldsFields(t *testing.T) {
	schema := labelsPatchSchema()
	with := func(labels cty.Value) cty.Value {
		m := labelsPatchConfig(cty.StringVal("choudoufu:alpha")).AsValueMap()
		m["labels"] = labels
		return cty.ObjectVal(m)
	}
	if !fieldGranularHoldsFields(with(cty.MapVal(map[string]cty.Value{"owner": cty.StringVal("a")})), schema) {
		t.Error("a read holding the owner label was called absent")
	}
	if fieldGranularHoldsFields(with(cty.MapValEmpty(cty.String)), schema) {
		t.Error("a read holding no label was called present")
	}
	if fieldGranularHoldsFields(with(cty.NullVal(cty.Map(cty.String))), schema) {
		t.Error("a read with null labels was called present")
	}
	if !fieldGranularHoldsFields(with(cty.UnknownVal(cty.Map(cty.String))), schema) {
		t.Error("an unknown labels value was called absent")
	}
}
