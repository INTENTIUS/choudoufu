// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoStatelessProseInShippedStrings is issue #1172 item 1's guard: the
// word "stateless" may not reach a user through a string this fork ships.
//
// The product was never stateless. It keeps state, in a file, the way
// OpenTofu does; what a live block removes is the file's AUTHORITY, which
// #685's ruling re-established in 2026-08-30 after the name had driven an
// over-rotation into deleting the file outright. "live" is the name that
// already won - the live block, live/, internal/live/, live-cert,
// live-import, live-ls, live-mv - so a "stateless plan" is a live plan, a
// "stateless replan" is a live replan, and a "stateless run" is a live
// run.
//
// # What this guard does NOT touch, deliberately
//
// The ~2,500 IDENTIFIERS carrying the word (statelessProviders,
// statelessRunner, StatelessRun, StatelessForeign, ...) were item 3 of
// #1172, and the maintainer ruled on 2026-09-17: stateless* is
// permanently-internal vocabulary, exported names included, and no rename
// is scheduled. So the doc comments that explain them keep the word too,
// and internal/configs/live.go's head now says so outright rather than
// hedging that the name is what the code calls it for now.
//
// This guard is what makes that ruling safe, and is the reason given for
// it: the harm the name does is that it regenerates into new prose, and
// this stops the prose. It reads STRING LITERALS ONLY, parsed out of the
// AST with comments discarded, which is exactly the set a user can be
// shown and exactly the set item 1 covers. Renaming an identifier cannot
// make it pass and cannot make it fail.
//
// Test files are out of scope for the same reason: nothing in a _test.go
// reaches a user. The shipped string is what this checks, and a test
// asserting on one fails on its own when that string changes.
//
// # Why the existing guard does not cover this
//
// live/no_state_absence_claims_test.go enforces a different sentence of
// internal/configs/live.go's rule - the one about treating the file's
// absence as the product - by sweeping for the fixed claims its own
// repoWide list enumerates, plus one further claim over the enumerated
// files that define what live mode IS. Read that list there; it is not
// repeated here, because a file that recites those phrases is a file that
// guard has to report, and #1225 spent a round discovering that about its
// own source.
//
// None of the claims on it appears in the diagnostics #1172 item 1 named:
// those carry the wrong NAME rather than a false absence claim, so a
// phrase sweep walks straight past them, and three of the four files they
// live in (internal/live/lint/overlong_address.go,
// internal/live/lint/residue_attribute.go,
// internal/live/liveimport/stamp.go) are outside that guard's enumerated
// file list as well. The two are kept apart rather than merged: that one
// is a phrase sweep, repo-wide over every tracked file of any kind; this
// one is an AST string sweep, Go-only. Folding either into the other
// would cost the half that does not fit.
func TestNoStatelessProseInShippedStrings(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "internal")

	fset := token.NewFileSet()
	var scanned int
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Comments are dropped on purpose (no parser.ParseComments): a
		// comment naming statelessRunner is item 3's business, not this
		// guard's, and reading them would make this test fail on ~2,300
		// lines it has no ruling to change.
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Errorf("parsing %s: %v", path, perr)
			return nil
		}
		scanned++
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				// A literal the standard unquoter cannot read is a
				// literal this guard has not read. Say so rather than
				// skipping it: a scan that silently drops what it
				// cannot parse is a guard that passes by not looking.
				t.Errorf("%s:%d: cannot unquote a string literal: %v", rel, fset.Position(lit.Pos()).Line, uerr)
				return true
			}
			for _, at := range occurrences(text) {
				if exemptStatelessOccurrence(text, at) {
					continue
				}
				t.Errorf("%s:%d says %q in a shipped string; the product keeps state (a disposable cache, choudoufu-cache.tfstate, that is not the record of ownership - issue #685), so there is no such thing as a stateless plan, replan or run. Write \"live\": a live plan, a live replan, a live run (issue #1172 item 1). Identifiers and comments are item 3 and are not read by this guard.",
					rel, fset.Position(lit.Pos()).Line, excerpt(text, at))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	// A walk that parsed nothing reports no violations for the same
	// reason a blind scanner does. internal/ holds thousands of Go
	// files; anything near zero means the walk, not the tree, changed.
	if scanned < 1000 {
		t.Fatalf("scanned only %d Go files under %s; this guard is meant to read the whole of internal/ and has measured almost nothing", scanned, dir)
	}
}

// occurrences reports the byte offset of every case-insensitive
// "stateless" in text.
func occurrences(text string) []int {
	lower := strings.ToLower(text)
	var out []int
	for i := 0; ; {
		at := strings.Index(lower[i:], "stateless")
		if at < 0 {
			return out
		}
		out = append(out, i+at)
		i += at + len("stateless")
	}
}

// exemptStatelessOccurrence reports whether one occurrence is a use the
// word is still correct for.
//
// There are exactly two, both narrow enough that no prose can reach them:
//
//   - A log channel name. Every live-path log line is tagged
//     "[DEBUG] stateless/discovery: ...", "[TRACE] stateless/mv: ...",
//     "[WARN] stateless hint: ..." and so on. That is a channel operators
//     grep for, not a sentence, and renaming it belongs with the
//     identifiers in item 3. The exemption is the occurrence that opens
//     the literal immediately after a bracketed level, and nothing else:
//     prose LATER in the same log line is still caught, which is how
//     internal/live/projection/rootoutput.go's "on the next stateless
//     plan" was found.
//   - The literal "stateless" alone, which is
//     internal/live/projection.Manager's constant lock id - a value, not a
//     word, and one whose whole point (see its doc comment) is that there
//     is only one of it.
func exemptStatelessOccurrence(text string, at int) bool {
	if text == "stateless" {
		return true
	}
	before := text[:at]
	return strings.HasPrefix(before, "[") && strings.HasSuffix(before, "] ") &&
		!strings.ContainsAny(strings.TrimSuffix(strings.TrimPrefix(before, "["), "] "), "[] ")
}

// excerpt quotes enough of the string around an occurrence for the author
// to find it without opening the file.
func excerpt(text string, at int) string {
	start := at - 24
	if start < 0 {
		start = 0
	}
	end := at + 40
	if end > len(text) {
		end = len(text)
	}
	return strings.Join(strings.Fields(text[start:end]), " ")
}
