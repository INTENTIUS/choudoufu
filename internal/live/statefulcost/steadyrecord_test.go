// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefulcost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// valid is the shape of a measurement that should be publishable: both sides
// in their ordinary condition, the control proving the cache serves, every
// plan empty. Numbers are the real ones from the 79-object terralith.
func valid() SteadyRecord {
	return SteadyRecord{
		Estate: "terralith", Shape: "identity-heavy", Resources: 79,
		Substrate: "floci", Commit: "abc123", Date: "2026-09-17T00:00:00Z",
		Columns: []SteadyColumn{
			{Label: "stock-terraform", Condition: ConditionStateFile,
				Calls: []int{150, 150, 150}, Seconds: []float64{1, 1, 1},
				Verdicts: []string{"empty", "empty", "empty"}},
			{Label: "choudoufu-cache-warm", Condition: ConditionCacheWarm,
				Calls: []int{169, 169, 170}, Seconds: []float64{2, 2, 2},
				Verdicts: []string{"empty", "empty", "empty"}},
			{Label: "choudoufu-cache-off", Condition: ConditionCacheOff,
				Calls: []int{220, 220, 221}, Seconds: []float64{3, 3, 3},
				Verdicts: []string{"empty", "empty", "empty"}},
		},
		CacheControl: CacheControl{WarmCalls: 169, OffCalls: 220, Served: true},
	}
}

func TestGateAcceptsAValidMeasurement(t *testing.T) {
	if err := Gate(valid()); err != nil {
		t.Fatalf("a valid measurement was refused: %s", err)
	}
}

// TestGateRefusesACacheThatSavesNothing is the gate this file exists for.
//
// It is the only failure here with no natural symptom: the run succeeds, every
// plan is empty, the table prints, and the number is the cache-off number
// wearing the cache-on label. A comment saying "if these match, the cache is
// buying nothing" caught it zero times out of two.
func TestGateRefusesACacheThatSavesNothing(t *testing.T) {
	rec := valid()
	// The cache is present and doing nothing: both columns cost the same.
	rec.Columns[1].Calls = []int{220, 220, 220}
	rec.CacheControl = CacheControl{WarmCalls: 220, OffCalls: 220}

	err := Gate(rec)
	if err == nil {
		t.Fatal("a cache that saved nothing was accepted; this is the case the gate exists for")
	}
	if !strings.Contains(err.Error(), "saved nothing") {
		t.Errorf("the refusal should say the cache saved nothing, got: %s", err)
	}
}

// TestGateRefusesAMarginIndistinguishableFromNoise: a cache that saves one
// call satisfies "they differ" and means nothing. The gate has a floor for
// that reason, and the floor is proportional because the estates it covers
// differ by two orders of magnitude.
func TestGateRefusesAMarginIndistinguishableFromNoise(t *testing.T) {
	rec := valid()
	rec.Columns[1].Calls = []int{219, 219, 219} // one call saved out of 220
	rec.CacheControl = CacheControl{WarmCalls: 219, OffCalls: 220}

	err := Gate(rec)
	if err == nil {
		t.Fatal("a one-call margin was accepted")
	}
	if !strings.Contains(err.Error(), "cannot be told from noise") {
		t.Errorf("the refusal should name noise, got: %s", err)
	}
}

// TestGateRefusesAMissingControl: without the cache-off column there is no way
// to tell a flat number from a cache that is not serving. That is how an
// earlier version of this measurement came to report the cache as doing
// nothing, and it is why the control is required rather than optional.
func TestGateRefusesAMissingControl(t *testing.T) {
	rec := valid()
	rec.Columns = rec.Columns[:2] // drop the cache-off control

	err := Gate(rec)
	if err == nil {
		t.Fatal("a measurement with no control column was accepted")
	}
	if !strings.Contains(err.Error(), "control") {
		t.Errorf("the refusal should name the missing control, got: %s", err)
	}
}

// TestGateRefusesAMissingWarmColumn is the failure that actually happened
// downstream: with no cache-warm column the only choudoufu figure available is
// the cache-off one, and a publisher took it and divided it by stock's.
func TestGateRefusesAMissingWarmColumn(t *testing.T) {
	rec := valid()
	rec.Columns = []SteadyColumn{rec.Columns[0], rec.Columns[2]} // stock + cache-off only

	err := Gate(rec)
	if err == nil {
		t.Fatal("a measurement with no cache-warm column was accepted: its only choudoufu number is not comparable with stock's")
	}
	if !strings.Contains(err.Error(), "not comparable") {
		t.Errorf("the refusal should say the remaining figure is not comparable, got: %s", err)
	}
}

func TestGateRefusesANonEmptyPlan(t *testing.T) {
	rec := valid()
	rec.Columns[1].Verdicts = []string{"empty", "NOT EMPTY", "empty"}

	err := Gate(rec)
	if err == nil {
		t.Fatal("a column with a non-empty plan was accepted")
	}
	if !strings.Contains(err.Error(), "not comparable") {
		t.Errorf("the refusal should say it is not comparable, got: %s", err)
	}
}

// TestComparableRefusesTheInvalidRatio is the predicate that makes the wrong
// comparison inexpressible rather than merely discouraged. Cache-off against a
// stock state file is precisely the division that was published.
func TestComparableRefusesTheInvalidRatio(t *testing.T) {
	if !Comparable(ConditionStateFile, ConditionCacheWarm) {
		t.Error("stock against a warm cache is the comparison this bench is for")
	}
	if Comparable(ConditionStateFile, ConditionCacheOff) {
		t.Error("stock against cache-off was accepted; that is the ratio that was published and withdrawn")
	}
	if Comparable(ConditionCacheOff, ConditionStateFile) {
		t.Error("the predicate must refuse in both directions")
	}
}

// TestWriteSteadyRecordWritesNothingWhenRefused: the only output that cannot
// be misread is no output. A refused measurement must not leave a partial file
// that a later reader treats as a result.
func TestWriteSteadyRecordWritesNothingWhenRefused(t *testing.T) {
	rec := valid()
	rec.Columns[1].Calls = []int{220, 220, 220} // cache saves nothing
	path := filepath.Join(t.TempDir(), "nested", "steady.json")

	if err := WriteSteadyRecord(path, rec); err == nil {
		t.Fatal("writing a refused measurement returned no error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a refused measurement left a file at %s", path)
	}
}

func TestWriteSteadyRecordRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steady.json")
	if err := WriteSteadyRecord(path, valid()); err != nil {
		t.Fatalf("a valid measurement was not written: %s", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %s", err)
	}
	var got SteadyRecord
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %s", err)
	}
	if got.Schema != SteadyRecordSchema {
		t.Errorf("schema = %d, want %d", got.Schema, SteadyRecordSchema)
	}

	// The condition must survive onto each figure. A consumer that can read
	// a number without reading its condition is the whole failure mode.
	for _, c := range got.Columns {
		if c.Condition == "" {
			t.Errorf("column %q round-tripped with no condition: a bare number is what got published last time", c.Label)
		}
	}
	if !got.CacheControl.Served {
		t.Error("a written record must carry a control that proved the cache served")
	}
}

func TestMedianQuotesTheMiddleRun(t *testing.T) {
	// The 10,069 case: run two reported 19,673 against 18,510 on runs one and
	// three, 1,163 proxy EOFs each counted as a retry. The median is the
	// honest quote and a mean would have buried it.
	c := SteadyColumn{Calls: []int{18510, 19673, 18510}}
	if got := c.Median(); got != 18510 {
		t.Errorf("Median() = %d, want 18510 - the middle run, not the mean", got)
	}
}

// TestGateRefusesCallsWithoutSeconds is the half-truth gate.
//
// The removed terralith bench published a percentage on API calls and no
// wall-clock figure at all, at any size, on either substrate. The two numbers
// point in different directions on this estate - single-digit percent on calls
// beside a multiple on seconds - so a consumer handed only the calls will
// publish the flattering half, which is what happened.
func TestGateRefusesCallsWithoutSeconds(t *testing.T) {
	rec := valid()
	rec.Columns[1].Seconds = nil

	err := Gate(rec)
	if err == nil {
		t.Fatal("a column with calls and no seconds was accepted; that is the half-truth this gate exists for")
	}
	if !strings.Contains(err.Error(), "both or neither") {
		t.Errorf("the refusal should say a cost record carries both or neither, got: %s", err)
	}
}

func TestMedianSecondsQuotesTheMiddleRun(t *testing.T) {
	c := SteadyColumn{Seconds: []float64{13.0, 57.0, 13.2}}
	if got := c.MedianSeconds(); got != 13.2 {
		t.Errorf("MedianSeconds() = %v, want 13.2 - the middle run, not the mean", got)
	}
}
