// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

func TestDestinedTier(t *testing.T) {
	cases := []struct {
		name     string
		taggable bool
		path     string
		want     string
	}{
		{"taggable wins over a client-named path", true, surveyPathClientNamed, TierMarkerCarried},
		{"taggable, marker path", true, surveyPathMarker, TierMarkerCarried},
		{"untaggable, client-named", false, surveyPathClientNamed, TierDeclarationCarried},
		{"untaggable, parent-derived", false, surveyPathParentDerived, TierDeclarationCarried},
		{"untaggable, account-derived", false, surveyPathAccountDerived, TierDeclarationCarried},
		{"untaggable, unique-name", false, surveyPathUniqueName, TierDeclarationCarried},
		{"untaggable, enumerable unbindable", false, surveyPathEnumerableUnbindable, TierRecordCarried},
		{"untaggable, moves to Ops", false, surveyPathOps, TierRecordCarried},
		{"untaggable, unrecognized path fails safe", false, "some future token", TierRecordCarried},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := destinedTier(c.taggable, c.path); got != c.want {
				t.Errorf("destinedTier(%v, %q) = %q, want %q", c.taggable, c.path, got, c.want)
			}
		})
	}
}

func TestClassifyRejectedReason(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   string
	}{
		{"hand separator phrase", "issue #245's 'needs hand separator' slice: the CFN registry's primary_identifier is composite", StatusNeedsSeparator},
		{"no import section", "the pinned v6.59.0 provider docs carry no Import section at all for this resource.", StatusNeedsEvidence},
		{"no worked example", "There is no worked example anywhere in the doc to read a separator character off.", StatusNeedsEvidence},
		{"lack of import evidence", "Left unadmitted for lack of import evidence.", StatusNeedsEvidence},
		{"neither phrase, default", "verifies cleanly (server-assigned via id) but ties with an already-admitted alias.", StatusPendingRatification},
		{"empty reason, default", "", StatusPendingRatification},
		{"separator checked before evidence", "no worked example to read a hand separator from", StatusNeedsSeparator},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyRejectedReason(c.reason); got != c.want {
				t.Errorf("classifyRejectedReason(%q) = %q, want %q", c.reason, got, c.want)
			}
		})
	}
}

func TestClassifyPrecedence(t *testing.T) {
	tierD := map[string]bool{"aws_tier_d_example": true}

	t.Run("tier D overrides admission", func(t *testing.T) {
		// A tier-D type is never actually in identity.DefaultTable (the
		// harness ratchet enforces that), but this proves the precedence
		// holds even if it were, since Tier D is checked first.
		st := surveyType{Type: "aws_tier_d_example", Path: surveyPathMarker}
		st.Signals.Taggable = true
		row := classify(st, mappingRow{}, false, rejectedEntry{}, tierD)
		if row.Tier != TierExcludedByDesign || row.Status != StatusExcluded {
			t.Errorf("tier D type classified %s/%s, want %s/%s", row.Tier, row.Status, TierExcludedByDesign, StatusExcluded)
		}
	})

	t.Run("unrecognized untaggable type falls to pending-ratification by default", func(t *testing.T) {
		st := surveyType{Type: "aws_example_ops", Path: surveyPathOps}
		row := classify(st, mappingRow{}, false, rejectedEntry{}, tierD)
		if row.Tier != TierRecordCarried {
			t.Errorf("tier = %s, want %s", row.Tier, TierRecordCarried)
		}
		if row.Status != StatusPendingRatification {
			t.Errorf("status = %s, want %s", row.Status, StatusPendingRatification)
		}
		if row.Facts.Admitted || row.Facts.Markerless || row.Facts.TierD {
			t.Errorf("facts should be all false for this synthetic type: %+v", row.Facts)
		}
	})

	t.Run("rejected type surfaces its reason and needs-separator status", func(t *testing.T) {
		st := surveyType{Type: "aws_example_needs_sep", Path: surveyPathOps}
		re := rejectedEntry{Reason: "issue #245's 'needs hand separator' slice"}
		row := classify(st, mappingRow{}, true, re, tierD)
		if row.Status != StatusNeedsSeparator {
			t.Errorf("status = %s, want %s", row.Status, StatusNeedsSeparator)
		}
		if !row.Facts.Rejected || row.Facts.RejectedReason != re.Reason {
			t.Errorf("facts did not surface the rejected reason: %+v", row.Facts)
		}
	})

	t.Run("mapping facts are surfaced without changing tier or status", func(t *testing.T) {
		st := surveyType{Type: "aws_example_mapped", Path: surveyPathClientNamed}
		m := mappingRow{Via: "fold", FoldParent: "AWS::Example::Parent"}
		row := classify(st, m, false, rejectedEntry{}, tierD)
		if row.Facts.MappingVia != "fold" || row.Facts.MappingFoldParent != "AWS::Example::Parent" {
			t.Errorf("mapping facts not surfaced: %+v", row.Facts)
		}
		if row.Tier != TierDeclarationCarried || row.Status != StatusPendingRatification {
			t.Errorf("mapping facts changed the decision: tier=%s status=%s", row.Tier, row.Status)
		}
	})
}
