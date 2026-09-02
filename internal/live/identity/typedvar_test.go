// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// #251: a value chased through a module variable is the CALL's argument
// expression, evaluated raw. OpenTofu never uses that value - it converts the
// caller's value to the variable's declared type first
// ([prepareFinalInputVariableValue], internal/tofu/eval_variable.go) - so a
// declared type whose conversion is not identity-on-re-render makes the two
// disagree, and #999593ef51's per-key each.value binding turned that
// disagreement into a rendered identity.
//
// Every assertion below is on a rendered import ID and on the set of
// addresses resolved, never on a predicate: a wrong conversion produces an
// ordinary-looking CONCRETE resolution and only the rendered string shows it.

// childQueueIDs lists every module.child.aws_sqs_queue.q instance the result
// holds, address to import ID, so a test can assert the whole set at once -
// an extra instance, a missing one and a wrong ID are all visible in the same
// comparison.
func childQueueIDs(result *Result) map[string]string {
	out := map[string]string{}
	for _, res := range result.All() {
		addr := res.Addr.String()
		if strings.HasPrefix(addr, "module.child.aws_sqs_queue.q") {
			out[addr] = res.ImportID
		}
	}
	return out
}

func assertQueueIDs(t *testing.T, result *Result, want map[string]string) {
	t.Helper()
	got := childQueueIDs(result)
	if len(got) != len(want) {
		t.Errorf("resolved %d child queue instances, want %d\n got: %s\nwant: %s",
			len(got), len(want), formatIDs(got), formatIDs(want))
		return
	}
	for addr, id := range want {
		if got[addr] != id {
			t.Errorf("%s import ID is %q, want %q", addr, got[addr], id)
		}
	}
	for addr := range got {
		if _, expected := want[addr]; !expected {
			t.Errorf("%s resolved to import ID %q; it was not expected to resolve at all", addr, got[addr])
		}
	}
}

func formatIDs(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(m[k])
	}
	b.WriteString("}")
	return b.String()
}

// TestTypedModuleVarConvertsEachValue is #251's exact reproduction. The child
// declares map(number); the call supplies the string "007" alongside a
// dynamic sibling, which is what forces the per-key fallback. OpenTofu
// converts, so each.value for key "a" is the number 7 and the queue is
// created as q-7. Before the fix this rendered q-007.
func TestTypedModuleVarConvertsEachValue(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "typedvar-number"), nil)
	result, _ := ResolveIn(context.Background(), cfg, CloudContext{AccountID: "000000000000", Region: "us-east-1"})

	// Key "b" is aws_sqs_queue.seed.max_message_size, which nothing here can
	// read before the cloud is, so it refuses - as it did before the fix.
	assertQueueIDs(t, result, map[string]string{
		`module.child.aws_sqs_queue.q["a"]`: "https://sqs.us-east-1.amazonaws.com/000000000000/q-7",
	})

	// The provider identity attribute, not only the import ID: the two are
	// rendered from the same components but assembled separately, and a
	// conversion applied to one and not the other would show here.
	res := resolutionAt(t, result, `module.child.aws_sqs_queue.q["a"]`)
	if got := res.IdentityValues["url"]; got != "https://sqs.us-east-1.amazonaws.com/000000000000/q-7" {
		t.Errorf(`module.child.aws_sqs_queue.q["a"] identity url is %q, want %q`, got, "https://sqs.us-east-1.amazonaws.com/000000000000/q-7")
	}
}

// TestTypedModuleVarStringUnchanged is the case the original author reasoned
// about and got right: map(string) fed a numeric-looking string renders the
// same either way, so the conversion must leave it exactly as it was. This is
// the shape the corpus site the eachvalue merge cleared actually has, and it
// is here so that a fix which declines typed variables outright fails rather
// than passes.
//
// Key "b" (aws_sqs_queue.seed.max_message_size, a Computed attribute that is
// not part of aws_sqs_queue's identity) used to be simply ABSENT here - a
// hard refusal that never reached [Result], the same shape
// TestTypedModuleVarConvertsEachValue's "b" still is. Issue #301's
// [preservedExpr] carries the pre-conversion expression across a
// map(string)/object(string-attr) hop now, so "b" reaches identity
// composition exactly as it already did in TestUntypedModuleVarUnchanged
// (declared type cty.DynamicPseudoType, which never dropped the expression
// at all) - and gets the identical answer: max_message_size is not a
// registered identity attribute, so [resolver.parentPart] resolves it to
// [ClassNeedsDiscovery] via [DiscoveryMarkerFallback] instead of refusing.
// This is the point of the fix, not a side effect of it: whether a module
// variable declares map(string) or leaves the type off must not change
// whether a reference through it resolves, and before #301 it did.
func TestTypedModuleVarStringUnchanged(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "typedvar-string"), nil)
	result, _ := ResolveIn(context.Background(), cfg, CloudContext{AccountID: "000000000000", Region: "us-east-1"})

	assertQueueIDs(t, result, map[string]string{
		`module.child.aws_sqs_queue.q["a"]`: "https://sqs.us-east-1.amazonaws.com/000000000000/q-007",
		`module.child.aws_sqs_queue.q["b"]`: "",
	})

	b := resolutionAt(t, result, `module.child.aws_sqs_queue.q["b"]`)
	if b.Class != ClassNeedsDiscovery {
		t.Errorf(`module.child.aws_sqs_queue.q["b"] resolved %s, want NEEDS_DISCOVERY`, b.Class)
	}
	if b.Cause != DiscoveryMarkerFallback {
		t.Errorf(`module.child.aws_sqs_queue.q["b"] discovery cause is %s, want %s`, b.Cause, DiscoveryMarkerFallback)
	}
}

// TestUntypedModuleVarUnchanged: a variable with no type argument has
// ConstraintType cty.DynamicPseudoType, which converts nothing. The raw value
// is what OpenTofu uses too, so the answer is the raw one.
//
// Key "b" is aws_sqs_queue.seed.max_message_size read through an untyped
// module variable rather than a typed one. Before issue #301, this was the
// one member of the family whose "b" reached identity composition at all -
// TestTypedModuleVarConvertsEachValue's declared map(number) and
// TestTypedModuleVarStringUnchanged's declared map(string) both dropped the
// pre-conversion expression on the hop through their typed variable, so
// their "b" refused outright before identity composition ever ran. #301's
// [preservedExpr] closes that gap for the string-valued case -
// TestTypedModuleVarStringUnchanged's "b" now reaches identity composition
// and lands on exactly the same answer this test does. max_message_size is a
// real, Computed schema attribute of aws_sqs_queue, but not part of its
// identity - which GitHub issue #289's marker fallback now answers for
// aws_sqs_queue (taggable and enumerable): "b" resolves to
// [ClassNeedsDiscovery] rather than refusing, with no import ID.
func TestUntypedModuleVarUnchanged(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "typedvar-untyped"), nil)
	result, _ := ResolveIn(context.Background(), cfg, CloudContext{AccountID: "000000000000", Region: "us-east-1"})

	assertQueueIDs(t, result, map[string]string{
		`module.child.aws_sqs_queue.q["a"]`: "https://sqs.us-east-1.amazonaws.com/000000000000/q-007",
		`module.child.aws_sqs_queue.q["b"]`: "",
	})

	b := resolutionAt(t, result, `module.child.aws_sqs_queue.q["b"]`)
	if b.Class != ClassNeedsDiscovery {
		t.Errorf(`module.child.aws_sqs_queue.q["b"] resolved %s, want NEEDS_DISCOVERY`, b.Class)
	}
	if b.Cause != DiscoveryMarkerFallback {
		t.Errorf(`module.child.aws_sqs_queue.q["b"] discovery cause is %s, want %s`, b.Cause, DiscoveryMarkerFallback)
	}
}

// TestBareEachValueThroughTypedModuleVarBuildsAFormula is issue #301's
// headline shape: terraform-aws-modules/iam's "attach N policies to a role"
// pattern, reduced. `policies = { ImageBuilder = aws_iam_policy.imagebuilder.arn }`
// crosses a module-call boundary into a child that declares `variable
// "policies" { type = map(string) }` and reads the value back with a BARE
// `each.value` - no trailing `.attr`, unlike TestTypedModuleVarStringUnchanged's
// `each.value` used as a whole string interpolated into a name and unlike
// module-foreach-var's `each.value.role` (a selector, #260's original
// shape). The distinguishing fact from every other fixture in this file:
// the unproven element is not merely unresolvable (a Computed, non-identity
// attribute) but IS the whole value of a registered identity attribute
// (aws_iam_policy's arn) of a SIBLING resource - so once #301's
// [preservedExpr] lets the pre-conversion expression survive the map(string)
// hop, [resolver.resolveExpr]'s ordinary symbolic path
// ([resolver.isSymbolic], [resolver.resolveTraversal], [resolver.parentPart])
// builds a PARENT_DERIVED formula for it, the same mechanism issue #284
// already built for a DIRECT reference (`name = aws_acm_certificate.cert.arn`)
// - no [Context.ManagedResults] second pass required, because a bare
// each.value over a keyOnly expansion never was symbolic-vs-evaluable in the
// way a direct reference is; it was simply unreachable before #301 gave it
// an expression to reach through at all.
//
// "role" is var.role_name, a plain literal passed at the call site, so only
// policy_arn's formula half is symbolic; the rendered formula therefore has
// one literal part and one parent-derived part, exactly as
// aws_iam_role_policy_attachment's import syntax ROLENAME/POLICYARN says it
// should.
func TestBareEachValueThroughTypedModuleVarBuildsAFormula(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-foreach-var-typed-sibling-value"), nil)
	result, diags := Resolve(context.Background(), cfg)
	if diags.HasErrors() {
		t.Fatalf("refused: %s", diags.Err())
	}

	res := resolutionAt(t, result, `module.attach.aws_iam_role_policy_attachment.this["ImageBuilder"]`)
	if res.Class != ClassParentDerived {
		t.Fatalf(`module.attach.aws_iam_role_policy_attachment.this["ImageBuilder"] resolved %s, want PARENT_DERIVED (diags: %s)`, res.Class, diags.Err())
	}
	const want = `module.attach.aws_iam_role_policy_attachment.this["ImageBuilder"] PARENT_DERIVED gh-image-builder/${aws_iam_policy.imagebuilder.arn}`
	if got := res.String(); got != want {
		t.Errorf("rendered\n  %s\nwant\n  %s", got, want)
	}
}

// TestTypedModuleVarAppliesOptionalDefaults pins the half of
// prepareFinalInputVariableValue that is not the conversion: it applies the
// variable's optional-attribute defaults to the given value FIRST, and only
// then converts. cty's own convert leaves an absent optional attribute null,
// so the two halves are separable and this fails if only the conversion is
// applied - "alice-std" is reachable from cfg.TypeDefaults and from nowhere
// else, since the string never appears at the call site.
//
// Five of the thirteen typed module-variable hops in the corpus are
// map(ObjectWithOptionalAttrs), so this is the shape the population is
// actually made of, not a constructed edge.
func TestTypedModuleVarAppliesOptionalDefaults(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "typedvar-optional"), nil)
	result, _ := Resolve(context.Background(), cfg)

	res := resolutionAt(t, result, `module.child.aws_iam_user.u["a"]`)
	if res.Class != ClassConcrete {
		t.Errorf(`module.child.aws_iam_user.u["a"] resolved %s, want CONCRETE`, res.Class)
	}
	if res.ImportID != "alice-std" {
		t.Errorf(`module.child.aws_iam_user.u["a"] import ID is %q, want %q`, res.ImportID, "alice-std")
	}
	if got := res.IdentityValues["name"]; got != "alice-std" {
		t.Errorf(`module.child.aws_iam_user.u["a"] identity name is %q, want %q`, got, "alice-std")
	}
	// "b" reaches aws_iam_group.admins.name, which nothing here can read.
	if got, ok := result.Get(mustAddr(t, `module.child.aws_iam_user.u["b"]`)); ok {
		t.Errorf(`module.child.aws_iam_user.u["b"] resolved to import ID %q; its value reaches a managed resource`, got.ImportID)
	}
}

// TestTypedModuleVarFailedConversionRefuses: "not-a-number" is not
// convertible to the declared map(number). OpenTofu rejects the
// configuration; the resolver must refuse rather than fall back to the raw
// string, because a value OpenTofu would have rejected must never become a
// marker. Neither instance resolves - "a" because its value does not convert,
// "b" because it reaches a managed resource.
func TestTypedModuleVarFailedConversionRefuses(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "typedvar-badconv"), nil)
	result, _ := ResolveIn(context.Background(), cfg, CloudContext{AccountID: "000000000000", Region: "us-east-1"})

	assertQueueIDs(t, result, map[string]string{})
}

// TestUnreadableConversionEmptyIsKnownForEverySetTarget is #258: an empty key
// set reaching unreadableConversion answered "unknown" for a set target and
// "known, empty" for everything else, though emptiness is exactly as known
// either way - there is nothing left to lose track of. Function-level rather
// than a fixture, because this branch is not reachable through the corpus
// today: a literal empty collection evaluates whole and never reaches
// [rebuiltContainer]'s chase at all (see
// TestUnreadableConversionEmptyBranchIsUnreachableThroughAFixture below,
// which pins that whole-evaluation currently short-circuits this path so a
// future producer that DOES reach it - a comprehension that filters
// everything out, a splat over an empty parent - inherits the fix rather
// than the bug).
func TestUnreadableConversionEmptyIsKnownForEverySetTarget(t *testing.T) {
	setKeys, setVals, setOK := unreadableConversion(cty.Set(cty.String), nil, nil)
	if !setOK || len(setKeys) != 0 || len(setVals) != 0 {
		t.Errorf("unreadableConversion(set(string), nil, nil) = %v, %v, %v; want [], [], true", setKeys, setVals, setOK)
	}

	// The map case was already correct; pinned here so the two answers are
	// compared in one place rather than trusted to agree by construction.
	mapKeys, mapVals, mapOK := unreadableConversion(cty.Map(cty.String), nil, nil)
	if !mapOK || len(mapKeys) != 0 || len(mapVals) != 0 {
		t.Errorf("unreadableConversion(map(string), nil, nil) = %v, %v, %v; want [], [], true", mapKeys, mapVals, mapOK)
	}

	// Every other target rebuiltContainer/unreadableConversion dispatch on:
	// object, list and tuple. None of these should regress alongside the fix.
	for _, ty := range []cty.Type{
		cty.Object(map[string]cty.Type{"a": cty.String}),
		cty.List(cty.String),
		cty.Tuple([]cty.Type{cty.String}),
	} {
		keys, vals, ok := unreadableConversion(ty, nil, nil)
		if !ok || len(keys) != 0 || len(vals) != 0 {
			t.Errorf("unreadableConversion(%s, nil, nil) = %v, %v, %v; want [], [], true", ty.FriendlyName(), keys, vals, ok)
		}
	}
}

// TestRebuiltContainerOtherTwoRefusalsUnaffected is the fix's negative space:
// rebuiltContainer refuses for three reasons - empty, a repeated key, a
// non-consecutive integer key - and only the first is an ordinary known
// value. The other two are genuinely ambiguous shapes ("the container's own
// comment calls backstops") and must still refuse exactly as before, both at
// rebuiltContainer itself and through unreadableConversion for a set target,
// which is the dispatch #258's fix touches.
func TestRebuiltContainerOtherTwoRefusalsUnaffected(t *testing.T) {
	repeatedKeys := []cty.Value{cty.StringVal("a"), cty.StringVal("a")}
	repeatedBindings := []elemBinding{{val: cty.StringVal("x")}, {val: cty.StringVal("y")}}
	if _, ok := rebuiltContainer(repeatedKeys, repeatedBindings); ok {
		t.Error("rebuiltContainer with a repeated key succeeded; want false")
	}
	if _, _, ok := unreadableConversion(cty.Set(cty.String), repeatedKeys, repeatedBindings); ok {
		t.Error("unreadableConversion(set, repeated key) succeeded; want a hard refusal")
	}

	gapKeys := []cty.Value{cty.NumberIntVal(0), cty.NumberIntVal(2)}
	gapBindings := []elemBinding{{val: cty.StringVal("x")}, {val: cty.StringVal("y")}}
	if _, ok := rebuiltContainer(gapKeys, gapBindings); ok {
		t.Error("rebuiltContainer with a non-consecutive integer key succeeded; want false")
	}
	if _, _, ok := unreadableConversion(cty.Set(cty.String), gapKeys, gapBindings); ok {
		t.Error("unreadableConversion(set, non-consecutive integer key) succeeded; want a hard refusal")
	}
}

// TestUnreadableConversionEmptyBranchIsUnreachableThroughAFixture is the
// corpus-side half of #258's own claim: `s = []` against a set(string)
// module variable, chased through a for_each comprehension, resolves zero
// instances with no diagnostic today - because a literal empty collection
// evaluates WHOLE ([resolver.evaluatedCollElements]) and never reaches
// [resolver.staticCollElems]'s chase, so [rebuiltContainer] and
// unreadableConversion never run for it. If this test starts failing, #258's
// reachability analysis no longer holds and the fix above needs revisiting
// alongside whatever changed.
func TestUnreadableConversionEmptyBranchIsUnreachableThroughAFixture(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "typedvar-emptyset"), nil)
	result, diags := ResolveIn(context.Background(), cfg, CloudContext{AccountID: "000000000000", Region: "us-east-1"})
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	assertQueueIDs(t, result, map[string]string{})
}
