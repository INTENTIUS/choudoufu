// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestScalePlanCallsFromSliceCarriesBothLegs is item 5's first RED-then-GREEN
// proof - "a test that fails when plan_calls is present but a leg is
// missing" - tampered and restored; see this worker's own report for the
// quoted RED output. The numbers are the 79-instance row's own re-measure
// from site/content/docs/model/plan-cost.md ("tagging 1, native 512,
// configuration scan 26, boundary 9, post-sweep 0 - sweep 548, total 696"
// and "the read pass ... 148" against "stock ... 150"): DiscoverCalls is
// tagging+native+config-scan+boundary (1+512+26+9=548), PostSweepCalls is 0,
// so Sweep should read 548; BuildCalls (the read pass) is 148; stock's own
// plan on the same estate is 150.
func TestScalePlanCallsFromSliceCarriesBothLegs(t *testing.T) {
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

	pc, err := scalePlanCallsFromSlice(row)
	if err != nil {
		t.Fatalf("scalePlanCallsFromSlice: %v", err)
	}

	if pc.Sweep == nil {
		t.Fatal("Sweep is nil - the bench measured a sweep leg (DiscoverCalls+PostSweepCalls) and it must land in the record")
	} else if pc.Sweep.Choudoufu != 548 {
		t.Errorf("Sweep.Choudoufu = %d, want 548 (548 discover + 0 post-sweep)", pc.Sweep.Choudoufu)
	}

	if pc.ReadPass == nil {
		t.Fatal("ReadPass is nil - the bench measured a read pass (BuildCalls) and it must land in the record")
	} else {
		if pc.ReadPass.Choudoufu != 148 {
			t.Errorf("ReadPass.Choudoufu = %d, want 148", pc.ReadPass.Choudoufu)
		}
		if pc.ReadPass.Stock == nil || *pc.ReadPass.Stock != 150 {
			t.Errorf("ReadPass.Stock = %v, want 150 (stock's whole plan IS its read pass - it has no sweep)", pc.ReadPass.Stock)
		}
	}

	if pc.Total == nil {
		t.Fatal("Total is nil - both legs were measured, so the whole-plan total must land too")
	} else {
		if pc.Total.Choudoufu != 696 {
			t.Errorf("Total.Choudoufu = %d, want 696 (548 sweep + 148 read pass)", pc.Total.Choudoufu)
		}
		if pc.Total.Stock == nil || *pc.Total.Stock != 150 {
			t.Errorf("Total.Stock = %v, want 150", pc.Total.Stock)
		}
	}
}

// TestScalePlanCallsFromSliceRefusesMissingSplit proves the negative: a row
// with no leg_split at all (e.g. a hand-edited or malformed SLICE_OUT) is
// refused with an error rather than silently producing a nil or zero-valued
// PlanCalls - the same "refuse rather than guess" discipline
// ValidateScaleRecord already applies to the rest of this schema.
func TestScalePlanCallsFromSliceRefusesMissingSplit(t *testing.T) {
	row := sliceRowInput{Slice: "s0", StateInstances: 79, StockPlanCalls: 150}
	if _, err := scalePlanCallsFromSlice(row); err == nil {
		t.Fatal("scalePlanCallsFromSlice accepted a row with no leg_split; want an error")
	}
}
