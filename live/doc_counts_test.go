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
	"sort"
	"strconv"
	"strings"
	"testing"
)

// #1249 is #1228's sibling sweep: four more documents state a figure over
// rows in the same document, and one of them (live/SURVEY.md's Status
// vocabulary) was already wrong when the sweep found it. Each check below is
// a pure function of the committed bytes, like checkLimitationsCounts, so a
// merge that takes the rows from one side and the figure from the other
// fails here even when every generator would re-render cleanly. Each has a
// planted arm in TestDocCountsPlantedDisagreementIsCaught.
//
// live/GAUNTLET.md's stage count is held in tools/gauntlet
// (spec_figures_test.go), next to the renderer that now derives it.

const (
	surveyMDPath    = "live/SURVEY.md"
	coverageMDPath  = "live/COVERAGE.md"
	referenceMDPath = "site/content/docs/use/reference.md"
	handoffMDPath   = "HANDOFF.md"
)

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(src)
}

// TestDocCountsAgreeWithTheirRows is the guard over all four documents.
func TestDocCountsAgreeWithTheirRows(t *testing.T) {
	for _, c := range docCountChecks {
		t.Run(c.path, func(t *testing.T) {
			findings, err := c.check(readDoc(t, c.path))
			if err != nil {
				t.Fatalf("%s: %v", c.path, err)
			}
			for _, f := range findings {
				t.Errorf("%s: %s", c.path, f)
			}
		})
	}
}

var docCountChecks = []struct {
	path  string
	check func(string) ([]string, error)
}{
	{surveyMDPath, checkSurveyStatusRows},
	{coverageMDPath, checkCoverageContractCount},
	{referenceMDPath, checkReferenceTagVerbs},
	{handoffMDPath, checkHandoffDifferenceRows},
}

// span returns what lies between one generator's marker pair, block or
// inline alike (the begin marker is matched without its trailing newline).
func span(doc, tool, name string) (string, error) {
	begin := "<!-- " + tool + ":begin " + name + " -->"
	end := "<!-- " + tool + ":end " + name + " -->"
	if n := strings.Count(doc, begin); n != 1 {
		return "", fmt.Errorf("expected exactly one %q, found %d; nothing was checked", begin, n)
	}
	rest := doc[strings.Index(doc, begin)+len(begin):]
	j := strings.Index(rest, end)
	if j < 0 {
		return "", fmt.Errorf("%q has no closing %q", begin, end)
	}
	return rest[:j], nil
}

var reHTMLComment = regexp.MustCompile(`<!--.*?-->`)

// --- live/SURVEY.md -------------------------------------------------------

// checkSurveyStatusRows holds the Status vocabulary table's `Rows below`
// column to a tally of the per-type table's Status column, and requires every
// Status token the per-type table uses to have a vocabulary row: the document
// says "nothing outside them appears in those columns", and a token outside
// the vocabulary would be dropped by anything matching on it.
func checkSurveyStatusRows(doc string) ([]string, error) {
	tally := map[string]int{}
	perType := 0
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "| aws_") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 5 {
			return nil, fmt.Errorf("per-type row with %d cells: %s", len(cells), line)
		}
		tally[strings.TrimSpace(cells[2])]++
		perType++
	}
	if perType == 0 {
		return nil, fmt.Errorf("no per-type rows found; the table moved, so nothing was counted")
	}

	vocab := map[string]int{}
	var order []string
	inTable := false
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "| Status | Meaning |") {
			if inTable || len(order) > 0 {
				return nil, fmt.Errorf("more than one Status vocabulary table; this check would read only one")
			}
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if !strings.HasPrefix(trimmed, "|") {
			inTable = false
			continue
		}
		if strings.HasPrefix(trimmed, "|---") {
			continue
		}
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		token := strings.Trim(strings.TrimSpace(cells[0]), "`")
		cell := strings.TrimSpace(reHTMLComment.ReplaceAllString(cells[len(cells)-1], ""))
		n, err := strconv.Atoi(cell)
		if err != nil {
			return nil, fmt.Errorf("the Status vocabulary row for %q has a Rows cell %q that is not a number", token, cells[len(cells)-1])
		}
		vocab[token] = n
		order = append(order, token)
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("no Status vocabulary table (a `| Status | Meaning |` header); nothing was checked")
	}

	var findings []string
	for _, token := range order {
		if vocab[token] != tally[token] {
			findings = append(findings, fmt.Sprintf("the Status vocabulary says `%s` has %d rows, but the per-type table has %d", token, vocab[token], tally[token]))
		}
	}
	var missing []string
	for token := range tally {
		if _, ok := vocab[token]; !ok {
			missing = append(missing, token)
		}
	}
	sort.Strings(missing)
	for _, token := range missing {
		findings = append(findings, fmt.Sprintf("the per-type table uses Status `%s` in %d rows, and the Status vocabulary has no row for it", token, tally[token]))
	}
	return findings, nil
}

// --- live/COVERAGE.md -----------------------------------------------------

var reBacktickedType = regexp.MustCompile("`([a-z0-9_]+)`")

// checkCoverageContractCount holds the contract-count span to the number of
// distinct type names in the contract-types span. The two are separate spans
// hundreds of lines apart, so a merge can split them without touching a
// marker.
func checkCoverageContractCount(doc string) ([]string, error) {
	countSpan, err := span(doc, "survey-gen", "contract-count")
	if err != nil {
		return nil, err
	}
	stated, err := strconv.Atoi(strings.TrimSpace(countSpan))
	if err != nil {
		return nil, fmt.Errorf("the contract-count span holds %q, not a number", countSpan)
	}
	typesSpan, err := span(doc, "survey-gen", "contract-types")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, m := range reBacktickedType.FindAllStringSubmatch(typesSpan, -1) {
		seen[m[1]] = true
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("the contract-types span names no types; nothing was counted")
	}
	if stated != len(seen) {
		return []string{fmt.Sprintf("the contract-count span says %d types, but the contract-types span names %d", stated, len(seen))}, nil
	}
	return nil, nil
}

// --- site/content/docs/use/reference.md -----------------------------------

var (
	reTagVerbSplit = regexp.MustCompile(`(\d+) services carry an unambiguous tagging verb\. (\d+) do not`)
	reTagVerbCell  = regexp.MustCompile(`^(\d+)\. (.*?)(?: and (\d+) more)?$`)
)

// checkReferenceTagVerbs holds the tag-verbs-total span to the tag-verbs
// table under it: each action's count to the services it shows, the
// closing sentence's first figure to the sum of those counts, and the total
// to that sum plus the sentence's second figure.
func checkReferenceTagVerbs(doc string) ([]string, error) {
	totalSpan, err := span(doc, "tagverbs-gen", "tag-verbs-total")
	if err != nil {
		return nil, err
	}
	total, err := strconv.Atoi(strings.TrimSpace(totalSpan))
	if err != nil {
		return nil, fmt.Errorf("the tag-verbs-total span holds %q, not a number", totalSpan)
	}
	table, err := span(doc, "tagverbs-gen", "tag-verbs")
	if err != nil {
		return nil, err
	}
	rows, err := markdownRows(table, "tag-verbs")
	if err != nil {
		return nil, err
	}

	var findings []string
	sum := 0
	for _, row := range rows {
		if len(row) != 2 {
			return nil, fmt.Errorf("tag-verbs row with %d cells: %v", len(row), row)
		}
		action := strings.TrimSpace(row[0])
		m := reTagVerbCell.FindStringSubmatch(strings.TrimSpace(row[1]))
		if m == nil {
			return nil, fmt.Errorf("the %s row's Services cell %q is not \"N. names\"", action, row[1])
		}
		n := atoi(m[1])
		shown := len(strings.Split(m[2], ", "))
		if m[3] != "" {
			shown += atoi(m[3])
		}
		if n != shown {
			findings = append(findings, fmt.Sprintf("the %s row says %d services and lists %d", action, n, shown))
		}
		sum += n
	}

	split := reTagVerbSplit.FindStringSubmatch(table)
	if split == nil {
		return nil, fmt.Errorf(`the tag-verbs span no longer says "N services carry an unambiguous tagging verb. M do not"; the extraction is stale`)
	}
	withVerb, noVerb := atoi(split[1]), atoi(split[2])
	if withVerb != sum {
		findings = append(findings, fmt.Sprintf("%q, but the action rows sum to %d", split[0], sum))
	}
	if total != withVerb+noVerb {
		findings = append(findings, fmt.Sprintf("the tag-verbs-total span says %d services, but the table's sentence splits %d + %d = %d", total, withVerb, noVerb, withVerb+noVerb))
	}
	return findings, nil
}

// --- HANDOFF.md -----------------------------------------------------------

var numberWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
	"seven": 7, "eight": 8, "nine": 9, "ten": 10,
}

var (
	reHandoffThings = regexp.MustCompile(`Every difference is one of (\w+) things`)
	reHandoffRows   = regexp.MustCompile(`(?:None of those|which of the) (\w+) rows`)
)

// checkHandoffDifferenceRows holds every "N things" / "the N rows" phrase in
// HANDOFF.md to the number of rows in its `| Difference | Action |` table.
func checkHandoffDifferenceRows(doc string) ([]string, error) {
	i := strings.Index(doc, "| Difference | Action |")
	if i < 0 {
		return nil, fmt.Errorf("no `| Difference | Action |` table; nothing was counted")
	}
	j := strings.Index(doc[i:], "\n\n")
	if j < 0 {
		return nil, fmt.Errorf("the Difference table does not end")
	}
	rows, err := markdownRows(doc[i:i+j], "Difference")
	if err != nil {
		return nil, err
	}

	flat := strings.Join(strings.Fields(doc), " ")
	var phrases [][]string
	phrases = append(phrases, reHandoffThings.FindAllStringSubmatch(flat, -1)...)
	phrases = append(phrases, reHandoffRows.FindAllStringSubmatch(flat, -1)...)
	if len(phrases) == 0 {
		return nil, fmt.Errorf("no phrase stating how many Difference rows there are; the extraction is stale")
	}
	var findings []string
	for _, p := range phrases {
		n, ok := numberWords[strings.ToLower(p[1])]
		if !ok {
			var err error
			if n, err = strconv.Atoi(p[1]); err != nil {
				return nil, fmt.Errorf("%q: %q is not a number this check reads", p[0], p[1])
			}
		}
		if n != len(rows) {
			findings = append(findings, fmt.Sprintf("%q, but the Difference table has %d rows", p[0], len(rows)))
		}
	}
	return findings, nil
}

// --- the red arms ---------------------------------------------------------

// TestDocCountsPlantedDisagreementIsCaught plants one disagreement per
// document into a copy of the committed file and requires a finding. A
// guard written from the document it guards passes forever otherwise.
func TestDocCountsPlantedDisagreementIsCaught(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		check  func(string) ([]string, error)
		mutate func(t *testing.T, doc string) string
	}{
		{
			name: "a per-type row changes Status under an unchanged vocabulary", path: surveyMDPath, check: checkSurveyStatusRows,
			mutate: func(t *testing.T, d string) string {
				return replaceOnce(t, d, "| aws_sns_topic_subscription | parent-derived | markerless |", "| aws_sns_topic_subscription | parent-derived | ops |")
			},
		},
		{
			name: "a per-type row uses a Status with no vocabulary row", path: surveyMDPath, check: checkSurveyStatusRows,
			mutate: func(t *testing.T, d string) string {
				return replaceOnce(t, d, "| aws_sns_topic_subscription | parent-derived | markerless |", "| aws_sns_topic_subscription | parent-derived | deferred |")
			},
		},
		{
			name: "the contract count is one behind its types", path: coverageMDPath, check: checkCoverageContractCount,
			mutate: func(t *testing.T, d string) string {
				s, err := span(d, "survey-gen", "contract-count")
				if err != nil {
					t.Fatal(err)
				}
				n, _ := strconv.Atoi(strings.TrimSpace(s))
				return replaceOnce(t, d, "contract-count -->"+s, "contract-count -->"+strings.Replace(s, strconv.Itoa(n), strconv.Itoa(n-1), 1))
			},
		},
		{
			name: "a contract type went missing under an unchanged count", path: coverageMDPath, check: checkCoverageContractCount,
			mutate: func(t *testing.T, d string) string {
				return replaceOnce(t, d, "`aws_accessanalyzer_archive_rule`,", "")
			},
		},
		{
			name: "the tag-verbs total is one ahead of the table", path: referenceMDPath, check: checkReferenceTagVerbs,
			mutate: func(t *testing.T, d string) string {
				s, err := span(d, "tagverbs-gen", "tag-verbs-total")
				if err != nil {
					t.Fatal(err)
				}
				n, _ := strconv.Atoi(strings.TrimSpace(s))
				return replaceOnce(t, d, "tag-verbs-total -->"+s+"<", "tag-verbs-total -->"+strconv.Itoa(n+1)+"<")
			},
		},
		{
			name: "an action row's count disagrees with its services", path: referenceMDPath, check: checkReferenceTagVerbs,
			mutate: func(t *testing.T, d string) string {
				return replaceOnce(t, d, "| `TagQueue` | 1. SQS |", "| `TagQueue` | 2. SQS |")
			},
		},
		{
			name: "a Difference row is added under the stated five", path: handoffMDPath, check: checkHandoffDifferenceRows,
			mutate: func(t *testing.T, d string) string {
				return replaceOnce(t, d, "| Difference | Action |\n|---|---|\n", "| Difference | Action |\n|---|---|\n| planted | planted |\n")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := readDoc(t, tc.path)
			mutated := tc.mutate(t, doc)
			if mutated == doc {
				t.Fatalf("the mutation changed nothing, so this arm proves nothing")
			}
			findings, err := tc.check(mutated)
			if err != nil {
				t.Fatalf("the checker errored instead of reporting a disagreement: %v", err)
			}
			if len(findings) == 0 {
				t.Fatalf("the checker passed a %s whose figure and rows disagree; it cannot fail", tc.path)
			}
			t.Logf("red as required: %s", strings.Join(findings, "; "))
		})
	}
}

// TestDocCountsCheckerRefusesABrokenRead: a span or table that moves must
// fail the guard rather than leave it with nothing to check.
func TestDocCountsCheckerRefusesABrokenRead(t *testing.T) {
	for _, tc := range []struct {
		name      string
		path      string
		check     func(string) ([]string, error)
		old, repl string
	}{
		{"the Status vocabulary header is reworded", surveyMDPath, checkSurveyStatusRows, "| Status | Meaning |", "| Token | Meaning |"},
		{"the contract-count span is renamed", coverageMDPath, checkCoverageContractCount, "survey-gen:begin contract-count", "survey-gen:begin admitted-count"},
		{"the tag-verbs sentence is reworded", referenceMDPath, checkReferenceTagVerbs, "services carry an unambiguous tagging verb", "services have one tagging verb"},
		{"the Difference table header is reworded", handoffMDPath, checkHandoffDifferenceRows, "| Difference | Action |", "| Case | Action |"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := readDoc(t, tc.path)
			mutated := replaceOnce(t, doc, tc.old, tc.repl)
			if _, err := tc.check(mutated); err == nil {
				t.Fatalf("the checker reported no error on a %s it can no longer read", tc.path)
			} else {
				t.Logf("refused as required: %v", err)
			}
		})
	}
}

func replaceOnce(t *testing.T, doc, old, repl string) string {
	t.Helper()
	if n := strings.Count(doc, old); n != 1 {
		t.Fatalf("expected exactly one %q to plant into, found %d", old, n)
	}
	return strings.Replace(doc, old, repl, 1)
}
