// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"testing"
)

// TestExtractFormer2RowsAgainstExcerpt exercises extractFormer2Rows against
// a trimmed real excerpt of former2's own source
// (testdata/former2-excerpt.js), rather than only a hand-built fixture: the
// tolerant regex parser must find every real tracked_resources.push({...})
// pairing and skip the one push with no 'type' field at all.
func TestExtractFormer2RowsAgainstExcerpt(t *testing.T) {
	data, err := os.ReadFile("testdata/former2-excerpt.js")
	if err != nil {
		t.Fatal(err)
	}
	rows := extractFormer2Rows(string(data))

	want := map[string]string{
		"aws_instance":        "AWS::EC2::Instance",
		"aws_placement_group": "AWS::EC2::PlacementGroup",
		"aws_ebs_volume":      "AWS::EC2::Volume",
	}
	if len(rows) != len(want) {
		t.Fatalf("extractFormer2Rows found %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.TFType] = r.CFNType
	}
	for tf, cfn := range want {
		if got[tf] != cfn {
			t.Errorf("row for %s: cfn_type = %q, want %q", tf, got[tf], cfn)
		}
	}
	if _, ok := got["aws_dx_connection"]; ok {
		t.Error("aws_dx_connection has no 'type' field in the excerpt's push literal; extractFormer2Rows must not have paired it with anything")
	}
}

// TestFilterFormer2RowsDrops exercises every drop reason
// filterFormer2Rows can produce: a CFN type not in the registry at all, a
// CFN type in the registry but with no primaryIdentifier, a TF type not in
// the current provider roster, and former2 contradicting itself (the same
// TF type pushed with two different CFN types).
func TestFilterFormer2RowsDrops(t *testing.T) {
	rows := []Former2Row{
		{TFType: "aws_good", CFNType: "AWS::Good::Thing"},
		{TFType: "aws_unknown_cfn", CFNType: "AWS::Nope::Thing"},
		{TFType: "aws_no_primary_id", CFNType: "AWS::NoID::Thing"},
		{TFType: "aws_unknown_tf", CFNType: "AWS::Good::Thing"},
		{TFType: "aws_self_contradiction", CFNType: "AWS::Good::Thing"},
		{TFType: "aws_self_contradiction", CFNType: "AWS::Other::Thing"},
	}
	cfnWithPrimaryID := map[string]bool{"AWS::Good::Thing": true, "AWS::Other::Thing": true}
	cfnKnown := map[string]bool{"AWS::Good::Thing": true, "AWS::Other::Thing": true, "AWS::NoID::Thing": true}
	tfKnown := map[string]bool{"aws_good": true, "aws_no_primary_id": true, "aws_self_contradiction": true}

	usable, drops := filterFormer2Rows(rows, cfnWithPrimaryID, cfnKnown, tfKnown)

	if len(usable) != 1 || usable["aws_good"] != "AWS::Good::Thing" {
		t.Errorf("usable = %v, want exactly {aws_good: AWS::Good::Thing}", usable)
	}

	reasons := map[string]Former2DropReason{}
	for _, d := range drops {
		reasons[d.Row.TFType+"|"+d.Row.CFNType] = d.Reason
	}
	checks := []struct {
		key    string
		reason Former2DropReason
	}{
		{"aws_unknown_cfn|AWS::Nope::Thing", DropCFNUnknown},
		{"aws_no_primary_id|AWS::NoID::Thing", DropCFNNoPrimaryIdentifier},
		{"aws_unknown_tf|AWS::Good::Thing", DropTFUnknown},
		{"aws_self_contradiction|AWS::Good::Thing", DropSelfContradiction},
		{"aws_self_contradiction|AWS::Other::Thing", DropSelfContradiction},
	}
	for _, c := range checks {
		if reasons[c.key] != c.reason {
			t.Errorf("drop reason for %s = %q, want %q", c.key, reasons[c.key], c.reason)
		}
	}
	if len(drops) != len(checks) {
		t.Errorf("got %d drops, want %d: %+v", len(drops), len(checks), drops)
	}
}

// TestFormer2ContradictionsFinds checks former2Contradictions directly: a
// former2 row disagreeing with an already-mapped row is reported, a row
// agreeing is not, and a former2 row for a TF type this tool left at
// via:none is not a contradiction at all (buildMapping's own job, not
// former2Contradictions').
func TestFormer2ContradictionsFinds(t *testing.T) {
	cfnA, cfnB := "AWS::A::Thing", "AWS::B::Thing"
	existing := map[string]Row{
		"aws_agrees":    {TFType: "aws_agrees", Via: viaName, CFNType: &cfnA},
		"aws_disagrees": {TFType: "aws_disagrees", Via: viaAlias, CFNType: &cfnA},
		"aws_unmapped":  {TFType: "aws_unmapped", Via: viaNone},
	}
	usable := map[string]string{
		"aws_agrees":    cfnA,
		"aws_disagrees": cfnB,
		"aws_unmapped":  cfnB,
	}

	got := former2Contradictions(usable, existing)
	if len(got) != 1 {
		t.Fatalf("former2Contradictions = %+v, want exactly one entry", got)
	}
	c := got[0]
	if c.TFType != "aws_disagrees" || c.MappedCFN != cfnA || c.MappedVia != viaAlias || c.Former2CFN != cfnB {
		t.Errorf("contradiction = %+v, want {aws_disagrees, %s, %s, %s}", c, cfnA, viaAlias, cfnB)
	}
}
