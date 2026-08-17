// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package harness

import (
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/live/mdspan"
)

// DocRel is the rendered document, and the reason both registries render at
// all: a reader who is not going to open a Go file should still be able to
// see what the project believes and what it is driving down.
const DocRel = "live/HARNESS.md"

// Tool is the generator name mdspan stamps into the span markers.
const Tool = "harness-gen"

// Span names inside [DocRel].
const (
	SpanBurndown    = "burndown"
	SpanAssumptions = "assumptions"
)

// Markers is this generator's mdspan vocabulary.
func Markers() mdspan.Markers { return mdspan.For(Tool) }

// Render rewrites both spans of md from a live measurement of both
// registries.
//
// Every number it writes comes from a Measure or a Check that just ran, so
// the document cannot say something the code does not. That couples the
// document to the artifacts: a change to tools/row-gen/rejected.json moves a
// count here and the drift test goes red until the generator is re-run,
// which is the same contract live/mapping.json's own drift test has. It is
// also the point - the ledger ratchet sat two above its own file and nobody
// noticed until somebody went looking, twice in two days.
func Render(repo *Repo, md string) (string, error) {
	burndown, err := renderBurndown(repo)
	if err != nil {
		return "", err
	}
	assumptions, err := renderAssumptions(repo)
	if err != nil {
		return "", err
	}
	m := Markers()
	md, err = m.Replace(DocRel, md, SpanBurndown, burndown)
	if err != nil {
		return "", err
	}
	return m.Replace(DocRel, md, SpanAssumptions, assumptions)
}

func renderBurndown(repo *Repo) (string, error) {
	results := MeasureAll(repo, Burndown())

	var b strings.Builder
	b.WriteString("| quantity | now | bound | denominator | tracker |\n")
	b.WriteString("| --- | ---: | ---: | --- | --- |\n")
	for _, res := range results {
		if res.Err != nil {
			// A measurement that could not run has nothing to tabulate;
			// rendering its zero would be the blind-scanner failure.
			return "", fmt.Errorf("rendering %s: %w", res.Entry.ID, res.Err)
		}
		den := "none: " + firstSentence(res.Entry.NoDenominator)
		if res.Entry.Denominator != nil {
			den = fmt.Sprintf("`%s` at %d, floor %d",
				res.Entry.Denominator.Name, res.DenominatorValue, res.Entry.Denominator.Floor)
		}
		b.WriteString(fmt.Sprintf("| [`%s`](#%s) | %d | %s %d | %s | %s |\n",
			res.Entry.ID, res.Entry.ID, res.Reading.Value,
			res.Entry.Direction, res.Entry.Bound, den, res.Entry.Tracker))
	}

	for _, res := range results {
		e := res.Entry
		fmt.Fprintf(&b, "\n<a id=%q></a>\n### `%s`\n\n", e.ID, e.ID)
		fmt.Fprintf(&b, "%s\n\n", e.Claim)
		fmt.Fprintf(&b, "Now **%d %s**, %s **%d**", res.Reading.Value, e.Unit, e.Direction, e.Bound)
		switch {
		case res.Breach != nil:
			fmt.Fprintf(&b, ". **The bound does not hold**: %s\n\n", res.Breach)
		case res.Slack > 0:
			fmt.Fprintf(&b, ", so the bound is stale by %d and should be lowered to the measurement.\n\n", res.Slack)
		default:
			b.WriteString(". At the bound.\n\n")
		}
		if res.Reading.Note != "" {
			fmt.Fprintf(&b, "%s.\n\n", strings.TrimRight(res.Reading.Note, "."))
		}
		fmt.Fprintf(&b, "- Measured on %s.\n", e.Measured)
		fmt.Fprintf(&b, "- Held against %s. %s\n", e.Against, e.AgainstWhy)
		fmt.Fprintf(&b, "- Instrument: %s\n", e.Instrument)
		if e.Denominator != nil {
			fmt.Fprintf(&b, "- Denominator `%s`, measured at %d against a floor of %d. %s\n",
				e.Denominator.Name, res.DenominatorValue, e.Denominator.Floor, e.Denominator.Why)
		} else {
			fmt.Fprintf(&b, "- No denominator. %s\n", e.NoDenominator)
		}
		b.WriteString("\nWhat the instrument cannot see:\n\n")
		for _, s := range e.BlindSpots {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		if len(e.History) > 0 {
			b.WriteString("\nWhere the bound has been:\n\n")
			for _, h := range e.History {
				fmt.Fprintf(&b, "- %s\n", h)
			}
		}
	}
	return b.String(), nil
}

func renderAssumptions(repo *Repo) (string, error) {
	for _, a := range Assumptions() {
		if _, err := a.Check(repo); err != nil {
			return "", fmt.Errorf("rendering %s: its own check fails, so the document would state a "+
				"claim the tree contradicts: %w", a.ID, err)
		}
	}
	var b strings.Builder
	for _, a := range Assumptions() {
		fmt.Fprintf(&b, "<a id=%q></a>\n### `%s`\n\n", a.ID, a.ID)
		fmt.Fprintf(&b, "%s\n\n", a.Claim)
		fmt.Fprintf(&b, "**If this stops being true.** %s\n\n", a.Consequence)
		for _, rec := range a.Recorded {
			fmt.Fprintf(&b, "- `%s`\n", rec)
		}
		if len(a.Recorded) > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Evidence: %s\n\n", a.Evidence)
		fmt.Fprintf(&b, "Tracker: %s\n\n", a.Tracker)
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}
