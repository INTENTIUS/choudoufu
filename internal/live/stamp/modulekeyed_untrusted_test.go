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
// Stamping never wrote a marker into a body a module call's several instances
// share - that was #378's subject - and filed one of two skips. Which one was
// decided by "does this body set a tags argument at all", so `tags =
// var.tags`, what essentially every third-party child module writes, reported
// SkipModuleKeyedTrusted, a reason whose own doc comment said the markers were
// the operator's hand-written ones. internal/command's statelessStampGaps then
// exempts that reason by name. For a type whose instances can only ever be
// found by their marker that exemption covered a resource about to be created
// permanently unfindable, with no diagnostic of any severity anywhere in the
// run - audit finding C2's shape, reached down a different road.
//
// #379's own fix earned the trust rather than assuming it:
// [collectVisibleTagKeys] asks whether the marker keys are written as literal
// keys in the body, and a must-stamp resource whose tags argument is a
// variable, a function call or anything else this run cannot read got the same
// refusal a resource with no tags argument at all has always got.
//
// #378 then removed the premise for nearly all of that population, and this
// file moved with it. There IS a stamping capability now:
// [markers.ModulePrefixAttr] names the module instance, so a resource whose
// body declares no tofu-address is stamped rather than refused, and the four
// shapes #379 refused - `tags = var.tags`, a merge() of one, an object with no
// marker in it, an object with only tofu-estate - are all marked correctly
// instead. Their tests below assert the rendered marker by value, which is a
// strictly stronger statement than the refusal was.
//
// What is left of #379 is one shape, and it is the one #378 cannot reach: a
// body writing tofu-address BY HAND and not tofu-estate, on a marker-only
// type. This pass will not touch a hand-written address, and discovery lists
// an estate by tofu-estate before it binds an instance by tofu-address, so
// that instance would be applied with an address nothing looks for. It keeps
// the refusal, and [TestModuleKeyedTrustIsEarned_aHalfWrittenMarkerIsRefused]
// is what holds it.
//
// The tests below pin every direction by value, because only one of them is
// the safety property: a refusal that fires everywhere is not a fix, it is an
// outage, and a marker that is written but wrong is worse than both.

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

// TestModuleKeyedTrustIsEarned_aVariableIsNowMarked is what #378 did to
// #379's own population: the same module, the same marker-only type, the same
// four tags arguments that carried no visible marker - and now a correct
// per-module-instance marker on every one of them.
//
// This test used to assert the refusal. Asserting the marker instead is
// strictly stronger: a refusal says the run stopped, and these say what the
// provider is handed, for two different instances of the call, by exact
// value. A resource that can only ever be found by its marker now HAS one, so
// there is nothing left to refuse.
func TestModuleKeyedTrustIsEarned_aVariableIsNowMarked(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tags  string
		extra map[string]string
	}{
		{
			// The corpus shape: terraform-aws-modules/ecs's
			// modules/container-definition, and near enough every other
			// third-party child module, writes exactly this.
			name:  "tags come straight from a variable",
			tags:  `tags = var.tags`,
			extra: map[string]string{"Example": "x"},
		},
		{
			name:  "a merge() of a variable and unrelated tags",
			tags:  `tags = merge(var.tags, { Name = "app" })`,
			extra: map[string]string{"Example": "x", "Name": "app"},
		},
		{
			name:  "an object literal with no marker in it",
			tags:  `tags = { Name = "app" }`,
			extra: map[string]string{"Name": "app"},
		},
		{
			// Half the evidence was not evidence, and it is not needed as
			// evidence any more: the estate marker the author wrote is
			// verified as the constant it is, and the address this pass could
			// not compute before is now written beside it.
			name:  "only the estate marker is written",
			tags:  `tags = { tofu-estate = "repeat-unit" }`,
			extra: map[string]string{},
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
			assertNoErrors(t, diags)
			if hasSkip(res, "module.sites.aws_eip.app", SkipModuleKeyed) ||
				hasSkip(res, "module.sites.aws_eip.app", SkipModuleKeyedTrusted) {
				t.Errorf("a resource #378 can now stamp is still being skipped: %+v", res.Skipped)
			}

			callerTags := map[string]cty.Value{"var": cty.ObjectVal(map[string]cty.Value{
				"tags": cty.MapVal(map[string]cty.Value{"Example": cty.StringVal("x")}),
			})}
			for _, key := range []string{"a", "b"} {
				want := map[string]string{
					TagEstate:  "repeat-unit",
					TagAddress: "module.sites:" + key + ".aws_eip.app",
				}
				for k, v := range tc.extra {
					want[k] = v
				}
				got := evalTags(t, cfg.Children["sites"], "aws_eip.app",
					withModulePrefix(t, callerTags, `module.sites["`+key+`"]`))
				assertTags(t, got, want)
			}
		})
	}
}

// TestModuleKeyedTrustIsEarned_aHalfWrittenMarkerIsRefused is all that
// remains of #379's refusal, and it is the shape #378's fix cannot reach.
//
// The author wrote tofu-address by hand. This pass does not overwrite a
// hand-written marker value anywhere, keyed module or not, so it will not
// replace that address with one of its own - and having declined to touch the
// address it must not quietly add a tofu-estate beside it either, because the
// two together are one hand-written marker and half of it is the author's
// business. Marker discovery lists an estate by tofu-estate before it binds an
// instance by tofu-address, so this instance would be applied with an address
// nothing ever looks for. On a type that can only ever be found by its marker,
// that is the unrecoverable case, so it refuses.
func TestModuleKeyedTrustIsEarned_aHalfWrittenMarkerIsRefused(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "sites" {
  source   = "./impl"
  for_each = toset(["a", "b"])
  name     = each.key
}
`,
		"impl/main.tf": `
variable "name" { type = string }

resource "aws_eip" "app" {
  tags = {
    tofu-address = "module.sites[\"${var.name}\"].aws_eip.app"
  }
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
		t.Fatalf("a marker-only resource with a hand-written tofu-address and no tofu-estate applied with no diagnostic; #379 has regressed. Skips: %+v", res.Skipped)
	}
	assertDiagContains(t, diags,
		SummaryUnmarkedApply,
		"module.sites.aws_eip.app",
		"writes no tofu-estate",
		"the ownership marker is the only thing any later run can find it by",
	)
	// SkipModuleKeyed, not SkipModuleKeyedTrusted: internal/command's
	// statelessStampGaps exempts the trusted reason by name, so the reason
	// recorded here is the difference between this resource being reported a
	// second time by the command and being waved through by it.
	if !hasSkip(res, "module.sites.aws_eip.app", SkipModuleKeyed) {
		t.Errorf("want %s, got %+v", SkipModuleKeyed, res.Skipped)
	}
	// And nothing was written over the author's own address.
	got := evalTags(t, cfg.Children["sites"], "aws_eip.app", map[string]cty.Value{
		"var": cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("a")}),
	})
	assertTags(t, got, map[string]string{TagAddress: `module.sites["a"].aws_eip.app`})
}

// TestModuleKeyedTrustIsEarned_findableTypesAreMarkedToo is the other half of
// the blast radius, and it moved the good way.
//
// A resource that is NOT marker-only has another handle - a name AWS refuses
// to issue twice, an identity this configuration states outright - so #379's
// refusal deliberately never fired for it: its missing marker was #378's
// subject, not a reason to stop the run. #378 does not stop the run either.
// It writes the marker. So the identical `tags = var.tags` inside the
// identical for_each'd module call, with no NeedsDiscovery entry, is now
// stamped and still silent, and this asserts the marker by value for both
// instances of the call.
func TestModuleKeyedTrustIsEarned_findableTypesAreMarkedToo(t *testing.T) {
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
		t.Fatalf("a resource with another way to be found now reports %d diagnostic(s) for tags = var.tags inside a for_each'd module; a check has escaped its population:\n%s",
			len(diags), diags.Err())
	}
	if len(res.Stamped) != 1 {
		t.Fatalf("want the resource stamped, got %+v (skipped: %+v)", res.Stamped, res.Skipped)
	}
	callerTags := map[string]cty.Value{"var": cty.ObjectVal(map[string]cty.Value{
		"tags": cty.MapVal(map[string]cty.Value{"Example": cty.StringVal("x")}),
	})}
	for _, key := range []string{"a", "b"} {
		got := evalTags(t, cfg.Children["sites"], "aws_eip.app",
			withModulePrefix(t, callerTags, `module.sites["`+key+`"]`))
		assertTags(t, got, map[string]string{
			"Example":  "x",
			TagEstate:  "repeat-unit",
			TagAddress: "module.sites:" + key + ".aws_eip.app",
		})
	}
}
