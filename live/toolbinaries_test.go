// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Building a generator the obvious way - `go build ./tools/iamref-gen` from
// the checkout root - drops an executable named after the tool at the repo
// root. .gitignore has a hand-written line per tool to swallow those, and on
// 2026-08-14 that list was seven tools behind, so an 8.9MB arm64 binary went
// in with commit d7ec51f6ec and nothing noticed.
//
// A hand-maintained list of every tool is the same shape of debt this
// repository refuses everywhere else, so these two tests derive it instead.
// The first keeps the list complete as tools are added; the second is the
// backstop that does not care whether the list is right, only whether a
// binary actually got committed.

// TestEveryToolHasAGitignoreEntry checks the list against the directory it is
// a list of.
func TestEveryToolHasAGitignoreEntry(t *testing.T) {
	root := repoRoot(t)

	raw, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	ignored := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		ignored[strings.TrimSpace(line)] = true
	}

	entries, err := os.ReadDir(filepath.Join(root, "tools"))
	if err != nil {
		t.Fatalf("reading tools/: %v", err)
	}
	var missing []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !ignored["/"+e.Name()] {
			missing = append(missing, "/"+e.Name())
		}
	}
	if len(missing) > 0 {
		t.Errorf(".gitignore has no entry for %d tool(s): %s\n"+
			"`go build ./tools/<name>` from the checkout root writes an executable named <name> there, "+
			"and without the entry `git add -A` commits it. Add the lines to the block of tool names in "+
			".gitignore.", len(missing), strings.Join(missing, " "))
	}
}

// TestNoCompiledBinaryIsTracked is the backstop, and the one that would have
// caught d7ec51f6ec regardless of what the ignore list said: a tracked file
// is read, and if it opens with a Mach-O or ELF magic number it is a build
// artifact somebody committed.
//
// Ignore rules do not apply to a path git already tracks, so a missing entry
// added after the fact leaves the binary in place. This test fails until it
// is removed with `git rm --cached`.
//
// The three ways the `ls-files` call can end are kept apart on purpose. git
// missing, git exiting non-zero, and git naming tracked files are different
// facts about the machine and the tree, and they used to collapse into one
// blanket `t.Skipf("git ls-files unavailable")` on any error at all - a
// permanent green whenever anything went wrong, under a message that named
// the wrong cause for two of the three.
//
// Two counters below are the rest of that same argument. A git that exits 0
// and names nothing, and a tree whose files cannot be opened, both used to
// leave `found` empty and report clean - the second one loudly enough that
// #653 ranked it the worst check in this package: every `os.Open` failure
// was dropped by a bare `continue`, so a permissions problem covering the
// whole checkout hid every committed binary in it and exited 0. This test's
// whole claim is "we read the first four bytes of everything git tracks", so
// it now asserts that it read them.
func TestNoCompiledBinaryIsTracked(t *testing.T) {
	bin := gitBin(t)
	root := repoRoot(t)

	out, err := exec.Command(bin, "-C", root, "ls-files", "-z").Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		t.Fatalf("`%s -C %s ls-files -z` exited non-zero: %v %s\n"+
			"git was found on PATH, so this is git refusing to answer rather than a missing tool - "+
			"most likely %s is not inside a git repository, because repoRoot walks up to the nearest "+
			"go.mod and an extracted tarball or a copied tree has one without a .git. This is the "+
			"only check that reads what is actually committed rather than what .gitignore claims, so "+
			"it fails here rather than skipping: with no file list it has looked at nothing.",
			bin, root, err, stderr, root)
	}

	// Mach-O (all four arch/endian spellings, plus the universal binary) and
	// ELF. Enough to catch a build artifact from any machine that develops
	// this fork; a magic number rather than an extension, because these have
	// no extension.
	magics := [][]byte{
		{0xfe, 0xed, 0xfa, 0xce}, {0xce, 0xfa, 0xed, 0xfe},
		{0xfe, 0xed, 0xfa, 0xcf}, {0xcf, 0xfa, 0xed, 0xfe},
		{0xca, 0xfe, 0xba, 0xbe},
		{0x7f, 'E', 'L', 'F'},
	}

	var found, unreadable []string
	tracked, read := 0, 0
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" {
			continue
		}
		tracked++
		// testdata legitimately holds fixture bytes of every shape.
		if strings.Contains(path, "testdata/") {
			continue
		}
		f, err := os.Open(filepath.Join(root, path))
		if err != nil {
			// A path git names and the filesystem does not have is the one
			// benign case: a deleted-but-staged path, or a symlink whose
			// target is out of tree. Everything else - a permission bit, an
			// I/O error - is this check being unable to look, which is not
			// the same fact as looking and finding nothing.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			unreadable = append(unreadable, fmt.Sprintf("%s (%v)", path, err))
			continue
		}
		var head [4]byte
		n, _ := f.Read(head[:])
		f.Close()
		if n < 4 {
			// Genuinely too short to carry a magic number, or a directory:
			// the submodule gitlink git names is one. Neither can be a
			// committed executable, so neither counts as read.
			continue
		}
		read++
		for _, m := range magics {
			if bytes.Equal(head[:], m) {
				found = append(found, path)
				break
			}
		}
	}
	// The floors. `ls-files` exiting 0 with an empty list is the shape a git
	// answering about the wrong tree produces - an empty index, a worktree
	// git does not consider part of the repository - and it is indis-
	// tinguishable from a clean tree by `found` alone. The tree carried 7440
	// tracked paths when this was written; 1000 is a floor no checkout of
	// this fork can legitimately fall under, not an estimate of the real
	// number.
	if tracked < 1000 {
		t.Fatalf("`%s -C %s ls-files -z` named %d tracked path(s), want at least 1000\n"+
			"That is not this repository. git exited 0, so it answered - about an empty index, or about "+
			"a tree that is not this fork. With no file list to read, this check has looked at nothing, "+
			"and an empty `found` below would have reported that as clean.",
			bin, root, tracked)
	}
	if len(unreadable) > 0 {
		// A tree-wide cause names every path at once, so the list is capped:
		// ten of seven thousand identical permission errors say the same
		// thing the whole seven thousand do, and the count is the finding.
		shown := unreadable[:min(len(unreadable), 10)]
		more := ""
		if len(unreadable) > len(shown) {
			more = fmt.Sprintf(" (and %d more)", len(unreadable)-len(shown))
		}
		t.Errorf("%d tracked path(s) could not be opened: %s%s\n"+
			"This check reads the first bytes of every tracked file, so a path it cannot open is a path "+
			"it is not checking - and a committed binary under one of them is exactly what it exists to "+
			"catch. These are not missing files; a missing file is tolerated above. Fix the permissions "+
			"rather than the check.",
			len(unreadable), strings.Join(shown, " "), more)
	}
	if read == 0 {
		t.Fatalf("git named %d tracked path(s) and not one of them could be read for its first four bytes\n"+
			"A tree-wide read failure reads exactly like a clean tree here: no file opened, so no magic "+
			"number matched, so nothing found. This is the shape #653 ranked worst in this package.",
			tracked)
	}
	if len(found) > 0 {
		t.Errorf("compiled binaries are tracked in git: %s\n"+
			"These are build artifacts. Remove with `git rm --cached <path>` and make sure .gitignore "+
			"covers them - an ignore rule alone does nothing for a path git already tracks.",
			strings.Join(found, " "))
	}
}

// gitBin resolves the git binary once, and fails when it is not there.
//
// It fails rather than skips, for the reason gofmtBin gives one file over:
// git is not optional here. The repository is a git fork whose whole subject
// is what is and is not committed, `scripts/pickup.sh` is a git script, and
// the guards below and in brief_tracked_test.go recompute their claims by
// asking git and nothing else. A machine without git cannot have produced
// the tree under test, so its absence is a broken environment rather than a
// tree worth passing - and a guard that stands down when its tool goes
// missing is green forever on exactly the machine where it stopped
// measuring.
//
// Resolving it here also keeps "git is absent" apart from "git ran and
// exited non-zero". Both callers used to get both from one `.Output()` error
// and report both as "git ls-files unavailable": no git at all, a root that
// is not a git repository, and a git that failed for its own reasons were
// indistinguishable, and two of the three were named wrong.
func gitBin(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is not on PATH: %v\n"+
			"This guard recomputes its claim by asking git what is tracked, so without git it has "+
			"measured nothing, and reporting that as a pass is the silent-green shape this fork keeps "+
			"rediscovering. git is a hard requirement for working in this repository at all - install "+
			"it or put it on PATH rather than relaxing the check.", err)
	}
	return bin
}

// repoRoot walks up to the checkout root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}
