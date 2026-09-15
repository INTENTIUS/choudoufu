// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
)

// stageprovenance.go: the one-shot migration for issue #1069's per-stage
// provenance (EstateResult.StageRuns, artifact.go).
//
// Every row committed before that field existed carries one commit and one
// date for the whole row, so the artifact cannot say which of its fourteen
// verdicts the recorded run actually produced. This command fills that in
// as far as the committed artifact honestly supports, and no further. It is
// code, re-runnable and idempotent, because live/gauntlet.json is a
// measured artifact and is never hand-edited (HANDOFF.md).
//
// What it can recover, and how:
//
// A gauntlet-protocol row's last_run.stage_seconds holds exactly the stages
// that run's own script emitted a duration_s for, and since 9525811174 it
// is no longer merged across runs - it is this run's alone. So on a row
// that has a non-empty stage_seconds, a stage present in it was measured by
// the recorded run, and a stage carrying a pass or a fail while absent from
// it was not. That is the same witness BuildScaleRecordFromEstate already
// trusts for exactly this question (scalerecord.go).
//
// What it cannot recover, and does not invent:
//
//   - WHICH run measured a carried verdict. The artifact only ever kept one
//     commit per row, so the earlier run's commit and date are gone. Such a
//     stage gets an empty StageRun: "measured by a run this row cannot
//     name", which compares unequal to last_run and so reads as carried,
//     and which the board renders as exactly that rather than as a commit
//     it made up.
//   - Anything at all on a row with no stage_seconds. A legacy-protocol
//     row, or a gauntlet row from a script whose live/e2e/lib/gauntlet.sh
//     predates duration_s, has no witness: it is left with no provenance,
//     which reads as unknown, and its cells and its clear flag are exactly
//     what they were. Unknown is honest; a stamp asserting the recorded run
//     measured all fourteen stages would not be.
//   - A "not_run" or "n/a" cell's provenance. Neither asserts anything a
//     stale marker could protect a reader from, and n/a is written by
//     Rebuild rather than measured at all.
//
// On the artifact as committed at 3db8029364 this stamps exactly one row -
// terralith-scale, whose day2_count and day2_replace read pass from a run
// the recorded one never reached (#1125) - and moves no headline number,
// because that row is already not clear on its own greenfield/day2_remove
// verdicts. Every other row's last run reached every stage it reports.

// backfillStageProvenance writes per-stage provenance into every row the
// committed artifact can support one for, and returns one line per row it
// changed. Idempotent: a row already stamped comes out identical, so
// re-running it is a no-op rather than a second, different answer.
func backfillStageProvenance(a *Artifact) []string {
	var changed []string
	for i := range a.Estates {
		r := &a.Estates[i]
		if r.LastRun == nil || r.Protocol != ProtocolGauntlet {
			continue
		}
		witness := r.LastRun.Seconds
		if len(witness) == 0 {
			// No witness on this row: see the file comment. Nothing is
			// written, deliberately.
			continue
		}
		measured := 0
		var carried []string
		runs := map[string]StageRun{}
		for _, s := range Stages() {
			v := r.Stages[s.ID]
			if _, ok := witness[s.ID]; ok {
				runs[s.ID] = StageRun{Commit: r.LastRun.Commit, Date: r.LastRun.Date}
				measured++
				continue
			}
			if v != VerdictPass && v != VerdictFail {
				continue
			}
			// A pass or a fail this run emitted no duration_s for, on a row
			// where the run emitted durations for other stages: carried
			// from a run this artifact can no longer name.
			runs[s.ID] = StageRun{}
			carried = append(carried, s.ID)
		}
		if len(runs) == 0 {
			continue
		}
		if sameStageRuns(r.StageRuns, runs) {
			continue
		}
		r.StageRuns = runs
		sort.Strings(carried)
		if len(carried) == 0 {
			changed = append(changed, fmt.Sprintf("%s: stamped %d stage(s) as measured at %s; none carried", r.Name, measured, short(r.LastRun.Commit)))
			continue
		}
		changed = append(changed, fmt.Sprintf("%s: stamped %d stage(s) as measured at %s; %d carried from an unnamed earlier run: %v", r.Name, measured, short(r.LastRun.Commit), len(carried), carried))
	}
	return changed
}

// sameStageRuns reports whether two provenance maps are equal, so a re-run
// of the backfill can say "nothing to do" rather than rewriting identical
// bytes and reporting a change that did not happen.
func sameStageRuns(a, b map[string]StageRun) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || v != w {
			return false
		}
	}
	return true
}

// cmdBackfillStageProvenance is `gauntlet backfill-stage-provenance`. It
// rebuilds and re-renders afterwards exactly as `gauntlet run` does, so
// every derived field (each row's clear flag, the set summaries, the board)
// is recomputed from the rows rather than written by this command - the
// rule live/gauntlet.json's own aggregates exist under.
//
// -n prints what it would do and writes nothing.
func cmdBackfillStageProvenance(root string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("backfill-stage-provenance", flag.ContinueOnError)
	dry := fs.Bool("n", false, "print what would change and write nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, a, err := loadAll(root)
	if err != nil {
		return err
	}
	changed := backfillStageProvenance(a)
	if len(changed) == 0 {
		fmt.Fprintln(stdout, "backfill-stage-provenance: no row's provenance changed")
		return nil
	}
	for _, line := range changed {
		fmt.Fprintln(stdout, line)
	}
	if *dry {
		fmt.Fprintf(stdout, "backfill-stage-provenance: -n, wrote nothing (%d row(s) would change)\n", len(changed))
		return nil
	}
	bi, err := LoadBehaviorIndex(root)
	if err != nil {
		return err
	}
	a.Rebuild(m, bi, emulatorPin(root), oracleVersions(root))
	tt, err := LoadTypeIndexTotals(root)
	if err != nil {
		return err
	}
	scale, err := loadScaleRecordsBytes(root)
	if err != nil {
		return err
	}
	written, err := Render(root, m, a, tt, scale)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "backfill-stage-provenance: %d row(s) changed, rendered %d files\n", len(changed), len(written))
	return nil
}
