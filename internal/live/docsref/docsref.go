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
	ref := Ref{Doc: doc}
	if !hasHeadings {
		return ref, nil
	}

	for _, part := range strings.Split(rest, "/") {
		part = strings.TrimSpace(part)
		if part == "" {
			return Ref{}, fmt.Errorf("%q has an empty section between separators", s)
		}
		if !strings.HasPrefix(part, `"`) || !strings.HasSuffix(part, `"`) || len(part) < 2 {
			return Ref{}, fmt.Errorf("%q: section %s is not quoted; the form is `doc.md, \"Heading\"`", s, part)
		}
		heading := part[1 : len(part)-1]
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
		quoted[i] = `"` + h + `"`
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
// Fenced code blocks are skipped: a shell transcript containing a comment
// line is not a heading, and counting one would let a reference resolve
// against a code sample.
func Headings(md string) map[string]bool {
	out := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(trimmed, "#") {
			continue
		}
		text := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		if text != "" {
			out[text] = true
		}
	}
	return out
}
