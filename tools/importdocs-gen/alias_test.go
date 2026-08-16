// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

func TestAliasDeclaredFor_GroundTruth(t *testing.T) {
	doc := readTestdataDoc(t, "lb_target_group_attachment")
	got := aliasDeclaredFor(doc, "aws_lb_target_group_attachment")
	if len(got) != 1 || got[0] != "aws_alb_target_group_attachment" {
		t.Errorf("aliasDeclaredFor(lb_target_group_attachment, aws_lb_target_group_attachment) = %v, want [aws_alb_target_group_attachment]",
			got)
	}
}

func TestAliasDeclaredFor_WrongCanonicalNeverMatches(t *testing.T) {
	doc := readTestdataDoc(t, "lb_target_group_attachment")
	// The note names aws_lb_target_group_attachment as the canonical type;
	// asking on behalf of some other type must find nothing, even though
	// the doc text is unchanged - the canonical-name check is what keeps a
	// page from being read as a claim about a type it never names.
	if got := aliasDeclaredFor(doc, "aws_lb_target_group"); len(got) != 0 {
		t.Errorf("aliasDeclaredFor with the wrong canonical name = %v, want none", got)
	}
}

func TestAliasDeclaredFor_UnrelatedKnownAsPhraseIsNotAnAlias(t *testing.T) {
	// ses_configuration_set.html.markdown's own prose: "Resetting these
	// metrics is known as a fresh start" - "known as" with no backticked
	// aws_ pair either side. The regex's own shape excludes it before any
	// canonical-name check runs.
	doc := "* `last_fresh_start` - Resetting these metrics is known as a fresh start."
	if got := aliasDeclaredFor(doc, "aws_ses_configuration_set"); len(got) != 0 {
		t.Errorf("aliasDeclaredFor over unrelated 'known as' prose = %v, want none", got)
	}
}

func TestAliasDeclaredFor_SelfAliasIgnored(t *testing.T) {
	doc := "`aws_lb` is known as `aws_lb`. The functionality is identical."
	if got := aliasDeclaredFor(doc, "aws_lb"); len(got) != 0 {
		t.Errorf("aliasDeclaredFor with alias == canonical = %v, want none (not a real alias claim)", got)
	}
}
