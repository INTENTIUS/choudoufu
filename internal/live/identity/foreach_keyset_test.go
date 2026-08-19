// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// TestModuleForEachStaticKeysDynamicValues is the marker-level assertion
// for #187's key/value split, and it asserts on the resolved identities
// rather than on the analyzer's own boolean: three module instances, three
// distinct import IDs, no two sharing one. A key set that is not injective
// is how two instances came to collide on one live marker before (#178),
// so "the rule said yes" is not evidence here - the rendered set is.
//
// The construct is the one stock OpenTofu's own for_each error text tells
// users to write: "it's better to define the map keys statically in your
// configuration and place apply-time results only in the map values"
// (internal/lang/evalchecks/eval_for_each.go). Stock accepts it, because
// EvaluateForEachExpressionValue tests the map's own knownness and not its
// members'. Before this, the resolver refused it on account of a value no
// instance key ever contains.
func TestModuleForEachStaticKeysDynamicValues(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-foreach-keyonly"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`module.user["alice"].aws_iam_user.this`: `CONCRETE alice`,
		`module.user["bob"].aws_iam_user.this`:   `CONCRETE bob`,
		`module.user["carol"].aws_iam_user.this`: `CONCRETE carol`,
	})

	seen := map[string]string{}
	for _, r := range result.All() {
		if r.Class != ClassConcrete {
			continue
		}
		if prev, dup := seen[r.ImportID]; dup {
			t.Errorf("import ID %q rendered for both %s and %s: two instances, one live marker", r.ImportID, prev, r.Addr)
		}
		seen[r.ImportID] = r.Addr.String()
	}
	if len(seen) != 3 {
		t.Errorf("got %d distinct import IDs, want 3", len(seen))
	}
}

// TestModuleForEachStaticKeysValueStillRefuses is the boundary the split
// must not cross. The instances exist - the keys proved that - but an
// identity built from each.VALUE is not knowable before the cloud is read,
// and the run has to refuse it rather than substitute the key or any other
// value that happens to be at hand.
func TestModuleForEachStaticKeysValueStillRefuses(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-foreach-keyonly-value"), nil)

	result, _ := Resolve(context.Background(), cfg)
	for _, r := range result.All() {
		if r.Class == ClassConcrete {
			t.Errorf("%s resolved to CONCRETE %q from an each.value no static read can answer", r.Addr, r.ImportID)
		}
	}
}

// TestChildModuleRepetitionDataKeyOnly pins the same split at the seam
// localvalue.go actually calls: with the values unprovable, each.key is
// answered and each.value is left cty.NilVal, which is precisely what
// [configs.StaticEvaluator]'s repetitionAttr treats as "no answer" - so a
// reference to each.value refuses where it is written instead of being
// guessed at here.
func TestChildModuleRepetitionDataKeyOnly(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "keyset-scope"), nil)
	expr := parseTestExpr(t, `{ alice = data.aws_caller_identity.current.arn, bob = data.aws_caller_identity.current.arn }`)

	rd, ok := ChildModuleRepetitionData(context.Background(), cfg, `module "user"`, nil, expr, addrs.StringKey("alice"))
	if !ok {
		t.Fatal("ChildModuleRepetitionData refused a key it can prove")
	}
	if rd.EachKey.AsString() != "alice" {
		t.Errorf("EachKey = %#v, want alice", rd.EachKey)
	}
	if rd.EachValue != cty.NilVal {
		t.Errorf("EachValue = %#v, want cty.NilVal: no value was proven", rd.EachValue)
	}

	if _, ok := ChildModuleRepetitionData(context.Background(), cfg, `module "user"`, nil, expr, addrs.StringKey("mallory")); ok {
		t.Error("ChildModuleRepetitionData accepted a key its own expression does not produce")
	}
}

// TestStaticForEachKeyNamesShapes is the shape table: what the rule proves,
// and - the half that matters more - what it still refuses. Every "want
// refused" row is a shape whose key set could differ from what any static
// read here would compute, so admitting one would put a key on a live
// marker that the configuration does not actually produce.
func TestStaticForEachKeyNamesShapes(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "keyset-scope"), nil)

	tests := []struct {
		name string
		expr string
		want []string // nil means "must be refused"
	}{
		{
			name: "object constructor with dynamic values",
			expr: `{ alice = data.d.x.arn, bob = data.d.x.arn }`,
			want: []string{"alice", "bob"},
		},
		{
			name: "quoted and parenthesized keys",
			expr: `{ "alice" = data.d.x.arn, (local.dup_key) = data.d.x.arn }`,
			want: nil, // local.dup_key is "alice": two items, one instance
		},
		{
			name: "duplicate literal keys",
			expr: `{ alice = data.d.x.arn, alice = data.d.x.other }`,
			want: nil,
		},
		{
			name: "conditional on a static condition",
			expr: `local.pick ? { alice = data.d.x.arn } : { bob = data.d.x.arn }`,
			want: []string{"alice"},
		},
		{
			name: "conditional on a dynamic condition",
			expr: `data.d.x.flag ? { alice = data.d.x.arn } : { bob = data.d.x.arn }`,
			want: nil,
		},
		{
			name: "merge of a static map and a dynamic-valued constructor",
			expr: `merge(local.base, { carol = data.d.x.arn })`,
			want: []string{"alice", "carol"},
		},
		{
			name: "merge with a dynamic operand",
			expr: `merge(local.base, data.d.x.extra)`,
			want: nil,
		},
		{
			name: "any call other than merge",
			expr: `zipmap(["alice"], [data.d.x.arn])`,
			want: nil,
		},
		{
			name: "for-comprehension over a dynamic collection",
			expr: `{ for k, v in data.d.x.items : k => v }`,
			want: nil,
		},
		{
			name: "for-comprehension with an if clause reading only the key",
			expr: `{ for k, v in local.base : k => data.d.x.arn if k != "alice" }`,
			// local.base has exactly one entry, "alice", and the filter
			// excludes it - the proven key set is genuinely empty, which
			// [staticForEachKeyNames] reports the same way as "not proven"
			// (see its own doc). Gap A (issue #308) does evaluate this
			// clause now, structurally, per entry; it happens to answer
			// "no keys survive" for this particular source rather than
			// "cannot be evaluated at all" - the next two cases pin the
			// distinction on a source where the filter keeps something.
			want: nil,
		},
		{
			name: "for-comprehension filter reads one static attribute beside an unprovable one",
			expr: `{ for k, v in local.filtered_entries : k => v if v.active }`,
			// v.secret never evaluates (data.d.x.arn), but neither clause
			// reads it - only v.active, which is a plain literal on every
			// entry. Issue #308's Gap A: bob is excluded by the filter,
			// alice survives, and secret is never touched.
			want: []string{"alice"},
		},
		{
			name: "for-comprehension filter reads the unprovable attribute itself",
			expr: `{ for k, v in local.filtered_entries : k => v if v.secret != "" }`,
			want: nil, // the one attribute the filter needs is exactly the unprovable one
		},
		{
			name: "for-comprehension key clause reads a static value attribute",
			expr: `{ for k, v in local.filtered_entries : "${k}-${v.active}" => v.secret }`,
			want: []string{"alice-true", "bob-false"},
		},
		{
			name: "bare dynamic traversal",
			expr: `data.d.x.items`,
			want: nil,
		},
		{
			name: "empty object constructor",
			expr: `{}`,
			want: nil, // no instances is the caller's own case, not a proof
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := staticForEachKeyNames(context.Background(), cfg, `module "user"`, parseTestExpr(t, tc.expr))
			if tc.want == nil {
				if ok {
					t.Fatalf("proved key set %v for a shape that must be refused", got)
				}
				return
			}
			if !ok {
				t.Fatalf("refused a shape it can prove")
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestStaticForEachKeyNamesNoEvaluator is the degenerate input every
// exported entry point in this package has to survive: a module with no
// static evaluator answers nothing rather than panicking.
func TestStaticForEachKeyNamesNoEvaluator(t *testing.T) {
	if _, ok := staticForEachKeyNames(context.Background(), nil, "subject", parseTestExpr(t, `{ a = 1 }`)); ok {
		t.Error("proved a key set with no module")
	}
	if _, ok := staticForEachKeyNames(context.Background(), &configs.Config{Module: &configs.Module{}}, "subject", parseTestExpr(t, `{ a = 1 }`)); ok {
		t.Error("proved a key set with no static evaluator")
	}
}

func parseTestExpr(t *testing.T, src string) hcl.Expression {
	t.Helper()
	expr, diags := hclsyntax.ParseExpression([]byte(src), "test.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		t.Fatalf("parsing %q: %s", src, diags.Error())
	}
	return expr
}
