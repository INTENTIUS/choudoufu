// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package docsref parses and resolves the documentation references the live
// path's refusals carry.
//
// Every refusal in the three registries - internal/live/lint's Rule table,
// internal/live/identity's refusals.go and internal/live/passthrough's -
// names the shipped document that explains it, in one string:
//
//	live/LIMITATIONS.md, "unadmitted-type"
//	live/LIMITATIONS.md, "local-exec" / "remote-exec"
//	live/RECEIPTS.md, "Guard 4. The leaf rule"
//	live/MARKERS.md
//
// That string is rendered into the diagnostic a user reads, so it has always
// been prose. GitHub issue #110 makes it data as well: a generator has to
// turn the registries into live/LIMITATIONS.md's own entries, and a test has
// to fail when a refusal cites a heading nobody wrote. Both need the string
// parsed rather than matched, which is what this package is for.
//
// Nothing here changes the strings themselves. [Ref.String] round-trips what
// was parsed, and the registries stay the authority on what each refusal
// cites.
package docsref

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Ref is one parsed documentation reference.
type Ref struct {
	// Doc is the repository-relative path of the document, as written:
	// "live/LIMITATIONS.md".
	Doc string

	// Headings are the section titles cited within it, in the order
	// written, without their quotes. Empty when the reference names a
	// document and no section.
	Headings []string
}

// Parse reads one reference string.
//
// It is deliberately strict about the two shapes that appear in the
// registries and refuses anything else, because a malformed reference that
// parsed to "a document with no headings" would resolve successfully against
// any document and silently document nothing.
func Parse(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, fmt.Errorf("empty documentation reference")
	}

	doc, rest, hasHeadings := strings.Cut(s, ",")
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return Ref{}, fmt.Errorf("%q names no document", s)
	}
	if !strings.HasSuffix(doc, ".md") {
		return Ref{}, fmt.Errorf("%q does not name a markdown document", s)
	}
	// A refusal is documented in live/ or it is not documented. Criterion 4
	// of #110 was implemented as "the reference does not begin with GitHub
	// issue", and an audit pointed a rule at CHANGELOG.md and watched every
	// test pass. The rule a user needs is not "some markdown file in the
	// repository mentions this heading"; it is that the live-markers
	// documentation explains it.
	if !strings.HasPrefix(doc, "live/") || strings.Contains(doc, "..") {
		return Ref{}, fmt.Errorf("%q documents a refusal outside live/; a refusal is explained in the live-markers documentation or it is not explained", s)
	}
	ref := Ref{Doc: doc}
	if !hasHeadings {
		return ref, nil
	}

	// Headings are Go-quoted, because the registries build the reference
	// with %q and two of the pass-through summaries contain double quotes
	// of their own (`Invalid "path" attribute`). Unquoting rather than
	// trimming the outer pair is what makes those two round-trip; trimming
	// left the backslashes in and pointed at a heading nobody could write.
	//
	// The separator between several headings is "/", which means a heading
	// containing one cannot be expressed. That is a real limit and it fails
	// loudly here rather than mis-parsing.
	for _, part := range strings.Split(rest, "/") {
		part = strings.TrimSpace(part)
		if part == "" {
			return Ref{}, fmt.Errorf("%q has an empty section between separators", s)
		}
		heading, err := strconv.Unquote(part)
		if err != nil {
			return Ref{}, fmt.Errorf("%q: section %s is not a quoted string; the form is `doc.md, \"Heading\"`", s, part)
		}
		if strings.TrimSpace(heading) == "" {
			return Ref{}, fmt.Errorf("%q has an empty section title", s)
		}
		ref.Headings = append(ref.Headings, heading)
	}
	return ref, nil
}

// String renders the reference back into the form the registries store,
// so that a round trip through this package is a no-op.
func (r Ref) String() string {
	if len(r.Headings) == 0 {
		return r.Doc
	}
	quoted := make([]string, len(r.Headings))
	for i, h := range r.Headings {
		quoted[i] = strconv.Quote(h)
	}
	return r.Doc + ", " + strings.Join(quoted, " / ")
}

// Resolve checks that the document exists under root and that every heading
// cited appears in it as a markdown heading of some level.
//
// A heading match is exact on the text after the "#" run, trimmed. The
// documents this reads are ours and the convention is already enforced
// elsewhere (internal/live/lint's TestLimitationsDocCoversDirs matches
// "### <name>" as a literal), so a fuzzy match would only let a typo
// through.
func (r Ref) Resolve(root string) error {
	path := filepath.Join(root, filepath.FromSlash(r.Doc))
	src, err := os.ReadFile(path) //nolint:gosec // a repository-relative doc path from a compiled-in table
	if err != nil {
		return fmt.Errorf("%s: %w", r.Doc, err)
	}
	if len(r.Headings) == 0 {
		return nil
	}

	present := Headings(string(src))
	for _, want := range r.Headings {
		if !present[want] {
			return fmt.Errorf("%s has no heading %q", r.Doc, want)
		}
	}
	return nil
}

// Headings returns every markdown heading in a document, as a set of the
// text following the "#" run.
//
// Four things that look like headings are not, and an audit found this
// counting all four - so a refusal could cite a "heading" no reader would
// ever see, and the test that resolves references would pass:
//
//   - lines inside a fenced code block, ``` or ~~~. A shell transcript full
//     of comment lines is not a table of contents.
//   - lines inside an HTML comment. This document has generated regions
//     delimited by them, and a commented-out section is deliberately not
//     part of the page.
//   - indented code blocks, four spaces or a tab.
//   - a "#" run with no space after it, or longer than six, neither of
//     which markdown renders as a heading at all.
func Headings(md string) map[string]bool {
	out := map[string]bool{}
	var fence string
	inComment := false

	for _, line := range strings.Split(md, "\n") {
		// An indented code block is decided before trimming, since that is
		// the only thing distinguishing it.
		indented := strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")
		trimmed := strings.TrimSpace(line)

		if inComment {
			if strings.Contains(trimmed, "-->") {
				inComment = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "<!--") && !strings.Contains(trimmed, "-->") {
			inComment = true
			continue
		}

		switch {
		case fence != "":
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		case strings.HasPrefix(trimmed, "```"):
			fence = "```"
			continue
		case strings.HasPrefix(trimmed, "~~~"):
			fence = "~~~"
			continue
		}

		if indented || !strings.HasPrefix(trimmed, "#") {
			continue
		}
		hashes := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		if hashes > 6 || len(trimmed) == hashes || trimmed[hashes] != ' ' {
			continue
		}
		if text := strings.TrimSpace(trimmed[hashes:]); text != "" {
			out[text] = true
		}
	}
	return out
}
