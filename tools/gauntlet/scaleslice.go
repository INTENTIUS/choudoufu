// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// scaleslice.go: issue #1053's emulator-side half of #1051's own leftover
// gap - see scalerecord.go's own package comment for the full picture, and
// its ScalePlanCalls/ScaleCallPair doc comments for the schema this file
// populates. internal/live/discovery/slicing_bench_test.go's
// TestSlicingMatrixAgainstFloci already computes exactly the sweep/read-pass
// split (its own legSplit type) that plan-cost.md's 79/301/745 table
// describes in prose, and already writes it out - to SLICE_OUT, a path
// nothing commits. `gauntlet scale-import-slice <path>` is the converter:
// it reads that JSON (a stable, already-existing artifact) and turns ONE
// slice's numbers into a ScaleRecord's PlanCalls field, the same way
// `gauntlet scale-backfill` turns live/gauntlet.json's own rows into
// ScaleRecords - by parsing a committed-or-producible JSON shape, never by
// re-typing a number by hand.
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
// stage evidence that has nothing to do with plan_calls and everything to do
// with why that record exists at all. UpsertScaleRecord's replace-by-identity
// semantics (scalerecord.go) would silently discard all of it the instant
// this file called it with a bare, plan_calls-only record. So: find the
// existing (estate, floci, scale) row if there is one and set only its
// PlanCalls field (Source gains a note of where that ONE field came from,
// since it may be a different commit than the row's own Commit); build a
// fresh, minimal record only when scale-import-slice is the FIRST thing to
// ever measure this (estate, scale) pair on the emulator.
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
	planCalls, err := scalePlanCallsFromSlice(report.Slices[0])
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
			Schema:    ScaleRecordSchema,
			Estate:    *estate,
			Target:    "floci",
			Scale:     report.Scale,
			Commit:    report.Commit,
			Emulator:  report.Emulator,
			Resources: &ScaleResources{Total: report.Slices[0].StateInstances},
			PlanCalls: planCalls,
			Source:    measuredBy,
		}
		if err := ValidateScaleRecord(rec); err != nil {
			return fmt.Errorf("scale-import-slice: built an invalid scale record: %w", err)
		}
		sa.UpsertScaleRecord(rec)
		fmt.Printf("scale-import-slice: created a new record for estate=%s target=floci scale=%d (%s)\n", *estate, report.Scale, ScaleRecordsPath)
	} else {
		existing.PlanCalls = planCalls
		existing.Source = fmt.Sprintf("%s; plan_calls from %s", existing.Source, measuredBy)
		if err := ValidateScaleRecord(*existing); err != nil {
			return fmt.Errorf("scale-import-slice: merging would make the existing record invalid: %w", err)
		}
		fmt.Printf("scale-import-slice: merged plan_calls into the existing record for estate=%s target=floci scale=%d (%s)\n", *estate, report.Scale, ScaleRecordsPath)
	}

	if err := SaveScaleArtifact(root, sa); err != nil {
		return err
	}
	fmt.Printf("scale-import-slice: wrote %s\n", ScaleRecordsPath)
	return nil
}

// sliceReportInput and sliceRowInput mirror ONLY the fields
// slicing_bench_test.go's own slicingReport/sliceRow/legSplit types carry
// that this file needs - they are not, and must not become, the canonical
// definition of SLICE_OUT's shape (that stays slicing_bench_test.go's own
// job; see this file's package comment for why a second copy of the type,
// rather than an import, is the right shape here). An extra field on the
// real report is silently ignored by encoding/json, which is what is wanted:
// this converter reads a subset, and a future field added to the bench's own
// report does not need a matching change here unless this converter starts
// using it too.
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
}

// legSplitInput mirrors only the three legSplit fields whose SUM plan-
// cost.md's own "sweep" and "read pass" columns are built from (see that
// page's "Tagging, native, configuration scan, boundary and post-sweep sum
// to the sweep column above exactly" line): DiscoverCalls is the tagging
// leg, the native leg, the config-driven scan and the boundary pass added
// together (the bench computes each separately for its own report but they
// are all "calls made before Discover returns", which is what DiscoverCalls
// already totals); PostSweepCalls is the remainder of the sweep, made after
// Discover returns; BuildCalls is the read pass (BuildFrom) in full.
type legSplitInput struct {
	DiscoverCalls  int `json:"discover_calls"`
	PostSweepCalls int `json:"post_sweep_calls"`
	BuildCalls     int `json:"build_calls"`
}

// scalePlanCallsFromSlice turns one measured slice into a ScalePlanCalls:
// Sweep is choudoufu's own tagging+native+config-scan+boundary+post-sweep
// total (DiscoverCalls+PostSweepCalls); ReadPass is choudoufu's BuildCalls
// beside stock's own StockPlanCalls, because stock's whole plan cost IS its
// read pass (it has no sweep - see plan-cost.md's "The read pass is the
// number stock pays to read the same resources"); Total is each side's own
// plan cost in full. Sweep.Stock is left absent, not zero: the bench never
// computes "how many sweep calls did stock make" as its own quantity (stock
// has no such phase to measure), so there is nothing instrumented to report
// there, which is exactly the case ScaleCallPair's own doc comment reserves
// Stock's absence for.
func scalePlanCallsFromSlice(row sliceRowInput) (*ScalePlanCalls, error) {
	if row.Split == nil {
		return nil, fmt.Errorf("slice %q carries no leg_split - TestSlicingMatrixAgainstFloci did not run measureLegs for it", row.Slice)
	}
	sweep := row.Split.DiscoverCalls + row.Split.PostSweepCalls
	readPass := row.Split.BuildCalls
	stock := row.StockPlanCalls
	return &ScalePlanCalls{
		Sweep:    &ScaleCallPair{Choudoufu: sweep},
		ReadPass: &ScaleCallPair{Choudoufu: readPass, Stock: &stock},
		Total:    &ScaleCallPair{Choudoufu: sweep + readPass, Stock: &stock},
	}, nil
}
