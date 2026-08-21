// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// sweepRoots are the directory trees TestNoUnregisteredRefusalsInTheTree
// walks: every configuration committed to this repository, plus the
// third-party corpus when it has been fetched.
//
// internal/ is in the list because it holds upstream OpenTofu's own test
// configurations, which are the most adversarial set available - they were
// written to break a parser and an evaluator, by people with no interest in
// this fork's admission table. That is exactly the property wanted here.
var sweepRoots = []string{"live", "internal", ".corpus"}

// TestNoUnregisteredRefusalsInTheTree is the evidence behind the half of
// internal/live/passthrough's completeness claim that no source scan can
// reach.
//
// The scan-enforced half is internal/configs' static evaluator: that package
// cannot gain a diagnostic without failing passthrough's own test. The other
// half is HCL's expression evaluation and internal/addrs' reference parser,
// which are upstream surfaces whose whole diagnostic sets are not ours to
// enumerate - a scan of them would demand registry entries for parse errors
// that can never reach a live user, which would be fiction rather than
// documentation.
//
// So the argument for those entries is empirical, and this is the
// experiment. It runs both configuration-only passes over every
// configuration in the tree and fails on any refusal none of the five
// registries can name.
//
// It exists because the alternative was a number in a doc comment. That
// number was true when it was written and unreproducible the moment the
// scratch program that produced it was deleted, which is the failure mode
// this repository has spent three sessions on.
//
// It also catches panics, which no ranking ever can: a run that crashes
// produces no refusal to count. The marked-value crash in identity's
// buildExpansion was found exactly this way and would have been found by
// this test.
func TestNoUnregisteredRefusalsInTheTree(t *testing.T) {
	if testing.Short() {
		t.Skip("walks every configuration in the tree; skipped under -short")
	}

	root := flocitest.RepoRoot(t)
	dirs := configDirs(t, root)
	if len(dirs) < 500 {
		t.Fatalf("found only %d configuration directories under %v; the walk is not reaching the tree it is supposed to cover", len(dirs), sweepRoots)
	}
	// live and internal alone hold on the order of 1700 configuration
	// directories; .corpus, when present, adds several thousand more (7748
	// total measured 2026-08-19, .corpus fetched). 500 is loose enough that
	// a sweep silently missing .corpus entirely - which is exactly what
	// happened when filepath.WalkDir did not descend a symlinked .corpus
	// root - still clears it and passes. When .corpus is actually on disk,
	// demand a count only a real walk of it would produce.
	if corpusRoot := filepath.Join(root, ".corpus"); dirExists(corpusRoot) && len(dirs) < 3000 {
		t.Fatalf("found only %d configuration directories under %v even though %s exists; "+
			"a sweep root that resolves but is not actually being descended (e.g. a symlink filepath.WalkDir skipped) "+
			"would look exactly like this - see configDirs's filepath.EvalSymlinks step", len(dirs), sweepRoots, corpusRoot)
	}

	known := map[string]bool{}
	for _, r := range AllRefusals() {
		known[string(r.Layer)+"/"+r.ID] = true
	}

	var loaded int
	unregistered := map[string]string{}
	for _, dir := range dirs {
		report, panicked := analyzeSafely(t, dir)
		if panicked != nil {
			// A panic is worse than any refusal: the user gets a stack
			// trace instead of a diagnostic, and nothing counts it.
			t.Errorf("%s panicked during analysis: %v", rel(root, dir), panicked)
			continue
		}
		if !report.Readable() {
			continue
		}
		loaded++
		for _, f := range report.Findings {
			key := string(f.Layer) + "/" + f.ID
			if known[key] {
				continue
			}
			if _, seen := unregistered[key]; !seen {
				unregistered[key] = rel(root, dir)
			}
		}
	}

	if loaded == 0 {
		t.Fatal("no configuration in the tree loaded; the walk is broken rather than the registries being complete")
	}
	t.Logf("analyzed %d loadable configurations of %d directories under %v", loaded, len(dirs), sweepRoots)

	keys := make([]string, 0, len(unregistered))
	for k := range unregistered {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Errorf("%s reaches a user as a refusal and is in none of the five registries (first seen in %s).\n"+
			"Add it to internal/live/passthrough with the origin that raises it, then run `just limits`.", k, unregistered[k])
	}
}

// analyzeSafely runs the analysis and converts a panic into a value, so one
// crashing configuration reports itself instead of ending the test run.
func analyzeSafely(t *testing.T, dir string) (report Report, panicked any) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			panicked = r
		}
	}()
	return Dir(t.Context(), dir, Context{}), nil
}

// configDirs returns every directory under the sweep roots holding at least
// one .tf or .tf.json file. A root that does not exist is skipped: .corpus is
// gitignored and absent until someone runs `just corpus-fetch`.
//
// Every sweep root is resolved with filepath.EvalSymlinks before the walk.
// filepath.WalkDir Lstats its own root argument, so when the root itself is a
// symlink (most worktrees symlink .corpus in rather than refetch it - see
// HANDOFF.md), WalkDir sees a non-directory entry, calls the walk function
// once for that entry, and returns without ever descending into it. The
// walk then silently covers only the roots that happened to be real
// directories, finishes in a couple of seconds instead of several minutes,
// and reports a plausible-looking count. Resolving the root first turns a
// symlinked root into the real path WalkDir already handles correctly;
// nested symlinks under a sweep root are left alone; the corpus's own
// symlinks are all to individual files, not directories, so WalkDir's normal
// per-entry handling already treats them as leaves.
func configDirs(t *testing.T, root string) []string {
	t.Helper()

	seen := map[string]bool{}
	for _, name := range sweepRoots {
		base := filepath.Join(root, name)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(base); err == nil {
			base = resolved
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
			}
			if strings.HasSuffix(path, ".tf") || strings.HasSuffix(path, ".tf.json") {
				seen[filepath.Dir(path)] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %s", base, err)
		}
	}

	out := make([]string, 0, len(seen))
	for dir := range seen {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

// TestConfigDirsFollowsASymlinkedSweepRoot proves configDirs walks through a
// sweep root that is itself a symlink, not just a real directory.
//
// filepath.WalkDir Lstats its own root argument. When that root is a symlink
// to a directory, Lstat reports it as a non-directory entry, WalkDir calls
// the walk function once for that single entry, and returns without ever
// descending - the directory behind the symlink is never visited. Most
// worktrees symlink .corpus in rather than refetch it (see HANDOFF.md's
// "materialize a real directory" trap), and .corpus is itself one of
// sweepRoots, so this was not a hypothetical: it silently limited every such
// worktree's sweep to live/ and internal/ while still reporting a plausible
// "analyzed N loadable configurations" line and a clean pass.
//
// Revert the filepath.EvalSymlinks call in configDirs to see this test fail:
// dirs comes back without the .tf directory behind the symlinked root.
func TestConfigDirsFollowsASymlinkedSweepRoot(t *testing.T) {
	root := t.TempDir()

	real := t.TempDir()
	nested := filepath.Join(real, "modules", "example")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "main.tf"), []byte(`resource "null_resource" "x" {}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// ".corpus" is not a magic string here - it is drawn from sweepRoots
	// itself, so this test tracks whatever the real sweep actually walks.
	found := false
	for _, name := range sweepRoots {
		if name == ".corpus" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sweepRoots %v no longer contains \".corpus\"; update this test to name whichever root worktrees symlink in", sweepRoots)
	}

	link := filepath.Join(root, ".corpus")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	dirs := configDirs(t, root)

	want, err := filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatalf("resolving the expected directory: %s", err)
	}
	for _, d := range dirs {
		if d == want {
			return
		}
	}
	t.Fatalf("configDirs(%q) = %v, want it to include %s (the .tf directory reached only through the symlinked .corpus root); "+
		"a symlinked sweep root is being skipped instead of walked", root, dirs, want)
}

func rel(root, dir string) string {
	if r, err := filepath.Rel(root, dir); err == nil {
		return r
	}
	return dir
}

// dirExists reports whether path exists, following symlinks - the same
// existence test configDirs itself uses to decide whether a sweep root has
// been fetched.
func dirExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
