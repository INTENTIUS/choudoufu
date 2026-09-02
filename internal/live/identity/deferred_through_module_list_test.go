// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

// GitHub issue #368 named two estates. This file measured the second one,
// corpus-rds-complete-postgres, refuted #368's reading of it, and now carries
// the fix for what it found instead.
//
// #368's premise: "the gap is specifically the function application, not the
// routing (#354's fix already reaches the routing half)". The function
// application is real and #368 landed it - see transform.go and
// TestTransformSoleElementOverADeferredList. The routing half was NOT
// reached, and this fixture is what proved it: four variants of one identity
// argument, all reading the SAME module output (module.vpc.vpc_cidr_block,
// itself `try(aws_vpc.this[0].cidr_block, null)`) through the SAME
// module-call list argument, of which exactly one resolved.
//
//	[var.L[0].cidr_blocks]                                RESOLVED
//	[var.L[count.index].cidr_blocks]                      refused
//	compact(split(",", lookup(var.L[0], "cidr_blocks", …)))          refused
//	compact(split(",", lookup(var.L[count.index], "cidr_blocks", …))) refused  <- the estate
//
// The first line is why the other three were routing failures rather than
// analysis gaps: the same output, the same argument, the same chase. What
// stopped them is that neither spelling is a bare traversal, so
// [resolver.namedLeaf]'s hcl.AbsTraversalForExpr gate declined before the
// chase began. computedselect.go folds both spellings into the traversal the
// author would have written with a constant index, and hands the result to
// that same chase; all four lines now resolve, and to the same parent read.
//
// The tests below assert that BY VALUE - the rendered import ID against a
// lookup that hands back a real CIDR - because a class check would be
// satisfied by three strings that are wrong in a cloud tag: the fallback
// each lookup() names, the caller's own from_port, and the module's own
// literal name. The controls beside them are the boundaries the fold must
// not cross.

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

// deferredListLookup answers the fixture's parent reads with what the cloud
// holds. The CIDR is deliberately not any literal the configuration writes,
// so a formula that predicted the value instead of reading it would render a
// different string here rather than pass by coincidence.
func deferredListLookup(t *testing.T) func(addrs.AbsResourceInstance, string) (string, bool) {
	t.Helper()
	live := map[string]string{
		"module.vpc.aws_vpc.this[0].cidr_block":         "10.44.0.0/16",
		"module.sg.aws_security_group.this[0].id":       "sg-0abc123",
		"module.sg_typed.aws_security_group.this[0].id": "sg-0def456",
	}
	return func(inst addrs.AbsResourceInstance, attr string) (string, bool) {
		v, ok := live[inst.String()+"."+attr]
		return v, ok
	}
}

// deferredListCase asserts one variant's formula and the identity it renders.
func deferredListCase(t *testing.T, result *Result, prefix, wantFormula, wantID string) {
	t.Helper()

	res, ok := resolutionForPrefix(result, prefix)
	if !ok {
		t.Fatalf("%s produced no resolution at all", prefix)
	}
	if res.Class != ClassParentDerived {
		t.Fatalf("%s resolved %s, want PARENT_DERIVED", res.Addr, res.Class)
	}
	if got := res.Formula.String(); got != wantFormula {
		t.Errorf("%s formula is %q, want %q", res.Addr, got, wantFormula)
	}
	got, rendered := res.Formula.Render(deferredListLookup(t))
	if !rendered {
		t.Fatalf("%s did not render against a known parent", res.Addr)
	}
	if got != wantID {
		t.Errorf("%s renders %q, want %q", res.Addr, got, wantID)
	}
}

// TestDeferredThroughModuleListLiteralIndexResolves is the control that made
// the other three findings rather than "a module boundary stops everything",
// and it is unchanged by the fix: the same output, selected out of the same
// list argument with a constant index.
func TestDeferredThroughModuleListLiteralIndexResolves(t *testing.T) {
	deferredListCase(t, resolveDeferredThroughModuleList(t),
		"module.sg.aws_security_group_rule.literal_index[",
		"${module.sg.aws_security_group.this[0].id}_ingress_tcp_5435_5435_${module.vpc.aws_vpc.this[0].cidr_block}",
		"sg-0abc123_ingress_tcp_5435_5435_10.44.0.0/16")
}

// TestDeferredThroughModuleListCountIndexResolves is the first of the two
// routing gaps: `var.<list>[count.index]` is an IndexExpr, not a traversal,
// so the chase was never entered. The index is folded by evaluating it in
// this instance's own scope - the same evaluation
// [resolver.resolveIndexedTraversal] already makes for
// `aws_subnet.this[count.index].id` - and what it renders is byte-identical
// to the literal-index control above, which is the point.
func TestDeferredThroughModuleListCountIndexResolves(t *testing.T) {
	deferredListCase(t, resolveDeferredThroughModuleList(t),
		"module.sg.aws_security_group_rule.count_index_only[",
		"${module.sg.aws_security_group.this[0].id}_ingress_tcp_5433_5433_${module.vpc.aws_vpc.this[0].cidr_block}",
		"sg-0abc123_ingress_tcp_5433_5433_10.44.0.0/16")
}

// TestDeferredThroughModuleListLookupResolves is the second gap:
// `lookup(<a module-call argument>, "key", <default>)` had no route at all,
// because [resolver.resolveLookupCall] reads each.value alone. The call is
// folded into one attribute step, exactly as
// [resolver.eachValueSelector] folds the same call for each.value.
//
// The rendered ID is what makes this an assertion rather than a class check:
// "" is the fallback this lookup names, and a fold that took it would render
// nothing where 10.44.0.0/16 belongs.
func TestDeferredThroughModuleListLookupResolves(t *testing.T) {
	deferredListCase(t, resolveDeferredThroughModuleList(t),
		"module.sg.aws_security_group_rule.lookup_only[",
		`${module.sg.aws_security_group.this[0].id}_ingress_tcp_5434_5434_${one(compact(split(",", module.vpc.aws_vpc.this[0].cidr_block)))}`,
		"sg-0abc123_ingress_tcp_5434_5434_10.44.0.0/16")
}

// TestDeferredThroughModuleListEstateShapeResolves is
// corpus-rds-complete-postgres itself: both routing gaps at once, with #368's
// own compact/split transform on top of them.
func TestDeferredThroughModuleListEstateShapeResolves(t *testing.T) {
	deferredListCase(t, resolveDeferredThroughModuleList(t),
		"module.sg.aws_security_group_rule.estate_shape[",
		`${module.sg.aws_security_group.this[0].id}_ingress_tcp_5432_5432_${one(compact(split(",", module.vpc.aws_vpc.this[0].cidr_block)))}`,
		"sg-0abc123_ingress_tcp_5432_5432_10.44.0.0/16")
}

// TestDeferredThroughModuleListTypedHopsResolve is the declared-type gate
// proving it is a rule rather than a blanket refusal. Both declarations
// convert the selected leaf to a string, which is the identity function on
// what the caller wrote, so both resolve to the caller's own expression -
// and the map one is terraform-aws-modules/security-group's own declaration
// in shape.
func TestDeferredThroughModuleListTypedHopsResolve(t *testing.T) {
	result := resolveDeferredThroughModuleList(t)

	deferredListCase(t, result,
		"module.sg_typed.aws_security_group_rule.typed_object_string[",
		`${module.sg_typed.aws_security_group.this[0].id}_ingress_tcp_5437_5437_${one(compact(split(",", module.vpc.aws_vpc.this[0].cidr_block)))}`,
		"sg-0def456_ingress_tcp_5437_5437_10.44.0.0/16")

	deferredListCase(t, result,
		"module.sg_typed.aws_security_group_rule.typed_map_string[",
		`${module.sg_typed.aws_security_group.this[0].id}_ingress_tcp_5438_5438_${one(compact(split(",", module.vpc.aws_vpc.this[0].cidr_block)))}`,
		"sg-0def456_ingress_tcp_5438_5438_10.44.0.0/16")
}

// TestDeferredThroughModuleListControlsDoNotResolve is the safety half, and
// the reason the fold is a route rather than a licence. In both cases the
// value OpenTofu computes is lookup()'s THIRD argument, not the caller's own
// expression - and lookup() takes it silently, without raising, which is
// what makes this the wrong-marker shape rather than a refusal shape.
//
// Both fallbacks are deliberately uncomputable here (the caller sets them
// from a second VPC's attribute through a module output), so the right
// verdict is a refusal and nothing else can supply a correct answer from
// another route. With a computable fallback both of these resolve to it
// through [resolver.tolerantPart], correctly, and neither would be a control
// at all - which is how they were first written and why they are not now.
//
// Mutation-checked: making [declaredSelectionIsIdentity] answer true
// unconditionally, and changing nothing else, resolves typed_object_missing
// to `${module.vpc.aws_vpc.this[0].cidr_block}` - the caller's own leaf,
// dropped by the conversion before this module ever sees it.
func TestDeferredThroughModuleListControlsDoNotResolve(t *testing.T) {
	result := resolveDeferredThroughModuleList(t)

	for _, tc := range []struct{ prefix, why string }{
		{
			"module.sg.aws_security_group_rule.absent_key_control[",
			"the caller's element has no such key, so the language takes lookup()'s third argument; the chase finds nothing and must decline rather than render either side",
		},
		{
			"module.sg_typed.aws_security_group_rule.typed_object_missing[",
			"the declared object type does not have cidr_blocks, so the conversion drops what the caller wrote and the module reads the fallback",
		},
	} {
		if res, ok := resolutionForPrefix(result, tc.prefix); ok {
			t.Errorf("%s resolved to %q / %q - %s", res.Addr, res.ImportID, res.Formula.String(), tc.why)
		}
	}
}
