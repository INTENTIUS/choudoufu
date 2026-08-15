// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// handServerAssigned is internal/live/identity/table.go's own
// serverAssigned(...) cohort, transcribed by hand for this pin. Every one of
// these 17 types must classify server-assigned from the committed registry
// evidence alone - the issue's golden-test requirement.
var handServerAssigned = []string{
	"aws_vpc", "aws_subnet", "aws_security_group", "aws_route_table", "aws_internet_gateway",
	"aws_kms_key", "aws_route53_zone",
	"aws_lb", "aws_lb_target_group", "aws_lb_listener",
	"aws_vpc_security_group_ingress_rule", "aws_vpc_security_group_egress_rule",
	"aws_launch_template", "aws_acm_certificate", "aws_sfn_state_machine", "aws_ebs_volume",
	"aws_eip",
}

// handClientNamedSingle is DefaultTable's single-argument client-named
// cohort (a TypeIdentity with exactly one Components entry built from
// attr(name)), with the argument name attr() reads. table.go also has seven
// composite TypeIdentity entries (aws_sns_topic, aws_iam_role_policy(_attachment),
// aws_route53_record, aws_route, aws_route_table_association,
// aws_lb_target_group_attachment) that are deliberately excluded here: this
// tool's rules classify those from CFN registry evidence, which is a
// different shape than the TF-side composite table.go builds them from (see
// TestKnownComposites_NotSimpleClientNamed below), so they are not part of
// this pin.
var handClientNamedSingle = map[string]string{
	"aws_s3_bucket":               "bucket",
	"aws_iam_role":                "name",
	"aws_cloudwatch_log_group":    "name",
	"aws_ssm_parameter":           "name",
	"aws_dynamodb_table":          "name",
	"aws_ecs_cluster":             "name",
	"aws_cloudwatch_metric_alarm": "alarm_name",
	"aws_kms_alias":               "name",
	"aws_s3_bucket_policy":        "bucket",
}

// TestGolden_HandServerAssignedClassifyServerAssigned pins the issue's
// golden-test requirement: every one of table.go's 17 hand-written
// server-assigned types classifies server-assigned from live/registry.json
// alone.
func TestGolden_HandServerAssignedClassifyServerAssigned(t *testing.T) {
	proposals := loadAllForTest(t)
	byType := indexByType(proposals)

	for _, tf := range handServerAssigned {
		p, ok := byType[tf]
		if !ok {
			t.Errorf("%s: not found in the mapped set at all", tf)
			continue
		}
		if p.Bucket != bucketServerAssigned {
			t.Errorf("%s: bucket = %s, want %s (rule: %s)", tf, p.Bucket, bucketServerAssigned, p.Rule)
		}
	}
}

// TestGolden_HandClientNamedClassifyWithCorrectArgument pins the other half:
// every single-argument client-named type in table.go classifies
// client-named with the same TF argument name table.go's attr() call reads.
func TestGolden_HandClientNamedClassifyWithCorrectArgument(t *testing.T) {
	proposals := loadAllForTest(t)
	byType := indexByType(proposals)

	for tf, wantArg := range handClientNamedSingle {
		p, ok := byType[tf]
		if !ok {
			t.Errorf("%s: not found in the mapped set at all", tf)
			continue
		}
		if p.Bucket != bucketClientNamed {
			t.Errorf("%s: bucket = %s, want %s (rule: %s)", tf, p.Bucket, bucketClientNamed, p.Rule)
			continue
		}
		if p.ArgName != wantArg {
			t.Errorf("%s: argument = %q, want %q (source: %s)", tf, p.ArgName, wantArg, p.ArgSource)
		}
	}
}

// TestGolden_AWSRouteNeedsHandSeparator is the issue's #39 trap, run against
// the real committed live/registry.json: AWS::EC2::Route's primaryIdentifier
// is (RouteTableId, CidrBlock), arity 2, so aws_route must land in
// needs-hand-separator and never become a pastable row - whatever table.go
// itself does with it as a hand-composed parent-derived entry.
func TestGolden_AWSRouteNeedsHandSeparator(t *testing.T) {
	proposals := loadAllForTest(t)
	byType := indexByType(proposals)

	p, ok := byType["aws_route"]
	if !ok {
		t.Fatal("aws_route not found in the mapped set")
	}
	if p.Bucket != bucketNeedsHandSeparator {
		t.Errorf("aws_route bucket = %s, want %s", p.Bucket, bucketNeedsHandSeparator)
	}
	if len(p.PrimaryIdentifier) != 2 {
		t.Errorf("aws_route primaryIdentifier = %v, want arity 2 (RouteTableId, CidrBlock)", p.PrimaryIdentifier)
	}
}

// TestKnownComposites_NotSimpleClientNamed documents, rather than merely
// asserts, that table.go's composite TypeIdentity entries are classified
// from different (CFN-side) evidence than the TF-side shape that put them in
// table.go as composites - so it is not a bug that some of them come out
// server-assigned or fold here. This is what makes registry-only
// classification a check on the hand table rather than a copy of it.
func TestKnownComposites_NotSimpleClientNamed(t *testing.T) {
	proposals := loadAllForTest(t)
	byType := indexByType(proposals)

	composites := []string{
		"aws_sns_topic", "aws_iam_role_policy_attachment", "aws_iam_role_policy",
		"aws_route53_record", "aws_route", "aws_route_table_association",
		"aws_lb_target_group_attachment",
	}
	for _, tf := range composites {
		p, ok := byType[tf]
		if !ok {
			t.Errorf("%s: not found in the mapped set at all", tf)
			continue
		}
		if p.Bucket == bucketClientNamed {
			t.Errorf("%s: classified client-named (arg %q) from the registry; table.go treats it as a multi-component composite, so this pairing needs review, not a silent pass", tf, p.ArgName)
		}
	}

	// Confirm the ones DefaultTable actually has, still exist in the table,
	// so this test would fail loudly (not silently pass) if table.go ever
	// dropped one of them.
	for _, tf := range composites {
		if _, ok := identity.DefaultTable[tf]; !ok {
			t.Errorf("%s: no longer in identity.DefaultTable; update handClientNamedSingle/handServerAssigned/composites instead of leaving a stale entry", tf)
		}
	}
}

// TestSummaryCountsSumToMappedSetSize is the acceptance criterion's
// invariant: the buckets partition the mapped set, so they must sum to
// its size with no double-counting and no type left uncategorized.
func TestSummaryCountsSumToMappedSetSize(t *testing.T) {
	proposals := loadAllForTest(t)
	counts := tally(proposals)
	sum := counts.ServerAssigned + counts.ClientNamed + counts.Composite + counts.Assembled + counts.NeedsHandSeparator + counts.FoldChild + counts.EvidenceOnly
	if sum != len(proposals) {
		t.Errorf("bucket counts sum to %d, want %d (the mapped set size)", sum, len(proposals))
	}
	if counts.ServerAssigned == 0 || counts.ClientNamed == 0 || counts.Composite == 0 || counts.Assembled == 0 || counts.NeedsHandSeparator == 0 || counts.FoldChild == 0 || counts.EvidenceOnly == 0 {
		t.Errorf("expected every bucket to be non-empty over the full mapped set, got %+v", counts)
	}
}

// TestMappedSetIsSorted is the determinism check: loadMapping sorts by
// tf_type, and classifyAll must preserve enough order that repeated runs
// (and hence the golden file) are stable.
//
// classifyAll emits three consecutive runs - the CFN-mapped rows, then the
// fold rows, then the rows live/mapping.json gives no CFN model at all
// (classifyUnmapped) - so sortedness holds within each run rather than
// across the whole slice. Checking the runs separately would be a weakening
// on its own, so this also pins that the three runs are CONTIGUOUS and in a
// fixed order, and that between them they account for every proposal: a row
// escaping its run, or the runs interleaving, fails here rather than
// silently going unchecked.
func TestMappedSetIsSorted(t *testing.T) {
	proposals := loadAllForTest(t)

	group := func(p proposal) string {
		switch {
		case p.NoCFNModel:
			return "no-cfn-model"
		case p.FoldParent != "":
			return "fold"
		default:
			return "mapped"
		}
	}

	runs := map[string][]string{}
	var order []string
	for _, p := range proposals {
		g := group(p)
		runs[g] = append(runs[g], p.TFType)
		if len(order) == 0 || order[len(order)-1] != g {
			order = append(order, g)
		}
	}

	for _, g := range []string{"mapped", "fold", "no-cfn-model"} {
		if len(runs[g]) == 0 {
			t.Errorf("no %s proposals at all; the check would pass vacuously", g)
			continue
		}
		if !sort.StringsAreSorted(runs[g]) {
			t.Errorf("%s proposals are not sorted by TFType", g)
		}
	}

	want := []string{"mapped", "fold", "no-cfn-model"}
	if len(order) != len(want) {
		t.Errorf("classifyAll emitted the runs %v; want each of %v exactly once and contiguous", order, want)
	} else {
		for i := range want {
			if order[i] != want[i] {
				t.Errorf("classifyAll emitted the runs %v, want %v", order, want)
				break
			}
		}
	}

	if total := len(runs["mapped"]) + len(runs["fold"]) + len(runs["no-cfn-model"]); total != len(proposals) {
		t.Errorf("the three runs cover %d proposals, want all %d", total, len(proposals))
	}
}
