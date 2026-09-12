// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

// scaleAccountingTolerance is how far a record's Stages[*].Seconds plus its
// UnaccountedSeconds may drift from TotalSeconds before this guard calls it
// a defect rather than rounding noise. Shell-side stage durations are whole
// seconds (`date +%s` deltas - live/e2e/lib/gauntlet.sh's gauntlet_stage);
// TotalSeconds is Go's time.Since rounded to one decimal place
// (roundSeconds, run.go/livecert.go). A handful of independently
// whole-second-rounded stage durations summed against a sub-second-precision
// total can drift by close to a second from rounding alone; 1.0 second
// covers that without hiding a genuinely missing second-scale bucket of
// time, which is what this guard exists to catch (issue #1051/#1053's own
// finding: a published 11180.5s total whose two named per-stage seconds
// summed to 3,237, nearly 7,950 seconds short, with nothing saying so).
const scaleAccountingTolerance = 1.0

// TestScaleRecordsAccountForTheirOwnTotal is the wall-time-accounting unit's
// own regression guard. It loads live/gauntlet-scale.json exactly as
// committed (LoadScaleArtifact - the same function every reader of this
// file goes through) and fails if any record whose TotalSeconds is known
// does not add up: the sum of every stage's own Seconds (wall duration -
// OperationSeconds never counts here, see ScaleStage's own doc comment for
// why) plus UnaccountedSeconds must equal TotalSeconds, within
// scaleAccountingTolerance.
//
// This is deliberately a check on the DATA, not on the code that produced
// it: a future hand-edit of live/gauntlet-scale.json, a new code path that
// adds a stage's Seconds without recomputing UnaccountedSeconds, or a
// forgotten call to unaccountedSeconds after touching a record, would all
// be caught here - reading the committed artifact itself, the same way a
// reader of the chant-bench page would, rather than trusting that whatever
// produced it did the arithmetic right. Nothing checked this relationship
// at all before this unit, which is exactly how a breakdown that did not
// sum to its own total shipped unnoticed.
//
// One shape it accepts on purpose rather than refusing: a record whose
// stage seconds overcount its own total (a negative UnaccountedSeconds),
// but only when the record says so by name - AccountingInconsistent true
// and UnaccountedDetail naming an issue (issue #1069's own finding, in the
// terralith-scale/floci/scale-1 row: fourteen stage timers summing to more
// than the run's own total). That is never a wider tolerance; an ordinary
// record without that declaration still fails exactly as before.
func TestScaleRecordsAccountForTheirOwnTotal(t *testing.T) {
	root := repoRootForTest(t)
	sa, err := LoadScaleArtifact(root)
	if err != nil {
		t.Fatalf("loading %s: %v", ScaleRecordsPath, err)
	}
	if len(sa.Records) == 0 {
		t.Fatal("live/gauntlet-scale.json carries no records - nothing for this guard to check, which is itself suspicious for a committed artifact this repository relies on")
	}

	checked := 0
	for _, rec := range sa.Records {
		rec := rec
		if rec.TotalSeconds == nil {
			continue // nothing to check this record's arithmetic against
		}
		checked++
		name := rec.Estate + "/" + rec.Target + "/scale-" + strconv.Itoa(rec.Scale)
		t.Run(name, func(t *testing.T) {
			sum := 0.0
			for _, st := range rec.Stages {
				if st.Seconds != nil {
					sum += *st.Seconds
				}
			}
			var unaccounted float64
			if rec.UnaccountedSeconds != nil {
				unaccounted = *rec.UnaccountedSeconds
			} else if math.Abs(*rec.TotalSeconds-sum) > scaleAccountingTolerance {
				t.Fatalf("stage seconds sum to %.1f against a %.1f total (%.1f unexplained) and unaccounted_seconds is absent - a gap this large must be named, not silently dropped",
					sum, *rec.TotalSeconds, *rec.TotalSeconds-sum)
			}
			got := sum + unaccounted
			if math.Abs(got-*rec.TotalSeconds) > scaleAccountingTolerance {
				t.Fatalf("stage seconds (%.1f) + unaccounted_seconds (%.1f) = %.1f, want %.1f (total_seconds) within %.1fs - detail: %q",
					sum, unaccounted, got, *rec.TotalSeconds, scaleAccountingTolerance, rec.UnaccountedDetail)
			}
			if unaccounted < -scaleAccountingTolerance {
				// A negative remainder beyond rounding means the stages
				// overcount the total, which is impossible for sequential
				// slices of one run - refused outright UNLESS the record
				// itself declares this a KNOWN inconsistency
				// (AccountingInconsistent, issue #1069's own follow-up) and
				// names the issue tracking it. ValidateScaleRecord already
				// requires UnaccountedDetail to be non-empty and name an
				// issue whenever AccountingInconsistent is true - re-checked
				// here too, since this guard reads the committed artifact
				// directly and must not trust that whatever wrote it went
				// through that validation. The guard is widened by name,
				// never by a wider tolerance: an ordinary record
				// (AccountingInconsistent false, every record but this one
				// today) is still refused exactly as before.
				if !rec.AccountingInconsistent {
					t.Fatalf("unaccounted_seconds = %.1f, negative well beyond rounding - stage seconds overcount the total, and accounting_inconsistent is not set to say this is known and tracked", unaccounted)
				}
				if strings.TrimSpace(rec.UnaccountedDetail) == "" || !issueRefRe.MatchString(rec.UnaccountedDetail) {
					t.Fatalf("unaccounted_seconds = %.1f and accounting_inconsistent = true, but unaccounted_detail %q does not name an issue (\"#123\") - a known inconsistency must be tracked, not just asserted", unaccounted, rec.UnaccountedDetail)
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("every record in live/gauntlet-scale.json has total_seconds absent - this guard has nothing to check; if that is expected, this test itself needs updating, not silently passing")
	}
}
