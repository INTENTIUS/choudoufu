// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func testRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// readCommitted reads and decodes the committed live/ratification-queue.json.
func readCommitted(t *testing.T, root string) Artifact {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(OutputJSONRel))) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v (run `go run ./tools/ratification-queue-gen` and commit it)", OutputJSONRel, err)
	}
	var art Artifact
	if err := json.Unmarshal(data, &art); err != nil {
		t.Fatalf("decoding %s: %v", OutputJSONRel, err)
	}
	return art
}

// readReadinessPendingCount reads live/readiness.json directly (not through
// this package's own loader) and returns its
// counts.statuses["pending-ratification"], so this test does not trust its
// own Build() to have read that number correctly.
func readReadinessPendingCount(t *testing.T, root string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ReadinessJSONRel))) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v", ReadinessJSONRel, err)
	}
	var art struct {
		Counts struct {
			Statuses map[string]int `json:"statuses"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(data, &art); err != nil {
		t.Fatalf("decoding %s: %v", ReadinessJSONRel, err)
	}
	n, ok := art.Counts.Statuses[PendingRatificationStatus]
	if !ok {
		t.Fatalf("%s counts.statuses has no %q key", ReadinessJSONRel, PendingRatificationStatus)
	}
	return n
}

// TestAcceptPendingCountMatches is issue #426's own Accept criterion: the
// total number of types across every batch in the committed queue equals
// live/readiness.json's own pending-ratification count, read independently
// of this package's loader.
func TestAcceptPendingCountMatches(t *testing.T) {
	root := testRepoRoot(t)
	art := readCommitted(t, root)
	want := readReadinessPendingCount(t, root)

	if art.Counts.PendingRatification != want {
		t.Errorf("%s counts.pending_ratification = %d, live/readiness.json counts.statuses.pending-ratification = %d",
			OutputJSONRel, art.Counts.PendingRatification, want)
	}

	sum := 0
	seen := map[string]int{}
	for _, b := range art.Batches {
		sum += len(b.Types)
		for _, row := range b.Types {
			seen[row.Type]++
		}
	}
	if sum != want {
		t.Errorf("%s batches carry %d types total, live/readiness.json's pending-ratification count is %d", OutputJSONRel, sum, want)
	}
	for typ, n := range seen {
		if n != 1 {
			t.Errorf("%s appears %d times across %s's batches, want exactly once", typ, n, OutputJSONRel)
		}
	}
	if len(seen) != want {
		t.Errorf("%s carries %d distinct types across its batches, want %d", OutputJSONRel, len(seen), want)
	}
}

// TestBatchesNeverExceedTarget: every batch is at most BatchSizeTarget types.
func TestBatchesNeverExceedTarget(t *testing.T) {
	root := testRepoRoot(t)
	art := readCommitted(t, root)
	for _, b := range art.Batches {
		if len(b.Types) > art.BatchSizeTarget {
			t.Errorf("batch %d (%s) carries %d types, more than the batch_size_target %d", b.Number, b.Family, len(b.Types), art.BatchSizeTarget)
		}
		if len(b.Types) == 0 {
			t.Errorf("batch %d (%s) is empty", b.Number, b.Family)
		}
	}
}

// TestBatchNumbersAreSequential: batch numbers are 1..N with no gaps or
// repeats, in the order they appear.
func TestBatchNumbersAreSequential(t *testing.T) {
	root := testRepoRoot(t)
	art := readCommitted(t, root)
	for i, b := range art.Batches {
		if b.Number != i+1 {
			t.Errorf("batches[%d].number = %d, want %d", i, b.Number, i+1)
		}
	}
}

// TestPriorityFamiliesComeFirstInOrder: every batch whose family is one of
// PriorityFamilies appears, across the whole queue, in COVERAGE.md's stated
// order, and before every non-priority family's batches.
func TestPriorityFamiliesComeFirstInOrder(t *testing.T) {
	root := testRepoRoot(t)
	art := readCommitted(t, root)

	seenNonPriority := false
	lastRank := -1
	for _, b := range art.Batches {
		rank := priorityRank(b.Family)
		if rank < 0 {
			seenNonPriority = true
			continue
		}
		if seenNonPriority {
			t.Fatalf("batch %d (%s) is a priority family appearing after a non-priority family; priority families must all come first", b.Number, b.Family)
		}
		if rank < lastRank {
			t.Fatalf("batch %d (%s) has priority rank %d, which is out of COVERAGE.md's stated order (last seen rank %d)", b.Number, b.Family, rank, lastRank)
		}
		lastRank = rank
	}
}

// TestEvidenceAlwaysHasReadinessFacts: every row's evidence carries its
// readiness.json survey_path regardless of Source, since that field is
// never empty in live/readiness.json's own facts.
func TestEvidenceAlwaysHasReadinessFacts(t *testing.T) {
	root := testRepoRoot(t)
	art := readCommitted(t, root)
	for _, b := range art.Batches {
		for _, row := range b.Types {
			if row.Evidence.SurveyPath == "" {
				t.Errorf("%s: evidence.survey_path is empty", row.Type)
			}
			if row.Evidence.Source != "propose" && row.Evidence.Source != "readiness-facts" {
				t.Errorf("%s: evidence.source = %q, want \"propose\" or \"readiness-facts\"", row.Type, row.Evidence.Source)
			}
			if row.Evidence.Source == "propose" && row.Evidence.ProposeBlock == "" {
				t.Errorf("%s: evidence.source is \"propose\" but propose_block is empty", row.Type)
			}
		}
	}
}

// TestBatchTemplateIsPresent: the committed artifact carries issue #426's
// batch template (title pattern, spot-check steps, accept criteria) so a
// follow-up unit does not need to re-read the issue.
func TestBatchTemplateIsPresent(t *testing.T) {
	root := testRepoRoot(t)
	art := readCommitted(t, root)
	bt := art.BatchTemplate
	if bt.TitlePattern == "" {
		t.Error("batch_template.title_pattern is empty")
	}
	if len(bt.BodySpotCheckSteps) != 4 {
		t.Errorf("batch_template.body_spot_check_steps has %d entries, want the 4-step spot-check contract", len(bt.BodySpotCheckSteps))
	}
	if len(bt.Accept) == 0 {
		t.Error("batch_template.accept is empty")
	}
}

// TestArtifactMatchesCommitted rebuilds the artifact fresh (including a
// real `go run ./tools/row-gen -propose` subprocess) and checks it against
// what is committed - the same "generator, run again, matches disk" guard
// tools/readiness-gen/build_test.go's own TestArtifactMatchesCommitted
// holds itself to.
func TestArtifactMatchesCommitted(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to `go run ./tools/row-gen -propose`; skipped in -short")
	}
	root := testRepoRoot(t)
	want, err := Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := readCommitted(t, root)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s is stale: does not match a fresh `go run ./tools/ratification-queue-gen`; re-run and commit it", OutputJSONRel)
	}
}

// TestBuildIsDeterministic: two independent Build() calls - two fresh
// row-gen -propose subprocesses, two fresh reads of every input - produce
// identical output, issue #426's own "deterministic (two runs
// byte-identical)" Accept criterion.
func TestBuildIsDeterministic(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to `go run ./tools/row-gen -propose` twice; skipped in -short")
	}
	root := testRepoRoot(t)
	a, err := Build(root)
	if err != nil {
		t.Fatalf("Build (first run): %v", err)
	}
	b, err := Build(root)
	if err != nil {
		t.Fatalf("Build (second run): %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Error("two Build() runs produced different output; something in the pipeline is not deterministic")
	}
}

func TestTFPrefix(t *testing.T) {
	cases := map[string]string{
		"aws_instance":            "instance",
		"aws_ec2_carrier_gateway": "ec2",
		"aws_s3_bucket":           "s3",
		"aws_iam_role":            "iam",
	}
	for in, want := range cases {
		if got := tfPrefix(in); got != want {
			t.Errorf("tfPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFamilyOf(t *testing.T) {
	cases := []struct {
		tfType     string
		cfnNS      string
		wantFamily string
	}{
		{"aws_vpc", "EC2", "EC2/VPC"},
		{"aws_default_vpc", "", "EC2/VPC"},           // no CFN model; TF-prefix fallback
		{"aws_s3control_bucket", "S3Outposts", "S3"}, // priority match via S3-prefix namespace rule
		{"aws_lambda_function", "Lambda", "Lambda"},
		{"aws_sqs_queue", "SQS", "SQS/SNS"},
		{"aws_sns_topic", "SNS", "SQS/SNS"},
		{"aws_eks_cluster", "EKS", "EKS/ECS"},
		{"aws_ecs_cluster", "ECS", "EKS/ECS"},
		{"aws_lb", "ElasticLoadBalancingV2", "ELB"},
		{"aws_elb", "ElasticLoadBalancing", "ELB"},
		{"aws_route53domains_domain", "", "Route53"}, // no CFN model; TF-prefix fallback
		{"aws_cloudwatch_log_group", "Logs", "CloudWatch"},
		{"aws_redshift_cluster", "Redshift", "Redshift"},       // non-priority CFN family
		{"aws_quicksight_folder_membership", "", "Quicksight"}, // non-priority TF-prefix fallback
	}
	for _, c := range cases {
		if got := familyOf(c.tfType, c.cfnNS); got != c.wantFamily {
			t.Errorf("familyOf(%q, %q) = %q, want %q", c.tfType, c.cfnNS, got, c.wantFamily)
		}
	}
}

func TestBuildBatchesNeverMixesFamilies(t *testing.T) {
	rows := []Row{
		{Type: "aws_a1"}, {Type: "aws_a2"}, {Type: "aws_b1"},
	}
	familyOfType := map[string]string{"aws_a1": "A", "aws_a2": "A", "aws_b1": "B"}
	batches := buildBatches(rows, familyOfType)
	for _, b := range batches {
		for _, r := range b.Types {
			if familyOfType[r.Type] != b.Family {
				t.Errorf("batch %d declares family %q but contains %s (family %q)", b.Number, b.Family, r.Type, familyOfType[r.Type])
			}
		}
	}
}

func TestBuildBatchesSplitsLargeFamily(t *testing.T) {
	var rows []Row
	familyOfType := map[string]string{}
	for i := 0; i < 30; i++ {
		typ := "aws_big_" + string(rune('a'+i))
		rows = append(rows, Row{Type: typ})
		familyOfType[typ] = "Big"
	}
	batches := buildBatches(rows, familyOfType)
	if len(batches) != 2 {
		t.Fatalf("30 types in one family with BatchSizeTarget=%d should split into 2 batches, got %d", BatchSizeTarget, len(batches))
	}
	if len(batches[0].Types)+len(batches[1].Types) != 30 {
		t.Errorf("batches carry %d+%d types, want 30 total", len(batches[0].Types), len(batches[1].Types))
	}
	for _, b := range batches {
		if len(b.Types) > BatchSizeTarget {
			t.Errorf("batch %d carries %d types, more than BatchSizeTarget %d", b.Number, len(b.Types), BatchSizeTarget)
		}
	}
}

func TestParseProposeCandidatesEmpty(t *testing.T) {
	report := "some header text\n" +
		"\n================================================================\n" +
		"0 logical types proposed this run.\n" +
		"No rule class currently clears the bar.\n"
	got, err := parseProposeCandidates(report)
	if err != nil {
		t.Fatalf("parseProposeCandidates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("parseProposeCandidates on a zero-candidate report = %d candidates, want 0", len(got))
	}
}

func TestParseProposeCandidatesOne(t *testing.T) {
	report := "header\n" +
		"\n================================================================\n" +
		"1 logical type(s) proposed, one rule class's block each:\n" +
		"\n----------------------------------------------------------------\n" +
		"rule class track record: 15/15 (100%) admitted unchanged against internal/live/identity.DefaultTable; not recorded in tools/row-gen/rejected.json\n" +
		"## aws_detective_organization_admin_account -> AWS::Detective::OrganizationAdmin [proposed: client-named]\n" +
		"rule: import-grammar precedence: the guessed argument name is confirmed as a Required top-level argument in the provider's own Argument Reference\n" +
		"registry fields read: primary_identifier=[\"AccountId\"]\n" +
		"\nspot-check: read the provider's Import/Identity-Schema doc cited above, confirm no credential material, paste unedited, then build + test + floci as usual.\n"
	got, err := parseProposeCandidates(report)
	if err != nil {
		t.Fatalf("parseProposeCandidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("parseProposeCandidates = %d candidates, want 1", len(got))
	}
	c, ok := got["aws_detective_organization_admin_account"]
	if !ok {
		t.Fatal("missing aws_detective_organization_admin_account")
	}
	wantRule := "import-grammar precedence: the guessed argument name is confirmed as a Required top-level argument in the provider's own Argument Reference"
	if c.Rule != wantRule {
		t.Errorf("Rule = %q, want %q", c.Rule, wantRule)
	}
	wantTrack := "15/15 (100%) admitted unchanged against internal/live/identity.DefaultTable; not recorded in tools/row-gen/rejected.json"
	if c.TrackRecord != wantTrack {
		t.Errorf("TrackRecord = %q, want %q", c.TrackRecord, wantTrack)
	}
	if c.Block == "" {
		t.Error("Block is empty")
	}
}

func TestParseProposeCandidatesTwo(t *testing.T) {
	report := "header\n" +
		"\n================================================================\n" +
		"2 logical type(s) proposed, one rule class's block each:\n" +
		"\n----------------------------------------------------------------\n" +
		"rule class track record: 10/10 (100%) admitted unchanged\n" +
		"## aws_one -> AWS::One::Thing [proposed: server-assigned]\n" +
		"rule: rule text one\n" +
		"\n----------------------------------------------------------------\n" +
		"rule class track record: 20/20 (100%) admitted unchanged\n" +
		"## aws_two -> AWS::Two::Thing [proposed: client-named]\n" +
		"rule: rule text two\n"
	got, err := parseProposeCandidates(report)
	if err != nil {
		t.Fatalf("parseProposeCandidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("parseProposeCandidates = %d candidates, want 2", len(got))
	}
	if got["aws_one"].Rule != "rule text one" {
		t.Errorf("aws_one Rule = %q", got["aws_one"].Rule)
	}
	if got["aws_two"].Rule != "rule text two" {
		t.Errorf("aws_two Rule = %q", got["aws_two"].Rule)
	}
}
