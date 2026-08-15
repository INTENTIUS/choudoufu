// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"os/exec"
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

// forkOwnedMixedRoots are packages upstream owns that this fork has added
// files to. internal/command is the one: 21 live_*.go files, 11 of them
// tests, inside a package that is otherwise OpenTofu's.
//
// They are a separate list because the reason they were excluded is
// different: not "the fork does not own this" but "running it means running
// upstream's suite for the package too". That is a runtime cost, and it was
// stated rather than measured for as long as the exclusion stood.
//
// Measured, it is 60 seconds and green. So both checks now cover these
// roots, and 11 fork-authored test files - live_plan_test.go,
// live_mv_test.go, live_import_test.go and the rest - run in CI for the
// first time. They had never run: internal/command was in neither the gofmt
// step nor the test step, so the guard in this very file reported full
// coverage while the fork's own command surface had none.
//
// The class of bug here - #156, #164, #171 - is a check whose unit is the
// directory guarding a fork whose unit is the file. Adding a root is the
// cheap half of the fix; the expensive half is noticing that a package can
// be upstream's and still be somewhere the fork lives.
var forkOwnedMixedRoots = []string{"internal/command"}

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

// TestJustCIMirrorsTheWorkflow keeps the local rehearsal honest.
//
// `just ci` exists so a change can be checked before it is pushed, and it
// carries its own copy of the gofmt roots and the test globs. A copy drifts.
// This was not hypothetical for even one commit: widening the workflow to
// internal/command and running `just ci` reported green over the old
// narrower list, so the local run said "CI steps passed" about steps CI was
// no longer running. A green rehearsal of the wrong play is worse than no
// rehearsal, because it is trusted.
func TestJustCIMirrorsTheWorkflow(t *testing.T) {
	just, err := os.ReadFile("../justfile")
	if err != nil {
		t.Fatalf("reading justfile: %v", err)
	}
	wf, err := os.ReadFile(ciWorkflowRel)
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflowRel, err)
	}

	// Scope to the ci recipe. The justfile has a second `go test ./...`
	// line in another recipe, and matching the whole file would compare CI
	// against whichever one sorted first.
	recipe, ok := justRecipe(string(just), "ci")
	if !ok {
		t.Fatal("the justfile has no `ci:` recipe; this guard compares it against the workflow")
	}

	for _, c := range []struct {
		what string
		re   *regexp.Regexp
	}{
		{"gofmt roots", gofmtLine},
		{"go test package patterns", justGoTestLine},
	} {
		wfRe := c.re
		if c.what == "go test package patterns" {
			wfRe = goTestLine
		}
		wfm := wfRe.FindAllStringSubmatch(string(wf), -1)
		jm := c.re.FindAllStringSubmatch(recipe, -1)
		if len(wfm) != 1 {
			t.Fatalf("found %d %s in the workflow, want 1", len(wfm), c.what)
		}
		if len(jm) != 1 {
			t.Fatalf("found %d %s in the justfile, want 1 - `just ci` should mirror the workflow step for step", len(jm), c.what)
		}
		want := strings.Fields(wfm[0][1])
		got := strings.Fields(jm[0][1])
		// The justfile's test line carries `env -u PWD` for the symlink
		// trap; the workflow does not need it. Compare package patterns.
		got = dropFlags(got)
		want = dropFlags(want)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("`just ci` and CI disagree on %s.\n  workflow: %s\n  justfile: %s\n"+
				"They are the same check run in two places; a local `just ci` that passes over a "+
				"narrower list reports success for steps CI does not run.",
				c.what, strings.Join(want, " "), strings.Join(got, " "))
		}
	}
}

// justGoTestLine matches a `go test` invocation inside a just recipe, where
// there is no `run:` key and the line may be prefixed with `env -u PWD`.
var justGoTestLine = regexp.MustCompile(`(?m)^\s*(?:env -u PWD\s+)?go test\s+(.+)$`)

// justRecipe returns the body of a named just recipe: the indented lines
// following `name:` up to the next line that starts in column zero.
func justRecipe(doc, name string) (string, bool) {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if strings.TrimRight(line, " \t") != name+":" {
			continue
		}
		var body []string
		for _, l := range lines[i+1:] {
			if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
				break
			}
			body = append(body, l)
		}
		return strings.Join(body, "\n"), true
	}
	return "", false
}

// dropFlags removes leading-dash arguments and the env prefix, leaving the
// paths and package patterns the two files should agree on.
func dropFlags(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if strings.HasPrefix(f, "-") || f == "env" || f == "PWD" || f == "go" || f == "test" {
			continue
		}
		out = append(out, f)
	}
	return out
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

	// A mixed root is walked one level deep, because what the fork owns
	// there is the package itself and not its subtree. internal/command has
	// 20-odd pure-upstream subpackages - cliconfig, jsonformat, views and
	// the rest - that the fork has never touched, and recursing would drag
	// them into the fast tier under the banner of "fork-owned".
	shallow := make(map[string]bool, len(forkOwnedMixedRoots))
	for _, root := range forkOwnedMixedRoots {
		shallow[root] = true
	}

	for _, root := range append(append([]string{}, forkOwnedRoots...), forkOwnedMixedRoots...) {
		abs := filepath.Join("..", root)
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			t.Fatalf("fork-owned root %s does not exist; the list is stale", root)
		}
		if shallow[root] {
			entries, err := os.ReadDir(abs)
			if err != nil {
				t.Fatalf("reading %s: %v", root, err)
			}
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
					seen["./"+filepath.ToSlash(root)] = true
					break
				}
			}
			continue
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

// TestNoInheritedFileIsGofmtDirty is the cost check on adding a
// mostly-upstream package to the gofmt step.
//
// The old comment on that step said "a handful of upstream OpenTofu files
// are gofmt-dirty as inherited, and reformatting them would add permanent
// merge friction for no gain". That was the stated reason internal/command
// stayed out, and it was never true, or stopped being true without anyone
// rechecking: `gofmt -l` over the whole tree reports one file, and it is
// fork-added. So the trade the comment described did not exist.
//
// This test is what makes that claim recomputed rather than inherited. If an
// upstream sync ever does bring in dirty files, this fails and names them,
// and the fix at that point is a real decision - reformat them and carry the
// diff, or narrow the gofmt step back to fork-owned files by name - rather
// than the silent gap #171 found.
func TestNoInheritedFileIsGofmtDirty(t *testing.T) {
	for _, root := range forkOwnedMixedRoots {
		abs := filepath.Join("..", root)
		out, err := exec.Command("gofmt", "-l", abs).Output()
		if err != nil {
			t.Skipf("gofmt unavailable: %v", err)
		}
		var dirty, forkOwned []string
		for _, line := range strings.Fields(string(out)) {
			dirty = append(dirty, line)
			if strings.HasPrefix(filepath.Base(line), "live_") {
				forkOwned = append(forkOwned, line)
			}
		}
		if len(dirty) == 0 {
			continue
		}
		t.Errorf("%s has %d gofmt-dirty file(s), %d of them fork-added: %s\n"+
			"CI runs gofmt over this root because nothing in it was dirty when that was measured. "+
			"If these are fork files, run gofmt -w. If they came in from upstream, the choice is to "+
			"reformat and carry the diff, or to drop %s from forkOwnedMixedRoots and name the fork's "+
			"files individually in the workflow - but do not leave the root listed and the files dirty.",
			root, len(dirty), len(forkOwned), strings.Join(dirty, " "), root)
	}
}

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
	for _, want := range append(append([]string{}, forkOwnedRoots...), forkOwnedMixedRoots...) {
		if !got[want] {
			t.Errorf("CI's gofmt step does not cover %s, which this file calls fork-owned;\n"+
				"add it to the gofmt -l line in %s, or drop it from forkOwnedRoots/forkOwnedMixedRoots "+
				"if this fork no longer has files there",
				want, ciWorkflowRel)
		}
		delete(got, want)
	}
	for extra := range got {
		t.Errorf("CI's gofmt step covers %s, which neither forkOwnedRoots nor forkOwnedMixedRoots lists; "+
			"add it to one of them so the test step's coverage is decided too", extra)
	}
}
