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
func TestTypedModuleVarStringUnchanged(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "typedvar-string"), nil)
	result, _ := ResolveIn(context.Background(), cfg, CloudContext{AccountID: "000000000000", Region: "us-east-1"})

	assertQueueIDs(t, result, map[string]string{
		`module.child.aws_sqs_queue.q["a"]`: "https://sqs.us-east-1.amazonaws.com/000000000000/q-007",
	})
}

// TestUntypedModuleVarUnchanged: a variable with no type argument has
// ConstraintType cty.DynamicPseudoType, which converts nothing. The raw value
// is what OpenTofu uses too, so the answer is the raw one.
//
// Key "b" is aws_sqs_queue.seed.max_message_size read through an untyped
// module variable rather than a typed one - unlike its siblings
// TestTypedModuleVarConvertsEachValue and TestTypedModuleVarStringUnchanged,
// a typed variable's own conversion machinery refuses this shape before
// identity composition ever runs (a for_each/expansion refusal, which
// [DiscoverableFallbackTypes] never answers), so those two still see "b"
// refused outright. Here the untyped value reaches identity composition
// itself and raises "Not an identity attribute" - max_message_size is a
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
