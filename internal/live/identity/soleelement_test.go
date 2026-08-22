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
)

// A Component.SoleElement argument reached through a variable, not a
// syntactic list construct: [resolver.soleElementFromValue]'s whole point.
// One element must narrow and resolve; more than one must still refuse
// "Ambiguous list-valued identity argument", never "Non-string identity
// argument" - the collection must never reach stringValue whole.
func TestSoleElementFromValue(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "sole-element-from-value"), nil)

	result, diags := Resolve(context.Background(), cfg)

	res := resolutionAt(t, result, "aws_security_group_rule.from_default")
	if res.Class != ClassConcrete {
		t.Fatalf("from_default resolved %s; every value it needs is a literal or a defaulted variable", res.Class)
	}
	want := "sg-0123456789abcdef0_ingress_tcp_22_22_10.0.0.0/8"
	if res.ImportID != want {
		t.Errorf("from_default resolved to %q, want %q", res.ImportID, want)
	}

	if _, ok := result.Get(mustAddr(t, "aws_security_group_rule.ambiguous")); ok {
		t.Fatalf("aws_security_group_rule.ambiguous resolved; var.many has two elements, and the AWS API - not list order - decides how they compose")
	}
	foundAmbiguous := false
	foundNonString := false
	for _, d := range diags {
		switch d.Description().Summary {
		case "Ambiguous list-valued identity argument":
			foundAmbiguous = true
		case "Non-string identity argument":
			foundNonString = true
		}
	}
	if !foundAmbiguous {
		t.Fatalf("expected an %q diagnostic, got: %v", "Ambiguous list-valued identity argument", diags)
	}
	if foundNonString {
		t.Fatalf("var.many should refuse as an ambiguous collection, not reach stringValue as a whole non-string value: %v", diags)
	}
}

// TestSoleElementZeroIsNotAmbiguous is GitHub issue #369: a
// Component.SoleElement alternation member that is a definite ZERO-element
// list must not read as "unclear which member applies" when another member
// of the SAME alternation already carries a value - the identity is fully
// settled by the sibling, and the empty list means "this leg doesn't
// apply", not ambiguity. The reverse must still hold: when every member of
// the alternation is a definite empty list and nothing else is set, that is
// the ordinary all-absent case ("Identity argument not set"), never a
// resolved guess - HANDOFF's "never write a wrong marker" rule leaves no
// room for treating "nothing was set" as though something had been.
func TestSoleElementZeroIsNotAmbiguous(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "sole-element-from-value"), nil)

	result, diags := Resolve(context.Background(), cfg)

	res := resolutionAt(t, result, "aws_security_group_rule.resolved_by_sibling")
	if res.Class != ClassConcrete {
		t.Fatalf("resolved_by_sibling resolved %s; source_security_group_id is a literal and prefix_list_ids is a definite empty list, which must defer to it", res.Class)
	}
	want := "sg-0123456789abcdef0_ingress_tcp_80_80_sg-fedcba9876543210f"
	if res.ImportID != want {
		t.Errorf("resolved_by_sibling resolved to %q, want %q", res.ImportID, want)
	}
	for _, d := range diags {
		if strings.Contains(d.Description().Detail, "resolved_by_sibling") {
			t.Errorf("resolved_by_sibling left a diagnostic behind: %s: %s", d.Description().Summary, d.Description().Detail)
		}
	}

	if _, ok := result.Get(mustAddr(t, "aws_security_group_rule.all_empty_no_sibling")); ok {
		t.Fatalf("aws_security_group_rule.all_empty_no_sibling resolved; every alternation member is a definite empty list, so none of them settles the identity")
	}
	if !hasDiag(diags, "Identity argument not set", "all_empty_no_sibling") {
		t.Fatalf("expected all_empty_no_sibling to refuse as %q (every member proven empty, none set), got: %v", "Identity argument not set", diags)
	}
	for _, d := range diags {
		if strings.Contains(d.Description().Detail, "all_empty_no_sibling") && d.Description().Summary == "Ambiguous list-valued identity argument" {
			t.Fatalf("all_empty_no_sibling raised %q; zero elements is never ambiguous, it is nothing set: %s", d.Description().Summary, d.Description().Detail)
		}
	}
}
