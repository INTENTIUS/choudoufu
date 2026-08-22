// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// These tests follow count_index_markers_test.go's own rule and for its own
// reason: they do not ask the analyzer whether it is happy. A rule that stops
// refusing is measured by what the instances RESOLVE TO, because a wrong
// identity converges silently and moves no count. So both halves are asserted
// by value - the admitted fixture's three formulas as exact strings, and the
// collapsed fixture's refusal as the exact identity string it duplicates.

// TestSiblingSelectionRendersDistinctIdentities is the safety requirement for
// sibling_select.go, stated as values.
//
// The fixture is terraform-aws-modules/vpc v6.6.1's own aws_route_table_
// association shape with single_nat_gateway = true - the configuration
// corpus-eks-basic actually deploys - in which route_table_id collapses onto
// one route table for every instance and only subnet_id varies. Per-argument
// reasoning must refuse that; the pairs are nonetheless all distinct.
func TestSiblingSelectionRendersDistinctIdentities(t *testing.T) {
	// Both spellings, both pinned by value, and pinned to the SAME strings.
	// element(R[*].attr, idx) is resolveElementCall's shape; R[idx].attr is
	// resolveIndexedTraversal's. They live in separate directories because a
	// configuration holding both is a genuine collision - see the fixtures.
	for _, tc := range []struct {
		dir  string
		want map[string]string
	}{
		{
			dir: "testdata/count-index-sibling-select",
			want: map[string]string{
				"aws_route_table_association.private[0]": "${aws_subnet.private[0].id}/${aws_route_table.private[0].id}",
				"aws_route_table_association.private[1]": "${aws_subnet.private[1].id}/${aws_route_table.private[0].id}",
				"aws_route_table_association.private[2]": "${aws_subnet.private[2].id}/${aws_route_table.private[0].id}",
			},
		},
		{
			dir: "testdata/count-index-sibling-select-indexed",
			want: map[string]string{
				"aws_route_table_association.indexed[0]": "${aws_subnet.private[0].id}/${aws_route_table.private[0].id}",
				"aws_route_table_association.indexed[1]": "${aws_subnet.private[1].id}/${aws_route_table.private[0].id}",
				"aws_route_table_association.indexed[2]": "${aws_subnet.private[2].id}/${aws_route_table.private[0].id}",
			},
		},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			cfg := loadConfigDir(t, tc.dir)

			if got := refusedResources(t, cfg); len(got) != 0 {
				t.Fatalf("%s: want no count-index refusal, got %v", tc.dir, sortedKeys(got))
			}

			result, diags := identity.Resolve(t.Context(), cfg)
			if result == nil {
				t.Fatalf("resolving %s produced no result: %s", tc.dir, diags.Err())
			}
			if diags.HasErrors() {
				t.Fatalf("resolving %s reported errors: %s", tc.dir, diags.Err())
			}

			got := make(map[string]string)
			for _, res := range result.All() {
				addr := res.Addr.String()
				if _, wanted := tc.want[addr]; !wanted {
					continue
				}
				if res.Class != identity.ClassParentDerived {
					t.Errorf("%s resolved %s, want PARENT_DERIVED (both parents are server-assigned)", addr, res.Class)
					continue
				}
				got[addr] = res.Formula.String()
			}

			for _, addr := range sortedStringKeys(tc.want) {
				if got[addr] != tc.want[addr] {
					t.Errorf("%s renders %q, want %q", addr, got[addr], tc.want[addr])
				}
			}

			// The point of the fixture, restated as an assertion rather than
			// left to the reader of the three strings above: route_table_id
			// is the same for all three, and the identities are still
			// pairwise distinct.
			seen := make(map[string]string)
			for addr, id := range got {
				if prev, dup := seen[id]; dup {
					t.Errorf("%s and %s both render %q - two configuration addresses claiming one live object", prev, addr, id)
				}
				seen[id] = addr
			}
		})
	}
}

// TestSiblingSelectionCollapseIsCaughtByCollisionCheck is the other half, and
// it is what makes stepping aside in the rule above safe rather than merely
// permissive: where the selection really does collapse onto one live object,
// the configuration is still refused - by internal/live/identity's own
// checkCollisions, over the whole rendered identity, naming the string the
// instances share.
//
// If this test ever reports "no collision error", the count-index rule has
// been widened past the check that was supposed to catch what it stopped
// catching, and the next apply writes three configuration addresses onto one
// route table association.
func TestSiblingSelectionCollapseIsCaughtByCollisionCheck(t *testing.T) {
	const dir = "testdata/count-index-sibling-select-collision"
	cfg := loadConfigDir(t, dir)

	if got := refusedResources(t, cfg); len(got) != 0 {
		t.Fatalf("%s: want no count-index refusal (the rule steps aside here too), got %v", dir, sortedKeys(got))
	}

	_, diags := identity.Resolve(t.Context(), cfg)
	if !diags.HasErrors() {
		t.Fatalf("%s resolved with no error at all: every aws_route_table_association.collapsed instance selects aws_subnet.only[0] and aws_route_table.private[0], so three addresses claim one live object", dir)
	}

	const wantIdentity = "${aws_subnet.only[0].id}/${aws_route_table.private[0].id}"
	var found bool
	for _, d := range diags {
		desc := d.Description()
		if desc.Summary != "Two resources with the same identity" {
			continue
		}
		if strings.Contains(desc.Detail, wantIdentity) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no collision diagnostic quoting the identity %q; got: %s", wantIdentity, diags.Err())
	}
}

// TestSiblingSelectionIsGatedOnAKnownIdentityRow pins the gate itself, which
// is the part of sibling_select.go that is a claim about which downstream
// check runs rather than about an expression's shape. A type whose scope is
// walkAll (no identity row, or EXTERNAL_ADMITTED - the ClassRecordBacked case
// checkCollisions does not compare) must keep refusing the identical
// expression.
func TestSiblingSelectionIsGatedOnAKnownIdentityRow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scope countIndexScope
		want  bool
	}{
		{"identity row with named attrs", countIndexScope{attrs: map[string]bool{"subnet_id": true}}, true},
		{"no identity row at all", countIndexScope{walkAll: true}, false},
		{"whole type out of scope", countIndexScope{skip: true}, false},
		{"row present but no attrs", countIndexScope{}, false},
	} {
		if got := tc.scope.identityAttrsKnown(); got != tc.want {
			t.Errorf("%s: identityAttrsKnown() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSiblingSelectionReachesEveryComponentRow measures the rule's reach
// rather than asserting it in prose, and holds the gate to the population it
// claims: every admission-table row that names its identity attributes, and
// no row that does not.
//
// The count is logged rather than pinned because the table is generated and
// grows with the provider; what is asserted is the AGREEMENT between
// [countIndexScope.identityAttrsKnown] and the row itself, which is the
// property sibling_select.go's gate rests on. A floor is asserted so that a
// table that failed to load cannot make this test vacuously green.
func TestSiblingSelectionReachesEveryComponentRow(t *testing.T) {
	var reached, skipped int
	for typ, entry := range identity.DefaultTable {
		lt, isLogical := ClassifyLogicalType(typ)
		scope := countIndexScopeForType(typ, lt, isLogical)
		// A row names its identity attributes when some component reads an
		// argument. Several singleton types (an account-wide password
		// policy, a regional opt-in) have components that are all literals
		// and no argument at all, so the narrowed walk already looks at
		// nothing for them - see countIndexScopeForType's attrs.
		named := false
		for _, comp := range entry.Components {
			if len(comp.Attrs) > 0 {
				named = true
				break
			}
		}
		hasComponents := !isLogical && !entry.ServerAssigned && !entry.RecordBacked && named
		if got := scope.identityAttrsKnown(); got != hasComponents {
			t.Errorf("%s: identityAttrsKnown() = %v, but the row %s name its identity attributes",
				typ, got, map[bool]string{true: "does", false: "does not"}[hasComponents])
		}
		if hasComponents {
			reached++
		} else {
			skipped++
		}
	}
	if reached < 400 {
		t.Fatalf("only %d rows of %d name their identity attributes; the table is not loaded, so this test proves nothing",
			reached, reached+skipped)
	}
	t.Logf("sibling-instance selection reaches %d of %d admitted rows; %d are ServerAssigned, record-backed, logical or componentless",
		reached, reached+skipped, skipped)
}

func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
