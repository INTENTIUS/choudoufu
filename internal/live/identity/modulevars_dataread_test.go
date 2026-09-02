// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// Issue #313. The pre-resolution read phase's value has to survive a module
// CALL, not only a same-module reference, and the call it has to survive is
// the plain one: no count, no for_each. That was the gap - [resolver.callerVariables]
// declined to rebuild a var.* closure for a module path with no repeating
// call on it, so the child read var.azs through the closure
// [configs.ModuleCall.decodeStaticVariables] froze at load time, which by
// construction has never seen a data lookup.
//
// The two halves disagreed about one configuration as a result:
// internal/live/dataread's [Analyze] classified the source readable (its own
// liveModuleEvaluator rebuilds this same chain with no gate at all), [Read]
// read it for real against the provider, and resolution then refused the
// child's count anyway. corpus-security-group-complete is where that
// surfaced; terraform-aws-modules/vpc's azs list is the idiom.
//
// These assert the rendered identity by value, not a predicate: the whole
// point of carrying the value across the call is which live object each
// instance names, and an instance count that moved without the strings
// being checked has twice been a defect wearing an improvement's numbers.

// TestDataResultCrossesAPlainModuleCall pins the values.
func TestDataResultCrossesAPlainModuleCall(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "data-read-across-module-call"), nil)

	results := map[string]cty.Value{
		"data.aws_availability_zones.available": cty.ObjectVal(map[string]cty.Value{
			"names": cty.ListVal([]cty.Value{
				cty.StringVal("eu-west-1a"),
				cty.StringVal("eu-west-1b"),
				cty.StringVal("eu-west-1c"),
			}),
		}),
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{DataResults: results})
	assertNoErrors(t, diags)

	// slice(names, 0, 2) is two AZs, so each block makes exactly two
	// instances. The count is asserted apart from the key set on purpose:
	// one bug's entire signature was two instances where OpenTofu makes
	// three.
	want := map[string]string{
		`module.net.aws_cloudwatch_log_group.per_az["eu-west-1a"]`: "/net/eu-west-1a",
		`module.net.aws_cloudwatch_log_group.per_az["eu-west-1b"]`: "/net/eu-west-1b",
		`module.net.aws_cloudwatch_log_group.by_index[0]`:          "/net-idx/eu-west-1a",
		`module.net.aws_cloudwatch_log_group.by_index[1]`:          "/net-idx/eu-west-1b",
	}
	got := 0
	for _, r := range result.All() {
		if _, ok := want[r.Addr.String()]; ok {
			got++
		}
	}
	if got != len(want) {
		var have []string
		for _, r := range result.All() {
			have = append(have, r.Addr.String())
		}
		t.Fatalf("resolved %d of the %d expected instances; got %v", got, len(want), have)
	}
	for addr, id := range want {
		res := resolutionAt(t, result, addr)
		if res.Class != ClassConcrete {
			t.Errorf("%s resolved %s, want %s: with the data result present every value it needs is in hand", addr, res.Class, ClassConcrete)
		}
		if res.ImportID != id {
			t.Errorf("%s resolved to %q, want %q", addr, res.ImportID, id)
		}
	}
}

// TestPlainModuleCallStillRefusesWithoutResults is the disabled-fix proof
// and, at the same time, the offline guarantee. No read results means the
// resolver's data index is nil, [resolver.ancestorCarriesResults] answers
// false, and every module instance keeps the frozen closure it always had -
// so live-check, which never reads anything, is bit-for-bit unaffected by
// this change. If this ever stops refusing, the widening has escaped the
// condition it is supposed to be scoped to.
func TestPlainModuleCallStillRefusesWithoutResults(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "data-read-across-module-call"), nil)

	result, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatalf("resolution with no data results accepted a data-source reference feeding a child module's expansion")
	}
	if !hasDiag(diags, "Dynamic value in static context", "data.aws_availability_zones.available") {
		t.Errorf("expected the existing dynamic-value refusal naming the data source, got:\n%s", renderDiags(diags))
	}
	for _, r := range result.All() {
		if r.Addr.Module.IsRoot() {
			continue
		}
		t.Errorf("%s resolved despite the data source it expands over being refused", r.Addr)
	}
}

// TestAncestorCoverageDoesNotLeakSideways is the scoping proof for the new
// condition. [resolver.ancestorCarriesResults] must look only at module
// instances STRICTLY ABOVE the one being resolved: coverage that exists in
// a SIBLING module is not a reason to rebuild anything, and must never
// answer a reference in this one. Read through the resolver's own lookup,
// which is what the evaluator consults.
func TestAncestorCoverageDoesNotLeakSideways(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "data-read-across-module-call"), nil)

	// Coverage in a module that is not an ancestor of module.net.
	r := newResolver(context.Background(), cfg, Context{DataResults: map[string]cty.Value{
		"module.elsewhere.data.aws_availability_zones.available": cty.ObjectVal(map[string]cty.Value{
			"names": cty.ListVal([]cty.Value{cty.StringVal("eu-west-1a")}),
		}),
	}})
	if r.ancestorCarriesResults(mustAddr(t, "module.net.aws_cloudwatch_log_group.by_index[0]").Module) {
		t.Fatalf("a sibling module's read coverage was counted as an ancestor's")
	}
}
