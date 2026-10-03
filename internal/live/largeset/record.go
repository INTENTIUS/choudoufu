// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// record.go is the baseline half: what one live-plan of each estate costs
// for the A -> B bump, written as a gated record in the pattern of
// internal/live/statefulcost's steadyrecord.go. Later units of #1749 - the
// set plan (#1752), the grouped summary (#1753), wave planning (#1754) - are
// measured against it, so it carries each figure's conditions with the
// figure and refuses to exist when those conditions did not hold.
//
// The gates, each a way this measurement can look fine and not be:
//
//   - The steady-state control was not empty. Every estate is planned at A
//     after its own apply, before the bump; a plan there that proposes
//     anything means the apply left the estate unconverged (an emulator
//     gap, an unmarked resource), and the bump's figure would carry that
//     adoption work inside it.
//   - The bump moved nothing in some estate. Every estate calls the module,
//     so a bump that leaves one unmoved never reached it; its figure is the
//     steady state's wearing the bump's label.
//   - The bump destroyed something. Nothing in B removes a resource, so a
//     destroy is a wrong marker or a discovery miss, not the bump.
//   - Repeats disagreed about the plan. Plural readings exist so a
//     disagreement is visible; a record of a plan that changes between
//     identical runs is a finding, not a baseline.
//   - No estate differs from the group. The outlier exists so a grouped
//     summary has something to find; a fixture whose outlier vanished
//     would let #1753 pass by grouping everything into one bucket.
//   - Calls without seconds, or the reverse: a percentage on calls is not a
//     percentage on time (steadyrecord.go's reason, unchanged).
package largeset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BaselineRecordSchema is the record's version. A consumer that does not
// recognise it must refuse the record rather than read it optimistically.
const BaselineRecordSchema = 1

// Condition names how a plan figure was taken. It travels with the figure.
type Condition string

// ConditionLivePlanAfterOwnApply is `choudoufu live-plan` with default flags,
// in a root holding the record store and cache its own apply at A wrote, the
// module regenerated at B in place. It is the only condition this record
// carries; a later unit measuring another one adds a constant rather than
// reusing this label.
const ConditionLivePlanAfterOwnApply Condition = "choudoufu-live-plan-default-after-own-apply"

// PlanReading is one estate's plan, measured Repeats times.
type PlanReading struct {
	// Calls is the AWS API calls each repeat made, through the counting
	// proxy (internal/live/flocitest.CountingProxy). Plural, never a mean.
	Calls []int `json:"calls"`
	// Seconds is each repeat's wall clock. Emulator seconds: they grade the
	// machine as much as the tool, which is why calls are carried beside
	// them.
	Seconds []float64 `json:"seconds"`
	// Summaries is each repeat's plan summary line, verbatim ("Plan: 1 to
	// add, 3 to change, 0 to destroy." or "No changes.").
	Summaries []string `json:"summaries"`
	// Add, Change and Destroy are the first repeat's totals; the gate
	// requires every repeat's summary to agree.
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
	// Changed is the addresses the first repeat proposes anything for, in
	// rendered order.
	Changed []string `json:"changed,omitempty"`
	// OutputLines is the first repeat's line count: what a reader scrolls.
	OutputLines int `json:"output_lines"`
}

// Median is the middle call count. Returns 0 for no readings.
func (r PlanReading) Median() int {
	if len(r.Calls) == 0 {
		return 0
	}
	s := append([]int(nil), r.Calls...)
	sort.Ints(s)
	return s[len(s)/2]
}

// MedianSeconds is the middle wall-clock reading. Returns 0 for no readings.
func (r PlanReading) MedianSeconds() float64 {
	if len(r.Seconds) == 0 {
		return 0
	}
	s := append([]float64(nil), r.Seconds...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// EstateBaseline is one estate's two plans: the steady-state control at A,
// and the bump.
type EstateBaseline struct {
	Estate string `json:"estate"`
	Role   Role   `json:"role"`
	// Steady is live-plan at A after the estate's own apply. Its verdict
	// must be empty.
	Steady PlanReading `json:"steady"`
	// Bump is live-plan with the module at B.
	Bump PlanReading `json:"bump"`
}

// FixtureShape is what was measured.
type FixtureShape struct {
	Estates int     `json:"estates"`
	Source  Source  `json:"source"`
	Prefix  string  `json:"prefix"`
	From    Version `json:"from"`
	To      Version `json:"to"`
}

// BaselineRecord is one measurement of the bump, whole.
type BaselineRecord struct {
	Schema    int              `json:"schema"`
	Issue     string           `json:"issue"`
	Fixture   FixtureShape     `json:"fixture"`
	Condition Condition        `json:"condition"`
	Substrate string           `json:"substrate"`
	Emulator  string           `json:"emulator"`
	Commit    string           `json:"commit"`
	Date      string           `json:"date"`
	Repeats   int              `json:"repeats"`
	Estates   []EstateBaseline `json:"estates"`
	// Summary is the grouped summary of the bump (#1753), taken from the
	// same run's plans. Absent from a record written before it existed.
	Summary *SummaryReading `json:"summary,omitempty"`
}

// SummaryReading is what `choudoufu live-summary` made of the bump's plans
// (GitHub issue #1753): one `live-plan -out` per estate after the bump's
// readings, each `show -json`ed and wrapped into a set document.
type SummaryReading struct {
	// Groups is each group's size, largest first.
	Groups []int `json:"groups"`
	// Outliers names, by estate, every estate outside the largest group.
	Outliers []string `json:"outliers"`
	// SummaryLines is the text summary's line count; PlanLines is the sum
	// of every estate's bump output_lines, what a reviewer reads without
	// the summary. MarkdownChars is the merge-request note's size.
	SummaryLines  int `json:"summary_lines"`
	PlanLines     int `json:"plan_lines"`
	MarkdownChars int `json:"markdown_chars"`
}

// GateBaseline decides whether a measurement may be written, and returns the
// reason it may not. A hard refusal: the failure it prevents is a number
// that looks fine and is not what it says.
func GateBaseline(rec BaselineRecord) error {
	if strings.TrimSpace(rec.Commit) == "" {
		return fmt.Errorf("no commit: a record that does not name the commit it measured cannot be reproduced or checked")
	}
	if strings.TrimSpace(rec.Emulator) == "" {
		return fmt.Errorf("no emulator image: a figure taken on an unnamed emulator cannot be compared with one taken after a repin")
	}
	if rec.Condition != ConditionLivePlanAfterOwnApply {
		return fmt.Errorf("condition %q is not one this record knows; a consumer could not tell what the figures mean", rec.Condition)
	}
	if rec.Repeats < 1 {
		return fmt.Errorf("repeats is %d; a record needs at least one reading per plan", rec.Repeats)
	}
	if len(rec.Estates) == 0 || len(rec.Estates) != rec.Fixture.Estates {
		return fmt.Errorf("the fixture has %d estates and the record measured %d: a partial set is not a baseline for a set", rec.Fixture.Estates, len(rec.Estates))
	}
	seen := map[string]bool{}
	groups := map[string]int{}
	for _, e := range rec.Estates {
		if seen[e.Estate] {
			return fmt.Errorf("estate %s is measured twice", e.Estate)
		}
		seen[e.Estate] = true
		if err := gateReading(rec.Repeats, e.Steady); err != nil {
			return fmt.Errorf("%s steady plan: %w", e.Estate, err)
		}
		if err := gateReading(rec.Repeats, e.Bump); err != nil {
			return fmt.Errorf("%s bump plan: %w", e.Estate, err)
		}
		if e.Steady.Add+e.Steady.Change+e.Steady.Destroy != 0 || e.Steady.Summaries[0] != "No changes." {
			return fmt.Errorf("%s's steady-state plan at %s is %q, not empty: the apply left it unconverged, and the bump's figure would carry that work inside it",
				e.Estate, rec.Fixture.From, e.Steady.Summaries[0])
		}
		if e.Bump.Add+e.Bump.Change == 0 {
			return fmt.Errorf("%s's plan for the bump moves nothing (%q): every estate calls the module, so the bump never reached this one and its figure is the steady state's",
				e.Estate, e.Bump.Summaries[0])
		}
		if e.Bump.Destroy != 0 {
			return fmt.Errorf("%s's plan for the bump destroys %d: nothing in %s removes a resource, so this is a wrong marker or a discovery miss, not the bump",
				e.Estate, e.Bump.Destroy, rec.Fixture.To)
		}
		groups[fmt.Sprintf("%d/%d/%d", e.Bump.Add, e.Bump.Change, e.Bump.Destroy)]++
	}
	if len(rec.Estates) > 1 && len(groups) < 2 {
		return fmt.Errorf("every estate's plan for the bump has the same totals: the fixture's outlier did not show, and a grouped summary measured against this would have nothing to find")
	}
	if rec.Summary != nil {
		return gateSummary(rec, groups)
	}
	return nil
}

// gateSummary refuses a summary reading that is not the fixture's answer:
// the estates it names as outliers must be exactly the estates whose bump
// totals differ from the most common totals, and its plan-line figure must
// be the record's own. A summary that grouped the outlier in, or split the
// group, is a wrong summary and not a measurement of one.
func gateSummary(rec BaselineRecord, totals map[string]int) error {
	s := rec.Summary
	majority, best := "", 0
	for k, n := range totals {
		if n > best || n == best && k < majority {
			majority, best = k, n
		}
	}
	var want []string
	planLines := 0
	for _, e := range rec.Estates {
		planLines += e.Bump.OutputLines
		if fmt.Sprintf("%d/%d/%d", e.Bump.Add, e.Bump.Change, e.Bump.Destroy) != majority {
			want = append(want, e.Estate)
		}
	}
	got := append([]string(nil), s.Outliers...)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Errorf("the summary names %v as outliers and the plans' totals single out %v: the summary grouped what the plans tell apart, or split what they agree on", got, want)
	}
	if len(s.Groups) == 0 || s.Groups[0] != best {
		return fmt.Errorf("the summary's largest group is %v and %d estates share the most common totals", s.Groups, best)
	}
	if s.SummaryLines <= 0 || s.PlanLines != planLines {
		return fmt.Errorf("summary lines %d against plan lines %d, and the record's bump output_lines sum to %d: a ratio needs both, from this run", s.SummaryLines, s.PlanLines, planLines)
	}
	return nil
}

func gateReading(repeats int, r PlanReading) error {
	if len(r.Calls) != repeats || len(r.Seconds) != repeats || len(r.Summaries) != repeats {
		return fmt.Errorf("%d call, %d second and %d summary readings for %d repeats: a cost record carries all three for every repeat, because a percentage on calls is not a percentage on time",
			len(r.Calls), len(r.Seconds), len(r.Summaries), repeats)
	}
	for i, c := range r.Calls {
		if c <= 0 {
			return fmt.Errorf("repeat %d made %d calls through the proxy: the plan did not go through it, so the count measures nothing", i+1, c)
		}
	}
	for i, s := range r.Summaries {
		if s == "" {
			return fmt.Errorf("repeat %d printed no plan summary: a plan that refused to run has no cost to record", i+1)
		}
		if s != r.Summaries[0] {
			return fmt.Errorf("repeat %d planned %q and repeat 1 planned %q: a plan that changes between identical runs is a finding, not a baseline", i+1, s, r.Summaries[0])
		}
	}
	return nil
}

// WriteBaseline gates rec and writes it to path. It writes nothing when the
// gate refuses.
func WriteBaseline(path string, rec BaselineRecord) error {
	rec.Schema = BaselineRecordSchema
	rec.Issue = "#1750"
	if err := GateBaseline(rec); err != nil {
		return fmt.Errorf("refusing to write a large-set baseline: %w", err)
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

// ReadBaseline reads a record and refuses an unknown schema, which is what a
// later unit comparing against the baseline calls.
func ReadBaseline(path string) (BaselineRecord, error) {
	b, err := os.ReadFile(path) //nolint:gosec // caller-supplied path
	if err != nil {
		return BaselineRecord{}, err
	}
	var rec BaselineRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return BaselineRecord{}, err
	}
	if rec.Schema != BaselineRecordSchema {
		return BaselineRecord{}, fmt.Errorf("%s has schema %d; this build reads %d", path, rec.Schema, BaselineRecordSchema)
	}
	return rec, nil
}
