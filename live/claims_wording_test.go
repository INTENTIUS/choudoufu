// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A claim holds the promise and the proof that it works, nothing else. What
// is missing, untested or out of scope is a GitHub issue that links the
// claim, never a sentence on the claim. TestClaimWordingIsProofOnly holds
// every place claim text or claim status is written to that: the table, the
// claim pages, the text of claims.json and the site's proof pages and
// figures.
//
// Proving it red: put "open" back as a cell's status in claims.json, or add
// "This is not measured on real AWS." to a claim page.

// claimHedge matches a hedging term as a whole word, case-insensitively.
var claimHedge = regexp.MustCompile(`(?i)\b(unproven|not proven|not measured|unmeasured|not yet|has not|hasn['’]t|have not|open|stale|measured before|drafts?|restated|weaker|pending|tbd|caveats?|limitations?|cannot yet|no dated run|unknown)\b`)

// claimWordingMarkdown are the claim pages and the table, relative to live/.
func claimWordingMarkdown(t *testing.T) []string {
	t.Helper()
	pages, err := filepath.Glob(filepath.Join(siteClaimsPages, "*.md"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no claim pages under %s (%v); this test is checking nothing", siteClaimsPages, err)
	}
	sort.Strings(pages)
	return append([]string{"CLAIMS.md", "../site/src/content/docs/aws/proof.mdx", "../site/src/content/docs/kubernetes/proof.mdx"}, pages...)
}

// claimWordingSource are the site files that compute and print the claim
// and estate figures; every line of them is read, code and strings alike.
var claimWordingSource = []string{
	"../site/src/data/proof.ts",
	"../site/src/components/Landing.astro",
	"../site/src/components/Picker.astro",
	"../site/src/components/Room.astro",
}

var (
	inlineCode = regexp.MustCompile("`[^`]*`")
	linkTarget = regexp.MustCompile(`\]\([^)]*\)`)
)

// markdownHedges returns file:line findings in a markdown page's prose:
// code spans, fenced and indented code blocks and link targets are names,
// not wording, and are skipped.
func markdownHedges(path string, raw []byte) []string {
	var out []string
	fenced := false
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue
		}
		prose := linkTarget.ReplaceAllString(inlineCode.ReplaceAllString(line, ""), "]")
		for _, m := range claimHedge.FindAllString(prose, -1) {
			out = append(out, fmt.Sprintf("%s:%d: %q in %q", path, i+1, m, strings.TrimSpace(line)))
		}
	}
	return out
}

// sourceHedges returns file:line findings anywhere in a source file.
func sourceHedges(path string, raw []byte) []string {
	var out []string
	for i, line := range strings.Split(string(raw), "\n") {
		for _, m := range claimHedge.FindAllString(line, -1) {
			out = append(out, fmt.Sprintf("%s:%d: %q in %q", path, i+1, m, strings.TrimSpace(line)))
		}
	}
	return out
}

// claimsJSONHedges returns file:line findings in claims.json's string
// values, the statuses included. A value is located by the first line of
// the file that carries it, encoded as JSON.
func claimsJSONHedges(path string, raw []byte) ([]string, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	lineOf := func(s string) int {
		var b strings.Builder
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		_ = e.Encode(s)
		needle := strings.TrimSpace(b.String())
		for i, l := range lines {
			if strings.Contains(l, needle) {
				return i + 1
			}
		}
		return 0
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(v[k])
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		case string:
			prose := inlineCode.ReplaceAllString(v, "")
			for _, m := range claimHedge.FindAllString(prose, -1) {
				out = append(out, fmt.Sprintf("%s:%d: %q in %q", path, lineOf(v), m, around(prose, m)))
			}
		}
	}
	walk(doc)
	return out, nil
}

// around is the stretch of a long value either side of a finding.
func around(s, m string) string {
	i := strings.Index(s, m)
	lo, hi := max(0, i-60), min(len(s), i+len(m)+60)
	return s[lo:hi]
}

func claimWordingFindings(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, p := range claimWordingMarkdown(t) {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, markdownHedges(p, raw)...)
	}
	for _, p := range claimWordingSource {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, sourceHedges(p, raw)...)
	}
	raw, err := os.ReadFile(smokeClaimsPath)
	if err != nil {
		t.Fatal(err)
	}
	found, err := claimsJSONHedges(smokeClaimsPath, raw)
	if err != nil {
		t.Fatalf("decode %s: %v", smokeClaimsPath, err)
	}
	return append(out, found...)
}

// TestClaimWordingIsProofOnly: no claim text questions its claim.
func TestClaimWordingIsProofOnly(t *testing.T) {
	for _, f := range claimWordingFindings(t) {
		t.Errorf("%s; a claim states its promise and its proof, and anything missing goes in a GitHub issue", f)
	}
}

// TestClaimHedgeCatchesHedging is the lint's own red arm: each sample must
// be caught, and each name the scan skips must pass.
func TestClaimHedgeCatchesHedging(t *testing.T) {
	caught := []string{
		"This is not measured on real AWS.",
		"| 47 | x | open |",
		"proven, stale: measured before v0.30.0",
		"It hasn't run.",
		"The proof is a draft.",
		"no dated run",
	}
	for _, s := range caught {
		if len(markdownHedges("sample.md", []byte(s))) == 0 {
			t.Errorf("%q passes the lint", s)
		}
	}
	if got, _ := claimsJSONHedges("sample.json", []byte(`{"status":"unproven"}`)); len(got) == 0 {
		t.Errorf(`status "unproven" in claims.json passes the lint`)
	}
	passed := []string{
		"### staleness-costs-reads",
		"    just smoke k8s-staleness-costs-reads",
		"Prints `Cannot open the record store` and stops.",
		"[claim 3](staleness-costs-reads.md#on-aws)",
	}
	for _, s := range passed {
		if got := markdownHedges("sample.md", []byte(s)); len(got) != 0 {
			t.Errorf("%q is a name, not wording, and the lint flags it: %v", s, got)
		}
	}
}
