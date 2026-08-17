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

// TestPerElementRendersEveryGroup asserts the VALUE a Component.PerElement
// tail renders, not a predicate about it.
//
// The assertion is per instance and it names the whole string, because the
// two failures this component can have are both invisible to a boolean. A
// tail that renders only its first element still resolves ClassConcrete and
// still produces a plausible marker - "shepmaster/infra-deploy-playground"
// is a perfectly well-formed import ID for a user who is in two groups, and
// it points at an object that does not exist. And an instance COUNT that is
// wrong by one hides a whole resource: one defect's entire signature in this
// repository was two instances where OpenTofu makes three.
func TestPerElementRendersEveryGroup(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "per-element-groups"), nil)

	result, diags := Resolve(context.Background(), cfg)
	if diags.HasErrors() {
		t.Fatalf("resolution refused: %s", diags.Err())
	}

	want := map[string]string{
		// The each.value hop: `groups = each.value` over a local whose
		// values are lists of sibling references. Both two-group users
		// render in canonical order, which for shepmaster is the order the
		// configuration wrote and for Kobzol is the reverse of it - so a
		// passing assertion cannot be explained by "the sort did nothing".
		`aws_iam_user_group_membership.users["pietroalbini"]`: "pietroalbini/infra-admins",
		`aws_iam_user_group_membership.users["shepmaster"]`:   "shepmaster/infra-deploy-playground/infra-team",
		`aws_iam_user_group_membership.users["Kobzol"]`:       "Kobzol/infra-team/rustc-perf",
		// The list construct written inline, descending in the source.
		"aws_iam_user_group_membership.inline": "carols10cents/infra-admins/rustc-perf",
		// The collection reached through a typed variable.
		"aws_iam_user_group_membership.from_var": "jtgeibel/alpha/zeta",
	}

	got := map[string]string{}
	for _, r := range result.All() {
		if r.Addr.Resource.Resource.Type != "aws_iam_user_group_membership" {
			continue
		}
		if r.Class != ClassConcrete {
			t.Errorf("%s resolved %s, want %s; every group name it reads is a literal in this configuration", r.Addr, r.Class, ClassConcrete)
			continue
		}
		got[r.Addr.String()] = r.ImportID
	}

	// The count first: a missing instance would otherwise pass every
	// value check below by simply not being compared.
	if len(got) != len(want) {
		t.Fatalf("resolved %d aws_iam_user_group_membership instances, want %d: %v", len(got), len(want), got)
	}
	for addr, w := range want {
		if got[addr] != w {
			t.Errorf("%s rendered %q, want %q", addr, got[addr], w)
		}
	}
}

// TestPerElementLeavesUnkeyableOrderAlone is the all-or-nothing rule.
//
// An element whose value waits on a live read has no sort key, so the
// sequence has none either, and the configuration's own order stands. The
// assertion is that the ORDER is the source order - not that resolution
// refused, and not that the sortable elements moved around the unsortable
// one.
func TestPerElementLeavesUnkeyableOrderAlone(t *testing.T) {
	elems := [][]Part{
		{{Literal: "zeta"}},
		{{Parent: &ParentRef{Attr: "id"}}},
		{{Literal: "alpha"}},
	}
	canonicaliseElements(elems)
	if elems[0][0].Literal != "zeta" || elems[2][0].Literal != "alpha" {
		t.Errorf("canonicaliseElements reordered a sequence containing an element with no key: %v", elems)
	}

	all := canonicaliseElements([][]Part{{{Literal: "zeta"}}, {{Literal: "alpha"}}})
	if len(all) != 2 || all[0][0].Literal != "alpha" || all[1][0].Literal != "zeta" {
		t.Errorf("canonicaliseElements left a fully-keyed sequence unsorted: %v", all)
	}
}

// TestPerElementCollapsesEqualElements pins the other half of the argument
// that licenses reordering at all.
//
// Sorting is sound because the provider parses the tail back into a SET, so
// permutation changes nothing it sees. A set collapses duplicates on the same
// evidence, and rendering both would emit a segment the object's own ID does
// not have - a wrong identity, not a missing one. Reachable whenever the
// elements come from a list rather than a set: a list-typed variable, a
// concat, a flatten.
func TestPerElementCollapsesEqualElements(t *testing.T) {
	got := canonicaliseElements([][]Part{
		{{Literal: "b"}}, {{Literal: "a"}}, {{Literal: "b"}}, {{Literal: "a"}},
	})
	if len(got) != 2 {
		t.Fatalf("got %d elements, want 2 - equal elements are one segment, because the "+
			"provider parses the tail into a set: %v", len(got), got)
	}
	if got[0][0].Literal != "a" || got[1][0].Literal != "b" {
		t.Errorf("got %v, want [a b] - deduplication must not disturb the canonical order", got)
	}

	// The all-or-nothing rule covers both operations together: one unkeyable
	// element and the sequence is left exactly as written, duplicates and all.
	unkeyed := canonicaliseElements([][]Part{
		{{Literal: "b"}}, {{Parent: &ParentRef{Attr: "arn"}}}, {{Literal: "b"}},
	})
	if len(unkeyed) != 3 {
		t.Errorf("got %d elements, want 3 - an element with no key withdraws BOTH the sort "+
			"and the collapse, because neither can be justified without keys", len(unkeyed))
	}
}
