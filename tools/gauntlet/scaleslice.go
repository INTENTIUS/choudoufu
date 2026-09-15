// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
)

// scaleslice.go: issue #1053's emulator-side half of #1051's own leftover
// gap - see scalerecord.go's own package comment for the full picture, and
// its ScalePlanCalls/ScaleAuditCalls/ScaleCallPair doc comments for the
// schema this file populates. internal/live/discovery/slicing_bench_test.go's
// TestSlicingMatrixAgainstFloci already measures both numbers this file
// needs and already writes them out - to SLICE_OUT, a path nothing commits.
// `gauntlet scale-import-slice <path>` is the converter: it reads that JSON
// (a stable, already-existing artifact) and turns ONE slice's numbers into a
// ScaleRecord's PlanCalls and AuditCalls fields, the same way `gauntlet
// scale-backfill` turns live/gauntlet.json's own rows into ScaleRecords - by
// parsing a committed-or-producible JSON shape, never by re-typing a number
// by hand.
//
// PlanCalls comes from row.Plans, the bench's own labelled cold/warm CLI
// `tofu plan` pair (variant "default" - the bench also runs a
// SLICE_GUIDED_MATRIX set of variants nothing here reads, because those
// exist to compare discovery MODES against each other, not to answer "what
// does a plan cost"). AuditCalls comes from row.Split, the bench's
// in-process three-leg measurement taken with Request.CollectUnclaimed
// forced true (measureLegs, slicing_bench_test.go) - the account-inventory
// sweep, not a plan. An earlier version of this file (before #1053's
// correction) fed row.Split into what was then the ONLY field,
// ScaleRecord.PlanCalls, which is exactly the mistake that let chant-bench
// publish an audit's call count as though it were choudoufu's plan cost.
// The two sources must never be swapped again - see
// TestScalePlanCallsNeverSourcedFromAuditSplit in scaleslice_test.go, the
// guard this file's own correction added for that reason.
//
// Why a converter over the bench writing a ScaleRecord directly: the bench
// lives in package discovery (internal/live/discovery), a test file with no
// business importing package main (tools/gauntlet, where ScaleRecord and its
// Upsert/Save machinery live) - and even if the import direction were
// reversed, a test binary is not where this repository's convention puts
// artifact-writing code (scale-backfill and gauntlet live-cert both do it
// from tools/gauntlet, never from inside a _test.go file). SLICE_OUT is
// already a stable, versioned-by-this-package-comment JSON shape (slicingReport
// in slicing_bench_test.go); mirroring the handful of fields this file
// actually needs, by JSON tag, is the same "read a committed JSON shape you
// don't own the Go type of" move scale-backfill already makes reading
// live/gauntlet.json's Artifact at an old revision via `git show`.
//
// Why MERGE rather than upsert-and-replace: this estate (terralith-scale,
// target=floci) already has a scale=1 ScaleRecord in live/gauntlet-scale.json,
// built by `gauntlet run terralith-scale` from that estate's OWN crossing
// script (live/e2e/terralith-scale/run.sh, a completely different program
// from the slicing bench) - it carries day2_count/day2_remove/greenfield/etc.
// stage evidence that has nothing to do with plan_calls/audit_calls and
// everything to do with why that record exists at all. UpsertScaleRecord's
// replace-by-identity semantics (scalerecord.go) would silently discard all
// of it the instant this file called it with a bare, plan-cost-only record.
// So: find the existing (estate, floci, scale) row if there is one and set
// only its PlanCalls/AuditCalls fields (Source gains a note of where those
// came from, since it may be a different commit than the row's own Commit);
// build a fresh, minimal record only when scale-import-slice is the FIRST
// thing to ever measure this (estate, scale) pair on the emulator.
func cmdScaleImportSlice(root string, args []string) error {
	fs := flag.NewFlagSet("scale-import-slice", flag.ContinueOnError)
	estate := fs.String("estate", "terralith-scale", "the ScaleRecord estate this slice report's numbers belong to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("scale-import-slice needs exactly one path to a SLICE_OUT report, got %d", fs.NArg())
	}
	path := fs.Arg(0)

	b, err := os.ReadFile(path) //nolint:gosec // an operator-supplied path, the whole point of this subcommand
	if err != nil {
		return fmt.Errorf("scale-import-slice: reading %s: %w", path, err)
	}
	var report sliceReportInput
	if err := json.Unmarshal(b, &report); err != nil {
		return fmt.Errorf("scale-import-slice: parsing %s: %w", path, err)
	}
	if len(report.Slices) != 1 {
		return fmt.Errorf("scale-import-slice: %s carries %d slice(s); only a k=1 (SLICE_K=1, the default - a single, unpartitioned estate) report becomes one ScaleRecord, because a partitioned run's slices each measure a FRACTION of the estate's cost, not the estate's own plan cost", path, len(report.Slices))
	}
	planCalls, err := scalePlanCallsFromRow(report.Slices[0])
	var refused *refusedPlanError
	switch {
	case errors.As(err, &refused):
		// Loud, and it proceeds: audit_calls is still a real measurement.
		// See refusedPlanError's own doc comment. plan_calls stays nil and
		// is never written from a refused plan's count.
		fmt.Printf("scale-import-slice: %v\n", refused)
		fmt.Println("scale-import-slice: importing audit_calls only; plan_calls is left as it was, never derived from a refused plan")
		planCalls = nil
	case err != nil:
		return fmt.Errorf("scale-import-slice: %w", err)
	}
	auditCalls, err := scaleAuditCallsFromSlice(report.Slices[0])
	if err != nil {
		return fmt.Errorf("scale-import-slice: %w", err)
	}

	sa, err := LoadScaleArtifact(root)
	if err != nil {
		return err
	}
	measuredBy := fmt.Sprintf("internal/live/discovery/slicing_bench_test.go (TestSlicingMatrixAgainstFloci) at commit %s", report.Commit)

	var existing *ScaleRecord
	for i := range sa.Records {
		if sa.Records[i].Estate == *estate && sa.Records[i].Target == "floci" && sa.Records[i].Scale == report.Scale {
			existing = &sa.Records[i]
			break
		}
	}
	if existing == nil {
		rec := ScaleRecord{
			Schema:           ScaleRecordSchema,
			Estate:           *estate,
			Target:           "floci",
			Scale:            report.Scale,
			Commit:           report.Commit,
			Emulator:         report.Emulator,
			Resources:        &ScaleResources{Total: report.Slices[0].StateInstances},
			PlanCalls:        planCalls,
			AuditCalls:       auditCalls,
			Source:           measuredBy,
			CallCountsSource: freshSource(measuredBy, refused),
		}
		if err := ValidateScaleRecord(rec); err != nil {
			return fmt.Errorf("scale-import-slice: built an invalid scale record: %w", err)
		}
		sa.UpsertScaleRecord(rec)
		fmt.Printf("scale-import-slice: created a new record for estate=%s target=floci scale=%d (%s)\n", *estate, report.Scale, ScaleRecordsPath)
	} else {
		// A nil planCalls means this report's plan was refused. Leaving
		// the field alone is the point: it must never be overwritten with
		// nothing, and a value an earlier, successful bench measured is
		// still true of that run.
		what := "plan_calls and audit_calls"
		if planCalls != nil {
			existing.PlanCalls = planCalls
		} else {
			what = fmt.Sprintf("audit_calls (plan_calls absent: %v)", refused)
		}
		existing.AuditCalls = auditCalls
		// Its own field, not appended to Source: Source names the run this
		// row describes, and a later scale-backfill legitimately rewrites
		// it. See ScaleRecord.CallCountsSource.
		existing.CallCountsSource = fmt.Sprintf("%s from %s", what, measuredBy)
		if err := ValidateScaleRecord(*existing); err != nil {
			return fmt.Errorf("scale-import-slice: merging would make the existing record invalid: %w", err)
		}
		fmt.Printf("scale-import-slice: merged %s into the existing record for estate=%s target=floci scale=%d (%s)\n", what, *estate, report.Scale, ScaleRecordsPath)
	}

	if err := SaveScaleArtifact(root, sa); err != nil {
		return err
	}
	fmt.Printf("scale-import-slice: wrote %s\n", ScaleRecordsPath)
	return nil
}

// sliceReportInput, sliceRowInput, planRunInput and legSplitInput mirror
// ONLY the fields slicing_bench_test.go's own slicingReport/sliceRow/
// planRun/legSplit types carry that this file needs - they are not, and
// must not become, the canonical definition of SLICE_OUT's shape (that
// stays slicing_bench_test.go's own job; see this file's package comment
// for why a second copy of the type, rather than an import, is the right
// shape here). An extra field on the real report is silently ignored by
// encoding/json, which is what is wanted: this converter reads a subset,
// and a future field added to the bench's own report does not need a
// matching change here unless this converter starts using it too.
type sliceReportInput struct {
	Scale    int             `json:"scale"`
	Commit   string          `json:"commit"`
	Emulator string          `json:"emulator"`
	Slices   []sliceRowInput `json:"slices"`
}

type sliceRowInput struct {
	Slice          string         `json:"slice"`
	StateInstances int            `json:"state_instances"`
	StockPlanCalls int            `json:"stock_plan_calls"`
	Split          *legSplitInput `json:"leg_split"`
	Plans          []planRunInput `json:"plans"`
}

// planRunInput mirrors one entry of sliceRow.Plans (planRun in
// slicing_bench_test.go): one labelled CLI `tofu plan` the bench actually
// ran against the migrated estate. Variant distinguishes the
// SLICE_GUIDED_MATRIX discovery-mode comparisons ("guided-off",
// "cloudcontrol-off", ...) from the one this file wants, "default" - the
// bench's own default variant is always run, the others only under
// SLICE_GUIDED_MATRIX. Pass is "cold" (first plan of that variant) or
// "warm" (a second, back-to-back plan with nothing changed in between).
type planRunInput struct {
	Variant  string `json:"variant"`
	Pass     string `json:"pass"`
	Calls    int    `json:"calls"`
	ExitCode int    `json:"exit_code"`
}

// legSplitInput mirrors only the three legSplit fields whose SUM plan-
// cost.md's own "sweep" and "read pass" columns are built from (see that
// page's "Tagging, native, configuration scan, boundary and post-sweep sum
// to the sweep column above exactly" line): DiscoverCalls is the tagging
// leg, the native leg, the config-driven scan and the boundary pass added
// together (the bench computes each separately for its own report but they
// are all "calls made before Discover returns", which is what DiscoverCalls
// already totals); PostSweepCalls is the remainder of the sweep, made after
// Discover returns; BuildCalls is the read pass (BuildFrom) in full. This is
// [measureLegsWith]'s output with CollectUnclaimed forced true - the
// account-inventory audit, never a plan (see AuditCalls's own doc comment
// in scalerecord.go).
type legSplitInput struct {
	DiscoverCalls  int `json:"discover_calls"`
	PostSweepCalls int `json:"post_sweep_calls"`
	BuildCalls     int `json:"build_calls"`
}

// scalePlanCallsFromRow turns one measured slice's row.Plans into a
// ScalePlanCalls - the CLI's own cold and warm plan of the migrated estate,
// which is what a plan actually costs. It never reads row.Split: that field
// is the CollectUnclaimed-forced account-inventory sweep (see
// scaleAuditCallsFromSlice), and mixing the two is the exact defect #1053
// corrects (see this file's own package comment). Only the "default"
// variant is read - the SLICE_GUIDED_MATRIX comparison variants answer a
// different question (which discovery mode) and are not the plan this
// field means. Cold carries stock's own plan of the identical, unmigrated
// state beside it (row.StockPlanCalls, measured once, before migrate);
// Warm.Stock is always left absent, not zero, because the bench never
// takes a second stock plan to compare it against. A plan whose ExitCode
// is nonzero is a REFUSED plan's cost, not a plan's cost (the bench's own
// t.Errorf on this exact condition says so), so it is refused here too
// rather than silently recorded as though it were a real measurement.
// refusedPlanError is what scalePlanCallsFromRow returns when the bench's
// own CLI plan exited non-zero. It is a distinct type rather than a plain
// error because the two things this file imports have different fates when
// that happens, and collapsing them loses a real measurement.
//
// A refused plan's call count is how far the plan got before giving up, not
// what a plan costs, so plan_calls MUST NOT be written from it - that
// refusal is absolute and is what TestScalePlanCallsFromRowRefusesARefusedPlan
// pins. audit_calls is a different measurement entirely: measureLegs runs
// in process with Request.CollectUnclaimed forced true, after the plan, and
// does not care whether a CLI plan succeeded. Refusing to import it because
// a neighbouring number is unavailable would discard a real account
// inventory for no reason.
//
// terralith-scale at SLICE_SCALE=136 is why this exists. Its plans were
// refused by the count-index rule at 10,069 resources, while the same run
// measured the sweep and read pass in full - the numbers issue #1051 named
// this size to collect.
type refusedPlanError struct {
	Slice    string
	Variant  string
	Pass     string
	ExitCode int
}

func (e *refusedPlanError) Error() string {
	return fmt.Sprintf("slice %q plan[%s/%s] exited %d - a refused plan's call count is not a plan's cost, and cannot become plan_calls", e.Slice, e.Variant, e.Pass, e.ExitCode)
}

func scalePlanCallsFromRow(row sliceRowInput) (*ScalePlanCalls, error) {
	var cold, warm *ScaleCallPair
	for _, p := range row.Plans {
		if p.Variant != "default" {
			continue
		}
		if p.ExitCode != 0 {
			return nil, &refusedPlanError{Slice: row.Slice, Variant: p.Variant, Pass: p.Pass, ExitCode: p.ExitCode}
		}
		switch p.Pass {
		case "cold":
			if cold != nil {
				return nil, fmt.Errorf("slice %q carries more than one default/cold plan in row.Plans", row.Slice)
			}
			stock := row.StockPlanCalls
			cold = &ScaleCallPair{Choudoufu: p.Calls, Stock: &stock}
		case "warm":
			if warm != nil {
				return nil, fmt.Errorf("slice %q carries more than one default/warm plan in row.Plans", row.Slice)
			}
			warm = &ScaleCallPair{Choudoufu: p.Calls}
		}
	}
	if cold == nil {
		return nil, fmt.Errorf("slice %q carries no default/cold entry in row.Plans - TestSlicingMatrixAgainstFloci did not run a CLI plan for it (SLICE_PLAN_PASSES=1 drops warm but never cold)", row.Slice)
	}
	return &ScalePlanCalls{Cold: cold, Warm: warm}, nil
}

// scaleAuditCallsFromSlice turns one measured slice's row.Split (measureLegs,
// run with Request.CollectUnclaimed:true - the account inventory, #584's own
// matrix) into a ScaleAuditCalls: Sweep is choudoufu's own
// tagging+native+config-scan+boundary+post-sweep total
// (DiscoverCalls+PostSweepCalls); ReadPass is choudoufu's BuildCalls beside
// stock's own StockPlanCalls, because stock's whole plan cost IS its read
// pass (it has no sweep - see plan-cost.md's "The read pass is the number
// stock pays to read the same resources"); Total is each side's own cost in
// full. Sweep.Stock is left absent, not zero: the bench never computes "how
// many sweep calls did stock make" as its own quantity (stock has no such
// phase to measure), so there is nothing instrumented to report there,
// which is exactly the case ScaleCallPair's own doc comment reserves
// Stock's absence for. This is NOT a plan's cost - see this field's own
// ScaleAuditCalls doc comment in scalerecord.go for why CollectUnclaimed
// forces the full admission table regardless of narrowing or cache state.
func scaleAuditCallsFromSlice(row sliceRowInput) (*ScaleAuditCalls, error) {
	if row.Split == nil {
		return nil, fmt.Errorf("slice %q carries no leg_split - TestSlicingMatrixAgainstFloci did not run measureLegs for it", row.Slice)
	}
	sweep := row.Split.DiscoverCalls + row.Split.PostSweepCalls
	readPass := row.Split.BuildCalls
	stock := row.StockPlanCalls
	return &ScaleAuditCalls{
		Sweep:    &ScaleCallPair{Choudoufu: sweep},
		ReadPass: &ScaleCallPair{Choudoufu: readPass, Stock: &stock},
		Total:    &ScaleCallPair{Choudoufu: sweep + readPass, Stock: &stock},
	}, nil
}

// freshSource names a refused plan in a brand-new record's own provenance,
// so a reader of live/gauntlet-scale.json never has to wonder why
// plan_calls is missing from a row that carries audit_calls.
func freshSource(measuredBy string, refused *refusedPlanError) string {
	if refused == nil {
		return measuredBy
	}
	return fmt.Sprintf("%s; plan_calls absent: %v", measuredBy, refused)
}
