// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
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
func TestNoCompiledBinaryIsTracked(t *testing.T) {
	root := repoRoot(t)

	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
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

	var found []string
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" {
			continue
		}
		// testdata legitimately holds fixture bytes of every shape.
		if strings.Contains(path, "testdata/") {
			continue
		}
		f, err := os.Open(filepath.Join(root, path))
		if err != nil {
			continue // a deleted-but-staged path, or a symlink out of tree
		}
		var head [4]byte
		n, _ := f.Read(head[:])
		f.Close()
		if n < 4 {
			continue
		}
		for _, m := range magics {
			if bytes.Equal(head[:], m) {
				found = append(found, path)
				break
			}
		}
	}
	if len(found) > 0 {
		t.Errorf("compiled binaries are tracked in git: %s\n"+
			"These are build artifacts. Remove with `git rm --cached <path>` and make sure .gitignore "+
			"covers them - an ignore rule alone does nothing for a path git already tracks.",
			strings.Join(found, " "))
	}
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
