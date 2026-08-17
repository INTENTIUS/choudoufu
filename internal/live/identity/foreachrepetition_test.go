// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
)

// #213: a local value's own definition references each.value/each.key/
// count.index directly, reached from an identity-bearing expression that
// only ever names the local - never each/count itself. Fixed by
// configs.StaticEvaluator.WithRepetitionData plus resolve.go's evalPure
// threading [instScope.repetition] into it instead of the old top-level-only
// post-hoc EvalContext merge. See internal/configs/repetition_test.go for
// the mechanism-level tests (does the seam answer exactly what it was given,
// at any nesting depth, and refuse exactly what it was not); this file is
// the caller side - does resolve.go hand the seam this INSTANCE's own data.

// TestLocalAttrRepetition is the positive case and, more importantly, the
// wrong-marker check: two different for_each instances resolving the SAME
// local (locals are declared once, not per instance - the local's cty.Value
// is recomputed per call, but from the SAME unevaluated expression) must
// come back with two DIFFERENT identities, not the same one and not each
// other's.
func TestLocalAttrRepetition(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "local-attr-repetition"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`aws_iam_user.team["alice"]`: `CONCRETE user-alice`,
		`aws_iam_user.team["bob"]`:   `CONCRETE user-bob`,
	})
}

// TestLocalAttrRepetitionSymbolic is the safety boundary: each.key is known
// (the for_each key set resolves through #178's key-set fix), each.value is
// not (the for_each's values are a managed resource's attribute), and a
// local reached transitively reads each.value. This must refuse, not
// silently answer with each.key - see the fixture's own comment for why
// that specific failure mode is the one #213's brief calls out by name.
func TestLocalAttrRepetitionSymbolic(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "local-attr-repetition-symbolic"), nil)

	_, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatal("expected a refusal: local.suffix reads each.value, which this for_each never makes known")
	}
	// The summary moved with #260. each.value is no longer refused by
	// [configs.StaticEvaluator]'s reference pre-scan before anything looks at
	// it; the element expression is carried and SELECTED into, and here the
	// element is a one-element tuple, which a string interpolation cannot
	// take. So the refusal now names the real obstacle - a resource
	// reference this cannot follow through an expression - instead of
	// "each.value is dynamic". What has not moved, and is the whole point of
	// the fixture, is that it refuses rather than answering with each.key.
	if !diags.HasErrors() {
		t.Fatal("expected a refusal")
	}
	for _, res := range mustResolve(t, cfg) {
		if res.ImportID == "user-alice" || res.ImportID == "user-" {
			t.Errorf("%s resolved to %q - each.key was substituted for each.value", res.Addr, res.ImportID)
		}
	}
}

// TestForEachValueKeyOnlyResolvesAManagedParent is what #213's
// TestForEachValueKeyOnlyRefusesCleanly became under #260, and the rename is
// the point: the shape it pins - a bare, TOP-LEVEL each.value under a keyOnly
// expansion, whose element is a managed resource's identity attribute - is
// now the B-ref/managed case that RESOLVES, through the same
// [resolver.parentPart] a direct reference to that attribute has always used.
//
// The corpus site it was written for is not this shape: govuk-infrastructure's
// aws_security_group_rule.postgres_from_eks_workers reads a data source, and
// that half is pinned by TestForEachValueKeyOnlyDataStillRefuses below,
// unchanged in outcome.
func TestForEachValueKeyOnlyResolvesAManagedParent(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-value-keyonly"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	res := resolutionAt(t, result, `aws_iam_user.team["alice"]`)
	if res.ImportID != "admins" {
		t.Errorf(`aws_iam_user.team["alice"] import ID is %q, want "admins" - the group's own name, which is what each.value names`, res.ImportID)
	}
	if got := res.IdentityValues["name"]; got != "admins" {
		t.Errorf(`aws_iam_user.team["alice"] name is %q, want "admins"`, got)
	}
}

// TestForEachValueKeyOnlyDataStillRefuses is the boundary #260 did not move,
// and the shape the real corpus site actually has. A data source's attribute
// is knowable at plan time and not before, so an identity built from it is
// refused rather than guessed - and in particular the instance key, the one
// value that IS in scope, must not stand in for it.
func TestForEachValueKeyOnlyDataStillRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-value-keyonly-data"), nil)

	result, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatal("expected a refusal: each.value is a data-source read")
	}
	if got, ok := result.Get(mustAddr(t, `aws_iam_user.team["alice"]`)); ok {
		t.Errorf(`aws_iam_user.team["alice"] resolved to import ID %q; its value is a data-source read`, got.ImportID)
	}
	if hasDiag(diags, "Unsupported attribute", "") {
		t.Errorf("the old HCL-native diagnostic should have been superseded by a structured one; got:\n%s", renderDiags(diags))
	}
}

// mustResolve is [Resolve] with the result flattened, for a test that only
// wants to sweep every rendered identity for a value that must not appear.
func mustResolve(t *testing.T, cfg *configs.Config) []Resolution {
	t.Helper()
	result, _ := Resolve(context.Background(), cfg)
	return result.All()
}
