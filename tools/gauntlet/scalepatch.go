// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
)

// scalepatch.go: issue #1051/#1053's wall-time-accounting follow-up. A
// record's Stages[id].Seconds (this stage's own wall-clock duration) can
// only ever come from two places today: BuildScaleRecordFromLiveCert reading
// LiveCertResult.Seconds, or BuildScaleRecordFromEstate reading
// LastRun.Seconds - both parsed automatically from a committed artifact, by
// `gauntlet scale-backfill`. Neither can recover a number the SOURCE run
// itself never captured: terralith-scale's scale=50 real-AWS row
// (2026-09-11T12:31:25Z) predates LiveCertResult.Seconds existing at all, so
// its cold_deploy/migrate/test_plan stages' true wall durations were never
// committed anywhere BuildScaleRecordFromLiveCert can read them from - only
// this run's own surviving work directory and its GAUNTLET protocol output
// (read once, by a human, before this field existed to keep it) name them.
//
// `gauntlet scale-patch-seconds` is the narrow, generic tool that number is
// allowed to enter this schema through: it still goes via ValidateScaleRecord
// and SaveScaleArtifact like every other write to live/gauntlet-scale.json,
// it only ever sets Stages[id].Seconds on a stage the record already has
// (never invents a stage, a verdict, or a resource count), and it always
// recomputes UnaccountedSeconds from TotalSeconds afterward
// (unaccountedSeconds, scalerecord.go) so a patched record's own arithmetic
// still closes - never a hand-typed line spliced into the committed JSON.
//
// It is also the one entry point that may set AccountingInconsistent
// (issue #1069's own follow-up): a record whose stages are KNOWN to
// overcount its total, not merely unreconciled. `-accounting-inconsistent`
// requires `-note` in the same invocation - the flag alone would let a
// record opt out of the guard without saying why, which is exactly the
// silent-widening this schema exists to refuse (see ScaleRecord.
// AccountingInconsistent's own doc comment).
func cmdScalePatchSeconds(root string, args []string) error {
	fs := flag.NewFlagSet("scale-patch-seconds", flag.ContinueOnError)
	estate := fs.String("estate", "", "the ScaleRecord's estate (required)")
	target := fs.String("target", "", "floci or aws (required)")
	scale := fs.Int("scale", 0, "the ScaleRecord's scale (required, non-zero)")
	note := fs.String("note", "", "replaces UnaccountedDetail with this free text")
	inconsistent := fs.Bool("accounting-inconsistent", false, "mark the record's stage seconds as a KNOWN overcount of its total (requires -note naming the issue); see AccountingInconsistent")
	var stages stageSecondsFlag
	fs.Var(&stages, "stage", "id=seconds - sets Stages[id].Seconds; repeatable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *estate == "" || *target == "" || *scale == 0 {
		return fmt.Errorf("scale-patch-seconds needs -estate, -target and a non-zero -scale")
	}
	if len(stages) == 0 && *note == "" && !*inconsistent {
		return fmt.Errorf("scale-patch-seconds needs at least one -stage id=seconds, a -note, or -accounting-inconsistent")
	}
	if *inconsistent && *note == "" {
		return fmt.Errorf("scale-patch-seconds: -accounting-inconsistent requires -note naming what is inconsistent and which issue tracks it - the flag alone would let a record opt out of the guard silently")
	}

	sa, err := LoadScaleArtifact(root)
	if err != nil {
		return err
	}
	idx := -1
	for i := range sa.Records {
		if sa.Records[i].Estate == *estate && sa.Records[i].Target == *target && sa.Records[i].Scale == *scale {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("scale-patch-seconds: no existing record for estate=%s target=%s scale=%d - this command patches an existing record, it never creates one (use scale-backfill or scale-import-slice for that)", *estate, *target, *scale)
	}
	rec := &sa.Records[idx]
	if rec.TotalSeconds == nil {
		return fmt.Errorf("scale-patch-seconds: estate=%s target=%s scale=%d has no total_seconds to reconcile Stages[*].Seconds against - nothing to patch toward", *estate, *target, *scale)
	}
	for id, secs := range stages {
		st, ok := rec.Stages[id]
		if !ok {
			return fmt.Errorf("scale-patch-seconds: stage %q does not exist on this record - this command sets an existing stage's Seconds, it never invents a stage", id)
		}
		v := secs
		st.Seconds = &v
		rec.Stages[id] = st
	}
	rec.UnaccountedSeconds = unaccountedSeconds(*rec.TotalSeconds, rec.Stages)
	if *note != "" {
		rec.UnaccountedDetail = *note
	}
	if *inconsistent {
		rec.AccountingInconsistent = true
	}
	if err := ValidateScaleRecord(*rec); err != nil {
		return fmt.Errorf("scale-patch-seconds: patched record is invalid: %w", err)
	}
	if err := SaveScaleArtifact(root, sa); err != nil {
		return err
	}
	fmt.Printf("scale-patch-seconds: patched estate=%s target=%s scale=%d, unaccounted_seconds=%.1f, accounting_inconsistent=%v (%s)\n",
		*estate, *target, *scale, *rec.UnaccountedSeconds, rec.AccountingInconsistent, ScaleRecordsPath)
	return nil
}

// stageSecondsFlag implements flag.Value for a repeatable -stage id=seconds
// flag, collecting every occurrence into one map (a later -stage for the
// same id overrides an earlier one in the same invocation, the same way a
// flag package normally treats a repeated flag).
type stageSecondsFlag map[string]float64

func (f *stageSecondsFlag) String() string { return "" }

func (f *stageSecondsFlag) Set(s string) error {
	parts := strings.SplitN(s, "=", 2)
	if len(parts) != 2 || parts[0] == "" {
		return fmt.Errorf("expected id=seconds, got %q", s)
	}
	v, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return fmt.Errorf("%q is not a number: %w", parts[1], err)
	}
	if *f == nil {
		*f = stageSecondsFlag{}
	}
	(*f)[parts[0]] = v
	return nil
}
