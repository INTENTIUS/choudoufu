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

// TestForEachComprehensionOverListResolvesToIndices is the audit's
// wrong-key-set finding and #239's recovery of the capability that closing
// it cost.
//
// #189 taught [resolver.staticForEachKeys] to union a tuple's elements' own
// object keys, and [resolver.forExprKeys] read its source collection's key
// set through that - so a for-comprehension ranging over a LIST was
// answered with the union of the list elements' keys instead of the list's
// integer indices. The fixture's three-element list of {host, port} objects
// resolved to TWO instances under the invented keys "item-host" and
// "item-port", where OpenTofu creates three under "item-0", "item-1",
// "item-2". No diagnostic fired, because staticForEachKeys only runs where
// evaluating the expression whole has already failed - so the whole
// directory came back clean from check.Dir.
//
// The audit fix refused the shape. #239 resolves it instead, and this test
// is written to catch the original defect rather than to agree with the new
// rule: it asserts the exact resolved set, so a missing instance, a spare
// one, an invented key or a duplicated import ID each fails, and it asserts
// the COUNT separately, because the original bug's whole signature was two
// instances where OpenTofu makes three.
func TestForEachComprehensionOverListResolvesToIndices(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-comprehension-list-source"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`aws_iam_role.team`:           `CONCRETE team`,
		`aws_iam_user.this["item-0"]`: `CONCRETE item-0`,
		`aws_iam_user.this["item-1"]`: `CONCRETE item-1`,
		`aws_iam_user.this["item-2"]`: `CONCRETE item-2`,
	})
	assertInstancesInjective(t, result, "aws_iam_user.this", 3)
}

// TestForEachComprehensionOverListValueVar is the same idiom without an
// index variable: the key clause reads the ELEMENT, which is bound only
// because the source collection evaluated whole in the static scope.
func TestForEachComprehensionOverListValueVar(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-comprehension-list-value-var"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`aws_iam_role.team`:          `CONCRETE team`,
		`aws_iam_user.this["alpha"]`: `CONCRETE alpha`,
		`aws_iam_user.this["beta"]`:  `CONCRETE beta`,
	})
	assertInstancesInjective(t, result, "aws_iam_user.this", 2)
}

// TestForEachComprehensionFilteredList: an "if" clause decides membership,
// and where it can be decided the key set is still exact - and smaller than
// the source. Two of three elements survive `if h.keep`.
func TestForEachComprehensionFilteredList(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-comprehension-list-filtered"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`aws_iam_role.team`:           `CONCRETE team`,
		`aws_iam_user.this["item-0"]`: `CONCRETE item-0`,
		`aws_iam_user.this["item-1"]`: `CONCRETE item-1`,
	})
	assertInstancesInjective(t, result, "aws_iam_user.this", 2)
}

// TestForEachComprehensionGroupedList is the counterpart to the collision
// boundary below: the same non-injective key clause over the same list,
// with grouping (`=> v...`), where folding is what the configuration asks
// for and OpenTofu really does create two instances. Paired so that the
// refusal below is shown to be about the fold being unasked-for rather
// than about repeated keys as such.
func TestForEachComprehensionGroupedList(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-comprehension-list-grouped"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`aws_iam_role.team`:           `CONCRETE team`,
		`aws_iam_user.this["item-0"]`: `CONCRETE item-0`,
		`aws_iam_user.this["item-1"]`: `CONCRETE item-1`,
	})
	assertInstancesInjective(t, result, "aws_iam_user.this", 2)
}

// TestForEachComprehensionListBoundaries are the shapes #239 must still
// refuse, each isolating one reason:
//
//   - the list's LENGTH is not knowable, so the instance count is not;
//   - the length is knowable but the KEY clause reads a managed resource,
//     so the addresses are not;
//   - the key clause is a non-injective function of the index, so two
//     elements would share one address and one marker - HCL refuses that
//     configuration outright, and folding it into two instances is how
//     #178 lost an instance in the first place;
//   - the "if" clause cannot be decided, so membership is not knowable
//     even though every key expression evaluates fine.
//
// Each must produce a diagnostic AND enumerate no instance: a refusal that
// still leaves addresses behind is the failure mode the audit found.
func TestForEachComprehensionListBoundaries(t *testing.T) {
	for _, fixture := range []string{
		"foreach-comprehension-list-nonstatic-length",
		"foreach-comprehension-list-key-reads-resource",
		"foreach-comprehension-list-key-collides",
		"foreach-comprehension-filter-unreadable",
	} {
		t.Run(fixture, func(t *testing.T) {
			cfg := loadConfig(t, filepath.Join("testdata", fixture), nil)

			result, diags := Resolve(context.Background(), cfg)

			if !diags.HasErrors() {
				t.Fatal("expected a refusal: this comprehension's key set is not provable")
			}
			for _, r := range result.All() {
				if strings.HasPrefix(r.Addr.String(), "aws_iam_user.this[") {
					t.Errorf("%s enumerated under a key set nothing here can prove", r.Addr)
				}
			}
		})
	}
}

// assertInstancesInjective is the count-and-collision half of the bar #239
// was held to. The key set has to be complete (n instances, not n-1) and
// injective all the way through to what gets written: two addresses must
// never carry the same import ID, because the import ID names the live
// object and the address is what a tofu-address marker records.
func assertInstancesInjective(t *testing.T, result *Result, resType string, want int) {
	t.Helper()

	byImportID := map[string]string{}
	n := 0
	for _, r := range result.All() {
		if !strings.HasPrefix(r.Addr.String(), resType+"[") {
			continue
		}
		n++
		if prev, dup := byImportID[r.ImportID]; dup {
			t.Errorf("%s and %s both resolve to import ID %q: two addresses, one live object",
				prev, r.Addr, r.ImportID)
		}
		byImportID[r.ImportID] = r.Addr.String()
	}
	if n != want {
		t.Errorf("%s expanded to %d instances, want %d", resType, n, want)
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
