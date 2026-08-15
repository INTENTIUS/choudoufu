// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This file is issue #164's guard. CI's test step used to run
// ./internal/live/... ./tools/survey-gen/, which left eight packages and 36
// test files outside it - including tools/row-gen, whose
// TestConvergenceArtifactMatchesCommitted and
// TestNoRatifiedRowNamesAnUnknownArgument are the tests that catch a hand
// edit to the generated identity table, and ./live itself, which holds the
// provider-pin drift check next door in pins_drift_test.go.
//
// It was measured rather than argued: pointing aws_backup_vault's identity
// at an argument no provider schema has passed every step CI ran.
//
// A widened glob alone does not stay widened. A package added under a root
// nobody thought to list is invisible again, and the failure is silent in
// exactly the same way. So the list is checked against the tree rather than
// trusted: every fork-owned package holding a _test.go file must be matched
// by the glob CI actually runs, or named in ciExcludedPackages with a
// reason.

// forkOwnedRoots are the directories this fork authors. Upstream OpenTofu's
// own packages are deliberately out of scope: the fast tier exists to guard
// the fork's own surface, and running upstream's suite is a different job
// with a different runtime budget.
//
// site/ holds no test files today and is covered by CI's own "Docs site
// build" step instead; it is listed so that adding a test there is caught
// here rather than silently unrun.
var forkOwnedRoots = []string{"internal/live", "tools", "live", "cmd", "site"}

// ciExcludedPackages names a fork-owned test package CI deliberately does
// not run, and why. Empty is the intended state. An entry here is a
// standing decision, not a parking space: it should name what would have to
// change for the package to rejoin the tier.
var ciExcludedPackages = map[string]string{}

// ciWorkflowRel is the workflow whose test step this file holds to the tree.
const ciWorkflowRel = "../.github/workflows/ci.yml"

// goTestLine matches the workflow's `go test` invocation and captures its
// arguments. Anchored on "go test " so a future `go build` or `gofmt` step
// cannot be mistaken for it.
var goTestLine = regexp.MustCompile(`(?m)^\s*run:\s*go test\s+(.+)$`)

// TestCIRunsEveryForkOwnedTestPackage is the guard described above.
func TestCIRunsEveryForkOwnedTestPackage(t *testing.T) {
	patterns := ciTestPatterns(t)
	pkgs := forkOwnedTestPackages(t)

	if len(pkgs) == 0 {
		t.Fatal("found no fork-owned test packages; the walk is broken, not the tree")
	}

	for _, pkg := range pkgs {
		if reason, excluded := ciExcludedPackages[pkg]; excluded {
			t.Logf("%s is excluded from CI on purpose: %s", pkg, reason)
			continue
		}
		if !matchedByAny(pkg, patterns) {
			t.Errorf("package %s holds tests that CI never runs.\n"+
				"CI's step is: go test %s\n"+
				"Add the package to that step in .github/workflows/ci.yml, or add it to "+
				"ciExcludedPackages here with the reason it stays out.",
				pkg, strings.Join(patterns, " "))
		}
	}
}

// TestCIExclusionsAreReal keeps ciExcludedPackages from outliving its
// subjects: an entry naming a package that no longer has tests, or that the
// glob now covers anyway, is a stale exemption and reads as a live one.
func TestCIExclusionsAreReal(t *testing.T) {
	if len(ciExcludedPackages) == 0 {
		return
	}
	patterns := ciTestPatterns(t)
	have := make(map[string]bool)
	for _, pkg := range forkOwnedTestPackages(t) {
		have[pkg] = true
	}
	for pkg, reason := range ciExcludedPackages {
		switch {
		case !have[pkg]:
			t.Errorf("ciExcludedPackages names %s (%q), which holds no test files; delete the entry", pkg, reason)
		case matchedByAny(pkg, patterns):
			t.Errorf("ciExcludedPackages names %s (%q), but CI's glob already covers it; delete the entry", pkg, reason)
		}
	}
}

// ciTestPatterns reads the package patterns off the workflow's go test step.
func ciTestPatterns(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(ciWorkflowRel)
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflowRel, err)
	}
	m := goTestLine.FindAllStringSubmatch(string(data), -1)
	if len(m) != 1 {
		t.Fatalf("found %d `run: go test` steps in %s, want exactly 1 - this guard reads a single step and needs teaching about the others", len(m), ciWorkflowRel)
	}
	var patterns []string
	for _, f := range strings.Fields(m[0][1]) {
		if strings.HasPrefix(f, "-") {
			continue // a flag, not a package pattern
		}
		patterns = append(patterns, f)
	}
	if len(patterns) == 0 {
		t.Fatalf("CI's go test step names no package patterns: %q", m[0][1])
	}
	return patterns
}

// forkOwnedTestPackages walks forkOwnedRoots for directories holding at
// least one _test.go file, returned as repo-relative import patterns
// ("./tools/row-gen"). testdata trees are skipped: their .go files are
// fixtures rather than packages the toolchain builds.
func forkOwnedTestPackages(t *testing.T) []string {
	t.Helper()
	seen := make(map[string]bool)
	for _, root := range forkOwnedRoots {
		abs := filepath.Join("..", root)
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			t.Fatalf("fork-owned root %s does not exist; the list is stale", root)
		}
		err := filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel("..", filepath.Dir(path))
			if relErr != nil {
				return relErr
			}
			seen["./"+filepath.ToSlash(rel)] = true
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	out := make([]string, 0, len(seen))
	for pkg := range seen {
		out = append(out, pkg)
	}
	return out
}

// matchedByAny reports whether a package is covered by one of CI's patterns.
// "./x/..." covers ./x and everything beneath it; "./x/" and "./x" cover
// exactly ./x.
func matchedByAny(pkg string, patterns []string) bool {
	for _, p := range patterns {
		if strings.HasSuffix(p, "/...") {
			base := strings.TrimSuffix(p, "/...")
			if pkg == base || strings.HasPrefix(pkg, base+"/") {
				return true
			}
			continue
		}
		if strings.TrimSuffix(p, "/") == strings.TrimSuffix(pkg, "/") {
			return true
		}
	}
	return false
}

// gofmtLine matches the workflow's gofmt invocation and captures its roots.
var gofmtLine = regexp.MustCompile("gofmt -l ([^\"'`)\n]+)")

// TestCIGofmtCoversTheSameRootsAsTheTestStep keeps the two halves of "what
// this fork owns" from drifting apart.
//
// They had drifted. The test step reached ./internal/live/... and one tools
// package (#164); the gofmt step reached internal/live, cmd and site. So
// tools/ and live/ were inside the fork and outside both checks, and two
// files under tools/ were gofmt-dirty for as long as anyone had been
// looking - not caught, because nothing looked.
//
// One list, checked twice, is the point: forkOwnedRoots is what this file
// walks for test packages, and it is what the gofmt step must name.
func TestCIGofmtCoversTheSameRootsAsTheTestStep(t *testing.T) {
	data, err := os.ReadFile(ciWorkflowRel)
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflowRel, err)
	}
	m := gofmtLine.FindAllStringSubmatch(string(data), -1)
	if len(m) != 1 {
		t.Fatalf("found %d `gofmt -l` invocations in %s, want exactly 1", len(m), ciWorkflowRel)
	}

	got := make(map[string]bool)
	for _, root := range strings.Fields(m[0][1]) {
		got[root] = true
	}
	for _, want := range forkOwnedRoots {
		if !got[want] {
			t.Errorf("CI's gofmt step does not cover %s, which forkOwnedRoots calls fork-owned;\n"+
				"add it to the gofmt -l line in %s, or drop it from forkOwnedRoots if this fork no longer owns it",
				want, ciWorkflowRel)
		}
		delete(got, want)
	}
	for extra := range got {
		t.Errorf("CI's gofmt step covers %s, which forkOwnedRoots does not list; add it there so the test step covers it too", extra)
	}
}
