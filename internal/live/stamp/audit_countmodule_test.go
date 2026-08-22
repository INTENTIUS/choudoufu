// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestStamp_countModuleIsAddressedPerInstance is
// [TestStamp_forEachModuleIsAddressedPerInstance]'s missing twin, and it
// failed when it was written.
//
// moduleResourcesFrom decided keyedAncestor from call.ForEach alone. A module
// call that sets count expands the same way - module.sites[0] and
// module.sites[1] are two module instances over one *configs.Config and one
// *hclsyntax.Body - so the pass wrote the literal
// "module.sites.aws_route53_zone.zone" into that one body and every instance
// of the call carried it. [privateBody] cannot help: the sharing here is real,
// not an artifact of the parser cache.
//
// It is worse than GitHub issue #280's original, where the last call's address
// was at least right for one of the N. Identity resolution expands a module
// count (resolver.walkModule via [identity.ChildModuleCountKeys]), so the live
// objects answer to module.sites[0]... and module.sites[1]..., and the single
// address written matches neither. Two live objects, one marker, and no
// instance it belongs to.
//
// The assertions are on the EVALUATED tags, not on Result.Stamped: the pass
// reported this resource as successfully stamped, so a verdict-level check
// called the defect a success.
//
// Issue #378 changed the answer from a refusal to a per-instance marker, and
// this test's own question is unchanged by that: do module.sites[0] and
// module.sites[1] carry the same string. [markers.ModulePrefixAttr] makes the
// module half of the address an interpolation, so they do not - and the
// external oracle below is still the one that matters, because it is identity
// resolution, not this pass, that decides which addresses the live objects
// will be searched for.
func TestStamp_countModuleIsAddressedPerInstance(t *testing.T) {
	t.Run("directly count'd", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "sites" {
  source = "./impl"
  count  = 2
  name   = "site-${count.index}"
}
`,
			"impl/main.tf": `
variable "name" { type = string }

resource "aws_route53_zone" "zone" {
  name = var.name
}
`,
		})

		// The external source, so this test is not just agreeing with its
		// own rule: identity resolution is what decides the addresses the
		// live objects will be searched for, and it expands a module count
		// into keyed instances. Any single literal this pass could write is
		// therefore wrong for every one of them.
		keys, diag := identity.ChildModuleCountKeys(t.Context(), cfg.Module, `module "sites"`, cfg.Module.ModuleCalls["sites"].Count)
		if diag != nil {
			t.Fatalf("resolving the count'd module's keys: %s", diag.Detail)
		}
		if len(keys) != 2 || keys[0] == addrs.NoKey {
			t.Fatalf("identity resolution expands module.sites to %v; this test's premise is that it is keyed", keys)
		}

		res, diags := Stamp(t.Context(), Request{Estate: "repeat-unit", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)

		if len(res.Stamped) != 1 {
			t.Fatalf("want the resource inside the count'd module stamped, got %+v (skipped: %+v)", res.Stamped, res.Skipped)
		}

		// One tag map per key identity resolution just produced, so the two
		// sides of the comparison come from the same source rather than from
		// this test's own idea of what the keys are.
		seen := map[string]string{}
		for _, key := range keys {
			inst := addrs.RootModuleInstance.Child("sites", key)
			tags := evalTags(t, cfg.Children["sites"], "aws_route53_zone.zone", withModulePrefix(t, nil, inst.String()))
			seen[inst.String()] = tags[TagAddress]
		}
		want := map[string]string{
			"module.sites[0]": "module.sites:0.aws_route53_zone.zone",
			"module.sites[1]": "module.sites:1.aws_route53_zone.zone",
		}
		for inst, wantAddr := range want {
			if seen[inst] != wantAddr {
				t.Errorf("%s for %s = %q, want %q", TagAddress, inst, seen[inst], wantAddr)
			}
		}
		if seen["module.sites[0]"] == seen["module.sites[1]"] {
			t.Errorf("both instances of the count'd call carry %s = %q - two live objects, one marker, and no instance it belongs to",
				TagAddress, seen["module.sites[0]"])
		}
	})

	t.Run("count'd through an ancestor", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "sites" {
  source = "./wrap"
  count  = 2
  name   = "site-${count.index}"
}
`,
			"wrap/main.tf": `
variable "name" { type = string }

module "leaf" {
  source = "../leaf"
  name   = var.name
}
`,
			"leaf/main.tf": `
variable "name" { type = string }

resource "aws_route53_zone" "zone" {
  name = var.name
}
`,
		})

		res, diags := Stamp(t.Context(), Request{Estate: "repeat-unit", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)

		if len(res.Stamped) != 1 {
			t.Fatalf("want the leaf resource stamped, got %+v (skipped: %+v)", res.Stamped, res.Skipped)
		}
		for i, want := range map[string]string{
			"module.sites[0].module.leaf": "module.sites:0.module.leaf.aws_route53_zone.zone",
			"module.sites[1].module.leaf": "module.sites:1.module.leaf.aws_route53_zone.zone",
		} {
			tags := evalTags(t, cfg.Children["sites"].Children["leaf"], "aws_route53_zone.zone", withModulePrefix(t, nil, i))
			if tags[TagAddress] != want {
				t.Errorf("%s for %s = %q, want %q; keyedAncestor did not reach the leaf", TagAddress, i, tags[TagAddress], want)
			}
		}
	})

	// A count'd module whose resource carries its own tags is the documented
	// hand-stamped idiom, exactly as for for_each: trusted, not overwritten,
	// and not reported as a gap.
	t.Run("hand-written tags are trusted", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "sites" {
  source = "./impl"
  count  = 2
  name   = "site-${count.index}"
  idx    = count.index
}
`,
			"impl/main.tf": `
variable "name" { type = string }
variable "idx" { type = number }

resource "aws_route53_zone" "zone" {
  name = var.name
  tags = {
    tofu-estate  = "repeat-unit"
    tofu-address = "module.sites[${var.idx}].aws_route53_zone.zone"
  }
}
`,
		})

		res, diags := Stamp(t.Context(), Request{Estate: "repeat-unit", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)

		if !hasSkip(res, "module.sites.aws_route53_zone.zone", SkipModuleKeyedTrusted) {
			t.Errorf("a hand-stamped resource inside a count'd module was not skipped as %s: %+v", SkipModuleKeyedTrusted, res.Skipped)
		}
	})
}
