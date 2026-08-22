// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #368: [Formula] could not express a pure function applied to a
// value it only learns at render time.
//
// Every assertion here is on the RENDERED IDENTITY, by value, and the render
// is driven by a lookup that hands back the strings a real cloud would - not
// on the class, and not on a plan coming back empty. That is HANDOFF.md's
// safety rule applied to the one change most able to break it: a transform
// that guessed at the shape of an ARN instead of computing over the real one
// would still classify PARENT_DERIVED, still converge, and still adopt the
// wrong object.

// transformTestSchemas describes every parent whose non-identity attribute
// these fixtures read. aws_ecs_cluster.arn is Computed, which is both the
// real shape and the case [resolver.siblingLiteralExpr] refuses on purpose;
// aws_vpc.cidr_block is Optional+Computed for the same reason the real
// provider marks it so (an IPAM pool can supply it), which is what makes the
// read deferred rather than answerable from the configuration.
func transformTestSchemas() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"aws_ecs_cluster": {args: map[string]string{"name": "req", "arn": "comp"}},
		"aws_ecs_service": {args: map[string]string{"name": "req", "cluster": "req", "arn": "comp"}},
		"aws_appautoscaling_target": {args: map[string]string{
			"resource_id": "req", "scalable_dimension": "req", "service_namespace": "req",
		}},
		"aws_appautoscaling_policy": {args: map[string]string{
			"name": "req", "resource_id": "req", "scalable_dimension": "req", "service_namespace": "req",
		}},
		"aws_vpc":            {args: map[string]string{"cidr_block": "optcomp", "id": "optcomp"}},
		"aws_security_group": serverAssignedLike("name"),
		"aws_security_group_rule": {args: map[string]string{
			"security_group_id": "req", "type": "req", "cidr_blocks": "opt",
			"from_port": "req", "to_port": "req", "protocol": "req",
		}},
	})
}

// clusterARN is what the projection's lookup returns for the ECS cluster's
// arn once it has been imported and read. The name in it is deliberately
// NOT the same string as anything else in the fixture, so a transform that
// silently produced the module call's own `name` argument instead of
// splitting the real value would fail below rather than pass by coincidence.
const clusterARN = "arn:aws:ecs:us-east-1:123456789012:cluster/ex-fargate-live"

func resolveTransformFixture(t *testing.T) *Result {
	t.Helper()
	cfg := loadConfigTree(t, filepath.Join("testdata", "formula-transform"), nil)
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: transformTestSchemas()})
	if result == nil {
		t.Fatal("resolution produced no result at all")
	}
	// The three negative controls are the only things that may refuse here.
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		detail := d.Description().Detail
		if strings.Contains(detail, "_control") {
			continue
		}
		t.Errorf("unexpected refusal: %s | %s", d.Description().Summary, detail)
	}
	return result
}

// transformLookup answers a formula's parent reads with what the cloud holds.
func transformLookup(t *testing.T) func(addrs.AbsResourceInstance, string) (string, bool) {
	t.Helper()
	live := map[string]string{
		"module.cluster.aws_ecs_cluster.this[0].arn": clusterARN,
		"aws_security_group.this[0].id":              "sg-0abc123",
		"aws_vpc.this[0].cidr_block":                 "10.0.0.0/16",
	}
	return func(inst addrs.AbsResourceInstance, attr string) (string, bool) {
		v, ok := live[inst.String()+"."+attr]
		return v, ok
	}
}

// TestTransformECSClusterNameFromARN is corpus-ecs-fargate's own blocker,
// asserted by value.
//
// Four strings would satisfy a class check and be wrong in a cloud tag: the
// whole ARN (the untransformed read), the module call's `name` argument
// "ex-fargate" (a prediction of what the ARN's tail must be, which is exactly
// the guess this must not make), element 0 of the split, and the empty string
// try()'s own fallback supplies. The assertion is on the rendered import ID,
// and the live ARN's tail is deliberately a different string from the config's
// name so that only a real split can produce it.
func TestTransformECSClusterNameFromARN(t *testing.T) {
	result := resolveTransformFixture(t)

	res := resolutionAt(t, result, "module.svc.aws_appautoscaling_target.this[0]")
	if res.Class != ClassParentDerived {
		t.Fatalf("resolved %s, want PARENT_DERIVED", res.Class)
	}
	const wantFormula = `ecs/service/${element(split("/", module.cluster.aws_ecs_cluster.this[0].arn), 1)}/ex-fargate/ecs:service:DesiredCount`
	if got := res.Formula.String(); got != wantFormula {
		t.Errorf("formula is %q, want %q", got, wantFormula)
	}

	got, ok := res.Formula.Render(transformLookup(t))
	if !ok {
		t.Fatal("the formula did not render against a known parent")
	}
	const wantID = "ecs/service/ex-fargate-live/ex-fargate/ecs:service:DesiredCount"
	if got != wantID {
		t.Errorf("renders %q, want %q", got, wantID)
	}
}

// TestTransformCarriesThroughAnAttributeRead is the cascade the estate
// actually fails on: aws_appautoscaling_policy does not compute the cluster
// name itself, it reads aws_appautoscaling_target's resource_id back.
// [Resolution.attrParts] hands over the parent's own parts for that
// attribute, transform included, so the child's identity has to render to the
// same live string the parent's does.
func TestTransformCarriesThroughAnAttributeRead(t *testing.T) {
	result := resolveTransformFixture(t)

	res := resolutionAt(t, result, `module.svc.aws_appautoscaling_policy.this["cpu"]`)
	if res.Class != ClassParentDerived {
		t.Fatalf("resolved %s, want PARENT_DERIVED", res.Class)
	}
	got, ok := res.Formula.Render(transformLookup(t))
	if !ok {
		t.Fatal("the formula did not render against a known parent")
	}
	const wantID = "ecs/service/ex-fargate-live/ex-fargate/ecs:service:DesiredCount/cpu"
	if got != wantID {
		t.Errorf("renders %q, want %q", got, wantID)
	}
}

// TestTransformSoleElementOverADeferredList is the security-group half:
// `compact(split(",", <a deferred read>))` on a [Component.SoleElement]
// component, where the list's LENGTH is a function of the live value and
// neither existing narrowing can count it.
func TestTransformSoleElementOverADeferredList(t *testing.T) {
	result := resolveTransformFixture(t)

	res := resolutionAt(t, result, "aws_security_group_rule.ingress[0]")
	if res.Class != ClassParentDerived {
		t.Fatalf("resolved %s, want PARENT_DERIVED", res.Class)
	}
	const wantFormula = `${aws_security_group.this[0].id}_ingress_tcp_5432_5432_${one(compact(split(",", aws_vpc.this[0].cidr_block)))}`
	if got := res.Formula.String(); got != wantFormula {
		t.Errorf("formula is %q, want %q", got, wantFormula)
	}

	got, ok := res.Formula.Render(transformLookup(t))
	if !ok {
		t.Fatal("the formula did not render against a known parent")
	}
	const wantID = "sg-0abc123_ingress_tcp_5432_5432_10.0.0.0/16"
	if got != wantID {
		t.Errorf("renders %q, want %q", got, wantID)
	}
}

// TestTransformRefusesToRenderAnAmbiguousList is the render-time half of the
// safety rule, and the reason deferring the count is not the same as guessing
// it. The formula above is built without knowing how many elements the split
// produces; a value that produces two is exactly the case
// [resolver.soleElementExpr] refuses on the spot when it CAN count, so it has
// to refuse here too - as a formula that does not render, which is a missing
// marker rather than a wrong one.
func TestTransformRefusesToRenderAnAmbiguousList(t *testing.T) {
	result := resolveTransformFixture(t)
	res := resolutionAt(t, result, "aws_security_group_rule.ingress[0]")

	two := func(inst addrs.AbsResourceInstance, attr string) (string, bool) {
		if inst.String() == "aws_vpc.this[0]" && attr == "cidr_block" {
			return "10.0.0.0/16,10.1.0.0/16", true
		}
		return transformLookup(t)(inst, attr)
	}
	if got, ok := res.Formula.Render(two); ok {
		t.Errorf("rendered %q from a two-element list; want no identity at all", got)
	}

	// The same formula over a value whose split has an EMPTY member still
	// renders, because compact() drops it and one element is left. This is
	// the compact() step earning its place rather than being decoration.
	trailing := func(inst addrs.AbsResourceInstance, attr string) (string, bool) {
		if inst.String() == "aws_vpc.this[0]" && attr == "cidr_block" {
			return "10.0.0.0/16,", true
		}
		return transformLookup(t)(inst, attr)
	}
	got, ok := res.Formula.Render(trailing)
	if !ok {
		t.Fatal("a trailing empty member should compact away, leaving one element")
	}
	if want := "sg-0abc123_ingress_tcp_5432_5432_10.0.0.0/16"; got != want {
		t.Errorf("renders %q, want %q", got, want)
	}
}

// TestTransformControlsDoNotResolve pins the three boundaries the fixture's
// control resources name. Each must produce no resolution at all: a transform
// that quietly widened one of them would be invisible to every aggregate this
// repository records.
func TestTransformControlsDoNotResolve(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "formula-transform"), nil)
	result, _ := ResolveWith(context.Background(), cfg, Context{Schemas: transformTestSchemas()})

	for _, name := range []string{"multipart_control", "negative_index_control", "list_valued_control"} {
		prefix := "aws_security_group_rule." + name + "["
		for _, r := range result.All() {
			if strings.HasPrefix(r.Addr.String(), prefix) {
				t.Errorf("%s resolved to %q / %q; it names a boundary that must stay closed",
					r.Addr, r.ImportID, r.Formula.String())
			}
		}
	}
}

// TestApplyOpsBoundaries is [applyOps] on its own, because the rendering
// contract is the whole of what keeps a transform from becoming a guess.
// Every false below is an identity this package declines to write.
func TestApplyOpsBoundaries(t *testing.T) {
	split := Op{Kind: OpSplit, Sep: "/"}

	tests := []struct {
		name string
		ops  []Op
		in   string
		want string
		ok   bool
	}{
		{"no ops is the identity function", nil, "abc", "abc", true},
		{"element wraps modulo length, as element() does",
			[]Op{split, {Kind: OpElement, Index: 3}}, "a/b/c", "a", true},
		{"index does not wrap",
			[]Op{split, {Kind: OpIndex, Index: 3}}, "a/b/c", "", false},
		{"an arn that does not carry the separator yields one element",
			[]Op{split, {Kind: OpIndex, Index: 1}}, "no-separator-here", "", false},
		{"element over an empty list is undefined",
			[]Op{{Kind: OpSplit, Sep: ","}, {Kind: OpCompact}, {Kind: OpElement, Index: 0}}, "", "", false},
		{"sole selects the single member",
			[]Op{{Kind: OpSplit, Sep: ","}, {Kind: OpCompact}, {Kind: OpSole}}, ",10.0.0.0/16,", "10.0.0.0/16", true},
		{"sole declines two members",
			[]Op{{Kind: OpSplit, Sep: ","}, {Kind: OpSole}}, "a,b", "", false},
		{"a pipeline that ends holding a list renders nothing",
			[]Op{split}, "a/b", "", false},
		{"compact of a string is not a pipeline",
			[]Op{{Kind: OpCompact}}, "a", "", false},
		{"an unknown operation renders nothing",
			[]Op{{Kind: OpKind("upper")}}, "a", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := applyOps(tc.ops, tc.in)
			if ok != tc.ok || got != tc.want {
				t.Errorf("applyOps(%v, %q) = %q, %v; want %q, %v", tc.ops, tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestOpsYieldAgreesWithApplyOps keeps the resolve-time shape check and the
// render-time value check from drifting apart. A pipeline opsYield calls well
// formed and string-yielding must be one applyOps can actually run to a
// string for SOME input; one it rejects must never render.
func TestOpsYieldAgreesWithApplyOps(t *testing.T) {
	pipelines := [][]Op{
		nil,
		{{Kind: OpSplit, Sep: "/"}},
		{{Kind: OpSplit, Sep: "/"}, {Kind: OpElement, Index: 1}},
		{{Kind: OpSplit, Sep: ","}, {Kind: OpCompact}},
		{{Kind: OpSplit, Sep: ","}, {Kind: OpCompact}, {Kind: OpSole}},
		{{Kind: OpCompact}},
		{{Kind: OpSole}},
		{{Kind: OpSplit, Sep: "/"}, {Kind: OpSplit, Sep: ","}},
		{{Kind: OpSplit, Sep: "/"}, {Kind: OpIndex, Index: 0}, {Kind: OpCompact}},
	}
	for _, ops := range pipelines {
		isList, wellFormed := opsYield(ops)
		_, rendered := applyOps(ops, "a/b")
		switch {
		case !wellFormed && rendered:
			t.Errorf("%v: opsYield says malformed but applyOps rendered it", ops)
		case wellFormed && isList && rendered:
			t.Errorf("%v: opsYield says it ends holding a list but applyOps rendered a string", ops)
		}
	}
}
