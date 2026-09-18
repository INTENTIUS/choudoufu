// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// Issue #1143. live/live-cert/terralith-scale.sh's index_wait used to record
// exactly one number about itself, index_lag_s, and that number could not
// answer the only question a reader has: did the index catch up, or did the
// bound trip? Every real-AWS row on record reads index_lag_s=<the bound>,
// and every one of them tripped the bound BY CONSTRUCTION - the wait polled
// for all 33*SCALE+5 stamped objects, of which 11*SCALE are IAM roles
// resourcegroupstaggingapi never returns in any region (#1134). So the field
// looked like a measurement of index lag and was a measurement of nothing.
//
// index_converged= and index_target= are what make it a measurement. These
// tests are about what the PARSER does with them, one shape per case,
// including the two words that deliberately read as absent.

func TestParseIndexWaitDetailConverged(t *testing.T) {
	// The pass-path detail terralith-scale.sh writes, trimmed to the tokens.
	const detail = "post-migrate plan is empty in 412s; index_lag_s=63 index_converged=yes index_target=104 seconds=412 throttle=0 retry=0"
	converged, target := parseIndexWaitDetail(detail)
	if converged == nil {
		t.Fatal("converged = nil, want true")
	} else if !*converged {
		t.Errorf("converged = %v, want true", *converged)
	}
	if target == nil || *target != 104 {
		t.Errorf("target = %v, want 104 - the reachable ceiling at scale 50 in us-east-2", target)
	}
}

func TestParseIndexWaitDetailNotConverged(t *testing.T) {
	// A reachable target that was not reached: a genuine index lag, and the
	// one case where index_lag_s=<bound> means something.
	const detail = "the post-migrate plan exited 1 with 3 error(s), Rule: direct-read-unresolved index_lag_s=1800 index_converged=no index_target=1105 seconds=900"
	converged, target := parseIndexWaitDetail(detail)
	if converged == nil {
		t.Fatal("converged = nil, want false")
	} else if *converged {
		t.Errorf("converged = %v, want false", *converged)
	}
	if target == nil || *target != 1105 {
		t.Errorf("target = %v, want 1105", target)
	}
}

func TestParseIndexWaitDetailNonVerdictsReadAsAbsent(t *testing.T) {
	// "skipped" (not a real-AWS run) and "na" (no reachable target at all)
	// both mean the wait never ran. A record must not carry a verdict for a
	// step that did not execute - absent is the honest shape, the same one
	// a pre-#1143 row with no token at all produces. An unrecognized word
	// reads absent too, rather than being guessed into a boolean.
	for _, word := range []string{"skipped", "na", "maybe"} {
		detail := "post-migrate plan is empty in 9s; index_lag_s=0 index_converged=" + word + " index_target=0 seconds=9"
		converged, target := parseIndexWaitDetail(detail)
		if converged != nil {
			t.Errorf("index_converged=%s: converged = %v, want nil - the wait did not run, so there is no verdict to record", word, *converged)
		}
		// index_target is a plain number and is recorded whenever present,
		// even as 0: "polled to a target of 0" is itself readable, and it is
		// what the na case means.
		if target == nil || *target != 0 {
			t.Errorf("index_converged=%s: target = %v, want 0", word, target)
		}
	}
}

func TestParseIndexWaitDetailAbsentTokens(t *testing.T) {
	// Every row recorded before #1143. Nothing invented for them: their
	// index_lag_s still parses (a different function), but nothing claims
	// they converged.
	const detail = "post-migrate plan is empty in 412s; debug log 88 bytes, 0 throttling-error line(s), 0 retry line(s); index_lag_s=3600 seconds=412 throttle=0 retry=0"
	converged, target := parseIndexWaitDetail(detail)
	if converged != nil {
		t.Errorf("converged = %v, want nil for a pre-#1143 row", *converged)
	}
	if target != nil {
		t.Errorf("target = %v, want nil for a pre-#1143 row", *target)
	}
}

func TestBuildScaleRecordCarriesIndexWaitVerdict(t *testing.T) {
	r := LiveCertResult{
		Estate: "terralith-scale",
		Target: "aws",
		Clear:  true,
		Stages: map[string]string{"test_plan": "pass"},
		Detail: map[string]string{
			"test_plan": "post-migrate plan is empty in 412s; index_lag_s=63 index_converged=yes index_target=104 seconds=412 throttle=0 retry=0",
		},
	}
	rec := BuildScaleRecordFromLiveCert(r, "")
	if rec.IndexLagS == nil || *rec.IndexLagS != 63 {
		t.Errorf("IndexLagS = %v, want 63", rec.IndexLagS)
	}
	if rec.IndexConverged == nil {
		t.Error("IndexConverged = nil, want true")
	} else if !*rec.IndexConverged {
		t.Errorf("IndexConverged = %v, want true", *rec.IndexConverged)
	}
	if rec.IndexTargetN == nil || *rec.IndexTargetN != 104 {
		t.Errorf("IndexTargetN = %v, want 104", rec.IndexTargetN)
	}
}

// TestParseIndexWaitDetailAlongsideItsProse uses the WHOLE detail sentence
// terralith-scale.sh writes, prose clause and all. The tokens sit beside a
// human-readable "tag index did NOT converge: 12 of a reachable 104 after
// 1800s" clause that itself contains digits and the word "after", so this
// asserts the token patterns are anchored on their own keys and are not
// reading numbers out of the sentence around them. Everything else on the
// line - seconds, throttle, retry - must still come back unchanged.
func TestParseIndexWaitDetailAlongsideItsProse(t *testing.T) {
	const detail = "post-migrate plan is empty in 412s; zone/role tofu-address confirmed via the AWS CLI; " +
		"debug log 88 bytes, 0 throttling-error line(s), 2 retry line(s); " +
		"tag index did NOT converge: 12 of a reachable 104 after 1800s; " +
		"index_lag_s=1800 index_converged=no index_target=104 seconds=412 throttle=0 retry=2"

	converged, target := parseIndexWaitDetail(detail)
	if converged == nil {
		t.Fatal("converged = nil, want false")
	} else if *converged {
		t.Errorf("converged = %v, want false", *converged)
	}
	if target == nil || *target != 104 {
		t.Errorf("target = %v, want 104 - not 12, and not 1800, both of which appear in the prose", target)
	}

	seconds, throttle, retry, indexLag := parseTestPlanDetail(detail)
	if seconds == nil || *seconds != 412 {
		t.Errorf("seconds = %v, want 412", seconds)
	}
	if throttle == nil || *throttle != 0 {
		t.Errorf("throttle = %v, want 0", throttle)
	}
	if retry == nil || *retry != 2 {
		t.Errorf("retry = %v, want 2", retry)
	}
	if indexLag == nil || *indexLag != 1800 {
		t.Errorf("indexLag = %v, want 1800", indexLag)
	}
}
