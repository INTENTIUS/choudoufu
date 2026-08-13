// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Render mode (issue #25, increment 3): `go run ./tools/survey-gen -render`
// rewrites the derivable spans of live/SURVEY.md in place, between HTML
// comment markers, from two committed inputs - live/survey.json for the
// raw-signal counts and SURVEY.md's own per-type table for the summary
// tally. No provider and no network: rendering moves numbers out of
// transcription, not out of the survey.
//
// Only the numbers are rendered. The per-type table and every line of prose
// carry hand judgment and stay hand-written; the diff tests in
// survey_gen_test.go are what hold them to the artifact.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The two rendered spans. Each lives in SURVEY.md between a
// `<!-- survey-gen:begin NAME -->` line and a `<!-- survey-gen:end NAME -->`
// line; the renderer replaces everything between the pair and touches
// nothing outside it.
const (
	// spanRawSignals is the "Raw signals" counts sentence, rendered from
	// live/survey.json's counts block.
	spanRawSignals = "raw-signals"

	// spanSummary is the Summary path-count table, tallied from the
	// per-type table's own Path column (modulo summaryOverrides below).
	spanSummary = "summary"
)

// summaryOverrides pins the rows the Summary table counts under a different
// path than the one the per-type table shows. Both facts are SURVEY.md's
// own: the table shows the path the fork implements, the summary keeps the
// survey's original classing, and the file's "classification wrinkles"
// prose records each divergence. This map is that prose made mechanical, so
// the tally reproduces the survey's counts instead of quietly restyling
// them; a row leaving the wrinkle list should leave here in the same
// change.
var summaryOverrides = map[string]struct {
	counted string
	reason  string
}{
	"aws_iam_role_policy_attachment": {pathClientNamed,
		"the table groups it structurally as parent-derived, but both components are client-named strings and the survey counted it under client-named"},
	"aws_eip": {pathMarker,
		"taggable, so the survey's strongest-path classing is marker; the table shows the fork's list-plus-content wiring"},
}

// runRender is the -render entry point: read the committed artifacts and
// the committed docs, replace the marked spans, write the docs back. Two
// docs are rendered this way: live/SURVEY.md (this function's original
// scope) and live/LIMITATIONS.md's residue-roster spans (issue #49,
// renderResidueRoster in residue_render.go), both from committed JSON with
// no provider and no network.
func runRender() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	if err := renderSurveyMD(root); err != nil {
		return err
	}
	return renderLimitationsMD(root)
}

// renderSurveyMD rewrites live/SURVEY.md's raw-signals and summary spans
// from the committed live/survey.json and the doc's own per-type table.
func renderSurveyMD(root string) error {
	data, err := os.ReadFile(filepath.Join(root, surveyJSONRel)) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s (regenerate with `go run ./tools/survey-gen`): %w", surveyJSONRel, err)
	}
	var survey Survey
	if err := json.Unmarshal(data, &survey); err != nil {
		return fmt.Errorf("decoding %s: %w", surveyJSONRel, err)
	}

	mdPath := filepath.Join(root, surveyMDRel)
	md, err := os.ReadFile(mdPath) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return err
	}
	rows, err := readRoster(mdPath)
	if err != nil {
		return fmt.Errorf("parsing %s's per-type table: %w", surveyMDRel, err)
	}

	out, err := renderSpans(string(md), survey, rows)
	if err != nil {
		return err
	}
	if out == string(md) {
		fmt.Fprintf(os.Stderr, "survey-gen: %s's rendered spans are already current\n", surveyMDRel)
		return nil
	}
	if err := os.WriteFile(mdPath, []byte(out), 0o644); err != nil { //nolint:gosec // a committed doc, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr, "survey-gen: rewrote the %s and %s spans of %s\n", spanRawSignals, spanSummary, surveyMDRel)
	return nil
}

// renderSpans returns the doc with both marked spans replaced by their
// rendered bodies. The rest of the file passes through byte-for-byte.
func renderSpans(md string, survey Survey, rows []HandRow) (string, error) {
	md, err := replaceSpan(surveyMDRel, md, spanRawSignals, renderRawSignals(survey.Counts))
	if err != nil {
		return "", err
	}
	return replaceSpan(surveyMDRel, md, spanSummary, renderSummary(rows))
}

// renderRawSignals is the "Raw signals" headline sentence, in the exact
// shape survey_gen_test.go's headlineRe pins (the line break before "have"
// keeps the doc's own wrap width).
func renderRawSignals(c Counts) string {
	return fmt.Sprintf("On the %d curated types: %d are taggable, %d have native list resources, %d\nhave provider identity schemas.\n",
		c.Types, c.Taggable, c.ListResource, c.IdentitySchema)
}

// renderSummary tallies the per-type table's Path column into the Summary
// table, applying the two readings SURVEY.md's own prose asserts: an
// account-derived row is a refinement of client-named and counts there, and
// the summaryOverrides rows count under the survey's classing rather than
// the table's. The residue row is the remainder, held visible so a token
// ever escaping the tally shows up as a nonzero residue instead of a
// silently shrunken total.
func renderSummary(rows []HandRow) string {
	counts := map[string]int{}
	for _, r := range rows {
		path := r.Path
		if o, ok := summaryOverrides[r.Type]; ok {
			path = o.counted
		}
		if path == pathAccountDerived {
			path = pathClientNamed
		}
		counts[path]++
	}
	residue := len(rows) - counts[pathClientNamed] - counts[pathMarker] -
		counts[pathParentDerived] - counts[pathListContent] - counts[pathOps]

	var b strings.Builder
	b.WriteString("| Path | Count |\n")
	b.WriteString("|---|---|\n")
	fmt.Fprintf(&b, "| Client-named identity | %d |\n", counts[pathClientNamed])
	fmt.Fprintf(&b, "| Marker (tags) | %d |\n", counts[pathMarker])
	fmt.Fprintf(&b, "| Parent-derived | %d |\n", counts[pathParentDerived])
	fmt.Fprintf(&b, "| List + content match | %d |\n", counts[pathListContent])
	fmt.Fprintf(&b, "| Moves to Ops (excluded by the rule) | %d |\n", counts[pathOps])
	fmt.Fprintf(&b, "| Residue needing a store | %d |\n", residue)
	return b.String()
}

// spanMarkers returns the begin and end marker lines for a named span. The
// marker comment is "survey-gen" regardless of which doc it lives in - one
// tool, one comment vocabulary, across every doc it renders spans into.
func spanMarkers(name string) (begin, end string) {
	return fmt.Sprintf("<!-- survey-gen:begin %s -->\n", name),
		fmt.Sprintf("<!-- survey-gen:end %s -->", name)
}

// replaceSpan swaps the text between a span's markers for body. Exactly one
// begin and one end marker must exist, in order; anything else is an error
// rather than a guess, because the renderer writes the doc in place. docRel
// names the doc in error messages only; it does not affect which bytes are
// replaced.
func replaceSpan(docRel, md, name, body string) (string, error) {
	begin, end := spanMarkers(name)
	if n := strings.Count(md, begin); n != 1 {
		return "", fmt.Errorf("%s: expected exactly one %q marker, found %d", docRel, strings.TrimSpace(begin), n)
	}
	if n := strings.Count(md, end); n != 1 {
		return "", fmt.Errorf("%s: expected exactly one %q marker, found %d", docRel, end, n)
	}
	i := strings.Index(md, begin) + len(begin)
	j := strings.Index(md, end)
	if j < i {
		return "", fmt.Errorf("%s: the %q end marker precedes its begin marker", docRel, name)
	}
	return md[:i] + body + md[j:], nil
}

// spanContent extracts the committed text between a span's markers, for the
// drift test's per-span message. See [replaceSpan] for docRel.
func spanContent(docRel, md, name string) (string, error) {
	begin, end := spanMarkers(name)
	if n := strings.Count(md, begin); n != 1 {
		return "", fmt.Errorf("%s: expected exactly one %q marker, found %d", docRel, strings.TrimSpace(begin), n)
	}
	if n := strings.Count(md, end); n != 1 {
		return "", fmt.Errorf("%s: expected exactly one %q marker, found %d", docRel, end, n)
	}
	i := strings.Index(md, begin) + len(begin)
	j := strings.Index(md, end)
	if j < i {
		return "", fmt.Errorf("%s: the %q end marker precedes its begin marker", docRel, name)
	}
	return md[i:j], nil
}
