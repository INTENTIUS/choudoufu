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

	all := [][]Part{{{Literal: "zeta"}}, {{Literal: "alpha"}}}
	canonicaliseElements(all)
	if all[0][0].Literal != "alpha" || all[1][0].Literal != "zeta" {
		t.Errorf("canonicaliseElements left a fully-keyed sequence unsorted: %v", all)
	}
}
