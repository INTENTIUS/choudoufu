// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"
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
	if !hasDiag(diags, "Dynamic value in static context", "each.value") {
		t.Errorf("expected \"Dynamic value in static context\" naming each.value; got:\n%s", renderDiags(diags))
	}
}

// TestForEachValueKeyOnlyRefusesCleanly pins the one real corpus site this
// change touched (see the fixture's own comment): a bare, TOP-LEVEL
// each.value reference - not through a local - under a keyOnly expansion.
// Before #213, this refused via HCL's own generic "Unsupported attribute"
// (a symptom of the old unconditional scope.vars splice building an "each"
// object with no "value" key at all); after, it refuses through this
// package's own structured "Dynamic value in static context", categorized
// "other" ([configs.CategoryOther], since [addrs.ForEachAttr] has no more
// specific category). Both refuse - this pins that the reclassification
// did not also change WHETHER it refuses.
func TestForEachValueKeyOnlyRefusesCleanly(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-value-keyonly"), nil)

	_, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatal("expected a refusal: each.value is not statically known under this for_each")
	}
	if !hasDiag(diags, "Dynamic value in static context", "each.value") {
		t.Errorf("expected \"Dynamic value in static context\" naming each.value; got:\n%s", renderDiags(diags))
	}
	if hasDiag(diags, "Unsupported attribute", "") {
		t.Errorf("the old HCL-native diagnostic should have been superseded by the structured one; got:\n%s", renderDiags(diags))
	}
}
