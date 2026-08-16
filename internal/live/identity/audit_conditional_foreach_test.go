// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// The adversarial audit of the wall/conditional (#196) and wall/localvalue
// (#189) merges. Every marked-value case below crashed the process rather
// than refusing a site - from check.Dir, the entry point both front ends
// call - and the key-set case answered a for_each with a key set nothing in
// the configuration produces, silently, with check.Dir reporting the
// directory clean.

// TestConditionalSensitiveConditionRefuses: `variable "x" { type = bool,
// sensitive = true }` used as a conditional's condition made
// [resolver.resolveConditional] call cty.Value.True on a marked value,
// which panics. Refusing keeps the package's existing stance -
// [resolver.stringValue] already refuses a sensitive identity value, and
// which branch a sensitive condition chose is one bit of that same value,
// rendered into the marker and into plan output.
func TestConditionalSensitiveConditionRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "conditional-sensitive-condition"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatal("expected a refusal: the conditional's condition is a sensitive value")
	}
	if !hasDiag(diags, "Identity derived from a sensitive value", "selects between branches of a conditional expression using a sensitive value") {
		t.Errorf("wrong diagnostic:\n%s", renderDiags(diags))
	}
	if _, ok := result.Get(mustAddr(t, `aws_route_table_association.assoc`)); ok {
		t.Error("assoc resolved; which branch a sensitive condition selected is not recordable in an identity")
	}
}

// TestConditionalUnselectedBranchMayNotExist is the corpus idiom the whole
// of #196 exists for, and the audit's proof that skipping the unselected
// branch is required rather than merely convenient: two resources with
// mutually exclusive counts, and a conditional indexing [0] of whichever
// one exists. Consulting the branch not taken would index a count-0
// resource every time.
//
// It also pins the property the corpus evidence turned on: whatever the
// bare reference resolves to, the wrapped reference resolves to the same
// thing. Before #196 the wrapped form got a different (and less accurate)
// refusal than the bare form did in the same file.
func TestConditionalUnselectedBranchMayNotExist(t *testing.T) {
	dir := filepath.Join("testdata", "conditional-mutually-exclusive-count")

	t.Run("plain", func(t *testing.T) {
		cfg := loadConfig(t, dir, nil)
		result, diags := Resolve(context.Background(), cfg)
		assertNoErrors(t, diags)

		got := resolutionAt(t, result, `aws_s3_bucket_policy.wrapped`)
		if got.Class != ClassConcrete || got.ImportID != "plain-bucket" {
			t.Errorf("wrapped resolved %s %q, want CONCRETE \"plain-bucket\" - the false branch, since is_directory defaults to false",
				got.Class, got.ImportID)
		}
	})

	t.Run("directory", func(t *testing.T) {
		cfg := loadConfig(t, dir, map[string]cty.Value{"is_directory": cty.True})
		result, diags := Resolve(context.Background(), cfg)
		assertNoErrors(t, diags)

		got := resolutionAt(t, result, `aws_s3_bucket_policy.wrapped`)
		if got.Class != ClassConcrete || got.ImportID != "dir-bucket--use1-az1--x-s3" {
			t.Errorf("wrapped resolved %s %q, want CONCRETE \"dir-bucket--use1-az1--x-s3\" - the true branch",
				got.Class, got.ImportID)
		}
	})
}

// TestForEachComprehensionOverListRefuses is the audit's wrong-key-set
// finding. #189 taught [resolver.staticForEachKeys] to union a tuple's
// elements' own object keys, and [resolver.forExprKeys] read its source
// collection's key set through that - so a for-comprehension ranging over a
// LIST was answered with the union of the list elements' keys instead of
// the list's integer indices.
//
// The fixture's three-element list of {host, port} objects therefore
// resolved to TWO instances under the invented keys "item-host" and
// "item-port", where OpenTofu creates three under "item-0", "item-1",
// "item-2". No diagnostic fired, because staticForEachKeys only runs where
// evaluating the expression whole has already failed - so the whole
// directory came back clean from check.Dir.
func TestForEachComprehensionOverListRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-comprehension-list-source"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatal("expected a refusal: the for-comprehension ranges over a list, whose keys are integer indices")
	}
	if !hasDiag(diags, "Unable to compute static value", "aws_iam_user.this for_each depends on local.byidx") {
		t.Errorf("expected the ordinary for_each refusal to stand; got:\n%s", renderDiags(diags))
	}
	for _, r := range result.All() {
		if strings.HasPrefix(r.Addr.String(), "aws_iam_user.this[") {
			t.Errorf("%s enumerated: the key set was invented from the list elements' own object keys, not the list's indices", r.Addr)
		}
	}
}

// TestForEachTupleLiteralRefuses is the same defect's other face: a
// for_each whose expression is a tuple. OpenTofu rejects that outright
// ("the for_each argument must be a map, or set of strings"), but the
// TupleConsExpr case answered it with the union of the elements' keys.
func TestForEachTupleLiteralRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-tuple-literal"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatal("expected a refusal: for_each over a tuple is not a legal for_each at all")
	}
	for _, r := range result.All() {
		if strings.HasPrefix(r.Addr.String(), "aws_iam_user.this[") {
			t.Errorf("%s enumerated from a tuple-valued for_each", r.Addr)
		}
	}
}

// TestForEachSensitiveKeyRefuses covers both copies of the three lines that
// convert a key to a string and read it: [resolver.forExprKeys] (#189) and
// [resolver.objectConsKeys] (#178, and reachable ever since), plus the two
// copies in [resolver.forEachOverComprehension] and
// [resolver.forEachOverTupleComprehension] that the sweep for the same
// shape turned up. cty's AsString panics on a marked value; lint's and
// stamp's own copies of this pass both test IsMarked first, identity's
// whole-value for_each path does too, and these four did not.
func TestForEachSensitiveKeyRefuses(t *testing.T) {
	for _, fixture := range []string{
		"foreach-comprehension-sensitive-key",
		"foreach-object-sensitive-key",
		"foreach-comprehension-sensitive-key-over-resource",
	} {
		t.Run(fixture, func(t *testing.T) {
			cfg := loadConfig(t, filepath.Join("testdata", fixture), nil)

			result, diags := Resolve(context.Background(), cfg)

			if !diags.HasErrors() {
				t.Fatal("expected a refusal: the for_each key is built from a sensitive value")
			}
			for _, r := range result.All() {
				if strings.HasPrefix(r.Addr.String(), "aws_iam_user.this[") {
					t.Errorf("%s enumerated under a key nothing here can read", r.Addr)
				}
			}
		})
	}
}

// TestConditionalInsideForEachedModuleArgument is the cross-merge check the
// audit brief asked for: wall/conditional (#196) and wall/localvalue (#189)
// landed from independent branches and both write into the identity
// resolver, so a per-branch green says nothing about their composition.
//
// The fixture puts them in one expression - a conditional inside a
// for_each'd module call's argument, indexing two for_each'd siblings by
// the CALL's own each.key. Both instances must resolve, each to its own
// key's sibling, and flipping the condition must flip both.
func TestConditionalInsideForEachedModuleArgument(t *testing.T) {
	dir := filepath.Join("testdata", "module-foreach-conditional-arg")

	for _, tc := range []struct {
		name       string
		usePrimary cty.Value
		want       map[string]string
	}{
		{"primary", cty.True, map[string]string{
			`module.user["alice"].aws_iam_user.this`: "CONCRETE primary-alice",
			`module.user["bob"].aws_iam_user.this`:   "CONCRETE primary-bob",
		}},
		{"secondary", cty.False, map[string]string{
			`module.user["alice"].aws_iam_user.this`: "CONCRETE secondary-alice",
			`module.user["bob"].aws_iam_user.this`:   "CONCRETE secondary-bob",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadConfigTree(t, dir, map[string]cty.Value{"use_primary": tc.usePrimary})
			result, diags := Resolve(context.Background(), cfg)
			assertNoErrors(t, diags)

			for addr, want := range tc.want {
				got := resolutionAt(t, result, addr)
				if got.Class != ClassConcrete || "CONCRETE "+got.ImportID != want {
					t.Errorf("%s resolved %s %q, want %q", addr, got.Class, got.ImportID, want)
				}
			}
		})
	}
}
