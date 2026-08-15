// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// cfnGen builds a generator carrying a CFN-required table and the sibling
// roster the value tiers read. The type names are synthetic: the pass under
// test may not know any real one.
func cfnGen(cfnRequired map[string][]string, resources map[string]*configschema.Block) *generator {
	g := &generator{
		cohort:      "t",
		cfnRequired: cfnRequired,
		byType:      map[string]resourceAddr{},
		schemas:     providers.GetProviderSchemaResponse{ResourceTypes: map[string]providers.Schema{}},
	}
	for name, block := range resources {
		g.byType[name] = resourceAddr{Type: name, Label: "app"}
		g.schemas.ResourceTypes[name] = providers.Schema{Block: block}
	}
	return g
}

// TestCFNRequiredWiresAReferenceArgument is the pass earning its place: CFN
// requires a member the wire schema leaves optional, the member's name says
// which sibling it points at, and the cohort renders that sibling - so the
// fixture gets the reference instead of omitting the member the API would
// reject the create for. (The real shape is aws_nat_gateway's subnet_id,
// which today's facts artifact does not carry - see #174's report - so this
// test is the class, not that instance.)
func TestCFNRequiredWiresAReferenceArgument(t *testing.T) {
	childBlock := blockWith(map[string]*configschema.Attribute{
		"gadget_id": optString(),
	}, nil)
	gadgetBlock := blockWith(map[string]*configschema.Attribute{
		"id": {Type: cty.String, Computed: true},
	}, nil)
	g := cfnGen(
		map[string][]string{"aws_svc_child": {"GadgetId"}},
		map[string]*configschema.Block{"aws_svc_child": childBlock, "aws_gadget": gadgetBlock},
	)
	body := hclwrite.NewEmptyFile().Body()

	applied := g.applyCFNRequired(body, childBlock, g.byType["aws_svc_child"])
	if len(applied) != 1 || applied[0] != "gadget_id" {
		t.Fatalf("applied = %v, want [gadget_id]", applied)
	}
	got := strings.TrimSpace(string(body.GetAttribute("gadget_id").Expr().BuildTokens(nil).Bytes()))
	if got != "aws_gadget.app.id" {
		t.Errorf("gadget_id = %s, want the rendered sibling's id", got)
	}
}

// TestCFNRequiredRefusesAPlaceholderValue: CFN saying "required" tells us
// the member must be present, not what a valid value looks like. With no
// sibling to reference and no naming shape, the only value on offer is the
// generic placeholder, and presence without a real value fixes nothing.
func TestCFNRequiredRefusesAPlaceholderValue(t *testing.T) {
	childBlock := blockWith(map[string]*configschema.Attribute{
		"gadget_id": optString(),
	}, nil)
	g := cfnGen(
		map[string][]string{"aws_svc_child": {"GadgetId"}},
		map[string]*configschema.Block{"aws_svc_child": childBlock},
	)
	body := hclwrite.NewEmptyFile().Body()

	if applied := g.applyCFNRequired(body, childBlock, g.byType["aws_svc_child"]); len(applied) != 0 {
		t.Errorf("applied = %v; the only value on offer was the generic placeholder", applied)
	}
	if body.GetAttribute("gadget_id") != nil {
		t.Error("a placeholder was written for presence alone")
	}
}

// TestCFNRequiredNamesANameShapedMember: the deterministic per-type name is
// a real value (valueExpr's own naming tier), so a name-shaped member CFN
// requires and the wire schema leaves optional gets one.
func TestCFNRequiredNamesANameShapedMember(t *testing.T) {
	block := blockWith(map[string]*configschema.Attribute{
		"table_name": optString(),
	}, nil)
	g := cfnGen(
		map[string][]string{"aws_svc_widget": {"TableName"}},
		map[string]*configschema.Block{"aws_svc_widget": block},
	)
	body := hclwrite.NewEmptyFile().Body()

	applied := g.applyCFNRequired(body, block, g.byType["aws_svc_widget"])
	if len(applied) != 1 || applied[0] != "table_name" {
		t.Fatalf("applied = %v, want [table_name]", applied)
	}
	got := strings.TrimSpace(string(body.GetAttribute("table_name").Expr().BuildTokens(nil).Bytes()))
	if !strings.Contains(got, "tofu-t-cohort-") {
		t.Errorf("table_name = %s, want the generator's deterministic name", got)
	}
}

// TestCFNRequiredLeavesASetArgumentAlone: the doc-example seed and the
// identity pass run first, and a value either of them wrote is better
// evidence than presence-by-requiredness.
func TestCFNRequiredLeavesASetArgumentAlone(t *testing.T) {
	childBlock := blockWith(map[string]*configschema.Attribute{
		"gadget_id": optString(),
	}, nil)
	gadgetBlock := blockWith(map[string]*configschema.Attribute{
		"id": {Type: cty.String, Computed: true},
	}, nil)
	g := cfnGen(
		map[string][]string{"aws_svc_child": {"GadgetId"}},
		map[string]*configschema.Block{"aws_svc_child": childBlock, "aws_gadget": gadgetBlock},
	)
	body := hclwrite.NewEmptyFile().Body()
	body.SetAttributeRaw("gadget_id", exprTokens(`"gd-123"`))

	if applied := g.applyCFNRequired(body, childBlock, g.byType["aws_svc_child"]); len(applied) != 0 {
		t.Errorf("applied = %v; the argument was already set", applied)
	}
}

// TestCFNRequiredSkipsAnUnmappableMember: CFN's model flattens what the
// provider nests (AWS::CodeGuruReviewer::RepositoryAssociation's Name and
// Type live under repository's one-of children in the wire schema), so a
// member with no top-level counterpart is a shape mismatch, not a
// requiredness disagreement this pass can act on.
func TestCFNRequiredSkipsAnUnmappableMember(t *testing.T) {
	block := blockWith(map[string]*configschema.Attribute{
		"other": optString(),
	}, nil)
	g := cfnGen(
		map[string][]string{"aws_svc_assoc": {"Name", "Type"}},
		map[string]*configschema.Block{"aws_svc_assoc": block},
	)
	body := hclwrite.NewEmptyFile().Body()

	if applied := g.applyCFNRequired(body, block, g.byType["aws_svc_assoc"]); len(applied) != 0 {
		t.Errorf("applied = %v for members the wire schema has no top-level counterpart for", applied)
	}
}

// TestCFNRequiredIsSuppressedWhereAnOverrideExists, the same rule the
// doc-example seed follows and for the same reason: a pass's argument an
// override never mentions survives beside it and can conflict.
func TestCFNRequiredIsSuppressedWhereAnOverrideExists(t *testing.T) {
	var overridden string
	for name := range typeOverrides {
		overridden = name
		break
	}
	if overridden == "" {
		t.Skip("no overrides left to check")
	}
	block := blockWith(map[string]*configschema.Attribute{
		"table_name": optString(),
	}, nil)
	g := cfnGen(
		map[string][]string{overridden: {"TableName"}},
		map[string]*configschema.Block{overridden: block},
	)
	body := hclwrite.NewEmptyFile().Body()

	if applied := g.applyCFNRequired(body, block, g.byType[overridden]); len(applied) != 0 {
		t.Errorf("applied = %v into %s, which still has a hand override", applied, overridden)
	}
}

func TestAttrForCFNMemberFoldsCase(t *testing.T) {
	block := blockWith(map[string]*configschema.Attribute{
		"subnet_id": optString(),
		"vpc_id":    optString(),
	}, nil)
	if name, ok := attrForCFNMember(block, "SubnetId"); !ok || name != "subnet_id" {
		t.Errorf("SubnetId -> (%q, %v), want subnet_id", name, ok)
	}
	if name, ok := attrForCFNMember(block, "VpcId"); !ok || name != "vpc_id" {
		t.Errorf("VpcId -> (%q, %v), want vpc_id", name, ok)
	}
	if _, ok := attrForCFNMember(block, "Absent"); ok {
		t.Error("Absent mapped to something")
	}
}
