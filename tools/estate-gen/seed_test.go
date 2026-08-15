// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// seedGen builds a generator carrying one type's seed, with no provider
// schemas: these tests exercise the seeding rules, not the schema load.
func seedGen(tfType string, args ...seedArgument) *generator {
	return &generator{seed: exampleSeed{tfType: args}}
}

func str(v string) seedArgument { return seedArgument{Value: v, IsString: true} }
func at(a seedArgument, path ...string) seedArgument {
	a.Path = path
	return a
}

// blockWith builds a schema with one optional string attribute and,
// optionally, a single-instance nested block holding one.
func blockWith(attrs map[string]*configschema.Attribute, nested map[string]*configschema.NestedBlock) *configschema.Block {
	return &configschema.Block{Attributes: attrs, BlockTypes: nested}
}

func optString() *configschema.Attribute {
	return &configschema.Attribute{Type: cty.String, Optional: true}
}

func TestSeedFillsAnUnsetOptionalArgument(t *testing.T) {
	g := seedGen("aws_thing", at(str("Enabled"), "mode"))
	body := hclwrite.NewEmptyFile().Body()

	applied := g.seedFromExample(body, blockWith(map[string]*configschema.Attribute{"mode": optString()}, nil), "aws_thing")

	if len(applied) != 1 || applied[0] != "mode" {
		t.Errorf("applied = %v, want [mode]", applied)
	}
	if got := strings.TrimSpace(string(body.GetAttribute("mode").Expr().BuildTokens(nil).Bytes())); got != `"Enabled"` {
		t.Errorf("mode = %s, want \"Enabled\"", got)
	}
}

// TestSeedReplacesTheGenericPlaceholder is the case that makes this pass
// worth having, and the one the first implementation got wrong.
//
// The generic pass fills every schema-Required attribute, so
// aws_s3_bucket_versioning's status arrives already set to "placeholder" -
// which is not a member of its enum, and is precisely what its override
// existed to correct. A seed that refuses to touch anything already set
// does nothing for the case the issue names first.
func TestSeedReplacesTheGenericPlaceholder(t *testing.T) {
	g := seedGen("aws_thing", at(str("Enabled"), "status"))
	body := hclwrite.NewEmptyFile().Body()
	body.SetAttributeRaw("status", exprTokens(genericExprText(cty.String)))

	g.seedFromExample(body, blockWith(map[string]*configschema.Attribute{"status": optString()}, nil), "aws_thing")

	if got := strings.TrimSpace(string(body.GetAttribute("status").Expr().BuildTokens(nil).Bytes())); got != `"Enabled"` {
		t.Errorf("status = %s, want the documented \"Enabled\" to replace the placeholder", got)
	}
}

// TestSeedLeavesAComputedValueAlone is the other half. valueExpr computes
// identity names and parent references on purpose, and the page's own
// "example-bucket" would collide across cohorts if it won.
func TestSeedLeavesAComputedValueAlone(t *testing.T) {
	g := seedGen("aws_thing", at(str("example-bucket"), "bucket"))
	body := hclwrite.NewEmptyFile().Body()
	body.SetAttributeRaw("bucket", exprTokens(`"tofu-s3-cohort-bucket"`))

	if applied := g.seedFromExample(body, blockWith(map[string]*configschema.Attribute{"bucket": optString()}, nil), "aws_thing"); len(applied) != 0 {
		t.Errorf("applied = %v; a deliberately computed value must not be replaced", applied)
	}
	if got := strings.TrimSpace(string(body.GetAttribute("bucket").Expr().BuildTokens(nil).Bytes())); got != `"tofu-s3-cohort-bucket"` {
		t.Errorf("bucket = %s, want the generator's own name", got)
	}
}

// TestSeedIsSuppressedWhereAnOverrideExists is the rule terraform validate
// forced.
//
// The first version let Apply run afterwards and overwrite what it
// disagreed with. But an override only wins on arguments it SETS:
// aws_lambda_layer_version's page sets filename, its override sets s3_bucket
// and s3_key, the provider marks those mutually exclusive, and the seeded
// filename survived beside them. One cohort failed to validate.
func TestSeedIsSuppressedWhereAnOverrideExists(t *testing.T) {
	var overridden string
	for name := range typeOverrides {
		overridden = name
		break
	}
	if overridden == "" {
		t.Skip("no overrides left to check")
	}

	g := seedGen(overridden, at(str("x"), "mode"))
	body := hclwrite.NewEmptyFile().Body()

	if applied := g.seedFromExample(body, blockWith(map[string]*configschema.Attribute{"mode": optString()}, nil), overridden); len(applied) != 0 {
		t.Errorf("seeded %v into %s, which still has a hand override. The override only wins on "+
			"arguments it sets, so a seeded one survives beside it and can conflict.", applied, overridden)
	}
}

// TestSeedIncompleteNeverAddsAnArgument is the SSE guard: an argument from
// a block whose cross-resource reference was dropped is evidence, not a
// configuration, so it may never introduce an attribute the generic pass
// did not write.
func TestSeedIncompleteNeverAddsAnArgument(t *testing.T) {
	arg := at(str("aws:kms"), "sse_algorithm")
	arg.Incomplete = true
	g := seedGen("aws_thing", arg)
	body := hclwrite.NewEmptyFile().Body()

	if applied := g.seedFromExample(body, blockWith(map[string]*configschema.Attribute{"sse_algorithm": optString()}, nil), "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v from a block whose cross-resource reference was dropped; the result is a "+
			"configuration the provider rejects", applied)
	}
}

// TestSeedIncompleteNeverCreatesABlock is the same guard one level up: the
// real SSE case sits inside an optional nested block the generic pass never
// creates, and an incomplete example must not create it either.
func TestSeedIncompleteNeverCreatesABlock(t *testing.T) {
	arg := at(str("aws:kms"), "apply_server_side_encryption_by_default", "sse_algorithm")
	arg.Incomplete = true
	g := seedGen("aws_thing", arg)
	body := hclwrite.NewEmptyFile().Body()
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"apply_server_side_encryption_by_default": {
			Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1,
			Block: *blockWith(map[string]*configschema.Attribute{"sse_algorithm": optString()}, nil),
		},
	})

	if applied := g.seedFromExample(body, schema, "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v; an incomplete argument created the block it sits in", applied)
	}
	if body.FirstMatchingBlock("apply_server_side_encryption_by_default", nil) != nil {
		t.Error("the block was created for an incomplete argument")
	}
}

// TestSeedIncompleteReplacesThePlaceholder is the widening that guard
// allows: where the generic pass wrote its placeholder - the generator
// admitting it had nothing to offer - the page's literal for that exact
// argument is still the best evidence available, dropped reference or not.
// Measured 2026-08-15: refusing this kept 72 override types hand-written
// whose documented literal was byte-identical to the override's.
func TestSeedIncompleteReplacesThePlaceholder(t *testing.T) {
	arg := at(str("NONE"), "authorization")
	arg.Incomplete = true
	g := seedGen("aws_thing", arg)
	body := hclwrite.NewEmptyFile().Body()
	body.SetAttributeRaw("authorization", exprTokens(genericExprText(cty.String)))

	applied := g.seedFromExample(body, blockWith(map[string]*configschema.Attribute{"authorization": optString()}, nil), "aws_thing")
	if len(applied) != 1 || applied[0] != "authorization" {
		t.Fatalf("applied = %v, want [authorization]", applied)
	}
	if got := strings.TrimSpace(string(body.GetAttribute("authorization").Expr().BuildTokens(nil).Bytes())); got != `"NONE"` {
		t.Errorf("authorization = %s, want the documented \"NONE\" to replace the placeholder", got)
	}
}

func TestSeedSkipsAnArgumentTheSchemaDoesNotHave(t *testing.T) {
	g := seedGen("aws_thing", at(str("x"), "renamed_last_release"))
	body := hclwrite.NewEmptyFile().Body()

	if applied := g.seedFromExample(body, blockWith(map[string]*configschema.Attribute{"mode": optString()}, nil), "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v, which this provider version's schema does not declare", applied)
	}
}

func TestSeedSkipsAComputedOnlyAttribute(t *testing.T) {
	g := seedGen("aws_thing", at(str("x"), "arn"))
	body := hclwrite.NewEmptyFile().Body()
	schema := blockWith(map[string]*configschema.Attribute{
		"arn": {Type: cty.String, Computed: true},
	}, nil)

	if applied := g.seedFromExample(body, schema, "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v into a computed-only attribute; that is an error, not a fix", applied)
	}
}

// TestSeedCreatesASingleInstanceNestedBlock is how
// versioning_configuration { status = "Enabled" } is reached when the
// generic pass has not already made the block.
func TestSeedCreatesASingleInstanceNestedBlock(t *testing.T) {
	g := seedGen("aws_thing", at(str("Enabled"), "versioning_configuration", "status"))
	body := hclwrite.NewEmptyFile().Body()
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"versioning_configuration": {
			Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1,
			Block: *blockWith(map[string]*configschema.Attribute{"status": optString()}, nil),
		},
	})

	applied := g.seedFromExample(body, schema, "aws_thing")
	if len(applied) != 1 || applied[0] != "versioning_configuration.status" {
		t.Fatalf("applied = %v, want [versioning_configuration.status]", applied)
	}
	blk := body.FirstMatchingBlock("versioning_configuration", nil)
	if blk == nil {
		t.Fatal("the nested block was not created")
	}
	if got := strings.TrimSpace(string(blk.Body().GetAttribute("status").Expr().BuildTokens(nil).Bytes())); got != `"Enabled"` {
		t.Errorf("status = %s", got)
	}
}

// TestSeedCreatesTheOnlyInstanceOfAnOptionalBlock is issue #174's
// doc-example half: a COMPLETE example argument may create an optional
// block the body holds none of, whatever the schema's cap. The wire schema
// types aws_bedrock_inference_profile's model_source as an UNBOUNDED
// all-optional list even though the SDK rejects a create without exactly
// one - so a cap-shaped rule reaches neither it nor any of #174's other
// named fixtures. Creating the first instance answers "which element does
// this belong to" with the only answer there is.
func TestSeedCreatesTheOnlyInstanceOfAnOptionalBlock(t *testing.T) {
	g := seedGen("aws_thing", at(str("arn:aws:bedrock:us-west-2::foundation-model/x"), "model_source", "copy_from"))
	body := hclwrite.NewEmptyFile().Body()
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"model_source": {
			Nesting: configschema.NestingList, // no MinItems, no MaxItems: the bedrock shape
			Block:   *blockWith(map[string]*configschema.Attribute{"copy_from": optString()}, nil),
		},
	})

	applied := g.seedFromExample(body, schema, "aws_thing")
	if len(applied) != 1 || applied[0] != "model_source.copy_from" {
		t.Fatalf("applied = %v, want [model_source.copy_from]", applied)
	}
	blk := body.FirstMatchingBlock("model_source", nil)
	if blk == nil {
		t.Fatal("the optional block was not created for a complete argument")
	}
}

// TestSeedWritesIntoTheSingleRenderedInstance: a required multi-capable
// block (member_definition's MinItems 1, MaxItems 10 shape) arrives from
// the generic pass as exactly one empty instance, and a documented literal
// for it has exactly one place to land.
func TestSeedWritesIntoTheSingleRenderedInstance(t *testing.T) {
	g := seedGen("aws_thing", at(str("on"), "member_definition", "mode"))
	body := hclwrite.NewEmptyFile().Body()
	body.AppendNewBlock("member_definition", nil)
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"member_definition": {
			Nesting: configschema.NestingList, MinItems: 1, MaxItems: 10,
			Block: *blockWith(map[string]*configschema.Attribute{"mode": optString()}, nil),
		},
	})

	applied := g.seedFromExample(body, schema, "aws_thing")
	if len(applied) != 1 || applied[0] != "member_definition.mode" {
		t.Fatalf("applied = %v, want [member_definition.mode]", applied)
	}
	if len(matchingBlocks(body, "member_definition")) != 1 {
		t.Error("a second instance was created instead of writing into the rendered one")
	}
}

// TestSeedSkipsADuplicatedExamplePath: importdocs-gen flattens repeated
// unlabelled example blocks into one path, so a path the example set twice
// is two elements merged - which one wins is a guess, so neither does.
func TestSeedSkipsADuplicatedExamplePath(t *testing.T) {
	g := seedGen("aws_thing",
		at(str("a"), "rule", "prefix"),
		at(str("b"), "rule", "prefix"),
	)
	body := hclwrite.NewEmptyFile().Body()
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"rule": {
			Nesting: configschema.NestingList,
			Block:   *blockWith(map[string]*configschema.Attribute{"prefix": optString()}, nil),
		},
	})

	if applied := g.seedFromExample(body, schema, "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v from a path the example set twice; the values are two merged elements", applied)
	}
}

// TestSeedWritesANestedNameMember: the naming skip is about the RESOURCE's
// own name colliding across cohorts, which a nested member named "name"
// cannot do - it is a selector the provider validates (ecs setting's
// name = "containerInsights"). Skipping it created the block missing the
// very member the provider requires.
func TestSeedWritesANestedNameMember(t *testing.T) {
	g := seedGen("aws_thing",
		at(str("containerInsights"), "setting", "name"),
		at(str("enabled"), "setting", "value"),
	)
	body := hclwrite.NewEmptyFile().Body()
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"setting": {
			Nesting: configschema.NestingSet,
			Block: *blockWith(map[string]*configschema.Attribute{
				"name":  {Type: cty.String, Required: true},
				"value": {Type: cty.String, Required: true},
			}, nil),
		},
	})

	applied := g.seedFromExample(body, schema, "aws_thing")
	if len(applied) != 2 {
		t.Fatalf("applied = %v, want both setting.name and setting.value", applied)
	}
	blk := body.FirstMatchingBlock("setting", nil)
	if blk == nil || blk.Body().GetAttribute("name") == nil {
		t.Fatal("the nested name member was not written; the created block is missing its required selector")
	}
}

// TestSeedTopLevelNameStaysSkipped is the original half of that rule,
// unchanged: a top-level naming argument is the resource's own name, and
// the page's "example" would collide across cohorts.
func TestSeedTopLevelNameStaysSkipped(t *testing.T) {
	g := seedGen("aws_thing", at(str("example"), "name"))
	body := hclwrite.NewEmptyFile().Body()

	if applied := g.seedFromExample(body, blockWith(map[string]*configschema.Attribute{"name": optString()}, nil), "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v; a top-level naming argument belongs to the generator", applied)
	}
}

// TestSeedRefusesToCreateABlockMissingARequiredMember: an example that
// leaves a created block's schema-Required member out - a dropped map
// literal, a reference - is evidence about the block, not a configuration
// of it. Creating it anyway is how a fixture picked up an
// advanced_backup_setting with no backup_options.
func TestSeedRefusesToCreateABlockMissingARequiredMember(t *testing.T) {
	g := seedGen("aws_thing", at(str("EC2"), "advanced_backup_setting", "resource_type"))
	body := hclwrite.NewEmptyFile().Body()
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"advanced_backup_setting": {
			Nesting: configschema.NestingSet,
			Block: *blockWith(map[string]*configschema.Attribute{
				"resource_type":  {Type: cty.String, Required: true},
				"backup_options": {Type: cty.Map(cty.String), Required: true},
			}, nil),
		},
	})

	if applied := g.seedFromExample(body, schema, "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v into a created block whose required backup_options the example does not cover", applied)
	}
	if body.FirstMatchingBlock("advanced_backup_setting", nil) != nil {
		t.Error("the block was created missing a required member")
	}
}

// TestSeedIncompleteSiblingBlocksTheWholeBlock is the SSE guard one level
// deeper: the dropped reference sat in block b, so b must not be created
// even when the walk arrives via a COMPLETE argument nested further down.
func TestSeedIncompleteSiblingBlocksTheWholeBlock(t *testing.T) {
	partner := at(str("aws:kms"), "b", "algorithm")
	partner.Incomplete = true
	g := seedGen("aws_thing",
		partner,
		at(str("deep"), "b", "c", "x"),
	)
	body := hclwrite.NewEmptyFile().Body()
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"b": {
			Nesting: configschema.NestingList,
			Block: *blockWith(
				map[string]*configschema.Attribute{"algorithm": optString()},
				map[string]*configschema.NestedBlock{
					"c": {
						Nesting: configschema.NestingList,
						Block:   *blockWith(map[string]*configschema.Attribute{"x": optString()}, nil),
					},
				}),
		},
	})

	if applied := g.seedFromExample(body, schema, "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v; block b dropped a reference sibling, and a deeper complete argument created it anyway", applied)
	}
	if body.FirstMatchingBlock("b", nil) != nil {
		t.Error("the block with the dropped reference was created")
	}
}

// TestSeedRefusesAMapNestedBlock: a map-nested block's instances are keyed,
// and the page's path carries no key.
func TestSeedRefusesAMapNestedBlock(t *testing.T) {
	g := seedGen("aws_thing", at(str("x"), "keyed", "mode"))
	body := hclwrite.NewEmptyFile().Body()
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"keyed": {
			Nesting: configschema.NestingMap,
			Block:   *blockWith(map[string]*configschema.Attribute{"mode": optString()}, nil),
		},
	})

	if applied := g.seedFromExample(body, schema, "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v through a map-nested block with no key", applied)
	}
}

// TestSeedIncompleteNeverCreatesAnOptionalAtMostOneBlock is the SSE guard
// against exactly the widening above, written before it: the real
// apply_server_side_encryption_by_default is an OPTIONAL MaxItems-1 list
// block (MinItems 0), so once complete arguments may create that shape, an
// incomplete one must still refuse - its own block dropped a reference
// sibling (the KMS key ARN), and creating the block plants the security
// configuration with its reference half missing.
func TestSeedIncompleteNeverCreatesAnOptionalAtMostOneBlock(t *testing.T) {
	arg := at(str("aws:kms"), "apply_server_side_encryption_by_default", "sse_algorithm")
	arg.Incomplete = true
	g := seedGen("aws_thing", arg)
	body := hclwrite.NewEmptyFile().Body()
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"apply_server_side_encryption_by_default": {
			Nesting: configschema.NestingList, MinItems: 0, MaxItems: 1,
			Block: *blockWith(map[string]*configschema.Attribute{"sse_algorithm": optString()}, nil),
		},
	})

	if applied := g.seedFromExample(body, schema, "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v; an incomplete argument created the optional block whose reference half was dropped", applied)
	}
	if body.FirstMatchingBlock("apply_server_side_encryption_by_default", nil) != nil {
		t.Error("the block was created for an incomplete argument")
	}
}

// TestSeedRefusesTwoRenderedInstances. "Which element does this belong to"
// has two answers once the body holds two instances, and picking the first
// is the kind of guess that produces a configuration nobody can explain
// later. (The generic pass renders MinItems instances of a required block,
// so a MinItems-2 schema arrives here exactly like this.)
func TestSeedRefusesTwoRenderedInstances(t *testing.T) {
	g := seedGen("aws_thing", at(str("x"), "rule", "id"))
	body := hclwrite.NewEmptyFile().Body()
	body.AppendNewBlock("rule", nil)
	body.AppendNewBlock("rule", nil)
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"rule": {
			Nesting: configschema.NestingList, MinItems: 2,
			Block: *blockWith(map[string]*configschema.Attribute{"id": optString()}, nil),
		},
	})

	if applied := g.seedFromExample(body, schema, "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v into a block the body holds twice", applied)
	}
}

// TestSeedNamesNoResourceType is issue #136's own measure of success. If
// this pass has to know about a specific aws_* type, the override was moved
// rather than retired.
func TestSeedNamesNoResourceType(t *testing.T) {
	raw, err := os.ReadFile("seed.go")
	if err != nil {
		t.Fatalf("reading seed.go: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		code := line
		if i := strings.Index(code, "//"); i >= 0 {
			code = code[:i] // a comment may cite an example; control flow may not
		}
		if strings.Contains(code, `"aws_`) {
			t.Errorf("seed.go names a resource type in code: %s\n"+
				"Retiring an override by hardcoding its value here is the same override with a "+
				"different address.", strings.TrimSpace(line))
		}
	}
}
