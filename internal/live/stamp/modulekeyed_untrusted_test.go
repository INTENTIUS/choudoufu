// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// This file is GitHub issue #379: a marker-only resource inside a keyed
// module call, whose tags argument is set but carries no marker anyone can
// see, used to be exempted from the unmarked-apply refusal by name.
//
// [stamper.moduleKeyedResource] never writes a marker into a body a module
// call's several instances share - that is #378's subject and deliberate -
// and files one of two skips. Which one was decided by "does this body set a
// tags argument at all", so `tags = var.tags`, what essentially every
// third-party child module writes, reported SkipModuleKeyedTrusted, a reason
// whose own doc comment said the markers were the operator's hand-written
// ones. internal/command's statelessStampGaps then exempts that reason by
// name. For a type whose instances can only ever be found by their marker
// that exemption covered a resource about to be created permanently
// unfindable, with no diagnostic of any severity anywhere in the run - audit
// finding C2's shape, reached down a different road.
//
// The fix is not a new stamping capability: nothing here can compute a
// per-instance marker inside a keyed module call, and #379 does not try to.
// It is that the trust has to be EARNED for the population that cannot
// survive being wrong about it. [keyedMarkersMissing] asks whether the marker
// keys are written as literal keys in the body; a must-stamp resource whose
// tags argument is a variable, a function call or anything else this run
// cannot read gets the same refusal a resource with no tags argument at all
// has always got.
//
// The tests below pin both directions by value, because only one of them is
// the safety property: a refusal that fires everywhere is not a fix, it is an
// outage.

// TestModuleKeyedTrustIsEarned_handWrittenMarkersStayTrusted is the direction
// that must NOT move: the hand-stamped idiom live/LIMITATIONS.md documents,
// on a type mustStamp is true for, is still trusted and still silent.
//
// The markers are asserted by rendered value rather than by the skip alone.
// The skip says what the pass decided; these two tags are what the provider
// is handed, and a check that only read the decision would pass just as
// happily if the address were wrong.
func TestModuleKeyedTrustIsEarned_handWrittenMarkersStayTrusted(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags string
	}{
		{
			name: "an object literal",
			tags: `tags = {
    tofu-estate  = "repeat-unit"
    tofu-address = "module.sites[\"${var.name}\"].aws_eip.app"
  }`,
		},
		{
			// The same evidence through the other expression this pass can
			// read: the child module's own var.tags, with the markers merged
			// over it. Nothing about the variable half is readable and it does
			// not have to be - the marker keys are right there in the object.
			name: "a merge() whose object argument sets them",
			tags: `tags = merge(var.tags, {
    tofu-estate  = "repeat-unit"
    tofu-address = "module.sites[\"${var.name}\"].aws_eip.app"
  })`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadTree(t, map[string]string{
				"main.tf": `
module "sites" {
  source   = "./impl"
  for_each = toset(["a", "b"])
  name     = each.key
  tags     = { Example = "x" }
}
`,
				"impl/main.tf": `
variable "name" { type = string }
variable "tags" { type = map(string) }

resource "aws_eip" "app" {
  ` + tc.tags + `
}
`,
			})

			res, diags := Stamp(t.Context(), Request{
				Estate:         "repeat-unit",
				Config:         cfg,
				Schemas:        testSchemas(),
				NeedsDiscovery: needsDiscovery("module.sites.aws_eip.app"),
			})
			if len(diags) != 0 {
				t.Fatalf("a hand-stamped marker-only resource inside a for_each'd module now reports %d diagnostic(s); #379's fix has started refusing the case it exists to preserve:\n%s",
					len(diags), diags.Err())
			}
			if !hasSkip(res, "module.sites.aws_eip.app", SkipModuleKeyedTrusted) {
				t.Errorf("want %s, got %+v", SkipModuleKeyedTrusted, res.Skipped)
			}

			got := evalTags(t, cfg.Children["sites"], "aws_eip.app", map[string]cty.Value{
				"var": cty.ObjectVal(map[string]cty.Value{
					"name": cty.StringVal("a"),
					"tags": cty.MapVal(map[string]cty.Value{"Example": cty.StringVal("x")}),
				}),
			})
			want := map[string]string{
				TagEstate:  "repeat-unit",
				TagAddress: `module.sites["a"].aws_eip.app`,
			}
			if tc.name == "a merge() whose object argument sets them" {
				want["Example"] = "x"
			}
			assertTags(t, got, want)
		})
	}
}

// TestModuleKeyedTrustIsEarned_aVariableIsRefused is #379 itself, and the
// direction that had to move: the same module, the same marker-only type,
// with a tags argument nothing can read.
//
// The error is the point. There is no stamp to assert here and there will not
// be one until #378 is closed - what this pins is that the run stops, that it
// stops with the severity a resource that can never be found again earns, and
// that the message says which markers are missing and what to write instead.
// The rendered tags are asserted too, by value, so the test cannot pass on a
// refusal that fired while a marker was in fact present.
func TestModuleKeyedTrustIsEarned_aVariableIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags string
		want string
	}{
		{
			// The corpus shape: terraform-aws-modules/ecs's
			// modules/container-definition, and near enough every other
			// third-party child module, writes exactly this.
			name: "tags come straight from a variable",
			tags: `tags = var.tags`,
			want: "no tofu-estate or tofu-address is written as a literal key",
		},
		{
			// A merge() is readable, and reading it is what finds that the
			// markers are not in it. The variable half could carry them; the
			// point of the refusal is that nothing here can tell.
			name: "a merge() of a variable and unrelated tags",
			tags: `tags = merge(var.tags, { Name = "app" })`,
			want: "no tofu-estate or tofu-address is written as a literal key",
		},
		{
			// An object literal with no marker in it. "Sets a tags argument"
			// was the whole of the old test and this case passed it while
			// being as unmarked as the two above.
			name: "an object literal with no marker in it",
			tags: `tags = { Name = "app" }`,
			want: "no tofu-estate or tofu-address is written as a literal key",
		},
		{
			// Half the evidence is not evidence: discovery lists an estate by
			// tofu-estate and binds the instance by tofu-address, so an object
			// carrying only one of them is not findable either. The message
			// names the half that is missing rather than both.
			name: "only the estate marker is written",
			tags: `tags = { tofu-estate = "repeat-unit" }`,
			want: "no tofu-address is written as a literal key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadTree(t, map[string]string{
				"main.tf": `
module "sites" {
  source   = "./impl"
  for_each = toset(["a", "b"])
  tags     = { Example = "x" }
}
`,
				"impl/main.tf": `
variable "tags" { type = map(string) }

resource "aws_eip" "app" {
  ` + tc.tags + `
}
`,
			})

			res, diags := Stamp(t.Context(), Request{
				Estate:         "repeat-unit",
				Config:         cfg,
				Schemas:        testSchemas(),
				NeedsDiscovery: needsDiscovery("module.sites.aws_eip.app"),
			})
			if !diags.HasErrors() {
				t.Fatalf("a marker-only resource inside a for_each'd module call with %s applied with no diagnostic; #379 has regressed and this resource would be created unfindable. Skips: %+v",
					tc.tags, res.Skipped)
			}
			assertDiagContains(t, diags,
				SummaryUnmarkedApply,
				"module.sites.aws_eip.app",
				tc.want,
				// The refusal has to name what to do about it, and the
				// unfindability that makes it an error rather than a warning.
				"tofu-address = ...",
				"the ownership marker is the only thing any later run can find it by",
			)

			// SkipModuleKeyed, not SkipModuleKeyedTrusted: internal/command's
			// statelessStampGaps exempts the trusted reason by name, so the
			// reason recorded here is the difference between this resource
			// being reported a second time by the command and being waved
			// through by it.
			if !hasSkip(res, "module.sites.aws_eip.app", SkipModuleKeyed) {
				t.Errorf("want %s, got %+v", SkipModuleKeyed, res.Skipped)
			}

			// And no marker was written by the refusal: the pass still cannot
			// compute one inside a keyed module call, which is why the honest
			// outcome is stopping rather than stamping. #378 is the other half.
			got := evalTags(t, cfg.Children["sites"], "aws_eip.app", map[string]cty.Value{
				"var": cty.ObjectVal(map[string]cty.Value{
					"tags": cty.MapVal(map[string]cty.Value{"Example": cty.StringVal("x")}),
				}),
			})
			if v, ok := got[TagAddress]; ok {
				t.Errorf("%s = %q was rendered after all, so this refusal fired over a resource that had its marker", TagAddress, v)
			}
		})
	}
}

// TestModuleKeyedTrustIsEarned_findableTypesAreUnaffected holds the blast
// radius down to the population the safety rule is about.
//
// A resource that is NOT marker-only has another handle - a name AWS refuses
// to issue twice, an identity this configuration states outright - and a
// missing marker costs it nothing this pass can fix by stopping the run. The
// #379 check therefore runs only when [stamper.mustStamp] is true, and this
// asserts it: the identical `tags = var.tags` inside the identical for_each'd
// module call, with no NeedsDiscovery entry, is still trusted and still
// silent. Turning that population's skip into an error would refuse most
// third-party module trees in existence over a marker they never needed.
func TestModuleKeyedTrustIsEarned_findableTypesAreUnaffected(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "sites" {
  source   = "./impl"
  for_each = toset(["a", "b"])
  tags     = { Example = "x" }
}
`,
		"impl/main.tf": `
variable "tags" { type = map(string) }

resource "aws_eip" "app" {
  tags = var.tags
}
`,
	})

	res, diags := Stamp(t.Context(), Request{Estate: "repeat-unit", Config: cfg, Schemas: testSchemas()})
	if len(diags) != 0 {
		t.Fatalf("a resource with another way to be found now reports %d diagnostic(s) for tags = var.tags inside a for_each'd module; #379's check has escaped the must-stamp population:\n%s",
			len(diags), diags.Err())
	}
	if !hasSkip(res, "module.sites.aws_eip.app", SkipModuleKeyedTrusted) {
		t.Errorf("want %s, got %+v", SkipModuleKeyedTrusted, res.Skipped)
	}
}
