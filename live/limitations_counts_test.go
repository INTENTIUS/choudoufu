// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// live/LIMITATIONS.md holds two markdown tables and, underneath each one, a
// sentence stating how many rows it has: "**234 refusals**" under the refusal
// table and "**29 lint rules**" under the lint roster. Both the rows and the
// sentence are written by tools/limits-gen, and both live inside a single
// generated span, so regenerating the document always produces a figure that
// agrees with the rows it stands over.
//
// Merging does not. GitHub issue #1228: a rebase brought the file through
// saying 233 refusals over 234 rows. Main had added a refusal, git auto-merged
// the new table row into the span with no conflict, and the count sentence -
// a different line, far away in the same span - came through from the branch.
// No conflict, no warning, and a published document telling a reader a number
// its own table contradicts.
//
// That is the hazard CLAUDE.md records for live/gauntlet.json, reaching a file
// nobody had classified as a measured artifact: derived aggregates and the
// rows they summarise, in one file, merged line by line.
//
// tools/limits-gen's TestSpansAreCurrent would notice too, but only by
// re-rendering from the registries - it answers "does this document match the
// code", which is a different question with different ways of being
// unavailable. This one answers "does this document agree with itself", reads
// nothing but the committed bytes, and names both numbers when they disagree.
//
// The residue spans further down the same file (residue-hard-count and the
// two beside it) are single figures over an artifact rather than over rows in
// this document; tools/limits-gen's TestResidueCountsAgreeWithAnIndependentRead
// is what holds those.

const limitationsPath = "live/LIMITATIONS.md"

// TestLimitationsCountsAgreeWithTheirRows is the guard. Every stated figure in
// live/LIMITATIONS.md that stands for a number of rows in live/LIMITATIONS.md
// must equal the number of rows actually there.
func TestLimitationsCountsAgreeWithTheirRows(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(limitationsPath)))
	if err != nil {
		t.Fatalf("reading %s: %v", limitationsPath, err)
	}

	findings, err := checkLimitationsCounts(string(src))
	if err != nil {
		// The document changed shape under the extraction. That is a broken
		// check rather than a passing one, so it fails rather than skips.
		t.Fatalf("%s: %v", limitationsPath, err)
	}
	for _, f := range findings {
		t.Errorf("%s: %s", limitationsPath, f)
	}
	if len(findings) > 0 {
		t.Logf("a figure and its own rows disagree. This is usually a merge that took the rows " +
			"from one side and the sentence from the other (#1228), not a bad edit: re-run " +
			"`just limits` and commit the result rather than hand-patching the number.")
	}
}

// countFinding is one stated figure that disagrees with what it counts.
type countFinding struct {
	span   string // the limits-gen span the figure lives in
	phrase string // the figure as written, quoted back so it can be searched for
	stated int
	actual int
	counts string // what actual counted
}

func (f countFinding) String() string {
	return fmt.Sprintf("the %q span says %q, but %s is %d", f.span, f.phrase, f.counts, f.actual)
}

var (
	// "**234 refusals**, from every registry the live path has: ..."
	reRefusalTotal = regexp.MustCompile(`\*\*(\d+) refusals\*\*`)
	// "**29 lint rules**, from `internal/live/lint`'s own rule table."
	reLintTotal = regexp.MustCompile(`\*\*(\d+) lint rules\*\*`)
	// "... checked to exist when this table was rendered; 26 of the 29 rules have one."
	reWithFixtures = regexp.MustCompile(`(\d+) of the (\d+) rules have one`)
	// "The remaining 3 cite `live/RECEIPTS.md` ..."
	reRemaining = regexp.MustCompile(`The remaining (\d+) cite`)
)

// checkLimitationsCounts reports every figure in the document that disagrees
// with the rows it stands over.
//
// It is a pure function of the document text on purpose: it is what
// TestLimitationsCountsPlantedDisagreementIsCaught drives with a planted
// disagreement, so the guard is proven able to fail without anybody editing
// the real file to find out.
//
// An error means the extraction found nothing to check - a span or a sentence
// that moved, or a table with no rows. That is never a pass.
func checkLimitationsCounts(doc string) ([]countFinding, error) {
	var findings []countFinding

	refusals, err := limitsGenSpan(doc, "refusal-table")
	if err != nil {
		return nil, err
	}
	refusalRows, err := markdownRows(refusals, "refusal-table")
	if err != nil {
		return nil, err
	}
	stated, phrase, err := statedFigure(refusals, reRefusalTotal, "refusal-table", `**N refusals**`)
	if err != nil {
		return nil, err
	}
	if stated != len(refusalRows) {
		findings = append(findings, countFinding{
			span: "refusal-table", phrase: phrase, stated: stated,
			actual: len(refusalRows), counts: "the table under it has rows, and that count",
		})
	}

	roster, err := limitsGenSpan(doc, "lint-roster")
	if err != nil {
		return nil, err
	}
	rosterRows, err := markdownRows(roster, "lint-roster")
	if err != nil {
		return nil, err
	}
	withFixture := 0
	for _, row := range rosterRows {
		// The Fixture column is last, and limits-gen writes a literal "none"
		// for a rule whose documentation lives outside this file.
		if strings.TrimSpace(row[len(row)-1]) != "none" {
			withFixture++
		}
	}

	stated, phrase, err = statedFigure(roster, reLintTotal, "lint-roster", `**N lint rules**`)
	if err != nil {
		return nil, err
	}
	if stated != len(rosterRows) {
		findings = append(findings, countFinding{
			span: "lint-roster", phrase: phrase, stated: stated,
			actual: len(rosterRows), counts: "the table under it has rows, and that count",
		})
	}

	// The same sentence restates the total and splits it: "26 of the 29 rules
	// have one" and "The remaining 3 cite". All three have to hold, and they
	// are the halves most likely to survive a merge that replaced the total.
	split := reWithFixtures.FindStringSubmatch(roster)
	if split == nil {
		return nil, fmt.Errorf(`the "lint-roster" span no longer says "N of the M rules have one"; the extraction is stale, not the document`)
	}
	if total := atoi(split[2]); total != len(rosterRows) {
		findings = append(findings, countFinding{
			span: "lint-roster", phrase: split[0], stated: total,
			actual: len(rosterRows), counts: "the table under it has rows, and that count",
		})
	}
	if got := atoi(split[1]); got != withFixture {
		findings = append(findings, countFinding{
			span: "lint-roster", phrase: split[0], stated: got,
			actual: withFixture, counts: "the number of rows whose Fixture column is not `none`",
		})
	}

	remaining := reRemaining.FindStringSubmatch(roster)
	if remaining == nil {
		return nil, fmt.Errorf(`the "lint-roster" span no longer says "The remaining N cite"; the extraction is stale, not the document`)
	}
	if got, want := atoi(remaining[1]), len(rosterRows)-withFixture; got != want {
		findings = append(findings, countFinding{
			span: "lint-roster", phrase: remaining[0], stated: got,
			actual: want, counts: "the number of rows whose Fixture column is `none`",
		})
	}

	return findings, nil
}

// limitsGenSpan returns what lies between one limits-gen marker pair.
func limitsGenSpan(doc, name string) (string, error) {
	begin := "<!-- limits-gen:begin " + name + " -->"
	end := "<!-- limits-gen:end " + name + " -->"
	i := strings.Index(doc, begin)
	if i < 0 {
		return "", fmt.Errorf("no %q marker; the span was renamed or removed, so nothing was checked", begin)
	}
	if strings.Contains(doc[i+len(begin):], begin) {
		return "", fmt.Errorf("%q appears more than once; a duplicated marker pair means one copy is unchecked", begin)
	}
	rest := doc[i+len(begin):]
	j := strings.Index(rest, end)
	if j < 0 {
		return "", fmt.Errorf("%q has no closing %q", begin, end)
	}
	return rest[:j], nil
}

// markdownRows returns the body rows of the one markdown table in a span,
// each split into its cells. The header row and the |---| rule are dropped.
func markdownRows(span, name string) ([][]string, error) {
	var rows [][]string
	sawHeader, sawRule := false, false
	for _, line := range strings.Split(span, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		if !sawHeader {
			sawHeader = true
			continue
		}
		if !sawRule {
			if !strings.HasPrefix(line, "|---") {
				return nil, fmt.Errorf("the %q span's second table line is %q, not a |---| rule; the table's shape changed under this check", name, line)
			}
			sawRule = true
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		rows = append(rows, cells)
	}
	if !sawRule {
		return nil, fmt.Errorf("the %q span has no markdown table any more; nothing was counted", name)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("the %q span's table has no rows; an empty table would make any stated figure but zero wrong, so this is a broken read", name)
	}
	return rows, nil
}

// statedFigure pulls the single figure a pattern names out of a span.
func statedFigure(span string, re *regexp.Regexp, name, shape string) (int, string, error) {
	all := re.FindAllStringSubmatch(span, -1)
	switch len(all) {
	case 0:
		return 0, "", fmt.Errorf("the %q span no longer carries a %s sentence; the extraction is stale, not the document", name, shape)
	case 1:
		return atoi(all[0][1]), all[0][0], nil
	default:
		return 0, "", fmt.Errorf("the %q span carries %d %s sentences; this check would only have read the first", name, len(all), shape)
	}
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		// Unreachable: every call site passes a \d+ capture.
		panic(err)
	}
	return n
}

// TestLimitationsCountsPlantedDisagreementIsCaught is the red arm. It plants
// the exact #1228 shape - the rows from one side of a merge, the sentence from
// the other - into a copy of the real document and requires the checker to
// name both numbers.
//
// A guard written from the document it is guarding passes forever. This is
// what makes it fail on purpose, on every run, without anybody editing
// live/LIMITATIONS.md to find out.
func TestLimitationsCountsPlantedDisagreementIsCaught(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(limitationsPath)))
	if err != nil {
		t.Fatalf("reading %s: %v", limitationsPath, err)
	}
	doc := string(src)

	// The real figures, so the planted ones are wrong relative to today's
	// document rather than to a number written here and left to rot.
	refusals, err := limitsGenSpan(doc, "refusal-table")
	if err != nil {
		t.Fatalf("%v", err)
	}
	refusalRows, err := markdownRows(refusals, "refusal-table")
	if err != nil {
		t.Fatalf("%v", err)
	}
	roster, err := limitsGenSpan(doc, "lint-roster")
	if err != nil {
		t.Fatalf("%v", err)
	}
	rosterRows, err := markdownRows(roster, "lint-roster")
	if err != nil {
		t.Fatalf("%v", err)
	}

	// The last refusal row, put back together exactly as it is written, so one
	// arm can delete a real row rather than a rewritten one.
	lastRefusalRow := "|" + strings.Join(refusalRows[len(refusalRows)-1], "|") + "|\n"
	if !strings.Contains(doc, lastRefusalRow) {
		t.Fatalf("could not reconstruct the last refusal row from its cells: %q", lastRefusalRow)
	}

	for _, tc := range []struct {
		name       string
		mutate     func(string) string
		wantStated int
		wantActual int
	}{
		{
			// #1228 exactly: main added a row, the branch's sentence survived.
			name: "the refusal total is one behind its rows",
			mutate: func(d string) string {
				return strings.Replace(d,
					fmt.Sprintf("**%d refusals**", len(refusalRows)),
					fmt.Sprintf("**%d refusals**", len(refusalRows)-1), 1)
			},
			wantStated: len(refusalRows) - 1,
			wantActual: len(refusalRows),
		},
		{
			// The other direction: a row lost to a merge that kept the total.
			name: "a refusal row went missing under an unchanged total",
			mutate: func(d string) string {
				return strings.Replace(d, lastRefusalRow, "", 1)
			},
			wantStated: len(refusalRows),
			wantActual: len(refusalRows) - 1,
		},
		{
			name: "the lint-rule total is one ahead of its rows",
			mutate: func(d string) string {
				return strings.Replace(d,
					fmt.Sprintf("**%d lint rules**", len(rosterRows)),
					fmt.Sprintf("**%d lint rules**", len(rosterRows)+1), 1)
			},
			wantStated: len(rosterRows) + 1,
			wantActual: len(rosterRows),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := tc.mutate(doc)
			if mutated == doc {
				t.Fatalf("the mutation changed nothing, so this arm proves nothing")
			}
			findings, err := checkLimitationsCounts(mutated)
			if err != nil {
				t.Fatalf("the checker errored instead of reporting a disagreement: %v", err)
			}
			if len(findings) == 0 {
				t.Fatalf("the checker passed a document whose figure and rows disagree; it cannot fail, so it proves nothing green")
			}
			found := false
			for _, f := range findings {
				if f.stated == tc.wantStated && f.actual == tc.wantActual {
					found = true
				}
			}
			if !found {
				t.Fatalf("no finding named both numbers (stated %d, actual %d); got %v", tc.wantStated, tc.wantActual, findings)
			}
			t.Logf("red as required: %v", findings)
		})
	}
}

// TestLimitationsCountsCheckerRefusesABrokenRead pins the other failure mode:
// a span or a sentence that moves must fail the guard, not silently leave it
// with nothing to check. A check that passes an empty read is not a check.
func TestLimitationsCountsCheckerRefusesABrokenRead(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(limitationsPath)))
	if err != nil {
		t.Fatalf("reading %s: %v", limitationsPath, err)
	}
	doc := string(src)

	for _, tc := range []struct {
		name   string
		mutate func(string) string
	}{
		{"the refusal-table span is renamed", func(d string) string {
			return strings.ReplaceAll(d, "limits-gen:begin refusal-table", "limits-gen:begin refusal-index")
		}},
		{"the lint-roster span is renamed", func(d string) string {
			return strings.ReplaceAll(d, "limits-gen:begin lint-roster", "limits-gen:begin rule-roster")
		}},
		{"the refusal total sentence is reworded", func(d string) string {
			return strings.Replace(d, " refusals**", " refusal rows**", 1)
		}},
		{"the fixture split sentence is reworded", func(d string) string {
			return reWithFixtures.ReplaceAllString(d, "$1 of the $2 rules carry one")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := tc.mutate(doc)
			if mutated == doc {
				t.Fatalf("the mutation changed nothing, so this arm proves nothing")
			}
			if _, err := checkLimitationsCounts(mutated); err == nil {
				t.Fatalf("the checker reported no error on a document it can no longer read; it would pass by finding nothing")
			} else {
				t.Logf("refused as required: %v", err)
			}
		})
	}
}
