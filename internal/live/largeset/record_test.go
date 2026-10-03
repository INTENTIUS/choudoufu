// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package largeset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reading(calls int, summary string, add, change, destroy int) PlanReading {
	return PlanReading{
		Calls: []int{calls, calls}, Seconds: []float64{1.5, 1.4},
		Summaries: []string{summary, summary},
		Add:       add, Change: change, Destroy: destroy,
	}
}

// validBaseline is a publishable measurement: five estates, empty steady
// plans, every bump moving, the outlier differing.
func validBaseline() BaselineRecord {
	rec := BaselineRecord{
		Fixture:   FixtureShape{Estates: 5, Source: SourceLocal, Prefix: "ls", From: VersionA, To: VersionB},
		Condition: ConditionLivePlanAfterOwnApply,
		Substrate: "floci", Emulator: "ghcr.io/lex00/floci@sha256:x", Commit: "abc", Date: "2026-10-02T00:00:00Z",
		Repeats: 2,
	}
	for _, e := range []struct {
		name string
		role Role
	}{{"e01", RoleProducer}, {"e02", RoleMiddle}, {"e03", RoleLeaf}, {"e04", RoleOutlier}, {"e05", RolePlain}} {
		bump := reading(60, "Plan: 1 to add, 3 to change, 0 to destroy.", 1, 3, 0)
		if e.role == RoleOutlier {
			bump = reading(66, "Plan: 1 to add, 4 to change, 0 to destroy.", 1, 4, 0)
		}
		rec.Estates = append(rec.Estates, EstateBaseline{
			Estate: e.name, Role: e.role,
			Steady: reading(40, "No changes.", 0, 0, 0),
			Bump:   bump,
		})
	}
	return rec
}

func TestGateBaselineAcceptsAValidMeasurement(t *testing.T) {
	if err := GateBaseline(validBaseline()); err != nil {
		t.Fatalf("a valid measurement was refused: %s", err)
	}
}

func TestGateBaselineRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*BaselineRecord)
		want   string
	}{
		"no commit":         {func(r *BaselineRecord) { r.Commit = "" }, "no commit"},
		"no emulator":       {func(r *BaselineRecord) { r.Emulator = "" }, "no emulator"},
		"partial set":       {func(r *BaselineRecord) { r.Estates = r.Estates[:4] }, "a partial set is not a baseline"},
		"unknown condition": {func(r *BaselineRecord) { r.Condition = "cache-off" }, "not one this record knows"},
		"steady not empty": {func(r *BaselineRecord) {
			r.Estates[2].Steady = reading(40, "Plan: 1 to add, 0 to change, 0 to destroy.", 1, 0, 0)
		}, "not empty: the apply left it unconverged"},
		"bump moved nothing": {func(r *BaselineRecord) {
			r.Estates[4].Bump = reading(40, "No changes.", 0, 0, 0)
		}, "the bump never reached this one"},
		"bump destroyed": {func(r *BaselineRecord) {
			r.Estates[1].Bump = reading(60, "Plan: 1 to add, 3 to change, 1 to destroy.", 1, 3, 1)
		}, "not the bump"},
		"repeats disagree": {func(r *BaselineRecord) {
			r.Estates[0].Bump.Summaries[1] = "Plan: 2 to add, 3 to change, 0 to destroy."
		}, "changes between identical runs"},
		"calls without seconds": {func(r *BaselineRecord) { r.Estates[0].Bump.Seconds = nil }, "a percentage on calls is not a percentage on time"},
		"proxy saw nothing":     {func(r *BaselineRecord) { r.Estates[3].Bump.Calls[0] = 0 }, "did not go through it"},
		"outlier vanished": {func(r *BaselineRecord) {
			r.Estates[3].Bump = reading(60, "Plan: 1 to add, 3 to change, 0 to destroy.", 1, 3, 0)
		}, "outlier did not show"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := validBaseline()
			tc.mutate(&rec)
			err := GateBaseline(rec)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refused for the wrong reason: %s\nwant it to say %q", err, tc.want)
			}
			path := filepath.Join(t.TempDir(), "rec.json")
			if WriteBaseline(path, rec) == nil {
				t.Error("WriteBaseline wrote a record its gate refuses")
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("a refused record left a file behind")
			}
		})
	}
}

func TestBaselineRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.json")
	if err := WriteBaseline(path, validBaseline()); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != BaselineRecordSchema || len(got.Estates) != 5 || got.Estates[3].Bump.Median() != 66 {
		t.Errorf("round trip lost data: %+v", got)
	}
}
