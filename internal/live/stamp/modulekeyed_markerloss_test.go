// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// This file is GitHub issue #378, pinned by VALUE rather than by verdict.
//
// The finding, on live/e2e/corpus-ecs-fargate: after a live-import that
// genuinely stamped them, the stateless replan proposes REMOVING
// tofu-address and tofu-estate from two aws_cloudwatch_log_group instances
// reached through a for_each'd module call -
// module.ecs_service.module.container_definition["fluent-bit"] and
// module.ecs_task_definition.module.container_definition["al2023"] - while
// every other marker-eligible instance in the same plan is only asked to
// gain tofu-slot. The tags really are on the live objects; the plan's
// DESIRED tag set for those two carries no marker at all, so the ordinary
// tags diff renders the difference as a deletion.
//
// The mechanism is entirely in this package, and it is two separate things
// stacked:
//
//  1. A resource under a module call with more than one instance is
//     [stamper.moduleKeyedResource]'s, not [stamper.resource]'s: the
//     module's instances share one configuration body for the resource's
//     tags argument, and no expression writable in the child module can name
//     the parent call's own instance key, so there is no single literal that
//     is right for all of them. Nothing is injected. That much is deliberate
//     and documented (live/LIMITATIONS.md, "child-module").
//
//  2. Which of the two skips that resource gets is decided by hasTags -
//     "does this body set a tags argument at all" - and NOT by whether that
//     argument carries a marker. `tags = var.tags`, the idiom essentially
//     every terraform-aws-modules child module uses, therefore reports
//     SkipModuleKeyedTrusted, "its markers are trusted as written", about a
//     marker that was never written. internal/command's statelessStampGaps
//     exempts that reason precisely because it is supposed to mean "the
//     resource HAS its markers", so nothing downstream reports it either.
//
// The test below pins the marker loss by value. It asserts the CURRENT,
// defective behaviour deliberately: this is a finding, not a fix, and a pin
// that says what the code does today is what makes the next change to it
// visible. Whichever way #378 is eventually closed - by threading the module
// instance's address into the child so a correct per-instance marker can be
// written, or by dropping the instance to the record rung - these
// assertions have to be edited by hand, with the new values spelled out.
//
// The contrast subtest is the load-bearing half. It is the same module, the
// same resource, the same `tags = var.tags`, called WITHOUT for_each, and
// its rendered tags carry both markers by exact value. So the difference is
// the keyed ancestor and nothing else - not the type, not the tags idiom,
// not the count on the resource.
//
// The second half this file used to pin - that the same skip silenced the
// unmarked-apply refusal for a type that can ONLY be found by its marker -
// is GitHub issue #379, and it is fixed: modulekeyed_untrusted_test.go pins
// the refusal, and the hand-written case that must stay trusted, in its
// place. #378's own population is untouched by that fix, and the fixture
// below is why: an aws_cloudwatch_log_group is imported by the name this
// configuration states (live/survey-full.json: required_for_import is
// ["name"]), so it is findable without a marker and NeedsDiscovery is empty
// here exactly as it is for the corpus estate. Stamping still trusts the body
// below and the plan still proposes deleting the markers live-import wrote,
// which is what the subtests here still assert. Losing a marker on a resource
// that can be found again is #378's subject; losing one on a resource that
// cannot is #379's, and only the second is an error.

// TestModuleKeyedTagsFromAVariableRenderNoMarker is #378 itself: the desired
// tag set a plan computes for a taggable resource under a for_each'd module
// call, when that resource sets `tags = var.tags`.
//
// The fixture is the corpus shape, reduced: terraform-aws-modules/ecs's
// modules/container-definition declares
//
//	resource "aws_cloudwatch_log_group" "this" {
//	  count = var.create_cloudwatch_log_group && var.enable_cloudwatch_logging ? 1 : 0
//	  ...
//	  tags = var.tags
//	}
//
// and examples/fargate calls it with for_each over the container names.
func TestModuleKeyedTagsFromAVariableRenderNoMarker(t *testing.T) {
	const impl = `
variable "name" { type = string }
variable "tags" { type = map(string) }

resource "aws_cloudwatch_log_group" "this" {
  count = 1

  name = var.name
  tags = var.tags
}
`
	// The tags the module call passes down, which is every tag the child can
	// see. evalTags is given exactly this, so what it returns is what the
	// provider would be handed for that instance.
	callerTags := map[string]cty.Value{"var": cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("/aws/ecs/ex/fluent-bit"),
		"tags": cty.MapVal(map[string]cty.Value{"Example": cty.StringVal("ex-fargate")}),
	})}

	t.Run("for_each'd call: no marker is rendered, and the skip claims one was written", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "container_definition" {
  source   = "./impl"
  for_each = toset(["fluent-bit", "al2023"])
  name     = each.key
  tags     = { Example = "ex-fargate" }
}
`,
			"impl/main.tf": impl,
		})

		res, diags := Stamp(t.Context(), Request{Estate: "ecs-fargate-crossing", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)

		if !hasSkip(res, "module.container_definition.aws_cloudwatch_log_group.this", SkipModuleKeyedTrusted) {
			t.Errorf("want %s for a resource whose tags come from a variable inside a for_each'd module call, got %+v",
				SkipModuleKeyedTrusted, res.Skipped)
		}

		// The value assertion, and the whole point of this file: the desired
		// tag set carries NO marker. live-import stamped
		// module.container_definition:fluent-bit.aws_cloudwatch_log_group.this:0
		// onto the live object, so a plan whose desired tags are these three
		// keys is a plan that removes it.
		got := evalTags(t, cfg.Children["container_definition"], "aws_cloudwatch_log_group.this", callerTags)
		assertTags(t, got, map[string]string{"Example": "ex-fargate"})
		for _, key := range []string{TagEstate, TagAddress} {
			if v, ok := got[key]; ok {
				t.Errorf("%s = %q is rendered after all; this test's premise (and #378's) has changed and the fixed value belongs here by name", key, v)
			}
		}
	})

	t.Run("the same module without for_each: both markers, by value", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "container_definition" {
  source = "./impl"
  name   = "/aws/ecs/ex/fluent-bit"
  tags   = { Example = "ex-fargate" }
}
`,
			"impl/main.tf": impl,
		})

		res, diags := Stamp(t.Context(), Request{Estate: "ecs-fargate-crossing", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)

		if len(res.Stamped) != 1 || res.Stamped[0].Addr.String() != "module.container_definition.aws_cloudwatch_log_group.this" {
			t.Fatalf("want the one resource stamped, got %+v (skipped: %+v)", res.Stamped, res.Skipped)
		}

		// The same `tags = var.tags` expression, wrapped in a merge() this
		// pass appends its marker object to. The address is the module-
		// qualified one and carries the resource's own count key - exactly
		// the shape corpus-ecs-fargate's other marker-eligible instances get.
		//
		// tofu-slot is absent because this Request declares no Slots table,
		// not because a keyed ancestor is involved: the slot marker is a fact
		// about a live count set (see [stamper.slotExpr]) and neither subtest
		// supplies one. The two markers #378 is about are the two here.
		got := evalTags(t, cfg.Children["container_definition"], "aws_cloudwatch_log_group.this", withCountIndex(callerTags, 0))
		assertTags(t, got, map[string]string{
			"Example":  "ex-fargate",
			TagEstate:  "ecs-fargate-crossing",
			TagAddress: "module.container_definition.aws_cloudwatch_log_group.this:0",
		})
	})
}

// withCountIndex adds count.index to a variable scope, for a fixture whose
// resource carries a count and therefore a per-instance marker.
func withCountIndex(vars map[string]cty.Value, i int) map[string]cty.Value {
	out := make(map[string]cty.Value, len(vars)+1)
	for k, v := range vars {
		out[k] = v
	}
	out["count"] = cty.ObjectVal(map[string]cty.Value{"index": cty.NumberIntVal(int64(i))})
	return out
}
