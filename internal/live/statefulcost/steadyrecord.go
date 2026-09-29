// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// steadyrecord.go is this package's record half: the structured artifact a
// steady-state run writes, and the gates that decide whether it is allowed to
// write one.
//
// # Why this exists
//
// The measurement already existed and was already careful. What it did not
// have was a way to be consumed without being misread, and it was misread:
// a downstream benchmark published choudoufu's DEFAULT plan - which runs with
// the state cache off, by design, so drift is always visible - against stock
// holding the state file its own apply wrote, and reported the result as this
// fork's plan cost. That is a cached plan measured against an uncached one.
// The same asymmetry had already been caught once, in seconds rather than
// calls, and withdrawn at 2989f9b073.
//
// Both times the discipline was present and both times it was present as
// PROSE. The run logged a table, a human was expected to pick the right
// column, and a reader downstream picked a different one. A comment that says
// "if these two columns match, the cache is buying nothing" is not a check;
// nothing failed when they matched, and nothing failed when the wrong column
// was published.
//
// So this file does two things, and the second is the point:
//
//  1. It writes the measurement as a record, with each figure's CONDITIONS
//     attached to the figure rather than described beside it.
//  2. It refuses to write one at all unless the comparison was valid.
//
// # The gates
//
// [Gate] is the whole argument. A steady-state comparison is only meaningful
// when both sides hold what they ordinarily hold - stock its state file,
// choudoufu its warm cache - and when the cache can be shown to be doing
// something. Each of those is a condition that can silently not hold, and
// each has silently not held at least once:
//
//   - No record store after choudoufu's own apply: the column is measuring
//     the adoption path, not the steady state.
//   - No state cache: every plan is a cache miss, and the number is the
//     cache-off number wearing the cache-on label.
//   - Cache on and cache off costing the same: the cache is present and
//     buying nothing, which is a finding about the cache and NOT a cost
//     comparison. This is the one that has no natural symptom - the run
//     succeeds, the plans are empty, the table looks fine.
//
// A run that trips any of them writes no record. Refusing to produce a number
// is the only failure mode that cannot be misread.
package statefulcost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// SteadyRecordSchema is the record's version. A consumer that does not
// recognise it must refuse the record rather than read it optimistically:
// every field below means something specific about how the number was taken,
// and a reader guessing at an unknown schema is how the last one went wrong.
const SteadyRecordSchema = 1

// Condition names the state one side of the comparison was in when its
// figure was taken. It travels WITH the figure, never beside it, so that a
// consumer cannot select a number without also selecting what it means.
type Condition string

const (
	// ConditionStateFile is stock holding the terraform.tfstate its own
	// apply wrote - the ordinary condition for a stock plan.
	ConditionStateFile Condition = "stock-state-file"

	// ConditionCacheWarm is choudoufu holding the state cache and record
	// store its own apply wrote - the ordinary condition for a live plan,
	// and the only one comparable with ConditionStateFile.
	ConditionCacheWarm Condition = "choudoufu-cache-warm"

	// ConditionCacheOff is choudoufu with persistence disabled. It is the
	// control, not a result: it exists to prove the cache is doing
	// something, and it is also what a plan costs with the local state gone
	// entirely. It is NOT comparable with ConditionStateFile, and
	// [SteadyRecord.Comparable] says so.
	ConditionCacheOff Condition = "choudoufu-cache-off"
)

// Comparable reports whether two conditions may be divided into a ratio.
//
// Only the two ordinary conditions are, and deliberately so: this predicate
// is the thing that makes the invalid comparison inexpressible rather than
// merely discouraged. A publisher that wants a percentage has to ask, and the
// answer for cache-off against a stock state file is no.
func Comparable(a, b Condition) bool {
	if a == b {
		return true
	}
	return (a == ConditionStateFile && b == ConditionCacheWarm) ||
		(a == ConditionCacheWarm && b == ConditionStateFile)
}

// SteadyColumn is one measured configuration and the condition it was
// measured under.
type SteadyColumn struct {
	Label     string    `json:"label"`
	Condition Condition `json:"condition"`

	// Calls is the API call count for each repeat, in order. Plural rather
	// than a mean: three runs that disagree are a finding, and a mean hides
	// it. The 10,069-resource run that reported 19,673 on its second pass
	// and 18,510 on the other two - 1,163 proxy EOFs, each retry counted -
	// is exactly the case a single number would have buried.
	Calls []int `json:"calls"`

	// Seconds is wall clock per repeat. Recorded, never published as a
	// ratio by this record's own consumers: these are emulator seconds, and
	// an emulator grades the machine rather than the tool.
	Seconds []float64 `json:"seconds"`

	// Verdicts is each repeat's plan verdict. Every one must be "empty" or
	// the column is not comparable with any other.
	Verdicts []string `json:"verdicts"`
}

// Median is the middle call count of the repeats, which is what a reader
// should quote. Returns 0 for an empty column.
func (c SteadyColumn) Median() int {
	if len(c.Calls) == 0 {
		return 0
	}
	sorted := append([]int(nil), c.Calls...)
	sort.Ints(sorted)
	return sorted[len(sorted)/2]
}

// MedianSeconds is the middle wall-clock reading of the repeats. Returns 0
// for an empty column.
func (c SteadyColumn) MedianSeconds() float64 {
	if len(c.Seconds) == 0 {
		return 0
	}
	sorted := append([]float64(nil), c.Seconds...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

// SteadyRecord is one steady-state measurement, whole.
type SteadyRecord struct {
	Schema    int    `json:"schema"`
	Estate    string `json:"estate"`
	Shape     string `json:"shape"`
	Resources int    `json:"resources"`
	Substrate string `json:"substrate"`
	Emulator  string `json:"emulator,omitempty"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`

	Columns []SteadyColumn `json:"columns"`

	// CacheControl records what the control arm proved. A record only
	// exists when Served is true, so a consumer reading one can rely on the
	// cache having been shown to work; the numbers are carried anyway so
	// the margin is auditable rather than asserted.
	CacheControl CacheControl `json:"cache_control"`
}

// CacheControl is the control arm's own result.
type CacheControl struct {
	// WarmCalls and OffCalls are the median call counts with the cache
	// serving and disabled.
	WarmCalls int `json:"warm_calls"`
	OffCalls  int `json:"off_calls"`

	// Served is whether the cache demonstrably did something. False makes
	// the whole record unwritable - see [Gate].
	Served bool `json:"served"`
}

// minimumCacheMargin is how many calls the cache must save before the run
// counts as having a serving cache.
//
// One call would satisfy the letter of "they differ" while meaning nothing;
// the observed margins are large (220 against 169 at 79 objects, 25,629
// against 19,666 at 10,069) so a floor well above noise costs nothing real
// and refuses the case this gate exists for. Proportional rather than
// absolute, because the estates differ by two orders of magnitude.
const minimumCacheMarginFraction = 0.05

// Gate decides whether a measurement may be written as a record, and returns
// the reason it may not.
//
// It is deliberately a hard refusal rather than a warning. The failure this
// exists to prevent is a number that looks fine - the run succeeded, the
// plans were empty, the table printed - and is not comparable with the thing
// it will be divided by. There is no rendering of that number that is safe,
// so the only correct output is no number.
func Gate(rec SteadyRecord) error {
	if len(rec.Columns) == 0 {
		return fmt.Errorf("no columns measured")
	}

	var warm, off, stock *SteadyColumn
	for i := range rec.Columns {
		c := &rec.Columns[i]
		for _, v := range c.Verdicts {
			if v != "empty" {
				return fmt.Errorf("column %q has a %q verdict: a plan that is not empty is not comparable with one that is", c.Label, v)
			}
		}
		if len(c.Calls) == 0 {
			return fmt.Errorf("column %q measured no runs", c.Label)
		}
		// Calls without seconds is the half-truth this record exists to
		// stop. A percentage on API calls is not a percentage on time, and
		// the two have pointed in different directions on this very estate:
		// +6.2% on calls beside a wall-clock ratio several times that. A
		// consumer handed only the flattering half will publish it.
		if len(c.Seconds) != len(c.Calls) {
			return fmt.Errorf(
				"column %q has %d call reading(s) and %d second reading(s): a cost record carries both or neither, "+
					"because a percentage on calls is not a percentage on time",
				c.Label, len(c.Calls), len(c.Seconds))
		}
		switch c.Condition {
		case ConditionCacheWarm:
			warm = c
		case ConditionCacheOff:
			off = c
		case ConditionStateFile:
			stock = c
		}
	}

	if stock == nil {
		return fmt.Errorf("no %s column: there is nothing to compare against", ConditionStateFile)
	}
	if warm == nil {
		return fmt.Errorf("no %s column: without it the only choudoufu figure is the cache-off one, which is not comparable with stock's", ConditionCacheWarm)
	}
	if off == nil {
		return fmt.Errorf("no %s control column: without it a flat number cannot be told from a cache that is not serving", ConditionCacheOff)
	}

	// The control. This is the gate that has no natural symptom.
	saved := off.Median() - warm.Median()
	floor := int(float64(off.Median()) * minimumCacheMarginFraction)
	if saved <= 0 {
		return fmt.Errorf(
			"the cache saved nothing: %d calls with it serving against %d with it off. "+
				"The cache is present and buying nothing, which is a finding about the cache and not a cost comparison",
			warm.Median(), off.Median())
	}
	if saved < floor {
		return fmt.Errorf(
			"the cache saved %d calls of %d (%.1f%%), below the %.0f%% floor this gate requires. "+
				"A margin that small cannot be told from noise, and a number that cannot be told from noise must not be published as a comparison",
			saved, off.Median(), 100*float64(saved)/float64(off.Median()), 100*minimumCacheMarginFraction)
	}

	return nil
}

// WriteSteadyRecord gates rec and writes it to path, creating parent
// directories. It returns the gate's error unchanged when the measurement may
// not be published, and writes nothing in that case.
func WriteSteadyRecord(path string, rec SteadyRecord) error {
	rec.Schema = SteadyRecordSchema
	if err := Gate(rec); err != nil {
		return fmt.Errorf("refusing to write a steady-state record: %w", err)
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
