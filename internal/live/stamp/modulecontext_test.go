// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file exists because internal/live/stamp had 45 test files and not one
// of them drove a module tree: every fixture was a single root-module file.
// Every behaviour in the package whose answer depends on being inside a
// module was therefore in the position [moduleResource.keyedAncestor] was
// found in - possibly correct, definitely unmeasured. Each test below was
// established by deleting or unqualifying the mechanism it names and
// confirming that the whole of internal/live/... and internal/command stayed
// green without it.
//
// Every assertion is on EVALUATED tags, never on Result.Stamped: a check that
// the pass reported success proves nothing about the string it wrote, and
// that distinction is exactly what let GitHub issue #280 ship.

// TestStamp_countModuleInstanceIsAddressed is the defect this file was opened
// for, and it is issue #280's defect by a third route.
//
// A module call with count = 1 is admitted (issue #195, live/LIMITATIONS.md's
// "child-module", and live/e2e/limits/child-module/counted). Its resources
// are addressed by internal/live/identity as "module.counted[0].aws_vpc.main"
// - resolve.go's walkModule recurses once per instance key, and
// identity.ChildModuleCountKeys makes that key IntKey(0). Stamping, which
// read only a module call's for_each, qualified the same resource with the
// UNKEYED module path and wrote "module.counted.aws_vpc.main". That marker
// names nothing: discovery never looks for it, and the estate is unadoptable
// from the first apply.
//
// The count > 1 case is worse still, and is the subtest below it: N real
// cloud objects, one literal address, all of them claiming to be the same
// resource instance.
func TestStamp_countModuleInstanceIsAddressed(t *testing.T) {
	t.Run("count = 1 is stamped with its instance key", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "counted" {
  source = "./impl"
  count  = 1
}
`,
			"impl/main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.44.0.0/16"
}
`,
		})

		res, diags := Stamp(t.Context(), Request{Estate: "mod-unit", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)
		if len(res.Stamped) != 1 {
			t.Fatalf("stamped %d resources, want 1: %+v (skips %+v)", len(res.Stamped), res.Stamped, res.Skipped)
		}

		tags := evalTags(t, cfg.Children["counted"], "aws_vpc.main", nil)
		assertTags(t, tags, map[string]string{
			"tofu-estate": "mod-unit",
			// The escaped form of module.counted[0].aws_vpc.main: an
			// instance key is written as ":" plus the key, so the module
			// call's own key escapes exactly as a resource's own does.
			"tofu-address": discovery.EscapeAddress("module.counted[0].aws_vpc.main"),
		})
	})

	// The address the marker carries and the address identity resolution
	// computes are asserted to be the same STRING, derived from the same
	// configuration, rather than both hardcoded here. A test that pins two
	// hand-written spellings cannot notice the two sides drifting apart,
	// which is the only way this defect can come back.
	t.Run("the stamped address is the one identity resolves", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "counted" {
  source = "./impl"
  count  = 1
}
`,
			"impl/main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.44.0.0/16"
}
`,
		})

		_, diags := Stamp(t.Context(), Request{Estate: "mod-unit", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)
		stamped := evalTags(t, cfg.Children["counted"], "aws_vpc.main", nil)["tofu-address"]

		// The same walk internal/live/identity performs, over the same
		// configuration: the module call's own count keys, then the
		// resource's address under that instance.
		keys, diag := identity.ChildModuleCountKeys(t.Context(), cfg.Module, `module "counted"`, cfg.Module.ModuleCalls["counted"].Count)
		if diag != nil {
			t.Fatalf("the fixture's own count is not statically evaluable: %s", diag.Detail)
		}
		if len(keys) != 1 {
			t.Fatalf("the fixture's count expands to %d instances, want 1", len(keys))
		}
		rc := cfg.Children["counted"].Module.ManagedResources["aws_vpc.main"]
		want := rc.Addr().Absolute(addrs.RootModuleInstance.Child("counted", keys[0])).String()

		// discovery.AddressMatches is the comparison the live path itself
		// makes between a marker read off a cloud object and an address
		// resolved from configuration. Asserting through it, rather than
		// against a hand-written escaped string, is what makes this test
		// notice the two sides drifting rather than notice the escaping
		// changing.
		if !discovery.AddressMatches(stamped, want) {
			t.Errorf("the marker says %q and identity resolution says %q (escaped: %q); a marker discovery never looks for is a wrong marker on a real object, not a missing one", stamped, want, discovery.EscapeAddress(want))
		}
		if stamped != discovery.EscapeAddress(want) {
			t.Errorf("the marker is %q and the escaped resolved address is %q", stamped, discovery.EscapeAddress(want))
		}
	})

	// The read side of the same marker, which is a different property from
	// the two above and was wrong while both of them passed.
	//
	// AddressMatches compares two ESCAPED strings, so it is blind to what the
	// marker decodes back to: escaping is lossy about an instance key's type,
	// and module.counted[0] and module.counted["0"] escape to the same
	// "module.counted:0". discovery.UnescapeAddress is the other direction,
	// and it is not decoration - internal/live/discovery's classifyOrphans
	// runs it on the marker of every owned-but-undeclared resource and puts
	// the result in the identity.Resolution a destroy is planned and printed
	// at. It used to decode every module step as a string key, on the premise
	// that count on a module block was refused permanently; issue #195
	// retired the premise, and the subtests above are exactly the pass that
	// made it false. So the marker this fixture stamps came back as
	// module.counted["0"].aws_vpc.main - an address this configuration never
	// had.
	t.Run("the stamped marker decodes back to the address it was stamped for", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "counted" {
  source = "./impl"
  count  = 1
}
`,
			"impl/main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.44.0.0/16"
}
`,
		})

		_, diags := Stamp(t.Context(), Request{Estate: "mod-unit", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)
		stamped := evalTags(t, cfg.Children["counted"], "aws_vpc.main", nil)["tofu-address"]
		if stamped == "" {
			t.Fatal("nothing was stamped onto the count'd module's resource")
		}

		keys, diag := identity.ChildModuleCountKeys(t.Context(), cfg.Module, `module "counted"`, cfg.Module.ModuleCalls["counted"].Count)
		if diag != nil {
			t.Fatalf("the fixture's own count is not statically evaluable: %s", diag.Detail)
		}
		rc := cfg.Children["counted"].Module.ManagedResources["aws_vpc.main"]
		want := rc.Addr().Absolute(addrs.RootModuleInstance.Child("counted", keys[0]))

		back, ok := discovery.UnescapeAddress(stamped)
		if !ok {
			t.Fatalf("the stamped marker %q does not unescape at all", stamped)
		}
		if back.String() != want.String() {
			t.Errorf("the marker %q decodes to %s, but it was stamped for %s; removal planning prints that address and enters the prior state at it",
				stamped, back, want)
		}
		if len(back.Module) != 1 {
			t.Fatalf("the marker decodes to module path %s, want one step", back.Module)
		}
		if _, isInt := back.Module[0].InstanceKey.(addrs.IntKey); !isInt {
			t.Errorf("the decoded module step is keyed %T, want the addrs.IntKey the count expansion uses", back.Module[0].InstanceKey)
		}
	})

	t.Run("count > 1 is refused, not stamped with one literal", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "sites" {
  source = "./impl"
  count  = 3
}
`,
			"impl/main.tf": `
resource "aws_route53_zone" "zone" {
  name = "example.test"
}
`,
		})

		res, diags := Stamp(t.Context(), Request{Estate: "mod-unit", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)

		if !hasSkip(res, "module.sites.aws_route53_zone.zone", SkipModuleKeyed) {
			t.Errorf("a resource inside a count = 3 module was not skipped as %s: %+v", SkipModuleKeyed, res.Skipped)
		}
		if len(res.Stamped) != 0 {
			t.Fatalf("a resource inside a count = 3 module was stamped with a literal address, which is one address for three real objects: %+v", res.Stamped)
		}
		if tags := evalTags(t, cfg.Children["sites"], "aws_route53_zone.zone", nil); len(tags) != 0 {
			t.Errorf("markers reached a count-expanded module's shared body: %v", tags)
		}
	})

	// The escalation, not just the skip. aws_route53_zone is found only by
	// its marker, so an apply with no marker on it creates an object nothing
	// can ever recover - which is the whole reason moduleKeyedResource
	// escalates rather than staying quiet.
	t.Run("count > 1 escalates for a marker-only type", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "sites" {
  source = "./impl"
  count  = 2
}
`,
			"impl/main.tf": `
resource "aws_route53_zone" "zone" {
  name = "example.test"
}
`,
		})

		_, diags := Stamp(t.Context(), Request{
			Estate:         "mod-unit",
			Config:         cfg,
			Schemas:        testSchemas(),
			NeedsDiscovery: needsDiscovery("module.sites.aws_route53_zone.zone"),
		})
		if !diags.HasErrors() {
			t.Fatal("a marker-only resource inside a count = 2 module was left unmarked without an error")
		}
		assertDiagContains(t, diags, "module.sites.aws_route53_zone.zone", "more than one instance")
	})

	// count = 0 is the "count = var.enabled ? 1 : 0" idiom with the switch
	// off. There are no instances, so there is nothing to mark and nothing to
	// complain about: filing the must-stamp error here would refuse a
	// configuration whose module the operator has deliberately turned off.
	t.Run("count = 0 has no instances and raises nothing", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
variable "enabled" {
  type    = bool
  default = false
}

module "maybe" {
  source = "./impl"
  count  = var.enabled ? 1 : 0
}
`,
			"impl/main.tf": `
resource "aws_route53_zone" "zone" {
  name = "example.test"
}
`,
		})

		res, diags := Stamp(t.Context(), Request{
			Estate:         "mod-unit",
			Config:         cfg,
			Schemas:        testSchemas(),
			NeedsDiscovery: needsDiscovery("module.maybe.aws_route53_zone.zone"),
		})
		assertNoErrors(t, diags)
		if len(res.Stamped) != 0 {
			t.Errorf("a module with count = 0 had a resource stamped: %+v", res.Stamped)
		}
		if len(res.Skipped) != 0 {
			t.Errorf("a module with count = 0 filed a skip for a resource that has no instances: %+v", res.Skipped)
		}
		if tags := evalTags(t, cfg.Children["maybe"], "aws_route53_zone.zone", nil); len(tags) != 0 {
			t.Errorf("markers reached a module with no instances: %v", tags)
		}
	})

	// A count'd call inside a static call, and a static call inside a count'd
	// call: the instance key has to survive both directions of nesting, and
	// the key belongs to the call that set count, not to the depth.
	t.Run("the instance key survives nesting in both directions", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "outer" {
  source = "./outer"
  count  = 1
}
`,
			"outer/main.tf": `
module "inner" {
  source = "../inner"
}

resource "aws_vpc" "here" {
  cidr_block = "10.1.0.0/16"
}
`,
			"inner/main.tf": `
resource "aws_vpc" "deep" {
  cidr_block = "10.2.0.0/16"
}
`,
		})

		_, diags := Stamp(t.Context(), Request{Estate: "mod-unit", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)

		outer := cfg.Children["outer"]
		for _, tc := range []struct {
			cfg  *configs.Config
			addr string
			want string
		}{
			{outer, "aws_vpc.here", "module.outer[0].aws_vpc.here"},
			{outer.Children["inner"], "aws_vpc.deep", "module.outer[0].module.inner.aws_vpc.deep"},
		} {
			got := evalTags(t, tc.cfg, tc.addr, nil)["tofu-address"]
			if got != discovery.EscapeAddress(tc.want) {
				t.Errorf("%s's tofu-address is %q, want %q", tc.addr, got, discovery.EscapeAddress(tc.want))
			}
		}
	})
}

// TestStamp_perInstanceAddressIsModuleQualified pins the module prefix on the
// two per-instance address templates, count and for_each. Replacing
// [addressExpr]'s modInst with the root module leaves every test in
// internal/live and internal/command green without this one, and the value it
// writes is a wrong marker rather than a missing one: "aws_vpc.each:0" is an
// address a ROOT resource would hold.
func TestStamp_perInstanceAddressIsModuleQualified(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "net" {
  source = "./impl"
}
`,
		"impl/main.tf": `
resource "aws_vpc" "counted" {
  count      = 2
  cidr_block = "10.0.0.0/16"
}

resource "aws_vpc" "keyed" {
  for_each   = toset(["a", "b"])
  cidr_block = "10.1.0.0/16"
}
`,
	})

	_, diags := Stamp(t.Context(), Request{Estate: "mod-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	child := cfg.Children["net"]
	for _, tc := range []struct {
		addr string
		vars map[string]cty.Value
		want string
	}{
		{"aws_vpc.counted", countData(1), "module.net.aws_vpc.counted[1]"},
		{"aws_vpc.keyed", eachData("b"), `module.net.aws_vpc.keyed["b"]`},
	} {
		got := evalTags(t, child, tc.addr, tc.vars)["tofu-address"]
		if got != discovery.EscapeAddress(tc.want) {
			t.Errorf("%s's tofu-address is %q, want %q", tc.addr, got, discovery.EscapeAddress(tc.want))
		}
	}
}

// TestStamp_forEachLookupTableIsModuleQualified covers issue #210's
// precomputed lookup table, which is a SECOND source of a for_each block's
// address and does not go through [addressExpr] at all. Unqualifying its own
// base left the whole tree green: the only test that reaches this branch,
// TestStamp_eachKeyEscapingRoundTrips, is a root-module fixture, and at the
// root the two spellings are the same string.
//
// Every row of the table is checked, not one: the table is built in Go, so a
// prefix mistake is uniform across it and one row would be enough - but a
// SORTING or off-by-one mistake is not, and only reading every row can see
// that.
func TestStamp_forEachLookupTableIsModuleQualified(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "net" {
  source = "./impl"
}
`,
		// The parentheses are the point. addressExpr's own each.key template
		// already handles "." and ":", through the replace() chain
		// eachKeyEscapedExpr builds, so a key containing only those does NOT
		// route here. A character outside the AWS tag-value charset needs
		// markerkey.Encode's hex escape, which no replace() chain can
		// compute, and that is what forEachNeedsKeyLookup selects on.
		"impl/main.tf": `
resource "aws_vpc" "keyed" {
  for_each   = toset(["a(b)", "c(d)", "plain"])
  cidr_block = "10.1.0.0/16"
}
`,
	})

	_, diags := Stamp(t.Context(), Request{Estate: "mod-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	// A guard for the guard: if this block stopped routing through
	// forEachLookupAddressExpr, every assertion below would still pass on
	// addressExpr's template and the lookup table would be untested again.
	if !forEachNeedsKeyLookup([]string{"a(b)", "c(d)", "plain"}) {
		t.Fatal("the fixture's keys no longer need markerkey.Encode, so this test does not reach forEachLookupAddressExpr at all")
	}

	child := cfg.Children["net"]
	for _, key := range []string{"a(b)", "c(d)", "plain"} {
		got := evalTags(t, child, "aws_vpc.keyed", eachData(key))["tofu-address"]
		want := discovery.EscapeAddress(`module.net.aws_vpc.keyed["` + key + `"]`)
		if got != want {
			t.Errorf("for_each key %q got tofu-address %q, want %q; an unqualified value means the lookup table was built from the root module path", key, got, want)
		}
	}
	// The three rows must be three different addresses. A table that lost its
	// key would return one row for every instance, which is issue #280's
	// shape again inside a single block.
	seen := make(map[string]string)
	for _, key := range []string{"a(b)", "c(d)", "plain"} {
		got := evalTags(t, child, "aws_vpc.keyed", eachData(key))["tofu-address"]
		if prev, dup := seen[got]; dup {
			t.Errorf("for_each keys %q and %q both stamp %q", prev, key, got)
		}
		seen[got] = key
	}
}

// TestStamp_chunkCountCountsTheModulePrefix pins the module prefix's
// contribution to how many tofu-address continuation tags a per-instance
// block needs (issue #71). Unqualifying [stamper.chunkCount]'s base left the
// tree green, and the failure mode is a wrong marker: too few chunks means
// the address is written TRUNCATED, and a truncated address is a different
// address.
//
// The fixture is built so the module prefix is what pushes it over the
// boundary - the same block at the root fits in one tag, and under the module
// it does not - so the test cannot pass by accident on a root-only reading.
func TestStamp_chunkCountCountsTheModulePrefix(t *testing.T) {
	// Long enough that the resource name alone is just inside one tag value
	// and the module prefix is not.
	longModule := strings.Repeat("n", 120)
	longName := strings.Repeat("r", 120)

	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "` + longModule + `" {
  source = "./impl"
}
`,
		"impl/main.tf": `
resource "aws_vpc" "` + longName + `" {
  count      = 2
  cidr_block = "10.0.0.0/16"
}
`,
	})

	_, diags := Stamp(t.Context(), Request{Estate: "mod-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	child := cfg.Children[longModule]
	tags := evalTags(t, child, "aws_vpc."+longName, countData(1))

	want := discovery.EscapeAddress("module." + longModule + ".aws_vpc." + longName + "[1]")
	if len(want) <= 256 {
		t.Fatalf("the fixture's address is %d characters, which fits one tag value; it cannot exercise chunking", len(want))
	}
	if _, ok := tags["tofu-address-2"]; !ok {
		t.Fatalf("a %d-character address was written without a continuation tag, so it is truncated: %v", len(want), tags)
	}

	var got strings.Builder
	got.WriteString(tags["tofu-address"])
	got.WriteString(tags["tofu-address-2"])
	if got.String() != want {
		t.Errorf("the chunks reassemble to %q, want %q", got.String(), want)
	}
}

// TestStamp_slotTableIsModuleQualified pins the module prefix on the tofu-slot
// lookup table. Unqualifying [stamper.slotExpr]'s prefix left the tree green.
//
// The failure mode here is a wrong marker too, and a quiet one: the table's
// rows are matched against a templated key, so an unqualified prefix matches
// nothing, every instance falls through to slotExpr's empty-string default,
// and the resource carries tofu-slot = "" - an assignment this run made and
// then discarded.
func TestStamp_slotTableIsModuleQualified(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "pool" {
  source = "./impl"
}
`,
		"impl/main.tf": `
resource "aws_eip" "addr" {
  count = 2
}
`,
	})

	_, diags := Stamp(t.Context(), Request{
		Estate:  "mod-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		Slots: map[string]string{
			"module.pool.aws_eip.addr:0": "5",
			"module.pool.aws_eip.addr:1": "9",
		},
	})
	assertNoErrors(t, diags)

	child := cfg.Children["pool"]
	for i, want := range map[int]string{0: "5", 1: "9"} {
		got := evalTags(t, child, "aws_eip.addr", countData(i))["tofu-slot"]
		if got != want {
			t.Errorf("count instance %d's tofu-slot is %q, want %q; an empty value means the table's own prefix did not match the key it is looked up by", i, got, want)
		}
	}
}

// TestStamp_policyUntagKeyIsModuleQualified pins the address
// [Request.PolicyUntag] is looked up by. internal/command builds that map from
// addrs.ConfigResource, module-qualified (statelessPolicyUntagMap); stamping
// looked it up by the same spelling, and replacing that lookup with the bare
// resource address left the whole tree green, because at the root the two
// spellings are the same string.
//
// The consequence is a policy verb that silently does not happen: the
// operator asked for the estate marker to be released and it is written
// anyway.
func TestStamp_policyUntagKeyIsModuleQualified(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "net" {
  source = "./impl"
}
`,
		"impl/main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`,
	})

	res, diags := Stamp(t.Context(), Request{
		Estate:      "mod-unit",
		Config:      cfg,
		Schemas:     testSchemas(),
		PolicyUntag: map[string]string{"module.net.aws_vpc.main": TagEstate},
	})
	assertNoErrors(t, diags)

	tags := evalTags(t, cfg.Children["net"], "aws_vpc.main", nil)
	if _, present := tags[TagEstate]; present {
		t.Errorf("declared_tagged = \"untag\" did not release %s on a resource inside a module: %v", TagEstate, tags)
	}
	if got := tags[TagAddress]; got != "module.net.aws_vpc.main" {
		t.Errorf("tofu-address is %q, want %q", got, "module.net.aws_vpc.main")
	}
	if len(res.Untagged) != 1 || res.Untagged[0].Addr.String() != "module.net.aws_vpc.main" || res.Untagged[0].Key != TagEstate {
		t.Errorf("Untagged is %+v, want one entry releasing %s from module.net.aws_vpc.main", res.Untagged, TagEstate)
	}
}

// TestStamp_discoveryLookupIsModuleQualified pins the address
// [Request.NeedsDiscovery] is looked up by, which is what decides whether an
// unmarked resource is a warning or the "applying this creates an object you
// can never find again" error. Unqualifying [stamper.discovery]'s key left the
// tree green; the consequence is that every marker-only resource inside a
// module is silently downgraded to a warning, which is the exact failure the
// comma-ok in that function was written to prevent at the root.
func TestStamp_discoveryLookupIsModuleQualified(t *testing.T) {
	// aws_iam_group_policy_attachment carries no tag surface at all, so it
	// takes stamper.resource's untaggable branch, where mustStamp alone
	// decides between silence and an error.
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "iam" {
  source = "./impl"
}
`,
		"impl/main.tf": `
resource "aws_iam_group_policy_attachment" "a" {
  group      = "devs"
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}
`,
	})

	_, quiet := Stamp(t.Context(), Request{Estate: "mod-unit", Config: cfg, Schemas: testSchemas()})
	if quiet.HasErrors() {
		t.Fatalf("an untaggable resource with no discovery entry errored: %s", quiet.Err())
	}

	_, loud := Stamp(t.Context(), Request{
		Estate:         "mod-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("module.iam.aws_iam_group_policy_attachment.a"),
	})
	if !loud.HasErrors() {
		t.Fatal("a marker-only resource inside a module was left unmarked with no error: the NeedsDiscovery lookup did not find its module-qualified address")
	}
	assertDiagContains(t, loud, "module.iam.aws_iam_group_policy_attachment.a")
}

// TestStamp_moduleMarkerAgreesWithIdentityShape is the cross-check that keeps
// the two halves of [markerBase] honest.
//
// The marker's address must be the module INSTANCE path; the addresses this
// package looks things UP by - NeedsDiscovery, PolicyUntag, Stamped, Skipped -
// must be the module path with the keys dropped. Conflating them is a real
// mistake in either direction: keys in the lookup key means nothing matches,
// and no keys in the marker means the marker names nothing.
func TestStamp_moduleMarkerAgreesWithIdentityShape(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "counted" {
  source = "./impl"
  count  = 1
}
`,
		"impl/main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`,
	})

	res, diags := Stamp(t.Context(), Request{
		Estate:      "mod-unit",
		Config:      cfg,
		Schemas:     testSchemas(),
		PolicyUntag: map[string]string{"module.counted.aws_vpc.main": TagAddress},
	})
	assertNoErrors(t, diags)

	// The lookup side: keyless, or the PolicyUntag entry above would not have
	// matched and tofu-address would still be written.
	tags := evalTags(t, cfg.Children["counted"], "aws_vpc.main", nil)
	if _, present := tags[TagAddress]; present {
		t.Errorf("PolicyUntag keyed by the unkeyed block address did not match, so %s was written anyway: %v", TagAddress, tags)
	}
	if len(res.Stamped) != 1 || res.Stamped[0].Addr.String() != "module.counted.aws_vpc.main" {
		t.Errorf("Stamped is %+v, want one entry at the unkeyed block address module.counted.aws_vpc.main", res.Stamped)
	}

	// The marker side: keyed. Asserted through the estate marker's own
	// resource, since tofu-address was just released above.
	if got := tags[TagEstate]; got != "mod-unit" {
		t.Errorf("tofu-estate is %q, want %q", got, "mod-unit")
	}
	if got := markerBase(cfg.Children["counted"].Module.ManagedResources["aws_vpc.main"], addrs.RootModuleInstance.Child("counted", addrs.IntKey(0))); got != "module.counted[0].aws_vpc.main" {
		t.Errorf("markerBase is %q, want the keyed instance address %q", got, "module.counted[0].aws_vpc.main")
	}
}

// TestChildExpansion_readsEveryModuleCallShape pins [childExpansion]'s own
// three-way answer directly, including the shapes the fixtures above cannot
// reach: a count this pass cannot evaluate at all.
func TestChildExpansion_readsEveryModuleCallShape(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
variable "n" {
  type    = number
  default = 4
}

module "static" {
  source = "./impl"
}

module "one" {
  source = "./impl"
  count  = 1
}

module "zero" {
  source = "./impl"
  count  = 0
}

module "fromvar" {
  source = "./impl"
  count  = var.n
}

module "keyed" {
  source   = "./impl"
  for_each = toset(["a"])
}
`,
		"impl/main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`,
	})

	for _, tc := range []struct {
		call string
		want moduleExpansion
		key  addrs.InstanceKey
	}{
		{"static", expansionSingle, addrs.NoKey},
		{"one", expansionSingle, addrs.IntKey(0)},
		{"zero", expansionNone, addrs.NoKey},
		{"fromvar", expansionMany, addrs.NoKey},
		// A single-key for_each is deliberately expansionMany: the operator
		// has a supported idiom for writing that marker by hand and
		// moduleKeyedResource trusts it, so computing one here would start
		// verifying hand-written markers this pass cannot always read. See
		// childExpansion's doc comment.
		{"keyed", expansionMany, addrs.NoKey},
	} {
		got, key := childExpansion(t.Context(), cfg.Module, tc.call)
		if got != tc.want || key != tc.key {
			t.Errorf("module %q expands as (%v, %v), want (%v, %v)", tc.call, got, key, tc.want, tc.key)
		}
	}

	// A name with no module call at all - the walk asks about children it
	// found in the config tree, so this should never happen, and answering
	// "many" for it would refuse a static module on a lookup miss.
	if got, key := childExpansion(t.Context(), cfg.Module, "absent"); got != expansionSingle || key != addrs.NoKey {
		t.Errorf("an unknown module call expands as (%v, %v), want (%v, %v)", got, key, expansionSingle, addrs.NoKey)
	}
}

// TestChildExpansion_countFromAVariableTheCallSupplies is the case that makes
// count = var.enabled ? 1 : 0 usable one level down: the count expression
// lives in a child module and reads a variable the PARENT call supplied, so
// the evaluator that answers it has to be the child's own.
func TestChildExpansion_countFromAVariableTheCallSupplies(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "wrap" {
  source  = "./wrap"
  enabled = true
}
`,
		"wrap/main.tf": `
variable "enabled" {
  type    = bool
  default = false
}

module "inner" {
  source = "../inner"
  count  = var.enabled ? 1 : 0
}
`,
		"inner/main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`,
	})

	got, key := childExpansion(t.Context(), cfg.Children["wrap"].Module, "inner")
	if got != expansionSingle || key != addrs.IntKey(0) {
		t.Fatalf("module \"inner\" expands as (%v, %v), want (%v, %v); the child module's own static evaluator did not resolve the variable its caller set", got, key, expansionSingle, addrs.IntKey(0))
	}

	_, diags := Stamp(t.Context(), Request{Estate: "mod-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	tags := evalTags(t, cfg.Children["wrap"].Children["inner"], "aws_vpc.main", nil)
	assertTags(t, tags, map[string]string{
		"tofu-estate":  "mod-unit",
		"tofu-address": discovery.EscapeAddress("module.wrap.module.inner[0].aws_vpc.main"),
	})
}
