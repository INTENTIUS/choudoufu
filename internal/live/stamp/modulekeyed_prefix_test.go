// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// This file is issue #378's FIX, pinned by value.
//
// #378's finding was that a taggable resource reached through a module call
// with more than one instance got no marker at all, so a stateless replan's
// desired tag set for it carried none and the ordinary tags diff rendered
// that as a DELETION of the marker live-import had genuinely written. The
// cause was that a module call's several instances share exactly one
// *hclsyntax.Body for the resource's tags argument, and no literal in that
// body is the right tofu-address for all of them.
//
// The fix is one new evaluator symbol, [markers.ModulePrefixAttr], whose
// value is the evaluating module INSTANCE's own escaped path. The literal
// this pass could not write becomes an interpolation, exactly as each.key
// already is for a for_each'd resource, and the module half of the address
// resolves per instance at plan and apply time.
//
// HANDOFF.md's safety rule is what decides what has to be proven here, and it
// is not "no error". A marker that is silently WRONG adopts or displaces a
// real object, so these tests assert the rendered tag map BY VALUE for two
// distinct instances of one keyed module call and require the two to differ,
// and they assert the refusal that fires wherever the instance is not known.

// TestModuleKeyedMarkerIsPerModuleInstance is the happy path, and the whole
// safety argument: two instances of one for_each'd module call, one shared
// tags body, two DIFFERENT and exactly correct marker sets.
//
// The prefix each instance is evaluated with is not hand-written here: it
// comes from [markers.ModulePrefix], the same function
// internal/tofu/evaluate.go's GetTerraformAttr calls at plan and apply time,
// applied to a parsed module instance address. The expected tag maps ARE hand
// written, whole and literal, so what this test proves is that the production
// prefix function composed with the production template produces exactly
// those strings and nothing else.
func TestModuleKeyedMarkerIsPerModuleInstance(t *testing.T) {
	// The corpus shape #378 was found on, reduced: terraform-aws-modules/ecs's
	// modules/container-definition writes `tags = var.tags`, and
	// examples/fargate calls it with for_each over the container names.
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "container_definition" {
  source   = "./impl"
  for_each = toset(["fluent-bit", "al2023"])
  name     = each.key
  tags     = { Example = "ex-fargate" }
}
`,
		"impl/main.tf": `
variable "name" { type = string }
variable "tags" { type = map(string) }

resource "aws_cloudwatch_log_group" "this" {
  count = 1

  name = var.name
  tags = var.tags
}
`,
	})

	res, diags := Stamp(t.Context(), Request{Estate: "ecs-fargate-crossing", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	if len(res.Stamped) != 1 || res.Stamped[0].Addr.String() != "module.container_definition.aws_cloudwatch_log_group.this" {
		t.Fatalf("want the one resource stamped, got %+v (skipped: %+v)", res.Stamped, res.Skipped)
	}
	if hasSkip(res, "module.container_definition.aws_cloudwatch_log_group.this", SkipModuleKeyedTrusted) {
		t.Errorf("still reporting %s about a marker nobody wrote: %+v", SkipModuleKeyedTrusted, res.Skipped)
	}

	base := map[string]cty.Value{"var": cty.ObjectVal(map[string]cty.Value{
		"tags": cty.MapVal(map[string]cty.Value{"Example": cty.StringVal("ex-fargate")}),
	})}

	// The two live objects #378 named, by their real addresses.
	fluentBit := evalTags(t, cfg.Children["container_definition"], "aws_cloudwatch_log_group.this",
		withModulePrefix(t, withCountIndex(base, 0), `module.container_definition["fluent-bit"]`))
	al2023 := evalTags(t, cfg.Children["container_definition"], "aws_cloudwatch_log_group.this",
		withModulePrefix(t, withCountIndex(base, 0), `module.container_definition["al2023"]`))

	assertTags(t, fluentBit, map[string]string{
		"Example":  "ex-fargate",
		TagEstate:  "ecs-fargate-crossing",
		TagAddress: "module.container_definition:fluent-bit.aws_cloudwatch_log_group.this:0",
	})
	assertTags(t, al2023, map[string]string{
		"Example":  "ex-fargate",
		TagEstate:  "ecs-fargate-crossing",
		TagAddress: "module.container_definition:al2023.aws_cloudwatch_log_group.this:0",
	})

	// Stated separately from the two assertions above, because "both are
	// correct" and "the two differ" are different claims and only the second
	// one rules out the failure this fix exists to remove: one body, one
	// literal, every instance carrying the same marker.
	if fluentBit[TagAddress] == al2023[TagAddress] {
		t.Errorf("both instances of the module call render %s = %q; the whole point of the module prefix is that they must not",
			TagAddress, fluentBit[TagAddress])
	}
}

// TestModuleKeyedMarkerReachesEveryInstanceShape walks the three shapes the
// resource's OWN repetition can take underneath a keyed module call, and the
// two shapes the CALL can take. Every combination renders one exact string.
//
// The count'd module call is the half mechanism B reaches that the rejected
// alternative did not: addrs.ModuleInstance carries an IntKey step exactly as
// naturally as a StringKey one, so `module.sites[1]` needs no new machinery
// beyond what `module.sites["b"]` already needed.
func TestModuleKeyedMarkerReachesEveryInstanceShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		call     string
		resource string
		prefix   string
		vars     map[string]cty.Value
		want     string
	}{
		{
			name:     "for_each'd call, plain resource",
			call:     `for_each = toset(["a", "b"])`,
			resource: `resource "aws_eip" "app" { tags = { Name = "n" } }`,
			prefix:   `module.sites["b"]`,
			want:     "module.sites:b.aws_eip.app",
		},
		{
			name:     "count'd call, plain resource",
			call:     `count = 3`,
			resource: `resource "aws_eip" "app" { tags = { Name = "n" } }`,
			prefix:   `module.sites[1]`,
			want:     "module.sites:1.aws_eip.app",
		},
		{
			name:     "for_each'd call, count'd resource",
			call:     `for_each = toset(["a", "b"])`,
			resource: `resource "aws_eip" "app" { count = 2 }`,
			prefix:   `module.sites["a"]`,
			vars:     map[string]cty.Value{"count": cty.ObjectVal(map[string]cty.Value{"index": cty.NumberIntVal(1)})},
			want:     "module.sites:a.aws_eip.app:1",
		},
		{
			name:     "for_each'd call, for_each'd resource",
			call:     `for_each = toset(["a", "b"])`,
			resource: `resource "aws_eip" "app" { for_each = toset(["x"]) }`,
			prefix:   `module.sites["a"]`,
			vars:     map[string]cty.Value{"each": cty.ObjectVal(map[string]cty.Value{"key": cty.StringVal("x")})},
			want:     "module.sites:a.aws_eip.app:x",
		},
		{
			// Issue #210's out-of-charset key, under a keyed module call. The
			// replace()-chain template cannot compute markerkey.Encode's hex
			// escape, so this must take the precomputed-lookup branch - with
			// the module half still supplied by the symbol.
			name:     "for_each'd call, for_each'd resource with an encoded key",
			call:     `for_each = toset(["a"])`,
			resource: `resource "aws_eip" "app" { for_each = toset(["a(b)"]) }`,
			prefix:   `module.sites["a"]`,
			vars:     map[string]cty.Value{"each": cty.ObjectVal(map[string]cty.Value{"key": cty.StringVal("a(b)")})},
			want:     "module.sites:a.aws_eip.app:a+000028b+000029",
		},
		{
			// A module instance key that itself needs escaping: "." and ":"
			// are the escaped address's own separators, so a key carrying one
			// has to come back doubled or the address could not be split
			// again.
			name:     "for_each'd call whose own key needs escaping",
			call:     `for_each = toset(["eu-west-1.a"])`,
			resource: `resource "aws_eip" "app" { tags = { Name = "n" } }`,
			prefix:   `module.sites["eu-west-1.a"]`,
			want:     "module.sites:eu-west-1@da.aws_eip.app",
		},
		{
			// Two levels of nesting, only the outer one keyed: the symbol
			// carries the whole path, not just the keyed step.
			name:     "nested under a keyed call",
			call:     `for_each = toset(["a"])`,
			resource: `resource "aws_eip" "app" { tags = { Name = "n" } }`,
			prefix:   `module.sites["a"].module.leaf`,
			want:     "module.sites:a.module.leaf.aws_eip.app",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadTree(t, map[string]string{
				"main.tf":      "module \"sites\" {\n  source = \"./impl\"\n  " + tc.call + "\n}\n",
				"impl/main.tf": tc.resource + "\n",
			})

			_, diags := Stamp(t.Context(), Request{Estate: "repeat-unit", Config: cfg, Schemas: testSchemas()})
			assertNoErrors(t, diags)

			got := evalTags(t, cfg.Children["sites"], "aws_eip.app", withModulePrefix(t, tc.vars, tc.prefix))
			if got[TagAddress] != tc.want {
				t.Errorf("%s = %q, want %q (whole tag map: %v)", TagAddress, got[TagAddress], tc.want, got)
			}
			if got[TagEstate] != "repeat-unit" {
				t.Errorf("%s = %q, want %q", TagEstate, got[TagEstate], "repeat-unit")
			}
		})
	}
}

// TestModuleKeyedMarkerComposesLikeAWholeAddress is the property the two-part
// marker rests on, checked against the one-part answer rather than against
// itself: for every module instance and resource instance shape, the prefix
// this fork evaluates plus the suffix this pass writes is byte-for-byte what
// [markers.EscapeAddress] produces for the whole address at once - which is
// the string internal/live/discovery computes when it goes looking for the
// object.
//
// A marker that does not satisfy this is not merely ugly: it is a marker
// discovery will never match, on a real object.
func TestModuleKeyedMarkerComposesLikeAWholeAddress(t *testing.T) {
	for _, whole := range []string{
		`module.container_definition["fluent-bit"].aws_cloudwatch_log_group.this[0]`,
		`module.sites[1].aws_eip.app`,
		`module.sites["eu-west-1.a"].aws_eip.app["x:y"]`,
		`module.outer["a"].module.leaf.aws_eip.app`,
		`module.outer[0].module.inner["b@c"].aws_eip.app[2]`,
	} {
		t.Run(whole, func(t *testing.T) {
			inst, diags := addrs.ParseAbsResourceInstanceStr(whole)
			if diags.HasErrors() {
				t.Fatalf("parsing %s: %s", whole, diags.Err())
			}

			prefix, ok := markers.ModulePrefix(inst.Module)
			if !ok {
				t.Fatalf("markers.ModulePrefix declined %s, which is not the root", inst.Module)
			}
			suffix := markers.EscapeAddress(inst.Resource.String())

			if got, want := prefix+"."+suffix, markers.EscapeAddress(whole); got != want {
				t.Errorf("prefix+suffix = %q, but the whole address escapes to %q", got, want)
			}
		})
	}
}

// TestModuleKeyedHandWrittenMarkerIsStillTrusted holds the one case #378's
// fix deliberately leaves alone, and it is live/e2e/estate-module-keyed's own
// idiom: a resource that already declares tofu-address, built from a variable
// the module call threads through from its own each.key.
//
// This pass has never overwritten a hand-written marker value and does not
// start here. The regression this guards against is the opposite of #378's:
// an estate that works today must not start being refused, or stamped twice,
// because the pass gained a way to write the marker itself.
func TestModuleKeyedHandWrittenMarkerIsStillTrusted(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "wrapped" {
  source   = "./impl"
  for_each = toset(["a", "b"])
  key      = each.key
}
`,
		"impl/main.tf": `
variable "key" { type = string }

resource "aws_eip" "app" {
  tags = {
    tofu-estate  = "ec2-module-keyed-cohort"
    tofu-address = "module.wrapped[\"${var.key}\"].aws_eip.app"
  }
}
`,
	})

	res, diags := Stamp(t.Context(), Request{
		Estate:         "ec2-module-keyed-cohort",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("module.wrapped.aws_eip.app"),
	})
	assertNoErrors(t, diags)

	if !hasSkip(res, "module.wrapped.aws_eip.app", SkipModuleKeyedTrusted) {
		t.Errorf("want %s for a resource declaring its own %s, got %+v", SkipModuleKeyedTrusted, TagAddress, res.Skipped)
	}
	if len(res.Stamped) != 0 {
		t.Errorf("a hand-written marker was stamped over: %+v", res.Stamped)
	}

	// By value, and unchanged: exactly what the operator wrote, for both
	// instances of the call, with nothing appended.
	for _, key := range []string{"a", "b"} {
		got := evalTags(t, cfg.Children["wrapped"], "aws_eip.app", map[string]cty.Value{
			"var": cty.ObjectVal(map[string]cty.Value{"key": cty.StringVal(key)}),
		})
		assertTags(t, got, map[string]string{
			TagEstate:  "ec2-module-keyed-cohort",
			TagAddress: `module.wrapped["` + key + `"].aws_eip.app`,
		})
	}
}

// TestModuleKeyedOverlongAddressIsRefusedNotTruncated is the one shape the
// fix refuses rather than stamping, and the reason is the safety rule again.
//
// A keyed resource carries its address in ONE tag: the module instance's own
// path is supplied at evaluation time, so its LENGTH is not known where the
// continuation split would have to be decided, and splitting on an
// underestimate truncates the marker. A truncated marker is a wrong marker.
// So the pass refuses when the part it CAN measure already overflows a
// single tag value.
func TestModuleKeyedOverlongAddressIsRefusedNotTruncated(t *testing.T) {
	// 300 characters of resource label, so the resource half alone is past
	// markers.MaxTagValue (256) before any module path is added.
	label := ""
	for len(label) < 300 {
		label += "x"
	}
	cfg := loadTree(t, map[string]string{
		"main.tf": `
module "sites" {
  source   = "./impl"
  for_each = toset(["a", "b"])
}
`,
		"impl/main.tf": `resource "aws_eip" "` + label + `" {}` + "\n",
	})

	res, diags := Stamp(t.Context(), Request{
		Estate:         "repeat-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("module.sites.aws_eip." + label),
	})
	if !diags.HasErrors() {
		t.Fatalf("an address that cannot fit one tag value was not refused; skips: %+v", res.Skipped)
	}
	if len(res.Stamped) != 0 {
		t.Errorf("stamped an address that cannot fit one tag value: %+v", res.Stamped)
	}
	if tags := evalTags(t, cfg.Children["sites"], "aws_eip."+label, withModulePrefix(t, nil, `module.sites["a"]`)); len(tags) != 0 {
		t.Errorf("a truncatable marker reached the body anyway: %v", tags)
	}
}

// TestStaticEvaluatorRefusesTheModulePrefixUntilThreaded is the failure-safe
// half, and it matters as much as the happy path.
//
// internal/configs' static evaluator belongs to a *configs.Module, and every
// instance of a for_each'd or count'd call above that module shares the one
// evaluator - which is the whole fact #378 is about. So an evaluation that
// was not told WHICH instance it is for has no honest answer, and the answer
// it must not give is the empty string: an empty prefix renders
// ".aws_eip.app", a marker no discovery will ever match, silently, on a real
// object. HANDOFF.md ranks that strictly worse than a missing marker.
//
// The test lives in this package rather than in internal/configs on purpose.
// just ci's test tier runs ./internal/live/... and not ./internal/configs/,
// the same per-package-versus-per-file gap issues #156, #164 and #171 were
// each about; a refusal pinned where CI does not look is a refusal that can
// be deleted without anything going red.
func TestStaticEvaluatorRefusesTheModulePrefixUntilThreaded(t *testing.T) {
	cfg := loadTree(t, map[string]string{
		"main.tf": `
locals {
  prefix = tofu.` + markers.ModulePrefixAttr + `
}
`,
	})
	eval := cfg.Module.StaticEvaluator
	if eval == nil {
		t.Fatal("the loaded module has no static evaluator")
	}
	expr := cfg.Module.Locals["prefix"].Expr
	ident := configs.StaticIdentifier{Subject: "local.prefix", DeclRange: cfg.Module.Locals["prefix"].DeclRange}

	t.Run("not threaded: refuses, and does not answer with an empty prefix", func(t *testing.T) {
		val, diags := eval.Evaluate(t.Context(), expr, ident)
		if !diags.HasErrors() {
			t.Fatalf("evaluating %s with no module instance threaded in produced %#v and no error", markers.ModulePrefixRef, val)
		}
		if val.IsKnown() && !val.IsNull() && val.Type() == cty.String && val.AsString() == "" {
			t.Errorf("answered with the empty string; that is the silently-wrong-marker outcome this refusal exists to prevent")
		}
	})

	t.Run("the root module: refuses too, for its own reason", func(t *testing.T) {
		_, diags := eval.WithModuleInstance(addrs.RootModuleInstance).Evaluate(t.Context(), expr, ident)
		if !diags.HasErrors() {
			t.Fatalf("the root module has no marker prefix, but %s evaluated cleanly there", markers.ModulePrefixRef)
		}
	})

	t.Run("threaded: answers that instance's prefix, by value", func(t *testing.T) {
		for _, tc := range []struct{ inst, want string }{
			{`module.sites["a"]`, "module.sites:a"},
			{`module.sites[1]`, "module.sites:1"},
			{`module.outer["a"].module.leaf`, "module.outer:a.module.leaf"},
		} {
			inst, diags := addrs.ParseModuleInstanceStr(tc.inst)
			if diags.HasErrors() {
				t.Fatalf("parsing %s: %s", tc.inst, diags.Err())
			}
			val, evalDiags := eval.WithModuleInstance(inst).Evaluate(t.Context(), expr, ident)
			if evalDiags.HasErrors() {
				t.Fatalf("evaluating %s for %s: %s", markers.ModulePrefixRef, tc.inst, evalDiags.Error())
			}
			if got := val.AsString(); got != tc.want {
				t.Errorf("%s for %s = %q, want %q", markers.ModulePrefixRef, tc.inst, got, tc.want)
			}
		}
	})

	t.Run("threading one instance does not leak into another evaluator", func(t *testing.T) {
		// WithModuleInstance copies, exactly as WithRepetitionData does. If it
		// mutated in place, the base evaluator would silently start answering
		// - with whichever instance was threaded last, which is the wrong
		// instance for every other one.
		inst, _ := addrs.ParseModuleInstanceStr(`module.sites["a"]`)
		_ = eval.WithModuleInstance(inst)
		if _, diags := eval.Evaluate(t.Context(), expr, ident); !diags.HasErrors() {
			t.Error("the base evaluator answers after another evaluator was threaded; WithModuleInstance mutated in place")
		}
	})
}

// withModulePrefix binds tofu.marker_module_prefix for one module instance,
// the way internal/tofu's evaluator binds it at plan and apply time.
//
// The value comes from [markers.ModulePrefix] applied to a parsed module
// instance address - the production function, not a string spelled out here -
// so a test that then asserts a whole marker by value is asserting what the
// real evaluator would produce, not what this helper decided.
func withModulePrefix(t *testing.T, vars map[string]cty.Value, modInst string) map[string]cty.Value {
	t.Helper()

	inst, diags := addrs.ParseModuleInstanceStr(modInst)
	if diags.HasErrors() {
		t.Fatalf("parsing module instance %s: %s", modInst, diags.Err())
	}
	prefix, ok := markers.ModulePrefix(inst)
	if !ok {
		t.Fatalf("markers.ModulePrefix declined %s", modInst)
	}

	out := make(map[string]cty.Value, len(vars)+1)
	for k, v := range vars {
		out[k] = v
	}
	obj := cty.ObjectVal(map[string]cty.Value{markers.ModulePrefixAttr: cty.StringVal(prefix)})
	out["tofu"] = obj
	out["terraform"] = obj
	return out
}
