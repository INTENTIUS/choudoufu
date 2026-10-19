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

	"github.com/intentius/choudoufu/tools/statpages"
)

// TestSiteContentMeasuredFiguresCarryProvenance is issue #679's guard for
// the site half of the published-figure-provenance defect, in the same
// spirit as internal/live/harness's TestHandWrittenProseCarriesNoFigures:
// a number that describes a measurement and that nothing recomputes is
// exactly the shape that has gone stale, silently, every time someone has
// gone looking in this repository (docs/progress's stamped pages and
// HARNESS.md's own guard are the fix pattern this test copies onto the
// site's pages, site/src/content/docs since the Astro cutover, #2092).
//
// # What counts as a "measured figure" here, and why it is this narrow
//
// HARNESS.md's guard can flag every bare digit outside a generated span,
// because that file's hand-written prose is short and disciplined enough to
// carry none. The site is not: ordinary prose there cites version
// numbers ("OpenTofu 1.12"), code references
// ("setup.md:267"), dates ("2026-08-30") and issue/PR numbers throughout,
// none of which is the defect class #679 is about. Flagging every digit
// would drown the real findings in noise on line one.
//
// So this test only matches a number immediately followed by a measurement
// unit word - "926 lines", "43 warnings", "512 calls", "377 round trips",
// "745 resources", "68 cohorts" and the like (measuredFigureUnitWords is the
// exact, closed list; extend it there, not by loosening the regex). That
// shape:
//
//   - does NOT match a version number ("1.13.0", "v0.5.0"), because a bare
//     version is never followed by one of these words;
//   - does NOT match a code-line reference ("line 601", "setup.md:267"),
//     because those put the word before the number, or a colon with no
//     space between them, never a unit word directly after;
//   - does NOT match an issue or PR reference ("#679", "(#588)"), because
//     the token is a hash-prefixed integer, not <number><space><unit-word>.
//
// The trade-off, stated rather than hidden: a figure phrased with its unit
// word earlier in the sentence ("the untaggable share of *instances* was 41
// of 79") is invisible to this test. That is a real blind spot, the same
// kind HARNESS.md's own registry entries are required to name for their
// instrument (see that file's "Each entry also records its instrument and
// what that instrument cannot see"). Narrow and documented beats broad and
// wrong.
//
// # What makes a matched figure count as provenanced
//
// A matched figure is fine if, anywhere in the same block (a run of
// non-blank lines - roughly a paragraph, a table, or a bullet), one of
// these appears:
//
//   - a backticked commit sha, 7 to 12 lowercase hex characters - the
//     stamp every docs/progress page and every fix in #679 uses;
//   - the word "Stale" - the explicit disagreement marker docs/progress
//     pages render when a pin has moved since the figure was measured.
//
// A figure inside a fenced code block (literal command output, like
// plan-cost.md's `sweep universe=1027 ...` reproduction) or inside a
// `<!-- *-gen:begin ... -->` / `<!-- *-gen:end -->` rendered span (the
// figure is generated, not hand-typed, and that generator's own drift test
// is the guard) is exempt without needing a stamp of its own.
//
// # Proving it red
//
// Adding a sentence like "a two-resource estate's first plan scanned 761
// types" to any in-scope page with no nearby commit or "Stale" marker fails
// this test, naming the file, line and figure. That is how this test was
// proved red while writing it, against a synthetic copy of the setup page's
// own real, now-fixed defect. It walks every .md and .mdx page under
// site/src/content/docs.
func TestSiteContentMeasuredFiguresCarryProvenance(t *testing.T) {
	root := repoRoot(t)
	base := filepath.Join(root, "site", "src", "content", "docs")

	var mdFiles []string
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".mdx") {
			return nil
		}
		mdFiles = append(mdFiles, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", base, err)
	}
	if len(mdFiles) == 0 {
		t.Fatal("found no site/src/content/docs markdown files; this test is checking nothing")
	}
	sort.Strings(mdFiles)

	for _, path := range mdFiles {
		raw, err := os.ReadFile(path) //nolint:gosec // paths come from walking a fixed directory in the checkout
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		rel := mustRel(t, root, path)
		for _, bad := range unstampedMeasuredFigures(string(raw)) {
			t.Errorf("%s:%d: hand-typed measured figure %q with no commit or \"Stale\" stamp in its paragraph: %q\n"+
				"Either it is derivable from a committed artifact (compute it at build from live/, as "+
				"site/src/data/proof.ts does, or link the generated stat page under live/ that stamps a commit), or it is a one-off measurement "+
				"(stamp it inline: \"at commit `<sha>`\", or \"**Stale**: ...\" if it disagrees with a current "+
				"artifact), or it is unsupportable (delete the sentence). See this test's own doc comment for what "+
				"counts as a measured figure and what counts as a stamp.",
				rel, bad.line, bad.figure, bad.context)
		}
	}
}

// The Hugo site's generated progress pages (site/content/docs/progress/)
// were skipped here because tools/gauntlet stamped them itself. They are
// live/BOARD.md now, and TestStatPagesHandTextCarriesNoUnstampedFigures
// below holds that page's hand-written text to the same rule. Nothing under
// site/src/content/docs is skipped.
//
// site/content/docs/examples/ used to be skipped too, on the grounds that a
// walkthrough's figures are hedged ("On the sample fixture it is around 56
// requests") and self-verifying. That reasoning did not survive contact with
// #979: terralith-migration.md's other hedged figure, "around 29 requests"
// for the one-estate `-refresh=false` plan, re-measured at 7 on v0.15.0
// (#978), and it had been wrong by a factor of four for as long as nobody
// re-ran it. A hedge is not provenance.

// measuredFigureUnitWords is the closed, documented list of unit words this
// guard treats as turning a bare number into a measured figure. Extend this
// list, deliberately, when a real defect of this shape is found outside it;
// do not loosen the surrounding regex to catch more by accident.
var measuredFigureUnitWords = `types?|calls?|lines?|resources?|instances?|warnings?|records?|trips?|cohorts?|requests?`

// measuredFigureRe matches a number - plain, comma-grouped, or decimal -
// immediately followed by one of measuredFigureUnitWords, with an optional
// "round" between them ("377 round trips"). The number must be followed by
// a word boundary before the unit word, and by \s+ (never a colon or a
// bare hyphen) so "setup.md:267" and "55-resource" cannot match.
var measuredFigureRe = regexp.MustCompile(
	`\b[0-9][0-9,]*(?:\.[0-9]+)?\s+(?:round\s+)?(?:` + measuredFigureUnitWords + `)\b`,
)

// properNounNumberRe matches text ending in "Route " right before a
// measuredFigureRe match, the one AWS product name in this closed unit-word
// list that collides with it: "Route 53 record changes" is the service
// name, not a count of 53 records. Named narrowly, the same way this file's
// package doc comment says to extend measuredFigureUnitWords - by the
// specific collision found, not by loosening the match.
var properNounNumberRe = regexp.MustCompile(`\bRoute\s*$`)

// stampRe recognizes the three provenance shapes this repository's fix for
// #679 uses: a backticked short commit sha; the literal word "Stale"
// (matching the board's bolded "**Stale**:" convention and inline
// "**Stale**: ..." / "**Stale on ...**" notes); or a link to a specific
// section anchor, same-page (`](#...)`) or cross-page (`](<page>#...)`, a
// "#" in the target; a Hugo relref with one before #2092) - a headline
// table links each row to the section that measures it, rather than
// repeating the citation in the cell. A link with no "#" fragment does NOT
// match: that is ordinary page-to-page navigation, not a citation to a
// specific measurement, and accepting it here would make this guard too
// weak to mean anything on a site that cross-links constantly.
var stampRe = regexp.MustCompile("`[0-9a-f]{7,12}`|Stale|\\]\\([^)\\s]*#")

// fencedCodeBlockRe matches a fenced code block, opened and closed by a
// line of three or more backticks. (?s) lets "." cross newlines.
var fencedCodeBlockRe = regexp.MustCompile("(?s)\\n```[^\\n]*\\n.*?\\n```")

// generatedSpanRe matches any generator's rendered span, from its
// `<!-- NAME-gen:begin ... -->` marker to the matching `:end` marker
// (mdspan.For's convention: tool name, then "-gen"). (?s) lets the body
// span newlines; the non-greedy .*? stops at the first end marker rather
// than swallowing every span that follows in the same document.
var generatedSpanRe = regexp.MustCompile(`(?s)<!--\s*\S+-gen:begin[^>]*-->.*?<!--\s*\S+-gen:end[^>]*-->`)

// frontMatterRe strips a leading front-matter block ("---\n...\n---"),
// so a page's own "weight: 7" can never be read as a measured figure.
var frontMatterRe = regexp.MustCompile(`(?s)\A---\n.*?\n---\n`)

// unstampedFigure is one measured figure this test found with no nearby
// provenance stamp.
type unstampedFigure struct {
	line    int
	figure  string
	context string
}

// headingRe matches an ATX markdown heading of level 1 or 2 (# or ##) -
// the unit this test scopes provenance search to. A level-3+ heading
// (### or deeper) starts a subsection of its enclosing ## section rather
// than a new provenance scope of its own, because that is how these pages
// are actually organized: a fixture and its commit are named once near a
// "##" section's top ("Generated terralith at three scales, applied with
// stock terraform... (commit `cfd0dc58d4`, ...)", model/plan-cost.md's "The
// measured split" section) and every subsection under it - "The read pass
// is the number stock pays...", "The native leg does not move" - reuses
// the same fixture without repeating the citation.
var headingRe = regexp.MustCompile(`^#{1,2}\s`)

// unstampedMeasuredFigures scans one document's raw markdown and returns
// every measuredFigureRe match that is not exempt (inside a fenced code
// block or a generated span) and whose containing section (from one
// heading up to, but not including, the next heading of any level - the
// unit these pages are actually written in: a citation established once
// near a section's top is read as covering the figures reused below it in
// the same section) carries neither a backticked commit sha nor the word
// "Stale". A finer unit (the single paragraph) was tried first and flagged
// dozens of reuses of an already-cited figure a few sentences later as if
// each needed its own citation, which is not the shape #679's own audit
// found (eleven specific figures, not every figure on these pages) - see
// this test's package-level doc comment.
func unstampedMeasuredFigures(raw string) []unstampedFigure {
	// Blank out (preserving line structure, so reported line numbers stay
	// accurate) the two exempt regions: fenced code blocks and generated
	// spans. Blanking rather than deleting keeps every other line's offset
	// unchanged.
	masked := frontMatterRe.ReplaceAllStringFunc(raw, blankKeepingNewlines)
	masked = fencedCodeBlockRe.ReplaceAllStringFunc(masked, blankKeepingNewlines)
	masked = generatedSpanRe.ReplaceAllStringFunc(masked, blankKeepingNewlines)

	lines := strings.Split(masked, "\n")

	// headingLines is every line index that starts a level-1 or level-2
	// heading, in order, with a sentinel at len(lines) so the last real
	// section always has an end.
	var headingLines []int
	for i, line := range lines {
		if headingRe.MatchString(line) {
			headingLines = append(headingLines, i)
		}
	}
	headingLines = append(headingLines, len(lines))

	// sectionOf(lineIdx) returns [start, end) for the section lineIdx sits
	// in: start is the nearest heading at or above it (0 if none, so
	// everything before the document's first heading is one section), end
	// is the next heading after start, or len(lines).
	sectionOf := func(lineIdx int) (start, end int) {
		start = 0
		end = len(lines)
		for _, h := range headingLines {
			if h > lineIdx {
				end = h
				break
			}
			start = h
		}
		return start, end
	}

	var out []unstampedFigure
	for lineIdx, line := range lines {
		matches := measuredFigureRe.FindAllStringIndex(line, -1)
		if len(matches) == 0 {
			continue
		}
		start, end := sectionOf(lineIdx)
		if stampRe.MatchString(strings.Join(lines[start:end], "\n")) {
			continue
		}
		for _, loc := range matches {
			if properNounNumberRe.MatchString(line[:loc[0]]) {
				continue // e.g. "Route 53 record changes" - a product name, not a count
			}
			out = append(out, unstampedFigure{
				line:    lineIdx + 1,
				figure:  line[loc[0]:loc[1]],
				context: strings.TrimSpace(line),
			})
		}
	}
	return out
}

// blankKeepingNewlines replaces every non-newline byte of s with a space,
// so a masked-out region contributes no regex matches but every surviving
// line's number is unchanged.
func blankKeepingNewlines(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' {
			b.WriteRune('\n')
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// mustRel is filepath.Rel with a test-fatal error, for turning an absolute
// walked path back into a repo-relative one for messages and the skip list.
func mustRel(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("computing a relative path for %s under %s: %v", path, root, err)
	}
	return rel
}

// TestStatPagesHandTextCarriesNoUnstampedFigures holds the generated stat
// pages under live/ (#2092) to the same rule as the site's pages. Their
// generated spans carry a commit or date by construction and are exempt,
// as they are on the site; the hand-written text around the spans is not,
// so a measured figure typed there needs a stamp like any other.
func TestStatPagesHandTextCarriesNoUnstampedFigures(t *testing.T) {
	root := repoRoot(t)
	pages := statpages.Pages()
	if len(pages) == 0 {
		t.Fatal("statpages.Pages() is empty; this guard is checking nothing")
	}
	for _, p := range pages {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p.Path)))
		if err != nil {
			t.Fatalf("reading %s: %v", p.Path, err)
		}
		for _, bad := range unstampedMeasuredFigures(string(raw)) {
			t.Errorf("%s:%d: hand-typed measured figure %q outside the generated spans, with no commit or \"Stale\" stamp in its paragraph: %q",
				p.Path, bad.line, bad.figure, bad.context)
		}
	}
}
