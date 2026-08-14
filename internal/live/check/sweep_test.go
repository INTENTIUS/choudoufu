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
func configDirs(t *testing.T, root string) []string {
	t.Helper()

	seen := map[string]bool{}
	for _, name := range sweepRoots {
		base := filepath.Join(root, name)
		if _, err := os.Stat(base); err != nil {
			continue
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

func rel(root, dir string) string {
	if r, err := filepath.Rel(root, dir); err == nil {
		return r
	}
	return dir
}
