// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"testing"
)

// TestRuleAdoption_GroupsByBucketAndRule pins the grouping key: two
// proposals in the same bucket but reached by a different Rule string never
// pool into one ruleStats, and a non-pastable bucket (fold-child here) never
// enters the ledger at all - see pastableBucket's own doc comment for why
// that exclusion exists (a fold-child's Matched rate is always 0 by
// construction, not a real signal).
func TestRuleAdoption_GroupsByBucketAndRule(t *testing.T) {
	rows := []convergenceRow{
		{TFType: "aws_a", ProposedBucket: "server-assigned", ProposedRule: "rule-1", Matched: true},
		{TFType: "aws_b", ProposedBucket: "server-assigned", ProposedRule: "rule-1", Matched: true},
		{TFType: "aws_c", ProposedBucket: "server-assigned", ProposedRule: "rule-1", Matched: false},
		{TFType: "aws_d", ProposedBucket: "server-assigned", ProposedRule: "rule-precedence", Matched: true},
		{TFType: "aws_e", ProposedBucket: "client-named", ProposedRule: "rule-1", Matched: true},
		{TFType: "aws_f", ProposedBucket: "fold-child", ProposedRule: "via==fold: property-child of X", Matched: false},
		{TFType: "aws_g", ProposedBucket: "needs-hand-separator", ProposedRule: "composite", Matched: false},
		{TFType: "aws_h", ProposedBucket: "evidence-only", ProposedRule: "rule-1", Matched: false},
	}

	stats := ruleAdoption(rows)

	if len(stats) != 3 {
		t.Fatalf("ruleAdoption produced %d rule classes, want 3 (fold-child/needs-hand-separator/evidence-only rows must be excluded): %+v", len(stats), stats)
	}

	sa1 := stats[ruleKey{Bucket: bucketServerAssigned, Rule: "rule-1"}]
	if sa1.Compared != 3 || sa1.Matched != 2 {
		t.Errorf("server-assigned/rule-1 = %+v, want Compared=3 Matched=2", sa1)
	}
	saP := stats[ruleKey{Bucket: bucketServerAssigned, Rule: "rule-precedence"}]
	if saP.Compared != 1 || saP.Matched != 1 {
		t.Errorf("server-assigned/rule-precedence = %+v, want Compared=1 Matched=1", saP)
	}
	cn1 := stats[ruleKey{Bucket: bucketClientNamed, Rule: "rule-1"}]
	if cn1.Compared != 1 || cn1.Matched != 1 {
		t.Errorf("client-named/rule-1 = %+v, want Compared=1 Matched=1", cn1)
	}
}

// TestRuleStats_Qualifies pins the auto-propose bar: 100% match AND at
// least proposeMinSample instances. Either condition failing alone must
// disqualify - a rule class does not get a pass for being unanimous over
// too few instances, or for having plenty of instances but even one
// disagreement.
func TestRuleStats_Qualifies(t *testing.T) {
	tests := []struct {
		name string
		s    ruleStats
		want bool
	}{
		{"perfect and large enough", ruleStats{Compared: proposeMinSample, Matched: proposeMinSample}, true},
		{"perfect but one below the floor", ruleStats{Compared: proposeMinSample - 1, Matched: proposeMinSample - 1}, false},
		{"large enough but one mismatch", ruleStats{Compared: proposeMinSample + 10, Matched: proposeMinSample + 9}, false},
		{"zero compared", ruleStats{Compared: 0, Matched: 0}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.qualifies(); got != tt.want {
				t.Errorf("qualifies() = %v, want %v (stats=%+v)", got, tt.want, tt.s)
			}
		})
	}
}

// TestQualifyingRules_FiltersToQualifyingOnly checks the ledger-to-qualifying
// filter keeps only classes ruleStats.qualifies accepts.
func TestQualifyingRules_FiltersToQualifyingOnly(t *testing.T) {
	stats := map[ruleKey]ruleStats{
		{Bucket: bucketServerAssigned, Rule: "clean"}:  {Compared: proposeMinSample, Matched: proposeMinSample},
		{Bucket: bucketServerAssigned, Rule: "dirty"}:  {Compared: proposeMinSample + 3, Matched: proposeMinSample + 2},
		{Bucket: bucketClientNamed, Rule: "too-small"}: {Compared: proposeMinSample - 1, Matched: proposeMinSample - 1},
	}
	got := qualifyingRules(stats)
	if len(got) != 1 {
		t.Fatalf("qualifyingRules returned %d classes, want 1: %+v", len(got), got)
	}
	if _, ok := got[ruleKey{Bucket: bucketServerAssigned, Rule: "clean"}]; !ok {
		t.Errorf("qualifyingRules dropped the one class that should have qualified: %+v", got)
	}
}

// TestSelectProposeCandidates covers every exclusion selectProposeCandidates
// applies, one proposal per reason so a broken exclusion fails on a specific
// case rather than a vague count mismatch.
func TestSelectProposeCandidates(t *testing.T) {
	qualifyingRule := ruleKey{Bucket: bucketServerAssigned, Rule: "clean"}
	qualifying := map[ruleKey]ruleStats{qualifyingRule: {Compared: 5, Matched: 5}}

	proposals := []proposal{
		{TFType: "aws_new_clean", Bucket: bucketServerAssigned, Rule: "clean"},           // should be proposed
		{TFType: "aws_already_admitted", Bucket: bucketServerAssigned, Rule: "clean"},    // excluded: already admitted
		{TFType: "aws_known_rejected", Bucket: bucketServerAssigned, Rule: "clean"},      // excluded: recorded rejection
		{TFType: "aws_wrong_rule", Bucket: bucketServerAssigned, Rule: "not-qualifying"}, // excluded: rule does not qualify
		{TFType: "aws_not_pastable", Bucket: bucketNeedsHandSeparator, Rule: "clean"},    // excluded: bucket never pastable
	}
	admitted := map[string]bool{"aws_already_admitted": true}
	rejected := map[string]bool{"aws_known_rejected": true}

	got := selectProposeCandidates(proposals, admitted, rejected, qualifying)

	if len(got) != 1 {
		t.Fatalf("selectProposeCandidates returned %d candidates, want 1: %+v", len(got), got)
	}
	if got[0].Proposal.TFType != "aws_new_clean" {
		t.Errorf("selectProposeCandidates returned %q, want aws_new_clean", got[0].Proposal.TFType)
	}
	if got[0].Rule != qualifyingRule {
		t.Errorf("candidate Rule = %+v, want %+v", got[0].Rule, qualifyingRule)
	}
}

// TestSelectProposeCandidates_Deterministic pins the sort order (TF type,
// ascending) so two runs over the same inputs always print candidates in
// the same order.
func TestSelectProposeCandidates_Deterministic(t *testing.T) {
	rule := ruleKey{Bucket: bucketClientNamed, Rule: "r"}
	qualifying := map[ruleKey]ruleStats{rule: {Compared: 5, Matched: 5}}
	proposals := []proposal{
		{TFType: "aws_zzz", Bucket: bucketClientNamed, Rule: "r"},
		{TFType: "aws_aaa", Bucket: bucketClientNamed, Rule: "r"},
		{TFType: "aws_mmm", Bucket: bucketClientNamed, Rule: "r"},
	}
	got := selectProposeCandidates(proposals, map[string]bool{}, map[string]bool{}, qualifying)
	want := []string{"aws_aaa", "aws_mmm", "aws_zzz"}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Proposal.TFType != w {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i].Proposal.TFType, w)
		}
	}
}

// TestLoadRejectedTypes_LedgerIsIntact is the regression tie to real history:
// aws_lambda_alias and aws_lambda_layer_version_permission were the identity
// table's own worked "Rejected, and deliberately absent from this table"
// example, recorded in prose in table_cohort_lambda.go until issue #96
// generated that table in full and moved every such ruling into
// rejected.json. If a future edit drops rows from the ledger, this fails
// loudly instead of silently losing the safety net.
func TestLoadRejectedTypes_LedgerIsIntact(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := loadRejectedTypes(root)
	if err != nil {
		t.Fatalf("loadRejectedTypes: %v", err)
	}
	for _, want := range []string{"aws_lambda_alias", "aws_lambda_layer_version_permission"} {
		if !rejected[want] {
			t.Errorf("loadRejectedTypes did not find %q, the identity table's own worked Rejected example", want)
		}
	}
	// Sentinels for the second recovery (#127): the remainder batch's
	// rejections lived only in prose banners a merge dropped before #96's
	// scrape ran, and were recovered separately from the remainder estate's
	// README. One from each of that README's two rejection sections.
	for _, want := range []string{"aws_fms_policy", "aws_waf_web_acl"} {
		if !rejected[want] {
			t.Errorf("loadRejectedTypes did not find %q, recovered from the remainder README in #127", want)
		}
	}
	// The ledger was recovered wholesale from deleted prose - 147 types from
	// the pre-#96 fragments, 65 more from the remainder README (#127); a
	// drop well below that count means rows were lost, not curated.
	if len(rejected) < 205 {
		t.Errorf("rejected.json carries %d types, want at least the 212 recovered from the pre-#96 fragments and the remainder README", len(rejected))
	}
}

// TestLoadRejectedTypes_RefusesEmptyLedger pins the fail-closed rule: an
// absent or empty ledger is an error, never an empty veto set.
func TestLoadRejectedTypes_RefusesEmptyLedger(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/tools/row-gen", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/tools/row-gen/rejected.json", []byte(`{"rejected":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRejectedTypes(dir); err == nil {
		t.Error("loadRejectedTypes accepted an empty ledger; it must fail closed")
	}
	if _, err := loadRejectedTypes(t.TempDir()); err == nil {
		t.Error("loadRejectedTypes accepted a missing ledger; it must fail closed")
	}
}
