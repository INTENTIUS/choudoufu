// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// The record-orphan leg's parent rule (maintainer ruling 2026-09-03, found
// by the carve-by-retag claim): an untaggable child's ownership is its
// parent's. These pin the two halves the rule is built from - splitting a
// flat import ID back into the ratified row's attribute values, and the
// "does this pass hold the parent" check - on the IAM shapes the carve
// moves, which the package's fake cloud does not serve and so cannot reach
// through [Discover] here. The Route 53 pair in recordorphan_read_test.go
// runs the same rule end to end through the leg.

func TestSplitImportIDByComponents_IAMInlinePolicy(t *testing.T) {
	entry, ok := identity.LookupType("aws_iam_role_policy")
	if !ok {
		t.Fatal("aws_iam_role_policy has no ratified row")
	}
	parts, ok := splitImportIDByComponents(entry, "tl-team-0001-role:tl-team-0001-inline")
	if !ok {
		t.Fatal("ROLENAME:POLICYNAME did not split")
	}
	if parts["role"] != "tl-team-0001-role" || parts["name"] != "tl-team-0001-inline" {
		t.Errorf("split = %v, want role=tl-team-0001-role name=tl-team-0001-inline", parts)
	}
}

func TestSplitImportIDByComponents_IAMAttachmentKeepsTheARNWhole(t *testing.T) {
	entry, ok := identity.LookupType("aws_iam_role_policy_attachment")
	if !ok {
		t.Fatal("aws_iam_role_policy_attachment has no ratified row")
	}
	const arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
	parts, ok := splitImportIDByComponents(entry, "tl-svc-0000-exec-role/"+arn)
	if !ok {
		t.Fatal("ROLENAME/POLICYARN did not split")
	}
	// The ARN carries its own "/" and ":" characters; the split has to stop
	// at the FIRST separator, which is the only one a role name cannot hold.
	if parts["role"] != "tl-svc-0000-exec-role" || parts["policy_arn"] != arn {
		t.Errorf("split = %v, want role=tl-svc-0000-exec-role policy_arn=%s", parts, arn)
	}
}

func TestSplitImportIDByComponents_RoundTripsCompose(t *testing.T) {
	for _, typeName := range []string{"aws_iam_role_policy", "aws_iam_role_policy_attachment", "aws_iam_user_policy"} {
		entry, ok := identity.LookupType(typeName)
		if !ok {
			t.Fatalf("%s has no ratified row", typeName)
		}
		var id string
		switch typeName {
		case "aws_iam_role_policy_attachment":
			id = "r/arn:aws:iam::123:policy/p"
		default:
			id = "owner:policy"
		}
		parts, ok := splitImportIDByComponents(entry, id)
		if !ok {
			t.Fatalf("%s: %q did not split", typeName, id)
		}
		back, ok := composeImportIDFromComponents(typeName, parts)
		if !ok || back != id {
			t.Errorf("%s: split then compose gave %q (ok=%v), want %q", typeName, back, ok, id)
		}
	}
}

func TestSplitImportIDByComponents_RefusesWhatItCannotSplitSoundly(t *testing.T) {
	entry, ok := identity.LookupType("aws_iam_role_policy")
	if !ok {
		t.Fatal("aws_iam_role_policy has no ratified row")
	}
	for _, id := range []string{"", "no-separator", ":leading", "trailing:"} {
		if parts, ok := splitImportIDByComponents(entry, id); ok {
			t.Errorf("%q split to %v; it has no sound reading under ROLENAME:POLICYNAME", id, parts)
		}
	}
	// Two adjacent attribute components with no literal between them have
	// no boundary to split at; the function refuses rather than guesses.
	adjacent := identity.TypeIdentity{Components: []identity.Component{{Attrs: []string{"a"}}, {Attrs: []string{"b"}}}}
	if parts, ok := splitImportIDByComponents(adjacent, "ab"); ok {
		t.Errorf("adjacent attribute components split to %v; want a refusal", parts)
	}
}

func TestParentHeldByThisPass(t *testing.T) {
	res := &Result{}
	res.Resolutions = []identity.Resolution{
		{Addr: mustAddr(t, "aws_iam_role.kept"), Class: identity.ClassConcrete, ImportID: "kept-role"},
		{Addr: mustAddr(t, "aws_iam_role.unbound"), Class: identity.ClassConcrete},
	}
	if !parentHeldByThisPass(res, "aws_iam_role", "kept-role") {
		t.Error("a resolved parent was not recognized as held")
	}
	if parentHeldByThisPass(res, "aws_iam_role", "moved-role") {
		t.Error("a parent no resolution names was reported as held")
	}
	// An unbound resolution carries no identity and vouches for nothing.
	if parentHeldByThisPass(res, "aws_iam_role", "") {
		t.Error("an empty identity matched an unbound resolution")
	}
	if parentHeldByThisPass(res, "aws_iam_user", "kept-role") {
		t.Error("a parent of another type matched on value alone")
	}
}
