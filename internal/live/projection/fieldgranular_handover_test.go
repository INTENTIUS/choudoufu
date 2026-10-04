// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/providers"
)

func handoverLabelsSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"labels":        {Type: cty.Map(cty.String), Required: true},
			"field_manager": {Type: cty.String, Optional: true},
			"force":         {Type: cty.Bool, Optional: true},
		},
	}}
}

func handoverRead(labels map[string]string) cty.Value {
	m := map[string]cty.Value{}
	for k, v := range labels {
		m[k] = cty.StringVal(v)
	}
	l := cty.MapValEmpty(cty.String)
	if len(m) > 0 {
		l = cty.MapVal(m)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"labels":        l,
		"field_manager": cty.StringVal("Terraform"),
		"force":         cty.NullVal(cty.Bool),
	})
}

// TestFieldGranularKeepDeclaredKeepsOnlyTheBlocksKeys (GitHub issue
// #1863): of what "Terraform" owns, the hand-over prior keeps the keys this
// configuration declares and nothing else - a key another stock
// configuration wrote under the shared default manager is not this
// block's, and a prior holding it would plan its removal.
func TestFieldGranularKeepDeclaredKeepsOnlyTheBlocksKeys(t *testing.T) {
	schema := handoverLabelsSchema()
	read := handoverRead(map[string]string{"team": "a", "theirs": "x"})
	declared := fieldGranularDeclaredMaps(map[string]cty.Value{
		"labels":        cty.MapVal(map[string]cty.Value{"team": cty.StringVal("b")}),
		"field_manager": cty.StringVal("choudoufu:e"),
	})
	if _, ok := declared["field_manager"]; ok {
		t.Errorf("declared maps carry a non-map attribute: %v", declared)
	}

	got := fieldGranularKeepDeclared(read, schema, declared)
	want := cty.MapVal(map[string]cty.Value{"team": cty.StringVal("a")})
	if !got.GetAttr("labels").RawEquals(want) {
		t.Errorf("labels = %#v, want %#v: the live value of the declared key alone", got.GetAttr("labels"), want)
	}
	if got.GetAttr("field_manager").AsString() != "Terraform" {
		t.Error("the prior's field manager moved; the plan's update is what moves it")
	}
	if !fieldGranularHoldsFields(got, schema) {
		t.Error("a prior holding a declared key reads as holding nothing")
	}
}

// TestFieldGranularKeepDeclaredEmptiesWhatNothingDeclares: with nothing
// declared - or nothing "Terraform" owns that is - the prior holds no
// field, so the instance stays absent and the plan proposes the create it
// proposed before the hand-over existed.
func TestFieldGranularKeepDeclaredEmptiesWhatNothingDeclares(t *testing.T) {
	schema := handoverLabelsSchema()
	read := handoverRead(map[string]string{"theirs": "x"})
	for name, declared := range map[string]map[string]cty.Value{
		"nothing declared": nil,
		"other keys":       {"labels": cty.MapVal(map[string]cty.Value{"team": cty.StringVal("a")})},
	} {
		got := fieldGranularKeepDeclared(read, schema, declared)
		if fieldGranularHoldsFields(got, schema) {
			t.Errorf("%s: the prior %#v holds fields; want none", name, got)
		}
	}
}

// TestFieldGranularKeepDeclaredKeepsMarks: a secret's data map comes back
// marked sensitive, and the cut keeps the mark.
func TestFieldGranularKeepDeclaredKeepsMarks(t *testing.T) {
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"data":          {Type: cty.Map(cty.String), Optional: true, Sensitive: true},
		"field_manager": {Type: cty.String, Optional: true},
	}}}
	read := cty.ObjectVal(map[string]cty.Value{
		"data":          cty.MapVal(map[string]cty.Value{"k": cty.StringVal("v"), "x": cty.StringVal("y")}).Mark(marks.Sensitive),
		"field_manager": cty.StringVal("Terraform"),
	})
	got := fieldGranularKeepDeclared(read, schema, map[string]cty.Value{"data": cty.MapVal(map[string]cty.Value{"k": cty.StringVal("v")})})
	data := got.GetAttr("data")
	if !data.HasMark(marks.Sensitive) {
		t.Error("the data map lost its sensitive mark")
	}
	if raw, _ := data.Unmark(); raw.LengthInt() != 1 {
		t.Errorf("data = %#v, want the one declared key", raw)
	}
}
