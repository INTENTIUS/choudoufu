// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Render mode: `go run ./tools/survey-gen -render` rewrites the derivable
// spans this tool owns in other documents, between HTML comment markers,
// from committed inputs and the compiled admission table. No provider and no
// network: rendering moves numbers out of transcription, not out of the
// survey.
//
// It used to render four spans of live/SURVEY.md as well - the raw-signal
// counts, a path-count summary, a provider-wide paragraph and a status
// vocabulary tally. That document and the path taxonomy it carried were
// retired under #696; the tiers in live/readiness.json are the coverage
// vocabulary that stays.
package main

import (
	"fmt"
	"strings"
)

// runRender is the -render entry point: read the committed artifacts and
// the committed docs, replace the marked spans, write the docs back. Three
// docs are rendered this way: live/LIMITATIONS.md's residue-roster spans
// and untaggable-admitted span (issue #49 and #54, renderLimitationsMD in
// residue_render.go and untaggable_render.go), live/MARKERS.md's governance
// spans (governance_render.go), and live/COVERAGE.md's admitted-set spans
// (issue #54, renderContractMDX in contract_render.go). All from committed
// JSON and the compiled admission table, with no provider and no network.
func runRender() error {
	root, err := repoRoot()
	if err != nil {
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
