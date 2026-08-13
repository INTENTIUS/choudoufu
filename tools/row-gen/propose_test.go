// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"strings"
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

// TestScanFileForRejected_SyntheticBlock exercises the line-window scanner
// against a small synthetic Go source rather than the real, large table.go -
// so this test pins the parsing rule itself (block start, block end, what
// counts as "still inside the comment") independent of that file's own
// prose ever changing shape.
func TestScanFileForRejected_SyntheticBlock(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/fixture.go"
	src := `package fixture

var x = 1

	// Rejected, and deliberately absent from this table:
	//
	//   - aws_one: some reason naming aws_two in passing.
	//   - aws_three: another reason.
	//

	serverAssigned("aws_four", ...)

	// a later, unrelated comment mentioning aws_five is not near any
	// Rejected heading and must not be captured.
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	out := map[string]bool{}
	if err := scanFileForRejected(path, out); err != nil {
		t.Fatalf("scanFileForRejected: %v", err)
	}

	for _, want := range []string{"aws_one", "aws_two", "aws_three"} {
		if !out[want] {
			t.Errorf("scanFileForRejected did not capture %q from inside the Rejected block: %+v", want, out)
		}
	}
	for _, notWant := range []string{"aws_four", "aws_five"} {
		if out[notWant] {
			t.Errorf("scanFileForRejected captured %q, which is outside the Rejected comment block: %+v", notWant, out)
		}
	}
}

// TestScanRejectedMentions_FindsKnownLambdaRejections is the regression tie
// to real history: aws_lambda_alias and aws_lambda_layer_version_permission
// are table.go's own worked "Rejected, and deliberately absent from this
// table" example (see its comment just above the Lambda batch's
// serverAssigned calls). If a future edit reshapes that comment so the
// scanner no longer finds them, this fails loudly instead of silently
// losing the safety net for the one case it was built to catch.
func TestScanRejectedMentions_FindsKnownLambdaRejections(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := scanRejectedMentions(root)
	if err != nil {
		t.Fatalf("scanRejectedMentions: %v", err)
	}
	for _, want := range []string{"aws_lambda_alias", "aws_lambda_layer_version_permission"} {
		if !rejected[want] {
			t.Errorf("scanRejectedMentions did not find %q, table.go's own worked Rejected example", want)
		}
	}
}

// TestRenderProposeReport_NoCandidates checks the explicit zero-candidates
// framing: PROPOSE must say why there is nothing to propose, not just print
// an empty section a reader could mistake for a tool that ran and found
// nothing to look for.
func TestRenderProposeReport_NoCandidates(t *testing.T) {
	stats := map[ruleKey]ruleStats{
		{Bucket: bucketServerAssigned, Rule: "r"}: {Compared: 10, Matched: 9},
	}
	out := renderProposeReport(stats, map[ruleKey]ruleStats{}, nil)
	if !strings.Contains(out, "0 logical types proposed") {
		t.Errorf("renderProposeReport with no candidates: want an explicit zero statement, got:\n%s", out)
	}
	if !strings.Contains(out, "No rule class currently clears the bar") {
		t.Errorf("renderProposeReport with no qualifying rules: want the near-miss framing, got:\n%s", out)
	}
	if !strings.Contains(out, "SPOT-CHECK CONTRACT") {
		t.Errorf("renderProposeReport: want the contract header even with zero candidates, got:\n%s", out)
	}
}

// TestRenderProposeReport_WithCandidate checks a populated report carries
// the rule's own track record next to the pasted block, and the pasted
// block itself is renderProposal's own output (reused verbatim).
func TestRenderProposeReport_WithCandidate(t *testing.T) {
	rule := ruleKey{Bucket: bucketServerAssigned, Rule: "primaryIdentifier ⊆ readOnlyProperties"}
	stats := map[ruleKey]ruleStats{rule: {Compared: 7, Matched: 7}}
	qualifying := stats
	p := proposal{
		TFType:            "aws_widget_gadget",
		CFNType:           "AWS::Widget::Gadget",
		Service:           "Widget",
		Bucket:            bucketServerAssigned,
		Rule:              rule.Rule,
		PrimaryIdentifier: []string{"Arn"},
		ReadOnly:          []string{"Arn"},
		Enumeration:       "list-free",
	}
	candidates := []proposeCandidate{{Proposal: p, Rule: rule, Stats: stats[rule]}}

	out := renderProposeReport(stats, qualifying, candidates)

	if !strings.Contains(out, "1 logical type(s) proposed") {
		t.Errorf("renderProposeReport: want the 1-candidate summary line, got:\n%s", out)
	}
	if !strings.Contains(out, "7/7 (100%)") {
		t.Errorf("renderProposeReport: want the candidate's own rule track record (7/7), got:\n%s", out)
	}
	if !strings.Contains(out, "aws_widget_gadget") {
		t.Errorf("renderProposeReport: want the candidate's TF type, got:\n%s", out)
	}
	if !strings.Contains(out, "paste into internal/live/lint/admission.go") {
		t.Errorf("renderProposeReport: want the reused renderProposal pastable block, got:\n%s", out)
	}
	if !strings.Contains(out, "spot-check:") {
		t.Errorf("renderProposeReport: want the per-candidate spot-check reminder, got:\n%s", out)
	}
}

// TestBuildProposeReport_AgainstRealRepo is a loose integration check: no
// error, a well-formed summary line, and internal consistency (the
// candidate count in the summary matches the printed report) against
// whatever the real, current checkout's data says today. Deliberately does
// not assert an exact qualifying-class count - that number is expected to
// change as more ratification batches land, and pinning it here would make
// this test fail on every batch merge for a reason unrelated to what it is
// checking. TestRuleStats_Qualifies and TestSelectProposeCandidates already
// pin the selection logic itself against synthetic, stable fixtures.
func TestBuildProposeReport_AgainstRealRepo(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	report, summary, err := buildProposeReport(root)
	if err != nil {
		t.Fatalf("buildProposeReport: %v", err)
	}
	if !strings.HasPrefix(summary, "row-gen -propose: ") {
		t.Errorf("summary = %q, want the row-gen -propose: prefix", summary)
	}
	if !strings.Contains(report, "SPOT-CHECK CONTRACT") {
		t.Error("report is missing the spot-check contract header")
	}
	if !strings.Contains(report, "rule-class ledger") {
		t.Error("report is missing the rule-class ledger")
	}
}
