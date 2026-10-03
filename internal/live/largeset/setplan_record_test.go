// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package largeset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func arm() ArmReading {
	return ArmReading{Calls: []int{300, 300}, Seconds: []float64{20, 21}, PeakRSSKB: []int64{900000, 910000}, PeakProcs: []int{12, 12}}
}

// validSetPlan is a publishable comparison against validBaseline.
func validSetPlan() (SetPlanRecord, BaselineRecord) {
	base := validBaseline()
	rec := SetPlanRecord{
		Fixture:   base.Fixture,
		Condition: ConditionSetPlanVsSeparate,
		Substrate: "floci", Emulator: "ghcr.io/lex00/floci@sha256:x", Commit: "def", Date: "2026-10-03T00:00:00Z",
		Repeats:  2,
		Baseline: BaselineRef{Path: "live/large-set/baseline-n5.json", Commit: base.Commit, Date: base.Date},
		Set:      SetArm{ArmReading: arm(), ParallelEstates: 5, ExitCodes: []int{2, 2}},
		Separate: SeparateArm{LivePlan: arm(), SlowestSeconds: []float64{5, 5}, ShowSeconds: []float64{3, 3}},
	}
	for _, e := range base.Estates {
		changed := []string{"module.shared.a", "module.shared.b", "module.shared.c", "module.shared.d"}
		if e.Role == RoleOutlier {
			changed = append(changed, "module.shared.e")
		}
		rec.Estates = append(rec.Estates, SetPlanEstate{Estate: e.Estate, Role: e.Role, Equivalent: true, Add: e.Bump.Add, Change: e.Bump.Change, Changed: changed})
	}
	for i := range base.Estates {
		base.Estates[i].Bump.Changed = append([]string(nil), rec.Estates[i].Changed...)
	}
	return rec, base
}

func TestGateSetPlanAcceptsAValidComparison(t *testing.T) {
	rec, base := validSetPlan()
	if err := GateSetPlan(rec, base); err != nil {
		t.Fatalf("a valid comparison was refused: %s", err)
	}
}

func TestGateSetPlanRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*SetPlanRecord, *BaselineRecord)
		want   string
	}{
		"no commit":           {func(r *SetPlanRecord, _ *BaselineRecord) { r.Commit = "" }, "no commit"},
		"another condition":   {func(r *SetPlanRecord, _ *BaselineRecord) { r.Condition = ConditionLivePlanAfterOwnApply }, "condition"},
		"baseline fails gate": {func(_ *SetPlanRecord, b *BaselineRecord) { b.Commit = "" }, "fails its own gate"},
		"another fixture":     {func(r *SetPlanRecord, _ *BaselineRecord) { r.Fixture.Prefix = "zz" }, "two different fixtures"},
		"not equivalent":      {func(r *SetPlanRecord, _ *BaselineRecord) { r.Estates[2].Equivalent = false }, "differs from the separate live-plan"},
		"totals off baseline": {func(r *SetPlanRecord, _ *BaselineRecord) { r.Estates[0].Change = 2 }, "the baseline's bump"},
		"addresses off":       {func(r *SetPlanRecord, _ *BaselineRecord) { r.Estates[1].Changed[0] = "module.shared.z" }, "the baseline's bump"},
		"a root failed":       {func(r *SetPlanRecord, _ *BaselineRecord) { r.Set.ExitCodes[1] = 4 }, "exited 4"},
		"set missing memory":  {func(r *SetPlanRecord, _ *BaselineRecord) { r.Set.PeakRSSKB = r.Set.PeakRSSKB[:1] }, "memory"},
		"separate no calls":   {func(r *SetPlanRecord, _ *BaselineRecord) { r.Separate.LivePlan.Calls[0] = 0 }, "did not go through"},
		"sampler saw nothing": {func(r *SetPlanRecord, _ *BaselineRecord) { r.Set.PeakProcs[0] = 0 }, "sampled no memory"},
		"partial set":         {func(r *SetPlanRecord, _ *BaselineRecord) { r.Estates = r.Estates[:4] }, "reports 4 estates"},
	} {
		t.Run(name, func(t *testing.T) {
			rec, base := validSetPlan()
			tc.mutate(&rec, &base)
			err := GateSetPlan(rec, base)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("gate said %v, want a refusal containing %q", err, tc.want)
			}
			path := filepath.Join(t.TempDir(), "r.json")
			if WriteSetPlan(path, rec, base) == nil {
				t.Error("WriteSetPlan wrote a record its gate refuses")
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("a refused record is on disk")
			}
		})
	}
}

func TestChangeSet(t *testing.T) {
	plan := json.RawMessage(`{"resource_changes":[
		{"address":"a","change":{"actions":["no-op"]}},
		{"address":"b","change":{"actions":["update"]}},
		{"address":"c","change":{"actions":["create"]}},
		{"address":"d","change":{"actions":["delete","create"]}},
		{"address":"data.x","change":{"actions":["read"]}}]}`)
	add, change, destroy, changed, err := ChangeSet(plan)
	if err != nil {
		t.Fatal(err)
	}
	if add != 2 || change != 1 || destroy != 1 || strings.Join(changed, ",") != "b,c,d,data.x" {
		t.Errorf("ChangeSet = %d/%d/%d %v", add, change, destroy, changed)
	}
}
