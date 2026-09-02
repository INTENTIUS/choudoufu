// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/zclconf/go-cty/cty"
)

// This file pins [StaticEvaluator.WithRepetitionData] (issue #213): the seam
// that lets a local value's own definition see the each.key/each.value/
// count.index a caller's per-instance evaluation already knows, at any
// nesting depth GetLocalValue's own reference resolution reaches - not only
// the top-level expression a caller hands to [StaticEvaluator.Evaluate].
//
// Every test here builds its own evaluator directly (mirroring
// TestStaticScope_GetLocalValue), rather than going through
// internal/live/identity, because the correctness question at this layer is
// narrower and mechanical: does the evaluator answer each/count exactly from
// what WithRepetitionData was given, at every depth, and refuse exactly
// when a field was left unset. internal/live/identity's own tests
// (foreachrepetition_test.go) cover the caller-side question - that the
// data handed in here is actually the calling instance's own data.

func repetitionEval(t *testing.T, source string) *StaticEvaluator {
	t.Helper()
	p := testParser(map[string]string{"test.tf": source})

	call := NewStaticModuleCall(
		addrs.RootModule, hcl.Range{},
		func(v *Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		".",
		"irrelevant",
	)
	mod, diags := p.LoadConfigDir(".", call)
	assertNoDiagnostics(t, diags)
	return NewStaticEvaluator(mod, call)
}

// TestStaticEvaluator_WithRepetitionData_TopLevel pins the base case: a
// direct each/count reference resolves when WithRepetitionData covers it,
// and still refuses cleanly - not a panic - when it does not.
func TestStaticEvaluator_WithRepetitionData_TopLevel(t *testing.T) {
	eval := repetitionEval(t, `locals { unused = "x" }`)
	ident := StaticIdentifier{Subject: "test.arg"}

	t.Run("no repetition data refuses", func(t *testing.T) {
		expr := hclExprMustParse(t, "each.value")
		_, diags := eval.Evaluate(t.Context(), expr, ident)
		if !diags.HasErrors() {
			t.Fatal("expected a refusal with no repetition data supplied")
		}
		if !diagsContain(diags, "Dynamic value in static context") {
			t.Errorf("wrong diagnostic:\n%s", diags.Error())
		}
	})

	t.Run("covered each.value resolves", func(t *testing.T) {
		withRep := eval.WithRepetitionData(instances.RepetitionData{
			EachKey:   cty.StringVal("k1"),
			EachValue: cty.StringVal("v1"),
		})
		expr := hclExprMustParse(t, "each.value")
		got, diags := withRep.Evaluate(t.Context(), expr, ident)
		assertNoDiagnostics(t, diags)
		if !got.RawEquals(cty.StringVal("v1")) {
			t.Errorf("got %#v, want cty.StringVal(\"v1\")", got)
		}
	})

	t.Run("count.index resolves and each.value still refuses when only count is given", func(t *testing.T) {
		withRep := eval.WithRepetitionData(instances.RepetitionData{
			CountIndex: cty.NumberIntVal(2),
		})

		idxExpr := hclExprMustParse(t, "count.index")
		got, diags := withRep.Evaluate(t.Context(), idxExpr, ident)
		assertNoDiagnostics(t, diags)
		if !got.RawEquals(cty.NumberIntVal(2)) {
			t.Errorf("got %#v, want cty.NumberIntVal(2)", got)
		}

		eachExpr := hclExprMustParse(t, "each.value")
		_, diags = withRep.Evaluate(t.Context(), eachExpr, ident)
		if !diags.HasErrors() {
			t.Fatal("expected each.value to refuse when only count data was given")
		}
	})

	// This is the direct test for the "bound to the key on both sides"
	// failure mode named in issue #213's brief: each.key covered without
	// each.value (the shape [expansion.scope] in internal/live/identity
	// builds when for_each iterates over another resource, or over an
	// object constructor with a statically-unknown value) must refuse
	// each.value, never silently answer it with the key.
	t.Run("each.key covered without each.value still refuses each.value", func(t *testing.T) {
		withRep := eval.WithRepetitionData(instances.RepetitionData{
			EachKey: cty.StringVal("k1"),
		})

		keyExpr := hclExprMustParse(t, "each.key")
		got, diags := withRep.Evaluate(t.Context(), keyExpr, ident)
		assertNoDiagnostics(t, diags)
		if !got.RawEquals(cty.StringVal("k1")) {
			t.Errorf("got %#v, want cty.StringVal(\"k1\")", got)
		}

		valExpr := hclExprMustParse(t, "each.value")
		_, diags = withRep.Evaluate(t.Context(), valExpr, ident)
		if !diags.HasErrors() {
			t.Fatal("each.value resolved even though only each.key was supplied - this is the wrong-marker shape #213 warns about")
		}
		if !diagsContain(diags, "Dynamic value in static context") {
			t.Errorf("wrong diagnostic:\n%s", diags.Error())
		}
	})
}

// TestStaticEvaluator_WithRepetitionData_ThroughLocal is #213 itself: a
// local value's OWN definition references each.value directly, and the
// caller only ever hands an expression referencing local.derived - never
// each.value itself - to Evaluate. Before the seam, GetLocalValue's nested
// scope had no repetition channel and always refused ("Dynamic value in
// static context", falling through StaticValidateReferences' default
// case). The seam carries WithRepetitionData's value across [staticScopeData
// .scope]'s nested [newStaticScope] call, which reuses the same *StaticEvaluator
// - and therefore the same repetition data - at every depth.
func TestStaticEvaluator_WithRepetitionData_ThroughLocal(t *testing.T) {
	eval := repetitionEval(t, `
		locals {
			derived      = "prefix-${each.value}"
			derived_key  = "prefix-${each.key}"
			two_deep     = local.derived
			derived_idx  = "n-${count.index}"
		}
	`)
	ident := StaticIdentifier{Subject: "test.arg"}

	t.Run("refuses with no repetition data, same as a direct reference would", func(t *testing.T) {
		expr := hclExprMustParse(t, "local.derived")
		_, diags := eval.Evaluate(t.Context(), expr, ident)
		if !diags.HasErrors() {
			t.Fatal("expected a refusal")
		}
	})

	t.Run("resolves through one level of local", func(t *testing.T) {
		withRep := eval.WithRepetitionData(instances.RepetitionData{
			EachKey:   cty.StringVal("k1"),
			EachValue: cty.StringVal("v1"),
		})
		expr := hclExprMustParse(t, "local.derived")
		got, diags := withRep.Evaluate(t.Context(), expr, ident)
		assertNoDiagnostics(t, diags)
		if !got.RawEquals(cty.StringVal("prefix-v1")) {
			t.Errorf("got %#v, want cty.StringVal(\"prefix-v1\")", got)
		}
	})

	t.Run("resolves through two levels of local", func(t *testing.T) {
		withRep := eval.WithRepetitionData(instances.RepetitionData{
			EachKey:   cty.StringVal("k1"),
			EachValue: cty.StringVal("v1"),
		})
		expr := hclExprMustParse(t, "local.two_deep")
		got, diags := withRep.Evaluate(t.Context(), expr, ident)
		assertNoDiagnostics(t, diags)
		if !got.RawEquals(cty.StringVal("prefix-v1")) {
			t.Errorf("got %#v, want cty.StringVal(\"prefix-v1\")", got)
		}
	})

	t.Run("count.index resolves through a local the same way", func(t *testing.T) {
		withRep := eval.WithRepetitionData(instances.RepetitionData{CountIndex: cty.NumberIntVal(5)})
		expr := hclExprMustParse(t, "local.derived_idx")
		got, diags := withRep.Evaluate(t.Context(), expr, ident)
		assertNoDiagnostics(t, diags)
		if !got.RawEquals(cty.StringVal("n-5")) {
			t.Errorf("got %#v, want cty.StringVal(\"n-5\")", got)
		}
	})

	// The wrong-marker question, at this layer: two different instances'
	// repetition data, evaluating the SAME local expression through the
	// SAME *Module (locals are declared once, not per instance), must
	// never see each other's values. If WithRepetitionData's dup ever
	// leaked - a shared field mutated in place instead of copied - this
	// test would see instance B's value bleed into instance A's answer.
	t.Run("two instances resolving the same local never share a value", func(t *testing.T) {
		instanceA := eval.WithRepetitionData(instances.RepetitionData{EachKey: cty.StringVal("a"), EachValue: cty.StringVal("A-val")})
		instanceB := eval.WithRepetitionData(instances.RepetitionData{EachKey: cty.StringVal("b"), EachValue: cty.StringVal("B-val")})

		expr := hclExprMustParse(t, "local.derived")

		gotA, diagsA := instanceA.Evaluate(t.Context(), expr, ident)
		assertNoDiagnostics(t, diagsA)
		gotB, diagsB := instanceB.Evaluate(t.Context(), expr, ident)
		assertNoDiagnostics(t, diagsB)

		if !gotA.RawEquals(cty.StringVal("prefix-A-val")) {
			t.Errorf("instance A got %#v, want cty.StringVal(\"prefix-A-val\")", gotA)
		}
		if !gotB.RawEquals(cty.StringVal("prefix-B-val")) {
			t.Errorf("instance B got %#v, want cty.StringVal(\"prefix-B-val\")", gotB)
		}

		// Re-evaluate A after B, to catch any in-place mutation B's call
		// might have made to shared state.
		gotAAgain, diagsAAgain := instanceA.Evaluate(t.Context(), expr, ident)
		assertNoDiagnostics(t, diagsAAgain)
		if !gotAAgain.RawEquals(cty.StringVal("prefix-A-val")) {
			t.Errorf("instance A's second evaluation got %#v, want cty.StringVal(\"prefix-A-val\") - repetition data leaked across evaluators", gotAAgain)
		}
	})

	t.Run("derived_key resolves each.key through a local without each.value ever being supplied", func(t *testing.T) {
		// each.key is often knowable when each.value is not (for_each over
		// another resource, or over an object constructor with a
		// statically-unknown value - see [expansion.scope] in
		// internal/live/identity). A local reading only each.key must
		// still resolve in that case.
		withRep := eval.WithRepetitionData(instances.RepetitionData{EachKey: cty.StringVal("k1")})
		expr := hclExprMustParse(t, "local.derived_key")
		got, diags := withRep.Evaluate(t.Context(), expr, ident)
		assertNoDiagnostics(t, diags)
		if !got.RawEquals(cty.StringVal("prefix-k1")) {
			t.Errorf("got %#v, want cty.StringVal(\"prefix-k1\")", got)
		}
	})

	t.Run("a local reading each.value refuses when only each.key is known", func(t *testing.T) {
		withRep := eval.WithRepetitionData(instances.RepetitionData{EachKey: cty.StringVal("k1")})
		expr := hclExprMustParse(t, "local.derived")
		_, diags := withRep.Evaluate(t.Context(), expr, ident)
		if !diags.HasErrors() {
			t.Fatal("expected a refusal: each.value was never supplied, only each.key")
		}
		if !diagsContain(diags, "Dynamic value in static context") {
			t.Errorf("wrong diagnostic:\n%s", diags.Error())
		}
	})
}

func hclExprMustParse(t *testing.T, src string) hcl.Expression {
	t.Helper()
	expr, diags := hclsyntax.ParseExpression([]byte(src), "test.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		t.Fatalf("parsing %q: %s", src, diags.Error())
	}
	return expr
}

func diagsContain(diags hcl.Diagnostics, summary string) bool {
	for _, d := range diags {
		if d.Summary == summary {
			return true
		}
	}
	return false
}
