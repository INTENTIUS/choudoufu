// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// setplan_record.go is #1752's figure: one live-plan-set of the whole fixture
// against N separate live-plan runs, both of the bump, recorded against the
// baseline record (record.go) and refused when the comparison does not hold.
//
// The gates, each a way this comparison can look fine and not be:
//
//   - The baseline it names is not the baseline it was compared to: a
//     different fixture shape, or a record whose own gate fails.
//   - Any root's set plan differs from that root's separate plan. The
//     figure compares the cost of two ways of producing the same plans; if
//     the plans differ it compares nothing.
//   - Any root's change set differs from the baseline's for the bump: the
//     set and the separate runs could agree with each other and both be
//     wrong about the fixture.
//   - The set did not exit 2 (every root planned, some changes) on every
//     repeat.
//   - An arm without calls, seconds or peak memory on every repeat.
package largeset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// SetPlanRecordSchema is the set-plan record's version.
const SetPlanRecordSchema = 1

// ConditionSetPlanVsSeparate is `choudoufu live-plan-set -json` over every
// root, against `choudoufu live-plan -out` then `show -json` in each root
// one after another, both after the fixture's own apply at A with the module
// regenerated at B in place.
const ConditionSetPlanVsSeparate Condition = "choudoufu-live-plan-set-vs-separate-live-plan-after-own-apply"

// ArmReading is one way of planning the whole fixture, measured Repeats
// times. Every slice is per repeat.
type ArmReading struct {
	// Calls is the AWS API calls the whole arm made through the counting
	// proxy.
	Calls []int `json:"calls"`
	// Seconds is the arm's wall clock: for the separate arm, the sum of its
	// sequential runs.
	Seconds []float64 `json:"seconds"`
	// PeakRSSKB is the highest resident memory the arm's process tree held
	// at once, sampled every 100ms from ps: for the separate arm, the
	// highest over its runs, since they ran one at a time.
	PeakRSSKB []int64 `json:"peak_rss_kb"`
	// PeakProcs is the most processes the sampler saw in the tree at once,
	// so a reader can tell the provider plugins were inside the sample.
	PeakProcs []int `json:"peak_procs"`
}

// SeparateArm is the N-separate-runs arm.
type SeparateArm struct {
	// LivePlan is the live-plan -out runs alone: the work N CI jobs do.
	LivePlan ArmReading `json:"live_plan"`
	// SlowestSeconds is the slowest single root's live-plan per repeat: the
	// wall clock N separate jobs would take if they all ran at once on
	// machines of their own.
	SlowestSeconds []float64 `json:"slowest_seconds"`
	// ShowSeconds is the show -json runs the equivalence check needs, kept
	// out of LivePlan's figure because a separate CI job would not run them.
	ShowSeconds []float64 `json:"show_seconds"`
}

// SetArm is the live-plan-set arm. Its figures include each root's init and
// show -json, which the set always runs.
type SetArm struct {
	ArmReading
	ParallelEstates int   `json:"parallel_estates"`
	ExitCodes       []int `json:"exit_codes"`
}

// SetPlanEstate is one estate's plan, as the set produced it.
type SetPlanEstate struct {
	Estate string `json:"estate"`
	Role   Role   `json:"role"`
	// Equivalent is whether the set's resource_changes for this root were
	// byte-identical, after re-encoding, to the separate run's, on every
	// repeat.
	Equivalent bool     `json:"equivalent"`
	Add        int      `json:"add"`
	Change     int      `json:"change"`
	Destroy    int      `json:"destroy"`
	Changed    []string `json:"changed"`
}

// BaselineRef names the baseline record the comparison was made against.
type BaselineRef struct {
	Path   string `json:"path"`
	Commit string `json:"commit"`
	Date   string `json:"date"`
}

// SetPlanRecord is one measurement of the set plan against separate runs.
type SetPlanRecord struct {
	Schema    int             `json:"schema"`
	Issue     string          `json:"issue"`
	Fixture   FixtureShape    `json:"fixture"`
	Condition Condition       `json:"condition"`
	Substrate string          `json:"substrate"`
	Emulator  string          `json:"emulator"`
	Commit    string          `json:"commit"`
	Date      string          `json:"date"`
	Repeats   int             `json:"repeats"`
	Baseline  BaselineRef     `json:"baseline"`
	Set       SetArm          `json:"set"`
	Separate  SeparateArm     `json:"separate"`
	Estates   []SetPlanEstate `json:"estates"`
}

// GateSetPlan decides whether rec may be written, against the baseline it
// was compared to.
func GateSetPlan(rec SetPlanRecord, base BaselineRecord) error {
	if strings.TrimSpace(rec.Commit) == "" || strings.TrimSpace(rec.Emulator) == "" {
		return fmt.Errorf("no commit or no emulator image: a figure that does not name both cannot be reproduced")
	}
	if rec.Condition != ConditionSetPlanVsSeparate {
		return fmt.Errorf("condition %q is not one this record knows", rec.Condition)
	}
	if err := GateBaseline(base); err != nil {
		return fmt.Errorf("the baseline compared against fails its own gate: %w", err)
	}
	if rec.Fixture != base.Fixture {
		return fmt.Errorf("the set plan measured fixture %+v and the baseline %+v: the comparison is between two different fixtures", rec.Fixture, base.Fixture)
	}
	if rec.Repeats < 1 {
		return fmt.Errorf("repeats is %d", rec.Repeats)
	}
	if err := gateArm(rec.Repeats, rec.Set.ArmReading); err != nil {
		return fmt.Errorf("set arm: %w", err)
	}
	if err := gateArm(rec.Repeats, rec.Separate.LivePlan); err != nil {
		return fmt.Errorf("separate arm: %w", err)
	}
	if len(rec.Separate.SlowestSeconds) != rec.Repeats || len(rec.Separate.ShowSeconds) != rec.Repeats {
		return fmt.Errorf("the separate arm's slowest-root and show readings do not cover every repeat")
	}
	if len(rec.Set.ExitCodes) != rec.Repeats {
		return fmt.Errorf("%d set exit codes for %d repeats", len(rec.Set.ExitCodes), rec.Repeats)
	}
	for i, c := range rec.Set.ExitCodes {
		if c != 2 {
			return fmt.Errorf("set repeat %d exited %d, not 2: either a root failed or the bump moved nothing, and neither is the plan the baseline measured", i+1, c)
		}
	}
	if len(rec.Estates) != len(base.Estates) {
		return fmt.Errorf("the set plan reports %d estates and the baseline %d", len(rec.Estates), len(base.Estates))
	}
	byName := map[string]EstateBaseline{}
	for _, e := range base.Estates {
		byName[e.Estate] = e
	}
	for _, e := range rec.Estates {
		b, ok := byName[e.Estate]
		if !ok {
			return fmt.Errorf("estate %s is not in the baseline", e.Estate)
		}
		if !e.Equivalent {
			return fmt.Errorf("%s: the set's plan differs from the separate live-plan's, so the costs compared are of two different plans", e.Estate)
		}
		if e.Add != b.Bump.Add || e.Change != b.Bump.Change || e.Destroy != b.Bump.Destroy {
			return fmt.Errorf("%s: the set plans %d/%d/%d and the baseline's bump %d/%d/%d", e.Estate, e.Add, e.Change, e.Destroy, b.Bump.Add, b.Bump.Change, b.Bump.Destroy)
		}
		want := append([]string(nil), b.Bump.Changed...)
		sort.Strings(want)
		got := append([]string(nil), e.Changed...)
		sort.Strings(got)
		if !slices.Equal(got, want) {
			return fmt.Errorf("%s: the set changes %v and the baseline's bump %v", e.Estate, got, want)
		}
	}
	return nil
}

func gateArm(repeats int, a ArmReading) error {
	if len(a.Calls) != repeats || len(a.Seconds) != repeats || len(a.PeakRSSKB) != repeats || len(a.PeakProcs) != repeats {
		return fmt.Errorf("%d call, %d second, %d memory and %d process readings for %d repeats", len(a.Calls), len(a.Seconds), len(a.PeakRSSKB), len(a.PeakProcs), repeats)
	}
	for i := range a.Calls {
		if a.Calls[i] <= 0 {
			return fmt.Errorf("repeat %d made %d calls through the proxy: the plans did not go through it", i+1, a.Calls[i])
		}
		if a.PeakRSSKB[i] <= 0 || a.PeakProcs[i] <= 0 {
			return fmt.Errorf("repeat %d sampled no memory: the sampler never saw the process tree", i+1)
		}
	}
	return nil
}

// WriteSetPlan gates rec against base and writes it to path.
func WriteSetPlan(path string, rec SetPlanRecord, base BaselineRecord) error {
	rec.Schema = SetPlanRecordSchema
	rec.Issue = "#1752"
	if err := GateSetPlan(rec, base); err != nil {
		return fmt.Errorf("refusing to write a set-plan record: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ChangeSet is a stock machine-readable plan's resource_changes reduced to
// what the baseline records: totals and the addresses that change. A
// replacement counts once as an add and once as a destroy, as the renderer's
// summary line counts it.
func ChangeSet(plan json.RawMessage) (add, change, destroy int, changed []string, err error) {
	var p struct {
		ResourceChanges []struct {
			Address string `json:"address"`
			Change  struct {
				Actions []string `json:"actions"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(plan, &p); err != nil {
		return 0, 0, 0, nil, err
	}
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		if len(acts) == 0 || (len(acts) == 1 && acts[0] == "no-op") {
			continue
		}
		changed = append(changed, rc.Address)
		for _, a := range acts {
			switch a {
			case "create":
				add++
			case "update":
				change++
			case "delete":
				destroy++
			}
		}
	}
	return add, change, destroy, changed, nil
}
