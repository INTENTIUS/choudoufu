// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoStateAbsenceClaims guards the rule internal/configs/live.go states
// about itself: "No comment, refusal text or test may treat the file's
// absence as the product." Issue #1171 found the rule broken in the same
// file that states it - the backend and cloud refusal messages said a live
// block "removes state entirely" - and #1172 found the same over-rotation
// #685 already unwound (2026-08-30: the state file loses its authority,
// not its existence) restated as the REASON for a refusal
// (internal/live/lint/lint.go) and repeated in three more doc comments
// (internal/tofu/marks.go, internal/live/lifecycle/doc.go,
// internal/live/onboard/onboard.go).
//
// Two checks, deliberately different in scope:
//
//   - A short list of phrases below assert, structurally, that live mode
//     REMOVES or ELIMINATES state, or that there is nothing "to put"
//     anywhere. These have no legitimate use anywhere in this tree: a
//     repo-wide search for each, done while writing this guard, found
//     zero hits outside the violations fixed alongside it. So they are
//     checked repo-wide, over every tracked file.
//   - The bare claim "no state file" is deliberately NOT checked
//     repo-wide. e2e fixtures, smoke scenarios and docs correctly say a
//     state file was deleted, or that stock's own backend found none -
//     the cache genuinely is disposable and deleting it is a supported
//     demonstration, and a stock backend genuinely can report an absent
//     state file. Those are true statements about an action or a stock
//     code path, not the false, permanent, definitional claim this guard
//     exists for. So "no state file" is checked only in the small,
//     enumerated set of files that state what live mode structurally IS,
//     or refuse a construct based on what it is - the exact family
//     #1171 and #1172 found repeating the claim. A new instance of the
//     same defect belongs on this list; the rest of the tree, where the
//     phrase is usually true, does not.
//
// internal/configs/live.go is on that list on purpose, as the positive
// control: it discusses the very same history ("removing the file was
// the over-rotation #685 unwound") and a correct fix must leave it green
// with no special-case allowance, because it talks about authority and
// history rather than asserting the file is gone or was never there.
func TestNoStateAbsenceClaims(t *testing.T) {
	root := repoRoot(t)

	// The one file the repo-wide scan below skips, and the only one it
	// will ever skip: this guard's own source. It has to spell each
	// banned phrase out three times over - once in the list, once in the
	// doc comment that says why the phrase is banned, and once in the
	// failure message that quotes the claim back at the author - so a
	// guard that scanned itself reports its own definitions forever and
	// can never go green. It did: this test landed red on PR #1225 with
	// twelve hits, every one of them a line of this file.
	//
	// The exemption is one path, compared with ==, never a prefix, a
	// directory or a second entry, so it cannot be reused to quiet a
	// real violation somewhere else. The three other ways out of the
	// self-match are all worse and are deliberately not taken: softening
	// the phrase list loses the prose this guard exists to catch,
	// weakening the patterns until they no longer match their own
	// definitions is the same thing wearing a disguise, and moving the
	// phrases into a data file hides the rule from the reader who comes
	// looking for it. The list belongs in source, in this file, and this
	// file is therefore the one thing the scan does not read.
	const selfPath = "live/no_state_absence_claims_test.go"

	// Phrases that are wrong everywhere: no file in this tree has a
	// legitimate reason to say live mode removes or eliminates state, or
	// that a stateless run has no state to put anywhere. Matched
	// case-insensitively as fixed strings (not regexes) against every
	// tracked file, git's own exclusions (.gitignore, binary detection)
	// apply automatically via `git grep`.
	repoWide := []string{
		"removes state entirely",
		"remove state entirely",
		"removing state entirely",
		"eliminates the state file",
		"eliminating the state file",
		"eliminates state",
		"no state to put",
		// Added after the self-exclusion above made the first real sweep
		// possible: internal/backend/local/backend.go said a live run had
		// "no state to store", when what is true is that its state never
		// reaches THAT backend's workspace directory. One hit in the tree,
		// and it was the defect.
		"no state to store",
	}

	for _, phrase := range repoWide {
		// --full-name pins the reported paths to the checkout root, so the
		// selfPath comparison below is against a stable spelling whatever
		// directory the test binary happens to run in.
		out, err := exec.Command("git", "-C", root, "grep", "--full-name", "-rnIiF", phrase, "--").Output()
		if err != nil {
			// git grep exits 1 when nothing matches; anything else is a
			// real failure worth seeing.
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
				continue
			}
			t.Fatalf("git grep -F %q: %v", phrase, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			path, _, ok := strings.Cut(line, ":")
			if !ok {
				// Not a "path:line:text" record. Fail loudly rather than
				// skip: a scan that silently drops what it cannot parse
				// is a guard that passes by not looking.
				t.Fatalf("git grep -F %q: cannot read a path out of %q", phrase, line)
			}
			if path == selfPath {
				continue
			}
			t.Errorf("%s\n\tasserts a live block removes or eliminates state, or has nothing to store; the state file loses its authority, not its existence (issue #685) - name what is actually kept (a disposable cache, choudoufu-cache.tfstate) and what changed (a marker is the record of ownership), the way HANDOFF.md's foundation section does", line)
		}
	}

	// The enumerated files below - the ones that state what live mode IS,
	// or refuse a construct based on what it is - get the stricter of the
	// two scans: every phrase above plus the bare "no state file" claim,
	// searched over text that has been folded back onto one line first.
	// The repo-wide scan cannot do either. It cannot look for "no state
	// file" at all, because e2e fixtures and stock code paths say it
	// truthfully; and being `git grep`, it cannot see a claim a margin
	// split in two. It missed exactly that: site/content/docs/use/
	// compatibility.md read "There is no state to\n  store."
	//
	// Add a new file here when a new instance of the defect turns up; do
	// not widen this to a repo-wide scan (see doc comment above).
	definitional := []string{
		"internal/configs/live.go",
		"internal/configs/module.go",
		"internal/live/lint/lint.go",
		"internal/tofu/marks.go",
		"internal/live/lifecycle/doc.go",
		"internal/live/onboard/onboard.go",
		// Found by the first sweep this guard was able to run, and all
		// four the same defect: the two pages that state the backend
		// refusal's reason to a prospect, the tutorial paragraph where a
		// reader forms the model in the first place, and the projection
		// doc comment that explained a missing output by the file not
		// existing.
		"internal/live/projection/outputs.go",
		"site/content/aws/compatibility.md",
		"site/content/docs/use/compatibility.md",
		"site/content/docs/tutorial.md",
	}

	for _, rel := range definitional {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		src := strings.ToLower(string(b))
		folded, offsets := foldWrapped(src)
		for _, phrase := range append(append([]string{}, repoWide...), "no state file") {
			idx := 0
			for {
				at := strings.Index(folded[idx:], phrase)
				if at < 0 {
					break
				}
				pos := idx + at
				idx = pos + len(phrase)
				// "no AUTHORITATIVE state file" is the accurate claim -
				// the file exists, it just is not the record of ownership
				// - and must not trip this guard.
				if phrase == "no state file" && strings.HasSuffix(folded[:pos], "no authoritative ") {
					continue
				}
				line := 1 + strings.Count(src[:offsets[pos]], "\n")
				t.Errorf("%s:%d says %q; live mode keeps a disposable cache (choudoufu-cache.tfstate by default) that just is not consulted for ownership - name what is kept and what changed (a marker is the record of ownership), the way HANDOFF.md's foundation section does, rather than treating the file's absence as the product", rel, line, phrase)
			}
		}
	}
}

// foldWrapped joins a claim that a line break split back into one
// searchable string, and returns a byte offset back into the original for
// every byte of the result so a hit still reports the line it came from.
//
// Without this the check reads whatever the margin happened to allow.
// site/content/docs/tutorial.md wrapped "choudoufu has no state / file to
// read" across two lines and a plain substring search walked straight past
// it; a Go doc comment hides the same claim behind its "//" lead. So a run
// of whitespace collapses to a single space, and so does the comment,
// quote or bullet marker that opens a continuation line.
//
// The repo-wide scan above cannot do this - `git grep` is line-based - which
// is one more reason the phrases it looks for are short ones that fit on a
// line.
func foldWrapped(src string) (string, []int) {
	isSpace := func(c byte) bool {
		return c == ' ' || c == '\t' || c == '\r' || c == '\n'
	}

	var out strings.Builder
	offsets := make([]int, 0, len(src))
	for i := 0; i < len(src); {
		if !isSpace(src[i]) {
			out.WriteByte(src[i])
			offsets = append(offsets, i)
			i++
			continue
		}
		start := i
		newline := false
		for i < len(src) {
			if isSpace(src[i]) {
				newline = newline || src[i] == '\n'
				i++
				continue
			}
			// A continuation line may reopen with a comment, blockquote or
			// bullet lead. Only after a newline: "a // b" on one line is
			// two things, not a wrapped one.
			if newline && strings.HasPrefix(src[i:], "//") {
				i += 2
				continue
			}
			if newline && (src[i] == '#' || src[i] == '>' || src[i] == '*') {
				i++
				continue
			}
			break
		}
		out.WriteByte(' ')
		offsets = append(offsets, start)
	}
	return out.String(), offsets
}
