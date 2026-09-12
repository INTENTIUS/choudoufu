// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"errors"
	"strings"
	"testing"
)

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

// TestScaleImportSliceKeepsAuditCallsWhenThePlanWasRefused is the guard for
// the asymmetry refusedPlanError exists to express: a refused plan must never
// become plan_calls, and must not take audit_calls down with it.
//
// The two numbers come from different measurements. plan_calls is the bench's
// own CLI `tofu plan`; audit_calls is measureLegs, run in process afterwards
// with Request.CollectUnclaimed forced true, which does not care whether a
// CLI plan succeeded. Before this, one refused plan discarded both.
//
// terralith-scale at SLICE_SCALE=136 is the run that found it. choudoufu's
// plans were refused by the count-index rule at 10,069 resources - the
// domain check is bounded at 256 and this estate's count is 2*SCALE - while
// the same run measured the sweep and the read pass in full. Those are the
// numbers issue #1051 named this size to collect, and they were nearly
// thrown away because a neighbouring number could not be had.
func TestScaleImportSliceKeepsAuditCallsWhenThePlanWasRefused(t *testing.T) {
	row := sliceRowInput{
		Slice:          "s0",
		StateInstances: 10069,
		StockPlanCalls: 18510,
		Plans: []planRunInput{
			{Variant: "default", Pass: "cold", Calls: 0, ExitCode: 1},
			{Variant: "default", Pass: "warm", Calls: 0, ExitCode: 1},
		},
		Split: &legSplitInput{DiscoverCalls: 7636, PostSweepCalls: 5576, BuildCalls: 18508},
	}

	_, err := scalePlanCallsFromRow(row)
	var refused *refusedPlanError
	if !errors.As(err, &refused) {
		t.Fatalf("scalePlanCallsFromRow error = %v, want a *refusedPlanError so the caller can tell a refused plan from a malformed report", err)
	}
	if refused.ExitCode != 1 {
		t.Errorf("refusedPlanError.ExitCode = %d, want 1", refused.ExitCode)
	}

	audit, err := scaleAuditCallsFromSlice(row)
	if err != nil {
		t.Fatalf("scaleAuditCallsFromSlice returned %v - the audit legs are measured independently of the plan and must survive its refusal", err)
	}
	if audit.Sweep == nil || audit.Sweep.Choudoufu != 7636+5576 {
		t.Errorf("Sweep = %+v, want choudoufu %d", audit.Sweep, 7636+5576)
	}
	if audit.ReadPass == nil || audit.ReadPass.Choudoufu != 18508 {
		t.Errorf("ReadPass = %+v, want choudoufu 18508", audit.ReadPass)
	}
	if audit.ReadPass.Stock == nil || *audit.ReadPass.Stock != 18510 {
		t.Errorf("ReadPass.Stock = %v, want 18510 - stock's whole plan cost IS its read pass", audit.ReadPass.Stock)
	}
	if audit.Total == nil || audit.Total.Choudoufu != 7636+5576+18508 {
		t.Errorf("Total = %+v, want choudoufu %d", audit.Total, 7636+5576+18508)
	}

	// And the provenance must say why plan_calls is missing, so a reader of
	// the artifact never has to guess.
	src := freshSource("the bench at commit abc123", refused)
	if !strings.Contains(src, "plan_calls absent") || !strings.Contains(src, "exited 1") {
		t.Errorf("freshSource = %q, want it to name the refused plan as the reason plan_calls is absent", src)
	}
	if freshSource("measured", nil) != "measured" {
		t.Error("freshSource must not decorate a source when no plan was refused")
	}
}
