// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #346: `corpus-vpc-complete`, `corpus-rds-complete-postgres`,
// `corpus-ecs-fargate` and `corpus-autoscaling-complete` all blocked on one
// shape, and three design passes each corrected the last about which code
// path it travels. The fixture below reproduces the shape from
// terraform-aws-modules/terraform-aws-vpc's own examples/complete/main.tf,
// and the measurement that settled it is in this file rather than in prose,
// because two of the three passes argued from a reading of the code and the
// third from a run.
//
// # The shape, and the three separate walls in it
//
// A module call argument is a literal map; one leaf of it is a ONE-ELEMENT
// LIST holding another module call's output; that output reads a managed
// sibling's Optional+Computed attribute. The receiving module iterates the map
// with for_each and builds a [Component.SoleElement] identity argument out of
// each.value.
//
// Because one leaf is unevaluable, the for_each source is not evaluable as a
// value, so each.value is bound as an EXPRESSION (#260, eachvalue.go) and the
// whole argument travels the symbolic route. Three things then refuse in
// sequence, and every one of them had to move:
//
//  1. The one-element list is never narrowed. [resolver.soleElementExpr]
//     narrows syntactically over the expression the ARGUMENT was written with,
//     which here is `each.value.cidr_blocks` - not a list construct. The list
//     construct is in the ELEMENT. Measured before the fix: even a plain
//     `cidr_blocks = ["10.97.0.0/16"]` refused, as "Non-string identity
//     argument: string required, but have tuple", over a list the
//     configuration wrote out with one member in it.
//     [resolver.eachValueSoleElement] is the fix.
//  2. The estate spells the selection `lookup(each.value, "cidr_blocks",
//     null)` rather than `each.value.cidr_blocks`, and no rule recognised the
//     call. [resolver.resolveLookupCall] is the fix, decided the same way
//     [resolver.resolveFallbackChain] decides try().
//  3. The sibling's attribute is Computed, so [resolver.siblingLiteralExpr]
//     will not fold the configuration's own expression for it, and
//     [resolver.parentPart] would not defer a read of it either because the
//     sibling's class is [ClassNeedsDiscovery] rather than [ClassConcrete].
//     Widening that gate is the fix; see parentPart's own comment for why
//     discovery makes the two cases one, and internal/live/projection's
//     parentdefer_test.go for the negative control on it.
//
// # What was measured, and what it refutes
//
// The issue's third design pass concluded that "nothing routes a module-output
// reference reached through eachValueSelect into moduleOutputValue in the
// first place". That is not what refuses: [resolver.selectStatic] already
// chases a bare `module.<call>.<output>` leaf, and
// TestModuleOutputSiblingComputedBareOutput below is that leg on its own,
// passing with no new module-output plumbing at all. What the third pass saw
// was wall (1) wearing a module output's diagnostic: the leaf is inside a list
// construct, the list construct is what reaches the evaluator whole, and the
// evaluator names the module output because that is what is inside it.
//
// The same pass also left "re-run the measurement WITH schemas populated" as
// the first thing to do next, since [resolver.siblingLiteralExpr] returns
// applicable=false with no schemas and so was never exercised. Re-run: the
// diagnostics are byte-identical with and without schemas, and identical again
// with [Context.ManagedResults] supplied by hand. Schemas do not change wall
// (1)'s shape, because wall (1) is decided before any schema is consulted.

func moduleSiblingComputedSchemas() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"aws_vpc": {
			args:     map[string]string{"id": "optcomp", "cidr_block": "optcomp", "tags": "opt"},
			identity: map[string]string{"id": "req"},
		},
		"aws_security_group_rule": {
			args: map[string]string{
				"id": "optcomp", "security_group_id": "req", "type": "req",
				"protocol": "req", "from_port": "req", "to_port": "req",
				"cidr_blocks": "opt", "description": "opt",
			},
		},
	})
}

func resolveModuleSiblingComputed(t *testing.T) *Result {
	t.Helper()
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-sibling-computed"), nil)
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: moduleSiblingComputedSchemas()})
	if result == nil {
		t.Fatal("resolution produced no result at all")
	}
	// The two-element control is the only thing that may refuse here, and it
	// has its own test below; everything else refusing would make the
	// assertions in the other tests vacuous.
	for _, d := range diags {
		if d.Description().Summary == "Ambiguous list-valued identity argument" &&
			strings.Contains(d.Description().Detail, "endpoints_two") {
			continue
		}
		if d.Severity() == tfdiags.Error {
			t.Errorf("unexpected refusal: %s | %s", d.Description().Summary, d.Description().Detail)
		}
	}
	return result
}

// TestModuleOutputSiblingComputed is the issue itself, in both of the
// spellings it measured as identical: the value is asserted, not the class.
//
// The formula must read the SIBLING's attribute. Two other strings would look
// right to a class check and be wrong in a cloud tag: the module output's own
// name, and the VPC's import ID - so the assertion is on the rendered formula
// string.
func TestModuleOutputSiblingComputed(t *testing.T) {
	result := resolveModuleSiblingComputed(t)

	for _, tc := range []struct{ addr, want string }{
		// lookup(each.value, "cidr_blocks", null), the spelling the module
		// publishes.
		{`module.endpoints.aws_security_group_rule.this["ingress_https"]`,
			"sg-fixed_ingress_tcp_443_443_${module.vpc.aws_vpc.this[0].cidr_block}"},
		// each.value.cidr_blocks, the spelling the issue proved refuses
		// identically.
		{`module.endpoints.aws_security_group_rule.dotted["ingress_https"]`,
			"sg-fixed_ingress_tcp_1443_1443_${module.vpc.aws_vpc.this[0].cidr_block}"},
	} {
		res := resolutionAt(t, result, tc.addr)
		if res.Class != ClassParentDerived {
			t.Errorf("%s resolved %s, want PARENT_DERIVED", tc.addr, res.Class)
			continue
		}
		if got := res.Formula.String(); got != tc.want {
			t.Errorf("%s renders %q, want %q", tc.addr, got, tc.want)
		}
	}
}

// TestModuleOutputSiblingComputedListNarrowing is wall (1) on its own, with
// nothing about a module boundary or a sibling in it: a one-element list of a
// plain literal, selected out of an element bound as an expression.
//
// It resolves CONCRETE to the literal itself, which is the assertion that
// matters - a narrowing that picked the wrong element, or that rendered the
// list, would still classify CONCRETE.
func TestModuleOutputSiblingComputedListNarrowing(t *testing.T) {
	result := resolveModuleSiblingComputed(t)

	for _, tc := range []struct{ addr, want string }{
		{`module.endpoints_literal_list.aws_security_group_rule.this["ingress_https"]`,
			"sg-fixed_ingress_tcp_444_444_10.97.0.0/16"},
		{`module.endpoints_literal_list.aws_security_group_rule.dotted["ingress_https"]`,
			"sg-fixed_ingress_tcp_1444_1444_10.97.0.0/16"},
	} {
		res := resolutionAt(t, result, tc.addr)
		if res.Class != ClassConcrete {
			t.Errorf("%s resolved %s, want CONCRETE", tc.addr, res.Class)
			continue
		}
		if res.ImportID != tc.want {
			t.Errorf("%s renders %q, want %q", tc.addr, res.ImportID, tc.want)
		}
	}
}

// TestModuleOutputSiblingComputedThroughAnOutput is wall (1) again over a
// module output that reads nothing managed anywhere: an ordinary configuration
// value that crossed a module boundary. It resolves to the root module's own
// local, which is the only place that string is written down, so a fabricated
// or defaulted value would not spell it.
func TestModuleOutputSiblingComputedThroughAnOutput(t *testing.T) {
	result := resolveModuleSiblingComputed(t)

	res := resolutionAt(t, result, `module.endpoints_output_list.aws_security_group_rule.this["ingress_https"]`)
	if res.Class != ClassConcrete {
		t.Fatalf("resolved %s, want CONCRETE", res.Class)
	}
	if want := "sg-fixed_ingress_tcp_445_445_10.99.0.0/16"; res.ImportID != want {
		t.Errorf("renders %q, want %q", res.ImportID, want)
	}
}

// TestModuleOutputSiblingComputedBareOutput is the leg that refutes the
// issue's third design pass directly: the same sibling Computed attribute
// with NO list construct around it. [resolver.selectStatic]'s existing module
// chase carries it into the child module unaided, so what this exercises is
// wall (3) alone - parentPart deferring to a needs-discovery parent.
func TestModuleOutputSiblingComputedBareOutput(t *testing.T) {
	result := resolveModuleSiblingComputed(t)

	res := resolutionAt(t, result, `module.endpoints_bare_computed.aws_security_group_rule.this["ingress_https"]`)
	if res.Class != ClassParentDerived {
		t.Fatalf("resolved %s, want PARENT_DERIVED", res.Class)
	}
	if want := "sg-fixed_ingress_tcp_446_446_${module.vpc.aws_vpc.this[0].cidr_block}"; res.Formula.String() != want {
		t.Errorf("renders %q, want %q", res.Formula.String(), want)
	}
}

// TestModuleOutputSiblingComputedLookupFallback is [resolver.resolveLookupCall]'s
// other arm: a key the element provably does not have, so the language takes
// lookup()'s third argument. Absence has to be PROVED, not inferred from a
// failed selection - see eachvalue.go's own note on why a key that is there
// and merely unresolvable must not fall back - and the proof is
// [resolver.eachAttrAbsent]'s.
func TestModuleOutputSiblingComputedLookupFallback(t *testing.T) {
	result := resolveModuleSiblingComputed(t)

	res := resolutionAt(t, result, `module.endpoints.aws_security_group_rule.absent["ingress_https"]`)
	if res.Class != ClassConcrete {
		t.Fatalf("resolved %s, want CONCRETE", res.Class)
	}
	if want := "sg-fixed_ingress_tcp_2443_2443_10.94.0.0/16"; res.ImportID != want {
		t.Errorf("renders %q, want %q", res.ImportID, want)
	}
}

// TestModuleOutputSiblingComputedTwoElementsRefused is the control that keeps
// the narrowing a rule rather than a licence. Two CIDRs in the list, and the
// AWS API - not this configuration's list order - decides how they compose, so
// this refuses with the same words the syntactic path refuses with.
//
// A fix that resolved this to either element would move every count in this
// repository in the "good" direction and write a marker naming a rule that
// does not exist.
func TestModuleOutputSiblingComputedTwoElementsRefused(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-sibling-computed"), nil)
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: moduleSiblingComputedSchemas()})

	for _, addr := range []string{
		`module.endpoints_two.aws_security_group_rule.this["ingress_https"]`,
		`module.endpoints_two.aws_security_group_rule.dotted["ingress_https"]`,
	} {
		for _, r := range result.All() {
			if r.Addr.String() == addr {
				t.Errorf("%s resolved to %q but must refuse: the list has two elements", addr, r.ImportID)
			}
		}
	}

	var saw int
	for _, d := range diags {
		if d.Description().Summary == "Ambiguous list-valued identity argument" &&
			strings.Contains(d.Description().Detail, "endpoints_two") &&
			strings.Contains(d.Description().Detail, "has 2 elements") {
			saw++
		}
	}
	if saw != 2 {
		t.Errorf("%d refusals said the list had two elements, want 2:\n%s", saw, renderDiags(diags))
	}
}

// TestModuleOutputSiblingComputedNeedsSchemas keeps wall (3) a schema rule.
// With no schemas there is no source of truth for what the sibling's object
// carries, so the deferred read is refused exactly as it was before #346 -
// which is also why internal/live/check's TestIdentityGolden, which resolves
// every fixture without schemas on purpose, records no line for the two
// module calls that need one.
func TestModuleOutputSiblingComputedNeedsSchemas(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-sibling-computed"), nil)
	result, diags := ResolveWith(context.Background(), cfg, Context{})

	if !diags.HasErrors() {
		t.Fatal("a sibling's Computed attribute was deferred to with no provider schemas")
	}
	for _, r := range result.All() {
		if strings.HasPrefix(r.Addr.String(), "module.endpoints.aws_security_group_rule.this") ||
			strings.HasPrefix(r.Addr.String(), "module.endpoints_bare_computed.aws_security_group_rule.this") {
			t.Errorf("%s resolved with no schemas to confirm aws_vpc carries cidr_block", r.Addr)
		}
	}
	// Wall (1) is decided before any schema is consulted, so the narrowing
	// still happens and the literal-list calls still resolve. That split is
	// the point: the golden's added lines are exactly these.
	res := resolutionAt(t, result, `module.endpoints_literal_list.aws_security_group_rule.this["ingress_https"]`)
	if res.Class != ClassConcrete {
		t.Errorf("the literal-list narrowing needs no schema and resolved %s", res.Class)
	}
}
