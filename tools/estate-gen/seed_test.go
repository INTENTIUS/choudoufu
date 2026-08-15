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

func TestSeedSkipsIncompleteArguments(t *testing.T) {
	arg := at(str("aws:kms"), "sse_algorithm")
	arg.Incomplete = true
	g := seedGen("aws_thing", arg)
	body := hclwrite.NewEmptyFile().Body()

	if applied := g.seedFromExample(body, blockWith(map[string]*configschema.Attribute{"sse_algorithm": optString()}, nil), "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v from a block whose cross-resource reference was dropped; the result is a "+
			"configuration the provider rejects", applied)
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

// TestSeedRefusesAMultiInstanceBlock. "Which element does this belong to" is
// a question one page's example does not answer, and picking the first is
// the kind of guess that produces a configuration nobody can explain later.
func TestSeedRefusesAMultiInstanceBlock(t *testing.T) {
	g := seedGen("aws_thing", at(str("x"), "rule", "id"))
	body := hclwrite.NewEmptyFile().Body()
	schema := blockWith(nil, map[string]*configschema.NestedBlock{
		"rule": {
			Nesting: configschema.NestingList, MinItems: 1,
			Block: *blockWith(map[string]*configschema.Attribute{"id": optString()}, nil),
		},
	})

	if applied := g.seedFromExample(body, schema, "aws_thing"); len(applied) != 0 {
		t.Errorf("seeded %v into a block that can hold many instances", applied)
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
