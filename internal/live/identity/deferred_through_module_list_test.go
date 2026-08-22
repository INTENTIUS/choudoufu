// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// GitHub issue #368 named two estates. This file is the measurement that
// refutes its reading of the second one, corpus-rds-complete-postgres, and
// says exactly what does block it instead.
//
// #368's premise: "the gap is specifically the function application, not the
// routing (#354's fix already reaches the routing half)". The function
// application is real and #368 landed it - see transform.go and
// TestTransformSoleElementOverADeferredList, where
// `compact(split(",", <a deferred read>))` on a [Component.SoleElement]
// component resolves and renders by value. corpus-ecs-fargate's eight
// identity diagnostics went to zero on exactly that mechanism.
//
// This estate did not move, and the fixture beside this file says why. Four
// variants of one identity argument, all reading the SAME module output
// (module.vpc.vpc_cidr_block, itself `try(aws_vpc.this[0].cidr_block,
// null)`) through the SAME module-call list argument:
//
//	[var.L[0].cidr_blocks]                                RESOLVES
//	[var.L[count.index].cidr_blocks]                      refuses
//	compact(split(",", lookup(var.L[0], "cidr_blocks", …)))          refuses
//	compact(split(",", lookup(var.L[count.index], "cidr_blocks", …))) refuses  <- the estate
//
// So the function application is one of TWO independent blockers, not the
// blocker, and the other one is routing:
//
//   - `var.<list>[count.index]` is not an absolute traversal, because the
//     index is not a constant. [resolver.namedLeaf] gates on
//     hcl.AbsTraversalForExpr, so it never chases the reference back across
//     the module-call boundary to the caller's element expression at all -
//     the hop that the literal-index variant takes and resolves through.
//   - `lookup(<a selectable expression>, "<static key>", <default>)` has no
//     route either. [resolver.resolveLookupCall] exists but is `each.value`'s
//     alone; a lookup over a module-call argument is a FunctionCallExpr that
//     [resolver.namedLeaf] does not recognize and no handler claims.
//
// Both are #354's family (a value-shaped route that cannot carry a deferred
// read), both are tractable, and neither is attempted here. When either is
// fixed, the corresponding line below stops being an expected refusal and
// this test is the thing that says so.

func resolveDeferredThroughModuleList(t *testing.T) *Result {
	t.Helper()
	cfg := loadConfigTree(t, filepath.Join("testdata", "deferred-through-module-list"), nil)
	result, _ := ResolveWith(context.Background(), cfg, Context{Schemas: transformTestSchemas()})
	if result == nil {
		t.Fatal("resolution produced no result at all")
	}
	return result
}

func resolutionForPrefix(result *Result, prefix string) (Resolution, bool) {
	for _, r := range result.All() {
		if strings.HasPrefix(r.Addr.String(), prefix) {
			return r, true
		}
	}
	return Resolution{}, false
}

// TestDeferredThroughModuleListLiteralIndexResolves is the control, and the
// reason the three refusals below are findings rather than "a module
// boundary stops everything". The very same module output, selected out of
// the very same list argument with a constant index, resolves to a formula
// over the VPC's own attribute.
func TestDeferredThroughModuleListLiteralIndexResolves(t *testing.T) {
	result := resolveDeferredThroughModuleList(t)

	res, ok := resolutionForPrefix(result, "module.sg.aws_security_group_rule.literal_index[")
	if !ok {
		t.Fatal("the literal-index variant produced no resolution at all")
	}
	if res.Class != ClassParentDerived {
		t.Fatalf("resolved %s, want PARENT_DERIVED", res.Class)
	}
	const want = "${module.sg.aws_security_group.this[0].id}_ingress_tcp_5435_5435_${module.vpc.aws_vpc.this[0].cidr_block}"
	if got := res.Formula.String(); got != want {
		t.Errorf("formula is %q, want %q", got, want)
	}
}

// TestDeferredThroughModuleListGapsStandRecords what does NOT resolve, and
// which of the two routing gaps each variant isolates. Every line here is a
// gap this repository has measured and not yet closed; a line that starts
// passing is a fix, and updating this test is how it gets claimed.
func TestDeferredThroughModuleListGapsStand(t *testing.T) {
	result := resolveDeferredThroughModuleList(t)

	for _, tc := range []struct {
		name string
		why  string
	}{
		{
			"count_index_only",
			"var.<list>[count.index] is not an absolute traversal, so resolver.namedLeaf never chases it across the module-call boundary",
		},
		{
			"lookup_only",
			"lookup(<a module-call argument>, \"key\", default) has no route; resolver.resolveLookupCall is each.value's alone",
		},
		{
			"estate_shape",
			"corpus-rds-complete-postgres itself: both gaps at once, plus the transform #368 landed on top of them",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := "module.sg.aws_security_group_rule." + tc.name + "["
			if res, ok := resolutionForPrefix(result, prefix); ok {
				t.Errorf("%s now resolves to %q / %q - if that is a fix, say so here and delete this case (%s)",
					res.Addr, res.ImportID, res.Formula.String(), tc.why)
			}
		})
	}
}
