// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package mdspan rewrites named regions of a committed markdown document in
// place.
//
// The convention it implements predates it: tools/survey-gen has been
// rendering live/SURVEY.md, live/LIMITATIONS.md and live/COVERAGE.md from
// committed artifacts since issue #49, marking each generated region with a
// pair of HTML comments and passing the rest of the file through
// byte-for-byte. Generated and hand-written prose live in one document, and
// a reader can tell which is which by looking.
//
//	<!-- survey-gen:begin residue-deprecated -->
//	...generated...
//	<!-- survey-gen:end residue-deprecated -->
//
// GitHub issue #110 added a second generator writing into the same document,
// which is what made the convention worth extracting rather than copying.
// The tool name is a parameter so each generator owns its own spans and two
// of them cannot silently claim the same region.
//
// Every operation is strict: exactly one begin marker, exactly one end
// marker, in that order. A generator writes the file in place, so a missing
// or duplicated marker has to be an error rather than a guess about which
// region was meant.
package mdspan

import (
	"fmt"
	"strings"
)

// Markers is one generator's marker vocabulary, built by [For].
type Markers struct {
	tool string
}

// For returns the marker vocabulary for a generator, named as it appears in
// the comments: "survey-gen", "limits-gen".
func For(tool string) Markers { return Markers{tool: tool} }

// Block returns the begin and end markers for a span whose body occupies
// whole lines - a paragraph, a table, a run of headings.
//
// The begin marker carries a trailing newline, which is what puts the body
// on its own line. That is wrong for a span inside a table cell or mid
// sentence; see [Markers.Inline].
func (m Markers) Block(name string) (begin, end string) {
	return fmt.Sprintf("<!-- %s:begin %s -->\n", m.tool, name),
		fmt.Sprintf("<!-- %s:end %s -->", m.tool, name)
}

// Inline returns the begin and end markers for a span that has to stay on
// one physical line, such as a markdown table cell or a phrase inside a
// sentence, both of which break if a literal newline lands in them.
func (m Markers) Inline(name string) (begin, end string) {
	return fmt.Sprintf("<!-- %s:begin %s -->", m.tool, name),
		fmt.Sprintf("<!-- %s:end %s -->", m.tool, name)
}

// Replace swaps the text between a block span's markers for body.
//
// doc names the document in error messages only; it does not affect which
// bytes are replaced.
func (m Markers) Replace(doc, md, name, body string) (string, error) {
	begin, end := m.Block(name)
	return replaceBetween(doc, md, name, begin, end, body)
}

// ReplaceInline is [Markers.Replace] for an inline span.
func (m Markers) ReplaceInline(doc, md, name, body string) (string, error) {
	begin, end := m.Inline(name)
	return replaceBetween(doc, md, name, begin, end, body)
}

// Content returns the committed text between a block span's markers, which
// is what a drift test compares against a fresh render.
func (m Markers) Content(doc, md, name string) (string, error) {
	begin, end := m.Block(name)
	return contentBetween(doc, md, name, begin, end)
}

// ContentInline is [Markers.Content] for an inline span.
func (m Markers) ContentInline(doc, md, name string) (string, error) {
	begin, end := m.Inline(name)
	return contentBetween(doc, md, name, begin, end)
}

func bounds(doc, md, name, begin, end string) (int, int, error) {
	if n := strings.Count(md, begin); n != 1 {
		return 0, 0, fmt.Errorf("%s: expected exactly one %q marker, found %d", doc, strings.TrimSpace(begin), n)
	}
	if n := strings.Count(md, end); n != 1 {
		return 0, 0, fmt.Errorf("%s: expected exactly one %q marker, found %d", doc, end, n)
	}
	i := strings.Index(md, begin) + len(begin)
	j := strings.Index(md, end)
	if j < i {
		return 0, 0, fmt.Errorf("%s: the %q end marker precedes its begin marker", doc, name)
	}
	return i, j, nil
}

func replaceBetween(doc, md, name, begin, end, body string) (string, error) {
	i, j, err := bounds(doc, md, name, begin, end)
	if err != nil {
		return "", err
	}
	return md[:i] + body + md[j:], nil
}

func contentBetween(doc, md, name, begin, end string) (string, error) {
	i, j, err := bounds(doc, md, name, begin, end)
	if err != nil {
		return "", err
	}
	return md[i:j], nil
}
