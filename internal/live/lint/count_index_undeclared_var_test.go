// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"strings"
	"testing"
)

// TestCountIndexSurvivesAnUndeclaredVariableInAModuleArgument is the
// regression gate for the crash GitHub issue #304's fix introduced.
//
// #304 gave StaticEvaluator a second evaluation mode, EvaluateStructural,
// which deliberately renders an expression against an hcl.EvalContext that
// was built WHILE diagnostics were accumulating - the whole point being that
// one refused reference must not blank out the perfectly static ones beside
// it. Every earlier consumer of such a context was Scope.EvalExpr, which
// returns at its own diags.HasErrors() gate before it ever calls
// expr.Value, so nothing had ever read a context entry built on an error
// path.
//
// One of those entries was ill-formed and had been for years.
// normalizeRefValue (internal/lang/eval.go) answered an errored Data lookup
// with cty.UnknownVal(val.Type()), and staticScopeData.GetInputVariable
// answers an UNDECLARED variable with cty.NilVal - whose Type() is the zero
// cty.Type, a struct with a nil typeImpl interface. cty.UnknownVal of that
// is a value which compares unequal to cty.NilVal, so every `== cty.NilVal`
// guard in the codebase waves it through, and which segfaults inside cty
// the moment anything asks its type a question. hclsyntax's BinaryOpExpr
// asks one immediately, in convert.Convert on each operand.
//
// So the crash needed four things at once, which is why it took a real
// vendored module to find: a module-call argument that is a binary
// operation, one operand an undeclared variable, the argument feeding a
// child module's count, and a resource under that count whose
// identity-bearing argument reads count.index. testdata's fixture is the
// minimum that holds all four; see its own comments.
//
// The assertion is deliberately about the OUTCOME rather than about the
// absence of a panic: `go test` reports a panic as a failure anyway, so a
// test that only called CheckContext would pass just as well if the whole
// analysis silently degraded to answering nothing. What must hold is that
// the count-index rule still reaches this resource and still refuses it,
// because a count nobody can compute is a count whose instances nobody can
// prove distinct.
func TestCountIndexSurvivesAnUndeclaredVariableInAModuleArgument(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/count-index-undeclared-var")

	issues := CheckContext(t.Context(), cfg)

	var got []Issue
	for _, iss := range issues {
		if iss.Rule == RuleCountIndex {
			got = append(got, iss)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 %s issue, got %d (all issues: %s)", RuleCountIndex, len(got), summarizeIssues(issues))
	}
	if want := "count.index in aws_network_acl_rule.r"; got[0].Construct != want {
		t.Errorf("count-index issue names %q, want %q", got[0].Construct, want)
	}
	if want := "module.child"; got[0].Module.String() != want {
		t.Errorf("count-index issue is in module %q, want %q", got[0].Module.String(), want)
	}
}

func summarizeIssues(issues []Issue) string {
	if len(issues) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(issues))
	for _, iss := range issues {
		parts = append(parts, string(iss.Rule)+":"+iss.Construct)
	}
	return strings.Join(parts, ", ")
}
