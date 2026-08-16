// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestCountIndexScopeForType pins countIndexScopeForType's answer for a
// representative type from each bucket #187's narrowing covers, against
// literal expectations rather than against identity.LookupType's own
// output - so this test can actually catch a regression in either
// countIndexScopeForType or the table row it reads, instead of only ever
// agreeing with whatever the table currently says.
func TestCountIndexScopeForType(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		wantSkip     bool
		wantWalkAll  bool
		wantAttrs    []string // nil when wantSkip or wantWalkAll
	}{
		{
			name:         "null_resource is RECORD_ADMITTED: identity is the record, not any argument",
			resourceType: "null_resource",
			wantSkip:     true,
		},
		{
			name:         "terraform_data is RECORD_ADMITTED: identity is the record, not any argument",
			resourceType: "terraform_data",
			wantSkip:     true,
		},
		{
			name:         "aws_instance is server-assigned: Resolve never reads an argument for it",
			resourceType: "aws_instance",
			wantSkip:     true,
		},
		{
			name:         "aws_vpc is server-assigned: Resolve never reads an argument for it",
			resourceType: "aws_vpc",
			wantSkip:     true,
		},
		{
			name:         "aws_security_group is server-assigned: Resolve never reads an argument for it",
			resourceType: "aws_security_group",
			wantSkip:     true,
		},
		{
			name:         "aws_network_acl_rule: identity-bearing set is exactly its four Components attrs",
			resourceType: "aws_network_acl_rule",
			wantAttrs:    []string{"egress", "network_acl_id", "protocol", "rule_number"},
		},
		{
			name:         "aws_route53_record: identity-bearing set is exactly its three Components attrs",
			resourceType: "aws_route53_record",
			wantAttrs:    []string{"name", "type", "zone_id"},
		},
		{
			name:         "unknown type: no table data, so every argument stays in scope",
			resourceType: "made_up_provider_made_up_type",
			wantWalkAll:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lt, isLogical := ClassifyLogicalType(tt.resourceType)
			scope := countIndexScopeForType(tt.resourceType, lt, isLogical)

			if scope.skip != tt.wantSkip {
				t.Errorf("skip = %v, want %v", scope.skip, tt.wantSkip)
			}
			if scope.walkAll != tt.wantWalkAll {
				t.Errorf("walkAll = %v, want %v", scope.walkAll, tt.wantWalkAll)
			}
			if tt.wantSkip || tt.wantWalkAll {
				return
			}
			var got []string
			for name := range scope.attrs {
				got = append(got, name)
			}
			sort.Strings(got)
			if !equalStrings(got, tt.wantAttrs) {
				t.Errorf("attrs = %v, want %v", got, tt.wantAttrs)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCountIndexRecordBackedAdmittedTypeIsFullyClean end-to-ends #187's
// RECORD_ADMITTED bucket through CheckContext, not just through
// countIndexScopeForType: with a record_store configured (so null_resource
// is admitted at all - internal/live/lint's checkManagedResources, GitHub
// issue #73), count.index inside triggers produces no issue of any rule,
// RuleCountIndex included. If the ordering fix in lint.go regressed - if
// checkCountIndex ran before ClassifyLogicalType's answer were available
// again - this is the test that would catch it.
func TestCountIndexRecordBackedAdmittedTypeIsFullyClean(t *testing.T) {
	const src = `
terraform {
  live {
    estate = "test-estate"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

resource "null_resource" "trigger" {
  count = 2

  triggers = {
    slot = "slot-${count.index}"
  }
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}

	cfg := loadConfigDir(t, dir)
	issues := CheckContext(t.Context(), cfg)
	if len(issues) != 0 {
		t.Fatalf("got %d issues for a RECORD_ADMITTED type with a record_store configured, want 0: %v", len(issues), issues)
	}
}

// TestCountIndexUnknownTypeStillWalksNestedBlocks pins the conservative
// default for a type absent from identity.LookupType entirely: every
// argument at every depth, nested blocks included, stays in scope, exactly
// as the rule behaved before #187. The nested value indexes into a
// collection (#192's narrowing only leaves a pure scalar unrefused) so it
// stays a genuine hit regardless of scope. The type is also outside the v0
// admission table, so RuleUnadmittedType fires too; this test only checks
// that RuleCountIndex is among the issues, which is what a regression in
// the walkAll fallback would remove.
func TestCountIndexUnknownTypeStillWalksNestedBlocks(t *testing.T) {
	const src = `
resource "made_up_provider_made_up_type" "nested" {
  count = 2

  block {
    value = ["a", "a", "c"][count.index]
  }
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}

	cfg := loadConfigDir(t, dir)
	issues := CheckContext(t.Context(), cfg)

	var found bool
	for _, issue := range issues {
		if issue.Rule == RuleCountIndex {
			found = true
		}
	}
	if !found {
		t.Fatalf("got no RuleCountIndex issue for count.index in a nested block of a type absent from identity.LookupType, want one (walkAll fallback): %v", issues)
	}
}

// parseExprForAnalysisTest parses a bare HCL expression for a test that
// exercises [analyzeCountIndexSafety] directly, without going through a
// whole resource body.
func parseExprForAnalysisTest(t *testing.T, src string) hclsyntax.Expression {
	t.Helper()

	expr, diags := hclsyntax.ParseExpression([]byte(src), "test.tf", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parsing %q: %s", src, diags.Error())
	}
	syn, ok := expr.(hclsyntax.Expression)
	if !ok {
		t.Fatalf("parsed %q as %T, not an hclsyntax.Expression", src, expr)
	}
	return syn
}

// TestAnalyzeCountIndexSafety is the direct, per-shape pin for #217's
// inverted rule: analyzeCountIndexSafety is the sole authority over what
// checkCountIndex treats as a provably injective, scale-down-stable
// function of count.index, so every shape this rule's doc comment claims
// safe - and every shape it claims unsafe - is asserted here against the
// function itself, not against an end-to-end fixture that could pass for
// the wrong reason.
func TestAnalyzeCountIndexSafety(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantHasIndex bool
		wantSafe     bool
	}{
		{"no reference to count.index at all", `var.x`, false, false},
		{"bare count.index", `count.index`, true, true},
		{"template with count.index between literal text", `"name-${count.index}"`, true, true},
		{"template wrap (sole interpolation)", `"${count.index}"`, true, true},
		{"addition of a literal constant", `100 + count.index`, true, true},
		{"subtraction of a literal constant", `count.index - 1`, true, true},
		{"subtraction from a non-index operand", `var.base - count.index`, true, true},
		{"multiplication by a nonzero literal constant", `2 * count.index`, true, true},
		{"multiplication by -1 via the constant on the left", `count.index * -1`, true, true},
		{"unary negation", `-count.index`, true, true},
		{
			"nested: injective operation wrapping a non-injective one stays unsafe",
			`100 + (count.index % 3)`,
			true, false,
		},
		{
			"conditional whose condition does not depend on count.index, both branches safe",
			`var.is_primary ? 100 + count.index : 200 + count.index`,
			true, true,
		},

		// Unsafe: the shapes #217's audit found accepted before the rule
		// was inverted, plus the ones the rule was already refusing.
		{"modulo", `count.index % 3`, true, false},
		{"integer division", `count.index / 2`, true, false},
		{"multiplication by zero", `count.index * 0`, true, false},
		{"multiplication by an unprovable (variable) operand", `count.index * var.n`, true, false},
		{"both operands of a subtraction reference the index", `count.index - count.index`, true, false},
		{"both operands of an addition reference the index", `count.index + count.index`, true, false},
		{"min()", `min(count.index, 5)`, true, false},
		{"max()", `max(count.index, 5)`, true, false},
		{"floor()", `floor(count.index / 2)`, true, false},
		{"format()", `format("id-%d", count.index)`, true, false},
		{"tostring() alone, no other unsafe wrapper", `tostring(count.index)`, true, false},
		{"comparison operator", `count.index > 2`, true, false},
		{"collection indexing", `var.list[count.index]`, true, false},
		{"offset collection indexing", `var.list[count.index + 1]`, true, false},
		{"element() accessor", `element(var.list, count.index)`, true, false},
		{"lookup() accessor", `lookup(var.m, tostring(count.index), "default")`, true, false},
		{
			"conditional whose own condition depends on count.index",
			`count.index == 0 ? 100 : 200`,
			true, false,
		},

		// The default-refuse proof: a node type analyzeCountIndexSafety has
		// no case for at all - a for-expression - reached through an
		// IndexExpr whose own Key does not reference count.index (a
		// literal 0), so the only way this comes back unsafe is the
		// default branch's generic containment check on the for-expression
		// itself. If the default branch silently fell through as safe
		// instead of refusing, this would report hasIndex=false or
		// safe=true, and this is the test that would catch it.
		{
			"unrecognized node type (for-expression) inside an IndexExpr's Collection",
			`[for i in [1] : count.index][0]`,
			true, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseExprForAnalysisTest(t, tt.src)
			got := analyzeCountIndexSafety(expr)
			if got.hasIndex != tt.wantHasIndex {
				t.Errorf("%s: hasIndex = %v, want %v", tt.src, got.hasIndex, tt.wantHasIndex)
			}
			if got.hasIndex && got.safe != tt.wantSafe {
				t.Errorf("%s: safe = %v, want %v", tt.src, got.safe, tt.wantSafe)
			}
		})
	}
}

// TestAnalyzeCountIndexSafetyUnrecognizedNodeTypeNeverFallsThrough is a
// second, narrower pin for the same default-refuse proof, using a node type
// count.index can appear in without any wrapping IndexExpr or FunctionCall
// at all: a splat expression's Each clause. This confirms the refusal
// comes from analyzeCountIndexSafety's own default case, not incidentally
// from some other case reached along the way.
func TestAnalyzeCountIndexSafetyUnrecognizedNodeTypeNeverFallsThrough(t *testing.T) {
	expr := parseExprForAnalysisTest(t, `[for x in var.list : count.index]`)
	got := analyzeCountIndexSafety(expr)
	if !got.hasIndex {
		t.Fatalf("%T: hasIndex = false, want true (a for-expression referencing count.index must be detected as containing it)", expr)
	}
	if got.safe {
		t.Fatalf("%T: safe = true, want false (a for-expression is not one of analyzeCountIndexSafety's enumerated safe shapes, so it must refuse, not fall through)", expr)
	}
}

// TestRecordBackedSkipIsRedundantWithTheClassSkip bounds the second, quieter
// door into countIndexScopeForType's skip.
//
// There are two legs that silence the count.index walk on a store-only type,
// and only one of them was ever guarded. The first keys on lt.Class ==
// ClassRecordAdmitted; [TestLocalFileKeepsItsCountIndexCheck] pins that local_file
// stays outside it, because its filename argument names a real file and two
// instances at distinct addresses still collide on one path. The second keys
// on the identity row's RecordBacked flag instead, and nothing checked it at
// all - so an identity row added on its own would silence the walk for a type
// lint had never classified, with no test to notice.
//
// That is what happened: row-gen's derivation marked random_string,
// random_uuid, random_uuid4 and random_uuid7 RecordBacked while lint's
// hand-written table still had no row for any of them, and the walk went
// quiet for four types through a leg nobody was watching. It was harmless -
// measured against hashicorp/random 3.9.0, random_uuid/uuid4/uuid7 take only
// keepers, and random_string takes length and character-class knobs plus
// keepers; not one argument of any of the four names anything outside the
// record, which is the property the skip rests on - but it was harmless by
// luck, not by construction.
//
// It is now harmless by construction, and this is the assertion that says so:
// both tables are derived from live/logical-schemas.json by one rule, so the
// RecordBacked set and the RECORD_ADMITTED set are equal, and the second leg
// can no longer reach a type the first would not already have skipped. This
// recomputes that over the whole identity table rather than restating it, so
// the day the two sets diverge again, the silenced walk is reported here
// instead of being found by an operator.
func TestRecordBackedSkipIsRedundantWithTheClassSkip(t *testing.T) {
	checked := 0
	for typ, entry := range identity.DefaultTable {
		if !entry.RecordBacked {
			continue
		}
		checked++

		lt, isLogical := ClassifyLogicalType(typ)
		if !isLogical || lt.Class != ClassRecordAdmitted {
			t.Errorf("countIndexScopeForType(%q) skips the count.index walk on the RecordBacked leg, "+
				"but lint classifies it isLogical=%v/%s - the walk is silenced by a leg no "+
				"classification guards", typ, isLogical, lt.Class)
			continue
		}

		// The class leg alone must already skip it: recomputed by asking for
		// the scope of a type carrying this class with no identity row behind
		// it at all.
		if scope := countIndexScopeForType(typ, lt, isLogical); !scope.skip {
			t.Errorf("countIndexScopeForType(%q) does not skip, but the type is RecordBacked", typ)
		}
	}
	if checked == 0 {
		t.Fatal("no RecordBacked row in identity.DefaultTable; this check is not exercising anything")
	}
}
