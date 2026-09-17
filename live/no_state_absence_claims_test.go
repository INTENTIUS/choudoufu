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
	}

	for _, phrase := range repoWide {
		out, err := exec.Command("git", "-C", root, "grep", "-rnIiF", phrase, "--").Output()
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
			t.Errorf("%s asserts a live block removes or eliminates state, or has nothing to store; the state file loses its authority, not its existence (issue #685) - name what is actually kept (a disposable cache) and what changed (a marker is the record of ownership), the way HANDOFF.md's foundation section does", line)
		}
	}

	// The bare "no state file" claim is only checked in the files that
	// state what live mode IS, or refuse something based on what it is.
	// Add a new file here when a new instance of the same defect turns
	// up; do not widen this to a repo-wide scan (see doc comment above).
	definitional := []string{
		"internal/configs/live.go",
		"internal/configs/module.go",
		"internal/live/lint/lint.go",
		"internal/tofu/marks.go",
		"internal/live/lifecycle/doc.go",
		"internal/live/onboard/onboard.go",
	}

	for _, rel := range definitional {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		lower := strings.ToLower(string(b))
		idx := 0
		for {
			at := strings.Index(lower[idx:], "no state file")
			if at < 0 {
				break
			}
			pos := idx + at
			// "no AUTHORITATIVE state file" is the accurate claim (the
			// file exists, it just is not the record of ownership) and
			// must not trip this guard; it is not a contiguous match of
			// "no state file" so it never reaches here, but a defensive
			// check is cheap and documents the exception in the same
			// place the code enforces it.
			before := lower[:pos]
			if strings.HasSuffix(strings.TrimRight(before, " "), "no authoritative") {
				idx = pos + len("no state file")
				continue
			}
			line := 1 + strings.Count(lower[:pos], "\n")
			t.Errorf("%s:%d claims there is no state file; live mode keeps a disposable cache (choudoufu-cache.tfstate by default) that just is not consulted for ownership - say that, not that nothing exists", rel, line)
			idx = pos + len("no state file")
		}
	}
}
