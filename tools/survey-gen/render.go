// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Render mode (issue #25, increment 3): `go run ./tools/survey-gen -render`
// rewrites the derivable spans of live/SURVEY.md in place, between HTML
// comment markers, from committed inputs - live/survey.json for the
// raw-signal counts, SURVEY.md's own per-type table for the summary tally,
// and the compiled admission table (identity.AdmittedTypes) for the wired
// count (issue #54). No provider and no network: rendering moves numbers
// out of transcription, not out of the survey.
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

	"github.com/intentius/choudoufu/internal/live/identity"
)

// The three rendered spans. Each lives in SURVEY.md between a
// `<!-- survey-gen:begin NAME -->` marker and a `<!-- survey-gen:end NAME -->`
// marker (a block span's begin marker ends its line; an inline span's does
// not, see inlineSpanMarkers below); the renderer replaces everything
// between the pair and touches nothing outside it.
const (
	// spanRawSignals is the "Raw signals" counts sentence, rendered from
	// live/survey.json's counts block.
	spanRawSignals = "raw-signals"

	// spanSummary is the Summary path-count table, tallied from the
	// per-type table's own Path column (modulo summaryOverrides below).
	spanSummary = "summary"

	// spanWiredCount is the Status table's `wired` row count: the admission
	// table's global size (issue #54), not a tally of this file's own rows
	// (identity.AdmittedTypes runs well past the curated 68, on the
	// registry-ratified batches' account). Rendered so a batch that grows
	// the admission table never has to hand-edit this digit.
	spanWiredCount = "wired-count"

	// spanProviderWide is the "Provider-wide" paragraph: the two substrate
	// findings and the trajectory percentages, computed from the
	// already-committed live/survey-full.json rather than typed by hand.
	// Issue #679: three of these four figures (1,691 total / 468
	// identity-schema / 183 list) had drifted from the committed artifact's
	// 1699 / 479 / 195 by the time they were audited, because nothing
	// recomputed them when the provider version moved.
	spanProviderWide = "provider-wide"
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
}

// runRender is the -render entry point: read the committed artifacts and
// the committed docs, replace the marked spans, write the docs back. Three
// docs are rendered this way: live/SURVEY.md (this function's original
// scope, plus its wired-count span, issue #54), live/LIMITATIONS.md's
// residue-roster spans and untaggable-admitted span (issue #49 and #54,
// renderLimitationsMD in residue_render.go and untaggable_render.go), and
// live/COVERAGE.md's admitted-set spans (issue #54,
// renderContractMDX in contract_render.go). All from committed JSON and the
// compiled admission table, with no provider and no network.
func runRender() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	if err := renderSurveyMD(root); err != nil {
		return err
	}
	if err := renderLimitationsMD(root); err != nil {
		return err
	}
	if err := renderMarkersMD(root); err != nil {
		return err
	}
	return renderContractMDX(root)
}

// renderSurveyMD rewrites live/SURVEY.md's raw-signals, summary and
// provider-wide spans from the committed live/survey.json,
// live/survey-full.json and the doc's own per-type table.
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

	fullData, err := os.ReadFile(filepath.Join(root, surveyFullJSONRel)) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s (regenerate with `go run ./tools/survey-gen -all`): %w", surveyFullJSONRel, err)
	}
	var full Survey
	if err := json.Unmarshal(fullData, &full); err != nil {
		return fmt.Errorf("decoding %s: %w", surveyFullJSONRel, err)
	}

	out, err := renderSpans(string(md), survey, full, rows)
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
	fmt.Fprintf(os.Stderr, "survey-gen: rewrote the %s, %s, %s and %s spans of %s\n", spanRawSignals, spanSummary, spanWiredCount, spanProviderWide, surveyMDRel)
	return nil
}

// renderSpans returns the doc with all four marked spans replaced by their
// rendered bodies. The rest of the file passes through byte-for-byte.
func renderSpans(md string, survey, full Survey, rows []HandRow) (string, error) {
	md, err := replaceSpan(surveyMDRel, md, spanRawSignals, renderRawSignals(survey.Counts))
	if err != nil {
		return "", err
	}
	md, err = replaceSpan(surveyMDRel, md, spanSummary, renderSummary(rows))
	if err != nil {
		return "", err
	}
	md, err = replaceSpan(surveyMDRel, md, spanProviderWide, renderProviderWide(full))
	if err != nil {
		return "", err
	}
	// Inline: the wired-count span sits inside a single Markdown table
	// cell, where a literal newline would split the row.
	return replaceSpanInline(surveyMDRel, md, spanWiredCount, renderWiredCount())
}

// renderProviderWide is the "Provider-wide" paragraph, computed from
// live/survey-full.json's own per-type Signals rather than a hand count.
// Percentages are floored (integer division), matching the convention the
// hand-typed prose this replaces already used - re-deriving today's inputs
// against a floor reproduces the figures that prose published before the
// provider roster grew, which is the check that this is the same formula
// and not a new one.
func renderProviderWide(full Survey) string {
	total := len(full.Types)
	var taggable, listResource, identitySchema, tagsOrIdentity int
	for _, r := range full.Types {
		if r.Signals.Taggable {
			taggable++
		}
		if r.Signals.ListResource {
			listResource++
		}
		if r.Signals.IdentitySchema {
			identitySchema++
		}
		if r.Signals.Taggable || r.Signals.IdentitySchema {
			tagsOrIdentity++
		}
	}
	pct := func(n int) int {
		if total == 0 {
			return 0
		}
		return n * 100 / total
	}
	return fmt.Sprintf(
		"Provider-wide, two substrate findings. The provider now publishes\n"+
			"`resource_identity_schemas` for %d types and growing: a per-type\n"+
			"declaration of exactly what identifies the resource, which is the\n"+
			"admission-rule metadata maintained upstream by the provider itself. And %d\n"+
			"native list resources exist already (the query/search work), including\n"+
			"nearly all high-traffic types.\n"+
			"\n"+
			"Global stats across all %d AWS resource types, for trajectory: %d%%\n"+
			"taggable, %d%% identity-schema (mid-rollout), %d%% list (early rollout), %d%%\n"+
			"tags-or-identity today. The long tail thins out, but usage concentrates in\n"+
			"the head, and both identity and list coverage are actively expanding\n"+
			"upstream.\n",
		identitySchema, listResource, total,
		pct(taggable), pct(identitySchema), pct(listResource), pct(tagsOrIdentity),
	)
}

// renderWiredCount is the admission table's global size, straight off
// identity.AdmittedTypes - the same set internal/live/lint/admission.go
// admits and SURVEY.md's Contract-adjacent docs cite. Not a tally of
// SURVEY.md's own per-type table: registry-ratified batches (#40, #44) add
// types this file's provider-schema survey never rostered.
func renderWiredCount() string {
	return fmt.Sprintf("%d", len(identity.AdmittedTypes()))
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
		counts[pathParentDerived] - counts[pathEnumerableUnbindable] -
		counts[pathUniqueName] - counts[pathOps]

	var b strings.Builder
	b.WriteString("| Path | Count |\n")
	b.WriteString("|---|---|\n")
	fmt.Fprintf(&b, "| Client-named identity | %d |\n", counts[pathClientNamed])
	fmt.Fprintf(&b, "| Marker (tags) | %d |\n", counts[pathMarker])
	fmt.Fprintf(&b, "| Parent-derived | %d |\n", counts[pathParentDerived])
	fmt.Fprintf(&b, "| Enumerable, unbindable (no admission path) | %d |\n", counts[pathEnumerableUnbindable])
	fmt.Fprintf(&b, "| Unique name (AWS-enforced, discovery-bound) | %d |\n", counts[pathUniqueName])
	fmt.Fprintf(&b, "| Moves to Ops (excluded by the rule) | %d |\n", counts[pathOps])
	fmt.Fprintf(&b, "| Residue needing a store | %d |\n", residue)
	return b.String()
}

// spanMarkers returns the begin and end marker lines for a named span. The
// marker comment is "survey-gen" regardless of which doc it lives in - one
// tool, one comment vocabulary, across every doc it renders spans into. The
// begin marker's trailing newline is what makes this the block form: the
// rendered body starts on its own line, which is right for a paragraph or a
// table but wrong for a span embedded inside a single table cell or a
// bullet's inline prose - see inlineSpanMarkers for that shape.
func spanMarkers(name string) (begin, end string) {
	return fmt.Sprintf("<!-- survey-gen:begin %s -->\n", name),
		fmt.Sprintf("<!-- survey-gen:end %s -->", name)
}

// inlineSpanMarkers is spanMarkers' sibling for a span that has to stay on
// the same physical line as the text around it - a Markdown table cell, or
// a phrase mid-sentence in a bullet's prose, both of which break if a
// literal newline lands inside them. Same comment vocabulary and the same
// begin/end text, just without the forced newline, so the rendered body
// sits directly between the two markers on one line.
func inlineSpanMarkers(name string) (begin, end string) {
	return fmt.Sprintf("<!-- survey-gen:begin %s -->", name),
		fmt.Sprintf("<!-- survey-gen:end %s -->", name)
}

// replaceSpan swaps the text between a span's markers for body. Exactly one
// begin and one end marker must exist, in order; anything else is an error
// rather than a guess, because the renderer writes the doc in place. docRel
// names the doc in error messages only; it does not affect which bytes are
// replaced.
func replaceSpan(docRel, md, name, body string) (string, error) {
	begin, end := spanMarkers(name)
	return replaceBetweenMarkers(docRel, md, name, begin, end, body)
}

// replaceSpanInline is replaceSpan's inline-marker sibling, for a span that
// has to stay on one physical line (see inlineSpanMarkers).
func replaceSpanInline(docRel, md, name, body string) (string, error) {
	begin, end := inlineSpanMarkers(name)
	return replaceBetweenMarkers(docRel, md, name, begin, end, body)
}

// replaceBetweenMarkers is replaceSpan and replaceSpanInline's shared
// implementation, parameterized on which marker pair to look for.
func replaceBetweenMarkers(docRel, md, name, begin, end, body string) (string, error) {
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
	return contentBetweenMarkers(docRel, md, name, begin, end)
}

// spanContentInline is spanContent's inline-marker sibling, for a span
// replaced with replaceSpanInline.
func spanContentInline(docRel, md, name string) (string, error) {
	begin, end := inlineSpanMarkers(name)
	return contentBetweenMarkers(docRel, md, name, begin, end)
}

// contentBetweenMarkers is spanContent and spanContentInline's shared
// implementation.
func contentBetweenMarkers(docRel, md, name, begin, end string) (string, error) {
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

// joinWithAnd renders a backtick-quoted type list as prose, the way a hand
// writer would: comma-separated, with the last item joined by "and" instead
// of a trailing comma. oxford controls whether that last comma survives
// ("a, b, and c" vs "a, b and c") - both conventions already appear in the
// docs this package renders into, so the caller picks the one already in
// use at its span.
func joinWithAnd(items []string, oxford bool) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	default:
		sep := " and "
		if oxford {
			sep = ", and "
		}
		return strings.Join(items[:len(items)-1], ", ") + sep + items[len(items)-1]
	}
}

// backtickTypes wraps each type name in backticks, for a prose enumeration.
func backtickTypes(types []string) []string {
	items := make([]string, len(types))
	for i, t := range types {
		items[i] = "`" + t + "`"
	}
	return items
}

// wrapIndented greedily word-wraps s to width columns (including indent's
// own width), prefixing every line with indent. Used for the rendered
// prose enumerations, whose committed line breaks are otherwise cosmetic:
// wrapping deterministically here is what makes them byte-stable rather
// than a hand-editing chore.
func wrapIndented(s string, width int, indent string) string {
	words := strings.Fields(s)
	var b strings.Builder
	lineLen := 0
	for i, w := range words {
		switch {
		case i == 0:
			b.WriteString(indent)
			b.WriteString(w)
			lineLen = len(indent) + len(w)
		case lineLen+1+len(w) > width:
			b.WriteString("\n")
			b.WriteString(indent)
			b.WriteString(w)
			lineLen = len(indent) + len(w)
		default:
			b.WriteString(" ")
			b.WriteString(w)
			lineLen += 1 + len(w)
		}
	}
	return b.String()
}
