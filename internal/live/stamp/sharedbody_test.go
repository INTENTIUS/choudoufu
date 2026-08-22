// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// TestStamp_repeatedLocalModuleGetsDistinctAddresses is GitHub issue #280.
//
// One local module source called N times is the ordinary way a Terraform
// configuration gets factored - one call per domain, per environment, per
// service. Every one of those calls parses the SAME files, and
// hclparse.Parser caches a parsed file by its name, so all N calls' resource
// blocks are backed by exactly one *hclsyntax.Body. Writing a literal
// tofu-address into that body once per call leaves the last call's address
// on every one of them.
//
// The estate then applies cleanly, stamps N real cloud objects with one
// identity, and is unplannable ever after - "Two live resources claiming one
// address". It was found on .corpus/simpleinfra/terraform/dns, which calls
// ./impl seven times and put
// "module.rustconf_com.aws_route53_zone.zone" on all seven live hosted
// zones.
//
// The assertion is on the EVALUATED tags of each call, not on the AST and
// not on Result.Stamped: a verdict-level check cannot see a wrong marker,
// only a missing one.
func TestStamp_repeatedLocalModuleGetsDistinctAddresses(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "alpha" {
  source = "./impl"
  name   = "alpha"
}

module "bravo" {
  source = "./impl"
  name   = "bravo"
}

module "charlie" {
  source = "./impl"
  name   = "charlie"
}
`,
		"impl/main.tf": `
variable "name" {
  type = string
}

resource "aws_route53_zone" "zone" {
  name = var.name
}
`,
	})

	res, diags := Stamp(t.Context(), Request{Estate: "repeat-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	if len(res.Stamped) != 3 {
		t.Fatalf("stamped %d resources, want 3: %+v", len(res.Stamped), res.Stamped)
	}

	for _, call := range []string{"alpha", "bravo", "charlie"} {
		child, ok := cfg.Children[call]
		if !ok {
			t.Fatalf("no child module %q in the loaded configuration", call)
		}
		want := "module." + call + ".aws_route53_zone.zone"
		tags := evalTags(t, child, "aws_route53_zone.zone", nil)
		assertTags(t, tags, map[string]string{
			"tofu-estate":  "repeat-unit",
			"tofu-address": want,
		})
	}
}

// TestStamp_repeatedLocalModuleNested is the same defect one level deeper:
// the repeated source is not itself called twice, it is reached through two
// calls of a wrapper. The shared body is the leaf's, and nothing about the
// depth changes that.
func TestStamp_repeatedLocalModuleNested(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "east" {
  source = "./wrap"
  name   = "east"
}

module "west" {
  source = "./wrap"
  name   = "west"
}
`,
		"wrap/main.tf": `
variable "name" {
  type = string
}

module "leaf" {
  source = "../leaf"
  name   = var.name
}
`,
		"leaf/main.tf": `
variable "name" {
  type = string
}

resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`,
	})

	_, diags := Stamp(t.Context(), Request{Estate: "repeat-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	for _, call := range []string{"east", "west"} {
		leaf := cfg.Children[call].Children["leaf"]
		if leaf == nil {
			t.Fatalf("no module.%s.leaf in the loaded configuration", call)
		}
		want := "module." + call + ".module.leaf.aws_vpc.main"
		tags := evalTags(t, leaf, "aws_vpc.main", nil)
		assertTags(t, tags, map[string]string{
			"tofu-estate":  "repeat-unit",
			"tofu-address": want,
		})
	}
}

// TestStamp_repeatedLocalModuleWithExistingTags covers the other three
// rewrite shapes over a shared body: an object literal that is appended to,
// a merge() call that gains an argument, and an unreadable expression that
// gets wrapped. Each one mutates a different node, and each node is shared
// between the calls.
func TestStamp_repeatedLocalModuleWithExistingTags(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags string
	}{
		{"object literal", `tags = { Name = var.name }`},
		{"merge call", `tags = merge({ Name = var.name }, { Tier = "web" })`},
		{"unreadable expression", `tags = var.extra`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadTree(t, map[string]string{
				"main.tf": `
module "one" {
  source = "./impl"
  name   = "one"
}

module "two" {
  source = "./impl"
  name   = "two"
}
`,
				"impl/main.tf": `
variable "name" {
  type = string
}

variable "extra" {
  type    = map(string)
  default = { Owner = "team" }
}

resource "aws_s3_bucket" "b" {
  bucket = var.name
  ` + tc.tags + `
}
`,
			})

			_, diags := Stamp(t.Context(), Request{Estate: "repeat-unit", Config: cfg, Schemas: testSchemas()})
			assertNoErrors(t, diags)

			// The fixture's own tags refer to var.*, which the plan
			// evaluator supplies and this one has to be handed.
			vars := map[string]cty.Value{"var": cty.ObjectVal(map[string]cty.Value{
				"name":  cty.StringVal("declared"),
				"extra": cty.MapVal(map[string]cty.Value{"Owner": cty.StringVal("team")}),
			})}
			for _, call := range []string{"one", "two"} {
				want := "module." + call + ".aws_s3_bucket.b"
				tags := evalTags(t, cfg.Children[call], "aws_s3_bucket.b", vars)
				if got := tags["tofu-address"]; got != want {
					t.Errorf("module.%s's tofu-address is %q, want %q (all tags: %v)", call, got, want, tags)
				}
			}
		})
	}
}

// TestStamp_twoResourcesSharingOneBodyIsRefused reaches [stamper.claimBody]'s
// refusal, which the loader is not supposed to be able to produce.
//
// That is exactly why it needs a test. The guard exists because
// [privateBody] rests on a fact about how configs.decodeResourceBlock
// allocates bodies, and a guard whose trigger nothing can reach is a guard
// that will be silently deleted, or silently broken, the next time that fact
// changes. So the fixture forges the condition: two module calls, and the
// second call's resource pointed at the first's body, which is precisely the
// state a loader that stopped allocating per-call bodies would produce.
//
// The assertion is that the run REFUSES, not that it stamps something
// plausible. A marker written into a body two resources share is wrong for at
// least one of them, and a wrong marker on a real cloud object outranks a
// missing one.
func TestStamp_twoResourcesSharingOneBodyIsRefused(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "alpha" {
  source = "./impl"
}

module "bravo" {
  source = "./impl"
}
`,
		"impl/main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`,
	})

	shared := cfg.Children["alpha"].Module.ManagedResources["aws_vpc.main"].Config
	if _, ok := shared.(*hclsyntax.Body); !ok {
		t.Fatalf("the fixture's body is %T, not an *hclsyntax.Body", shared)
	}
	other := cfg.Children["bravo"].Module.ManagedResources["aws_vpc.main"]
	if other.Config == shared {
		t.Fatal("the loader already hands two module calls one body: privateBody's premise is gone and issue #280 is back, unguarded")
	}
	other.Config = shared

	res, diags := Stamp(t.Context(), Request{Estate: "repeat-unit", Config: cfg, Schemas: testSchemas()})
	if !diags.HasErrors() {
		t.Fatalf("two resources sharing one body were stamped without complaint; skips: %+v", res.Skipped)
	}
	assertDiagContains(t, diags, SummarySharedBody, "module.alpha.aws_vpc.main", "module.bravo.aws_vpc.main")

	if !hasSkip(res, "module.bravo.aws_vpc.main", SkipSharedBody) {
		t.Errorf("no %s skip was filed for the resource that lost the claim: %+v", SkipSharedBody, res.Skipped)
	}
	if len(res.Stamped) != 0 {
		t.Errorf("Stamped is not empty after an error: %+v", res.Stamped)
	}
	// Nothing written anywhere, which is Stamp's own all-or-nothing rule and
	// is what makes the refusal safe to hit halfway through a walk.
	if tags := evalTags(t, cfg.Children["alpha"], "aws_vpc.main", nil); len(tags) != 0 {
		t.Errorf("a refused run still wrote tags: %v", tags)
	}
}

// TestPrivateBodyCoversEveryRewriteShape is the ratchet behind
// [privateBody]'s claim that it copies every node [rewrite.apply] mutates.
//
// privateBody cannot read apply's switch, so it names the four shapes by
// hand, and a fifth added to rewrite would be a node still shared between
// module calls - issue #280 back for that one shape only, invisible to every
// verdict-level check. This test consults the rewrite struct itself rather
// than a list of its own, so adding a field fails it.
func TestPrivateBodyCoversEveryRewriteShape(t *testing.T) {
	// The mutation targets privateBody copies, and the flags that only
	// select between them. Keep this in step with rewrite.apply.
	covered := map[string]string{
		"body":  "copied: body.Attributes is cloned",
		"obj":   "copied: the tags object literal and its Items",
		"merge": "copied: the tags merge() call and its Args",
		"wrap":  "copied: the tags attribute itself",

		"replace": "not a mutation target: selects between wrap's two branches",
		"items":   "not a mutation target: freshly built per resource",
		"rng":     "not a mutation target: a range, never written through",
	}

	rt := reflect.TypeOf(rewrite{})
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		if _, ok := covered[name]; !ok {
			t.Errorf("rewrite has a field %q that privateBody knows nothing about. If rewrite.apply mutates it, privateBody must copy it - otherwise N calls of one local module will all write into one shared node (GitHub issue #280). Add it to privateBody, then record it here.", name)
		}
		delete(covered, name)
	}
	for name := range covered {
		t.Errorf("this test still claims rewrite has a field %q, and it does not; the list has drifted from the struct", name)
	}
}

// TestStamp_forEachModuleIsAddressedPerInstance is the neighbouring case, and
// it is here because it had no test of its own.
//
// A module call that sets for_each really does give its several instances one
// *hclsyntax.Body - there is one *configs.Config node for the call, one
// *configs.Resource inside it, and the instances differ only at plan time -
// so [privateBody] does nothing for it and cannot: no COPY makes a literal
// vary per instance. [moduleResource.keyedAncestor] is what keeps that case
// from writing one address onto N instances, which is issue #280's defect by
// a different route.
//
// Deleting the line that propagates keyedAncestor to a child module left
// every test in internal/live and internal/command green, which is to say
// the mechanism guarding the for_each case was guarded by nothing. This test
// is that guard.
//
// What it asserts changed with issue #378, and the guard got stronger rather
// than weaker. Until #378 the mechanism's answer was a refusal - skip the
// resource, write nothing - and this test asserted that no literal reached
// the shared body. Now the answer is [markers.ModulePrefixAttr]: a template
// no copy is needed for, because the value varies per instance at evaluation
// time. So the assertion is on the VALUE two different instances of the call
// render, which is what "one address onto N instances" was ever about, and a
// keyedAncestor that stopped propagating would fail it by rendering the same
// string twice.
func TestStamp_forEachModuleIsAddressedPerInstance(t *testing.T) {
	t.Run("directly for_each'd", func(t *testing.T) {
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

resource "aws_route53_zone" "zone" {
  name = var.name
}
`,
		})

		res, diags := Stamp(t.Context(), Request{Estate: "repeat-unit", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)

		if hasSkip(res, "module.sites.aws_route53_zone.zone", SkipModuleKeyed) {
			t.Errorf("a resource inside a for_each'd module is still refused as %s; #378 made it stampable: %+v", SkipModuleKeyed, res.Skipped)
		}
		if len(res.Stamped) != 1 {
			t.Fatalf("want the one resource inside the for_each'd module stamped, got %+v (skipped: %+v)", res.Stamped, res.Skipped)
		}

		a := evalTags(t, cfg.Children["sites"], "aws_route53_zone.zone", withModulePrefix(t, nil, `module.sites["a"]`))
		b := evalTags(t, cfg.Children["sites"], "aws_route53_zone.zone", withModulePrefix(t, nil, `module.sites["b"]`))
		assertTags(t, a, map[string]string{
			TagEstate:  "repeat-unit",
			TagAddress: "module.sites:a.aws_route53_zone.zone",
		})
		assertTags(t, b, map[string]string{
			TagEstate:  "repeat-unit",
			TagAddress: "module.sites:b.aws_route53_zone.zone",
		})
		if a[TagAddress] == b[TagAddress] {
			t.Errorf("one address reached every instance of the for_each'd call: %q", a[TagAddress])
		}
	})

	t.Run("for_each'd through an ancestor", func(t *testing.T) {
		cfg := loadTree(t, map[string]string{
			"main.tf": `
module "sites" {
  source   = "./wrap"
  for_each = toset(["a", "b"])
  name     = each.key
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
		// keyedAncestor reaching the leaf is exactly what makes the module
		// half of this address an interpolation rather than the literal
		// "module.sites.module.leaf" - which would be one address for both
		// instances of the call, and the address of neither.
		a := evalTags(t, cfg.Children["sites"].Children["leaf"], "aws_route53_zone.zone",
			withModulePrefix(t, nil, `module.sites["a"].module.leaf`))
		b := evalTags(t, cfg.Children["sites"].Children["leaf"], "aws_route53_zone.zone",
			withModulePrefix(t, nil, `module.sites["b"].module.leaf`))
		if a[TagAddress] != "module.sites:a.module.leaf.aws_route53_zone.zone" {
			t.Errorf("%s = %q for module.sites[\"a\"].module.leaf; keyedAncestor did not reach the leaf", TagAddress, a[TagAddress])
		}
		if b[TagAddress] != "module.sites:b.module.leaf.aws_route53_zone.zone" {
			t.Errorf("%s = %q for module.sites[\"b\"].module.leaf", TagAddress, b[TagAddress])
		}
	})

	t.Run("hand-written tags are trusted, not overwritten", func(t *testing.T) {
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

resource "aws_route53_zone" "zone" {
  name = var.name
  tags = {
    tofu-estate  = "repeat-unit"
    tofu-address = "module.sites[\"${var.name}\"].aws_route53_zone.zone"
  }
}
`,
		})

		res, diags := Stamp(t.Context(), Request{Estate: "repeat-unit", Config: cfg, Schemas: testSchemas()})
		assertNoErrors(t, diags)

		if !hasSkip(res, "module.sites.aws_route53_zone.zone", SkipModuleKeyedTrusted) {
			t.Errorf("a hand-stamped resource inside a for_each'd module was not skipped as %s: %+v", SkipModuleKeyedTrusted, res.Skipped)
		}
		vars := map[string]cty.Value{"var": cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("a")})}
		tags := evalTags(t, cfg.Children["sites"], "aws_route53_zone.zone", vars)
		assertTags(t, tags, map[string]string{
			"tofu-estate":  "repeat-unit",
			"tofu-address": `module.sites["a"].aws_route53_zone.zone`,
		})
	})
}

// loadTree writes a whole module tree to a scratch directory and loads it the
// way internal/live/check.Load does - ONE configs.Parser for the root and
// every child, which is what the command uses and what makes two calls of one
// source share a parsed file.
func loadTree(t *testing.T, files map[string]string) *configs.Config {
	t.Helper()

	dir := t.TempDir()
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	parser := configs.NewParser(nil)
	rootCall := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)
	rootMod, diags := parser.LoadConfigDir(dir, rootCall)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}

	cfg, cfgDiags := configs.BuildConfig(t.Context(), rootMod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			childDir := filepath.Join(req.Parent.Module.SourceDir, req.SourceAddr.String())
			mod, modDiags := parser.LoadConfigDir(childDir, req.Call)
			return mod, nil, modDiags
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}
