// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestScaleAuditCallsFromSliceCarriesBothLegs is item 5's first RED-then-GREEN
// proof - "a test that fails when audit_calls is present but a leg is
// missing" - tampered and restored; see this worker's own report for the
// quoted RED output. The numbers are the 79-instance row's own re-measure
// from site/content/docs/model/plan-cost.md ("tagging 1, native 512,
// configuration scan 26, boundary 9, post-sweep 0 - sweep 548, total 696"
// and "the read pass ... 148" against "stock ... 150"): DiscoverCalls is
// tagging+native+config-scan+boundary (1+512+26+9=548), PostSweepCalls is 0,
// so Sweep should read 548; BuildCalls (the read pass) is 148; stock's own
// plan on the same estate is 150.
func TestScaleAuditCallsFromSliceCarriesBothLegs(t *testing.T) {
	row := sliceRowInput{
		Slice:          "s0",
		StateInstances: 79,
		StockPlanCalls: 150,
		Split: &legSplitInput{
			DiscoverCalls:  548,
			PostSweepCalls: 0,
			BuildCalls:     148,
		},
	}

	ac, err := scaleAuditCallsFromSlice(row)
	if err != nil {
		t.Fatalf("scaleAuditCallsFromSlice: %v", err)
	}

	if ac.Sweep == nil {
		t.Fatal("Sweep is nil - the bench measured a sweep leg (DiscoverCalls+PostSweepCalls) and it must land in the record")
	} else if ac.Sweep.Choudoufu != 548 {
		t.Errorf("Sweep.Choudoufu = %d, want 548 (548 discover + 0 post-sweep)", ac.Sweep.Choudoufu)
	}

	if ac.ReadPass == nil {
		t.Fatal("ReadPass is nil - the bench measured a read pass (BuildCalls) and it must land in the record")
	} else {
		if ac.ReadPass.Choudoufu != 148 {
			t.Errorf("ReadPass.Choudoufu = %d, want 148", ac.ReadPass.Choudoufu)
		}
		if ac.ReadPass.Stock == nil || *ac.ReadPass.Stock != 150 {
			t.Errorf("ReadPass.Stock = %v, want 150 (stock's whole plan IS its read pass - it has no sweep)", ac.ReadPass.Stock)
		}
	}

	if ac.Total == nil {
		t.Fatal("Total is nil - both legs were measured, so the whole-audit total must land too")
	} else {
		if ac.Total.Choudoufu != 696 {
			t.Errorf("Total.Choudoufu = %d, want 696 (548 sweep + 148 read pass)", ac.Total.Choudoufu)
		}
		if ac.Total.Stock == nil || *ac.Total.Stock != 150 {
			t.Errorf("Total.Stock = %v, want 150", ac.Total.Stock)
		}
	}
}

// TestScaleAuditCallsFromSliceRefusesMissingSplit proves the negative: a row
// with no leg_split at all (e.g. a hand-edited or malformed SLICE_OUT) is
// refused with an error rather than silently producing a nil or zero-valued
// ScaleAuditCalls - the same "refuse rather than guess" discipline
// ValidateScaleRecord already applies to the rest of this schema.
func TestScaleAuditCallsFromSliceRefusesMissingSplit(t *testing.T) {
	row := sliceRowInput{Slice: "s0", StateInstances: 79, StockPlanCalls: 150}
	if _, err := scaleAuditCallsFromSlice(row); err == nil {
		t.Fatal("scaleAuditCallsFromSlice accepted a row with no leg_split; want an error")
	}
}

// ---------------------------------------------------------------------------
// scalePlanCallsFromRow (issue #1053's correction): plan_calls must come
// from the bench's own labelled CLI plan pair (row.Plans), never from the
// CollectUnclaimed-forced audit split (row.Split). The numbers below are the
// truth stated in this worker's brief, measured independently on the
// emulator at scale 1, 79 resources: stock plan 150 calls, choudoufu plan
// 186, a second warm plan 186 with a byte-identical breakdown, and the
// audit path 719 (row.Split, exercised by the tests above instead).
// ---------------------------------------------------------------------------

// TestScalePlanCallsFromRowCarriesColdAndWarm is the GREEN proof that
// plan_calls is sourced from row.Plans: cold and warm CLI plans, each
// choudoufu's own call count, with stock's plan cost carried beside cold
// only (the bench never takes a second stock plan).
func TestScalePlanCallsFromRowCarriesColdAndWarm(t *testing.T) {
	row := sliceRowInput{
		Slice:          "s0",
		StateInstances: 79,
		StockPlanCalls: 150,
		Plans: []planRunInput{
			{Variant: "default", Pass: "cold", Calls: 186},
			{Variant: "default", Pass: "warm", Calls: 186},
		},
		// A deliberately different audit split, so a reader (and the guard
		// test below) can see that scalePlanCallsFromRow could not have
		// derived its numbers from this field even if it tried.
		Split: &legSplitInput{DiscoverCalls: 588, PostSweepCalls: 0, BuildCalls: 131},
	}

	pc, err := scalePlanCallsFromRow(row)
	if err != nil {
		t.Fatalf("scalePlanCallsFromRow: %v", err)
	}

	if pc.Cold == nil {
		t.Fatal("Cold is nil - the bench ran a default/cold plan and it must land in the record")
	} else {
		if pc.Cold.Choudoufu != 186 {
			t.Errorf("Cold.Choudoufu = %d, want 186 (the CLI plan's own call count, not the audit's sweep+read-pass total)", pc.Cold.Choudoufu)
		}
		if pc.Cold.Stock == nil || *pc.Cold.Stock != 150 {
			t.Errorf("Cold.Stock = %v, want 150 (stock's own plan of the unmigrated state)", pc.Cold.Stock)
		}
	}

	if pc.Warm == nil {
		t.Fatal("Warm is nil - the bench ran a default/warm plan and it must land in the record")
	} else {
		if pc.Warm.Choudoufu != 186 {
			t.Errorf("Warm.Choudoufu = %d, want 186 (byte-identical to cold, per the measured breakdown)", pc.Warm.Choudoufu)
		}
		if pc.Warm.Stock != nil {
			t.Errorf("Warm.Stock = %v, want nil - the bench never takes a second stock plan to compare against", pc.Warm.Stock)
		}
	}
}

// TestScalePlanCallsFromRowRefusesMissingColdPlan proves the negative: a row
// with no default/cold entry in Plans at all is refused rather than
// silently producing a nil or zero-valued PlanCalls.
func TestScalePlanCallsFromRowRefusesMissingColdPlan(t *testing.T) {
	row := sliceRowInput{Slice: "s0", StateInstances: 79, StockPlanCalls: 150}
	if _, err := scalePlanCallsFromRow(row); err == nil {
		t.Fatal("scalePlanCallsFromRow accepted a row with no Plans at all; want an error")
	}
}

// TestScalePlanCallsFromRowRefusesARefusedPlan proves that a plan whose
// ExitCode is nonzero - a REFUSED plan's call count, per the bench's own
// t.Errorf on exactly this condition (slicing_bench_test.go's "%d calls
// recorded here are a REFUSED plan's cost, not a plan's cost") - is refused
// here too, rather than recorded as though it measured something real.
func TestScalePlanCallsFromRowRefusesARefusedPlan(t *testing.T) {
	row := sliceRowInput{
		Slice:          "s0",
		StateInstances: 79,
		StockPlanCalls: 150,
		Plans: []planRunInput{
			{Variant: "default", Pass: "cold", Calls: 42, ExitCode: 1},
		},
	}
	if _, err := scalePlanCallsFromRow(row); err == nil {
		t.Fatal("scalePlanCallsFromRow accepted a plan with a nonzero exit code; want an error")
	}
}

// ---------------------------------------------------------------------------
// The guard: plan_calls must never again be sourced from a CollectUnclaimed
// measurement (issue #1053's own defect, and the reason this file was
// rewritten). This is item 4's RED-then-GREEN proof - see this worker's own
// report for the quoted RED output from deliberately reverting
// scalePlanCallsFromRow to read row.Split instead of row.Plans.
// ---------------------------------------------------------------------------

// TestScalePlanCallsNeverSourcedFromAuditSplit builds one row carrying BOTH
// a row.Plans CLI pair and a row.Split audit measurement with deliberately
// disjoint numbers, then asserts the resulting PlanCalls matches ONLY the
// CLI pair. If scalePlanCallsFromRow (or anything upstream of it) is ever
// changed to read row.Split - the CollectUnclaimed-forced sweep, #1053's
// original defect - this test fails, because 719 (588+131, the audit total)
// is nowhere close to 186 (the CLI plan).
func TestScalePlanCallsNeverSourcedFromAuditSplit(t *testing.T) {
	const (
		cliPlanCalls   = 186
		stockPlanCalls = 150
		auditDiscover  = 588
		auditPostSweep = 0
		auditBuild     = 131 // 588 + 131 = 719, the audit total this brief measured independently
	)

	row := sliceRowInput{
		Slice:          "s0",
		StateInstances: 79,
		StockPlanCalls: stockPlanCalls,
		Plans: []planRunInput{
			{Variant: "default", Pass: "cold", Calls: cliPlanCalls},
			{Variant: "default", Pass: "warm", Calls: cliPlanCalls},
		},
		Split: &legSplitInput{
			DiscoverCalls:  auditDiscover,
			PostSweepCalls: auditPostSweep,
			BuildCalls:     auditBuild,
		},
	}

	pc, err := scalePlanCallsFromRow(row)
	if err != nil {
		t.Fatalf("scalePlanCallsFromRow: %v", err)
	}
	auditTotal := auditDiscover + auditPostSweep + auditBuild
	if pc.Cold == nil {
		t.Fatal("Cold is nil")
	}
	if pc.Cold.Choudoufu == auditTotal {
		t.Fatalf("Cold.Choudoufu = %d, the AUDIT total (%d) - plan_calls has been sourced from the CollectUnclaimed sweep again, issue #1053's own defect", pc.Cold.Choudoufu, auditTotal)
	}
	if pc.Cold.Choudoufu != cliPlanCalls {
		t.Errorf("Cold.Choudoufu = %d, want %d (the CLI plan's own call count)", pc.Cold.Choudoufu, cliPlanCalls)
	}

	// And the audit split, read the correct way (scaleAuditCallsFromSlice),
	// must still be recoverable from the SAME row - this guard is about
	// which field gets which number, not about the audit split becoming
	// unreachable.
	ac, err := scaleAuditCallsFromSlice(row)
	if err != nil {
		t.Fatalf("scaleAuditCallsFromSlice: %v", err)
	}
	if ac.Total == nil || ac.Total.Choudoufu != auditTotal {
		t.Errorf("audit Total.Choudoufu = %v, want %d", ac.Total, auditTotal)
	}
}
