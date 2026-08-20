// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/terminal"
)

// This file is GitHub issue #342's guard, and the reason it lives in package
// residue rather than beside the view it measures is the check-unit trap
// HANDOFF.md names: `just ci`'s fast tier runs ./internal/command/, the
// package, and not ./internal/command/..., so a test in
// internal/command/views would not run in CI at all.
//
// What it holds is the seam that has now broken three times: live-import
// -approve's one-line summary is rendered by
// [views.StatelessImportHuman.Stamped] and asserted by exact string in
// twenty-odd live/e2e/*/run.sh crossing scripts that nothing in CI executes.
// Issue #340 inserted two columns into that line and updated one script; the
// other twenty stayed red and invisible for a day.
//
// The test consults BOTH sides and neither is this file's own opinion: the
// column labels are read off a REAL render, and the assertions are read off
// the scripts on disk. Widening the line again fails here, in three minutes,
// rather than in a ten-minute crossing run somebody happens to start.

// renderStampSummary returns live-import -approve's summary line, rendered
// by the real view from a report with a deliberately distinct count in every
// bucket, so no two columns can be confused for one another when the labels
// are recovered from it below.
func renderStampSummary(t *testing.T) string {
	t.Helper()

	// Distinct primes, one per outcome, so a mis-wired Printf argument
	// shows up as a swapped number rather than as two identical ones.
	counts := []struct {
		outcome string
		n       int
	}{
		{"STAMPED", 2},
		{"ALREADY_STAMPED", 3},
		{"RECORDED", 5},
		{"ALREADY_RECORDED", 7},
		{"FAILED", 11},
		{"SKIPPED", 13},
	}
	rep := views.StatelessImportStamped{Estate: "guard-estate"}
	for _, c := range counts {
		for i := 0; i < c.n; i++ {
			rep.Outcomes = append(rep.Outcomes, views.StatelessImportOutcome{
				Addr:     "aws_vpc.guard",
				TypeName: "aws_vpc",
				Outcome:  c.outcome,
				Detail:   "guard fixture",
			})
		}
	}

	streams, done := terminal.StreamsForTesting(t)
	views.NewStatelessImport(views.NewView(streams)).Stamped(rep)
	out := done(t).Stdout()

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "newly stamped") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("the rendered -approve report has no summary line at all:\n%s", out)
	return ""
}

var summaryDigits = regexp.MustCompile(`[0-9]+`)

// summaryColumnLabels recovers the line's column labels by cutting it at
// every run of digits. It derives the labels from the render rather than
// restating them, which is what makes the script check below fail on a
// column this file has never heard of.
func summaryColumnLabels(line string) []string {
	var labels []string
	for _, seg := range summaryDigits.Split(line, -1) {
		seg = strings.Trim(seg, " ,.")
		if seg != "" {
			labels = append(labels, seg)
		}
	}
	return labels
}

// TestApproveSummaryLineIsPinned is the issue's own suggested guard: render
// the line and assert it whole. A widening fails here first, with the before
// and after side by side, which is the message whoever changes the view
// needs - the script sweep below then says which scripts it costs.
func TestApproveSummaryLineIsPinned(t *testing.T) {
	const want = "2 resource(s) newly stamped, 3 already stamped, 5 newly recorded, 7 already recorded, 11 failed, 13 skipped."
	if got := renderStampSummary(t); got != want {
		t.Errorf("live-import -approve's summary line has changed.\n got: %s\nwant: %s\n\n"+
			"If the change is intended, update this pin AND every live/e2e/*/run.sh that\n"+
			"asserts the line - TestCrossingScriptsAssertTheCurrentSummaryLine names them.",
			got, want)
	}
}

// TestCrossingScriptsAssertTheCurrentSummaryLine is the half that would have
// caught #340. Every crossing script asserting the whole summary line has to
// carry every column the view currently renders, in order.
//
// A script line counts as a whole-line assertion when it names both the
// first column and the last one; a script that greps only the leading
// "resource(s) newly stamped" to echo the line for a human (corpus-eks-basic,
// corpus-xancloud-iac) is deliberately not one, and is left alone.
func TestCrossingScriptsAssertTheCurrentSummaryLine(t *testing.T) {
	labels := summaryColumnLabels(renderStampSummary(t))
	if len(labels) < 2 {
		t.Fatalf("recovered %d column label(s) from the summary line, want at least 2: %q", len(labels), labels)
	}
	first, last := labels[0], labels[len(labels)-1]

	scripts, err := filepath.Glob(filepath.Join("e2e", "*", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) < 20 {
		t.Fatalf("found %d live/e2e/*/run.sh scripts, expected at least 20 - this guard is looking in the wrong place", len(scripts))
	}
	sort.Strings(scripts)

	checked := 0
	for _, script := range scripts {
		body, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		for i, raw := range strings.Split(string(body), "\n") {
			// A shell regex escapes the parens in "resource(s)"; the
			// assertion is the same one either way.
			line := strings.ReplaceAll(raw, `\`, "")
			if !strings.Contains(line, first) || !strings.Contains(line, last) {
				continue
			}
			checked++
			rest := line
			for _, label := range labels {
				idx := strings.Index(rest, label)
				if idx < 0 {
					t.Errorf("%s:%d asserts live-import -approve's summary line but is missing the %q column:\n  %s\n\n"+
						"The line the view renders today is:\n  %s",
						script, i+1, label, strings.TrimSpace(raw), renderStampSummary(t))
					break
				}
				rest = rest[idx+len(label):]
			}
		}
	}
	if checked < 20 {
		t.Errorf("only %d script line(s) assert the whole summary line; at least 20 did when this guard was written - "+
			"if assertions were deliberately removed, lower this floor and say why", checked)
	}
}
