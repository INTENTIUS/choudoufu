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
	"sort"
	"strings"
	"testing"
)

// This file is issue #164's guard. CI's test step used to run
// ./internal/live/... ./tools/survey-gen/, which left eight packages and 36
// test files outside it - including tools/row-gen, whose
// TestMismatchLedgerMatchesCommitted and
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
//
// internal/engine/applying is the second, added on issue #353's follow-up
// pass and found the same way: an audit reverted the fork's own change
// there - the create-time provisioner's `self` value keeping its sensitivity
// marks - and the whole repository stayed green, because the package had no
// test file at all and no tier ran it. It is upstream's package with two
// fork-authored functions in it, which is internal/command's situation
// exactly. 0.5 seconds, and the alternative is a fix nothing can catch
// regressing.
//
// internal/tofu is the third, found by an adversarial audit on 2026-08-23:
// resource_identity.go and its 713-line resource_identity_test.go - the
// tests proving no-Importer stub synthesis is safe - sat in the engine's
// core package, itself upstream's, with no root here or in the workflow
// naming it. Same shape as #164 and #171: a directory-level check guarding
// a fork whose unit is the file. internal/tofu has no subpackages of its
// own (only testdata/, which every walk here already skips), so unlike
// internal/command there is no upstream subtree to avoid dragging in;
// walking it is walking exactly what the fork touched plus what it did not.
// Measured, the whole package is 5-6s of test time (~12s wall including
// build), so it runs unshallowed like the fork-owned roots rather than
// one-level-deep like internal/command.
//
// #591 is the fourth instance of the same shape, and it was this file's own
// fix for the third that left the door open. The one-level-deep walk was
// justified above as "internal/command has 20-odd pure-upstream subpackages
// the fork has never touched". That was not true when it was written:
// internal/command/arguments and internal/command/views already held
// fork-added files, from 2026-08-12 and 2026-08-13, three days before the
// #171 pass on 2026-08-15. So a fork-owned subpackage of a mixed root was
// invisible to the walk and to CI both, and #583 shipped -parallelism into
// internal/command/arguments/live_import.go with that package's 42 test
// files having never executed.
//
// The fix is to stop asserting which subpackages the fork has touched and
// recompute it: forkAuthoredMixedRootDirs diffs the tree against the
// upstream commit this fork starts from, so a subpackage is fork-owned when
// it actually holds a fork-added file, and the next one to appear is
// required by the guard on the run it lands in.
//
// #623 is the fifth, and it is the one that says the list itself is the
// bug. #591 recomputed which subpackages of a KNOWN root the fork owns; it
// left the roots hand-maintained, and they were already wrong on the day it
// landed. Running the same diff over the whole tree instead of over these
// three roots found fork-added .go files in five more places:
//
//	internal/backend/local  live.go, plan_guard.go   (#613's un-migration seam)
//	internal/configs        7 files, 3 of them tests
//	internal/plans          requires_replace_path.go
//	internal/plugin         grpc_provider_list.go + test
//	internal/plugin6        grpc_provider_list.go + test
//
// Five fork-authored test files in three packages that had never executed
// anywhere, and none of it caught by anything, because every check in this
// file started from a list that did not name them.
//
// So the list stays - it is the declaration of where the fork lives, and
// nothing can derive the difference between "upstream's package the fork
// added to" and "a directory the fork wholly owns" - but it is no longer
// trusted. TestForkAuthoredCodeLivesUnderADeclaredRoot recomputes the same
// diff over the whole tree and fails on any fork-added .go file outside
// every declared root, so the next one lands red instead of invisible.
var forkOwnedMixedRoots = []string{
	"internal/backend",
	"internal/command",
	"internal/configs",
	"internal/engine/applying",
	"internal/plans",
	"internal/plugin",
	"internal/plugin6",
	"internal/tofu",
}

// upstreamBaseCommit is the last upstream OpenTofu commit before this fork's
// first (5acc1ee12f, "choudoufu: OpenTofu with stateless mode"). Everything
// under a mixed root that is not in this commit's tree was added by this
// fork, which is what makes "does the fork own this subpackage" a
// measurement rather than a claim in a comment.
//
// It is the same upstream commit tools/forkdiff-gen calls the fork point -
// "RFC: Speed up tofu show <planfile> by embedding schemas into the planfile
// (#4239)" - but deliberately not the same SHA. forkdiff-gen names
// 03743ce6e8, the pre-rewrite hash, which the 2026-08-14 history purge and
// re-root left off HEAD's ancestry entirely; it is in this checkout only
// because the `upstream` remote is configured here, and that tool documents
// `git fetch upstream` as its prerequisite. 46ee2e77a3 is the re-rooted copy
// of that same commit - identical tree 262f6fdf23 - and it is an ancestor of
// HEAD, so the workflow's existing fetch-depth: 0 is enough and CI needs no
// upstream remote. A guard that has to reach the network for its ground
// truth is a guard that skips when the network is down.
//
// checkUpstreamBase below asserts the ancestry rather than trusting this
// comment, so a repin to a commit only a local clone can see fails here
// instead of only in CI.
const upstreamBaseCommit = "46ee2e77a318e5b71f349a925fd43a7673201eec"

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

	// A mixed root is walked in full, but only the directories that actually
	// hold a fork-added file count. The whole subtree is upstream's until
	// this fork puts a file in it, and running the parts it has not touched
	// is running upstream's suite, which is a different job with a different
	// runtime budget. Which directories those are is recomputed against the
	// upstream base rather than listed here - see forkAuthoredMixedRootDirs
	// and #591 for why a list was the bug.
	forkAuthored := forkAuthoredMixedRootDirs(t)
	mixed := make(map[string]bool, len(forkOwnedMixedRoots))
	for _, root := range forkOwnedMixedRoots {
		mixed[root] = true
	}

	for _, root := range append(append([]string{}, forkOwnedRoots...), forkOwnedMixedRoots...) {
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
			dir := filepath.ToSlash(rel)
			if mixed[root] && !forkAuthored[dir] {
				return nil // upstream's subtree; the fork has added nothing here
			}
			seen["./"+dir] = true
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

// checkUpstreamBase fails if upstreamBaseCommit is not an ancestor of HEAD.
//
// That is what makes the pin CI-reachable: `actions/checkout` fetches this
// repository, so a commit on HEAD's ancestry is guaranteed present under the
// workflow's fetch-depth: 0 and a commit off it is not. forkdiff-gen's
// 03743ce6e8 is the second kind - resolvable here only because a developer
// clone carries an `upstream` remote - and repinning to something like it
// would pass every local run and fail every CI one.
func checkUpstreamBase(t *testing.T) {
	t.Helper()
	cmd := exec.Command("git", "merge-base", "--is-ancestor", upstreamBaseCommit, "HEAD")
	cmd.Dir = ".."
	if err := cmd.Run(); err != nil {
		t.Fatalf("upstreamBaseCommit %s is not an ancestor of HEAD (%v).\n"+
			"This guard classifies mixed-root subpackages against that commit's tree, and CI reaches "+
			"it only through this repository's own history. Pin it to the last upstream commit that is "+
			"on HEAD's ancestry - the re-rooted copy, not tools/forkdiff-gen's pre-rewrite 03743ce6e8 - "+
			"or, if the checkout is shallow, deepen it.",
			upstreamBaseCommit, err)
	}
}

// forkAuthoredMixedRootDirs returns the directories under forkOwnedMixedRoots
// that hold at least one .go file this fork added, keyed by repo-relative
// slash path ("internal/command/arguments").
//
// "Added" is measured against upstreamBaseCommit's tree: a path the fork
// created is not in it. Files the fork only edited do not count, and that is
// a deliberate line rather than an oversight. The module rename alone touched
// 537 files under internal/command, so "edited" would classify nearly the
// whole subtree as fork-owned and mean ./internal/command/... - measured at
// +18s wall and +100s CPU on the fast tier, most of it internal/command/e2etest
// at 44s, whose fork-relevant assertions ("choudoufu apply \"tfplan\"") skip
// without TF_ACC anyway. The residue this leaves is a package the fork edits
// but never adds a file to; internal/command/e2etest, jsonformat and
// jsonprovider are the three today, and their whole fork diff is the binary's
// name in expected strings plus one gofmt alignment.
//
// It fails rather than skips when git cannot answer. A guard that goes quiet
// on a shallow checkout is the shape this repo has shipped four times; the
// workflow already checks out with fetch-depth: 0 for the gauntlet ancestry
// guard, so the history is there.
func forkAuthoredMixedRootDirs(t *testing.T) map[string]bool {
	t.Helper()
	dirs := forkAddedGoDirs(t, forkOwnedMixedRoots, upstreamTreeFiles(t, forkOwnedMixedRoots))
	for _, root := range forkOwnedMixedRoots {
		if !anyUnder(dirs, root) {
			t.Errorf("no file under %s is fork-added, yet forkOwnedMixedRoots names it as a package "+
				"upstream owns and this fork has added to. Either the fork's files there were removed - "+
				"drop the root from forkOwnedMixedRoots and from CI's gofmt and test steps - or "+
				"upstreamBaseCommit is pinned past them.", root)
		}
	}
	return dirs
}

// upstreamTreeFiles returns the paths in upstreamBaseCommit's tree, as
// repo-relative slash paths. A nil paths argument reads the whole tree;
// otherwise git is asked only about those pathspecs.
//
// It fails rather than skips when git cannot answer. A guard that goes quiet
// on a shallow checkout is the shape this repo has shipped four times; the
// workflow already checks out with fetch-depth: 0 for the gauntlet ancestry
// guard, so the history is there.
func upstreamTreeFiles(t *testing.T, paths []string) map[string]bool {
	t.Helper()
	checkUpstreamBase(t)

	args := []string{"ls-tree", "-r", "--name-only", upstreamBaseCommit}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = ".."
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		t.Fatalf("git ls-tree %s failed: %v %s\n"+
			"This guard classifies packages by diffing the tree against the upstream commit this "+
			"fork starts from, so it needs that commit's history present. If the checkout is "+
			"shallow, deepen it; if the SHA no longer exists, repin upstreamBaseCommit in this "+
			"file to the commit before the fork's first.",
			upstreamBaseCommit, err, stderr)
	}

	files := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files[line] = true
		}
	}
	scope := "the whole tree"
	if len(paths) > 0 {
		scope = strings.Join(paths, " ")
	}
	if len(files) == 0 {
		t.Fatalf("upstream base %s has no files under %s; the pin is wrong, "+
			"and left alone it would silently classify every upstream package as fork-owned",
			upstreamBaseCommit, scope)
	}
	return files
}

// forkAddedGoDirs walks roots and returns the directories holding at least
// one .go file that is not in the given upstream file set, keyed by
// repo-relative slash path ("internal/command/arguments"). A root of "."
// walks the whole repository.
//
// It walks the working tree rather than listing git's index on purpose: a
// fork file that has been written but not yet committed is exactly the case
// the guard is for, and the author should see it red before pushing rather
// than after.
//
// Hidden directories are skipped, along with testdata, vendor and
// node_modules. That is not tidiness: `.corpus` is the #102 corpus of
// third-party Terraform repositories, and it carries 127 .go files today -
// none of them this fork's, all of them absent from the upstream base. A
// guard that names 127 other people's files is a guard that gets deleted.
func forkAddedGoDirs(t *testing.T, roots []string, upstream map[string]bool) map[string]bool {
	t.Helper()
	dirs := make(map[string]bool)
	for _, root := range roots {
		abs := filepath.Join("..", root)
		err := filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == abs {
					return nil
				}
				name := d.Name()
				if name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			rel, relErr := filepath.Rel("..", path)
			if relErr != nil {
				return relErr
			}
			if slash := filepath.ToSlash(rel); !upstream[slash] {
				dirs[filepath.ToSlash(filepath.Dir(rel))] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	return dirs
}

// anyUnder reports whether any key of dirs is root or sits beneath it.
//
// The subtree form matters: internal/backend's own directory holds no
// fork-added file - backend.go, where #613 declared Operation.PlanGuard, is
// an upstream file the fork edited - while internal/backend/local holds two.
// A check anchored on the root directory alone would call that root stale
// and tell the author to remove it, which is how the seam got out of CI's
// reach in the first place.
func anyUnder(dirs map[string]bool, root string) bool {
	for dir := range dirs {
		if dir == root || strings.HasPrefix(dir, root+"/") {
			return true
		}
	}
	return false
}

// TestForkAuthoredCodeLivesUnderADeclaredRoot is #623's guard, and the one
// that makes the two lists at the top of this file checked rather than
// trusted.
//
// Everything else here starts from forkOwnedRoots and forkOwnedMixedRoots
// and asks whether CI covers what they name. Nothing asked whether they name
// everything. They did not, twice: #591 found fork code in subpackages of a
// listed root, and #623 found it in five directories no list mentioned -
// internal/backend/local (#613's un-migration seam), internal/configs (three
// fork-authored test files), internal/plans, internal/plugin and
// internal/plugin6. Five test files that had never run in any tier, in a
// repository whose CI-coverage guard reported clean.
//
// So this runs the same upstream-base diff #591 introduced, over the whole
// tree instead of over three roots, and fails on any directory holding a
// fork-added .go file that no declared root covers. Adding a root is cheap
// and is not a decision - it is a statement of where the fork's code is.
// Keeping a package out of CI is the decision, and ciExcludedPackages is
// where it is made, with a reason.
func TestForkAuthoredCodeLivesUnderADeclaredRoot(t *testing.T) {
	dirs := forkAddedGoDirs(t, []string{"."}, upstreamTreeFiles(t, nil))
	if len(dirs) == 0 {
		t.Fatal("no directory in the tree holds a fork-added .go file; the walk or the pin is broken, not the tree")
	}

	declared := append(append([]string{}, forkOwnedRoots...), forkOwnedMixedRoots...)
	var undeclared []string
	for dir := range dirs {
		covered := false
		for _, root := range declared {
			if dir == root || strings.HasPrefix(dir, root+"/") {
				covered = true
				break
			}
		}
		if !covered {
			undeclared = append(undeclared, dir)
		}
	}
	sort.Strings(undeclared)

	for _, dir := range undeclared {
		t.Errorf("%s holds a .go file this fork added, and neither forkOwnedRoots nor "+
			"forkOwnedMixedRoots covers it.\n"+
			"Fork code outside every declared root is code CI does not test and the gofmt step does "+
			"not check, and nothing else in this file can see it - that is #623, and #591 before it.\n"+
			"Add the root it belongs under to forkOwnedMixedRoots (or forkOwnedRoots, if this fork "+
			"wholly owns it), then to CI's gofmt step and, if the package holds tests, to CI's fast "+
			"tier. Measure what the fast tier gains before adding a whole subtree.",
			dir)
	}
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

// gofmtBin resolves the gofmt binary once, and fails when it is not there.
//
// It fails rather than skips. gofmt ships in the same toolchain directory as
// the `go` binary already running this test, so a machine without it is a
// broken environment and not a tree worth passing - and the guard below is
// the only thing that recomputes the claim CI's gofmt step rests on. A check
// that stands down when its tool goes missing is green forever on exactly
// the machine where it stopped measuring, which is the `t.Skip` shape this
// repo has now shipped four times.
//
// Resolving it here also keeps "gofmt is absent" apart from "gofmt ran and
// exited non-zero". The old code got both from one `.Output()` error and
// reported both as "gofmt unavailable", so a root that gofmt could not parse
// or could not find was indistinguishable from no gofmt at all.
func gofmtBin(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gofmt")
	if err != nil {
		t.Fatalf("gofmt is not on PATH: %v\n"+
			"This guard is the only recomputation of the claim CI's `gofmt -l` step rests on, so it "+
			"fails here rather than skipping: without gofmt it has measured nothing, and reporting "+
			"that as a pass is the silent-green shape the fork keeps rediscovering. gofmt is part of "+
			"the Go toolchain running this test - put `go env GOROOT`/bin on PATH (setup-go already "+
			"does in CI) rather than relaxing the check.", err)
	}
	return bin
}

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
//
// The three ways this can end are kept apart on purpose. gofmt missing,
// gofmt exiting non-zero, and gofmt reporting dirty files are different
// facts about the tree and the machine, and they used to collapse into one
// blanket `t.Skipf("gofmt unavailable")` on any error at all - which is to
// say a permanent green whenever anything went wrong, with a message that
// named the wrong cause for two of the three.
func TestNoInheritedFileIsGofmtDirty(t *testing.T) {
	bin := gofmtBin(t)
	for _, root := range forkOwnedMixedRoots {
		path := filepath.Join("..", root)
		out, err := exec.Command(bin, "-l", path).Output()
		if err != nil {
			var stderr string
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = strings.TrimSpace(string(ee.Stderr))
			}
			t.Errorf("`%s -l %s` exited non-zero: %v %s\n"+
				"gofmt was found on PATH, so this is gofmt refusing to read the root rather than a "+
				"missing tool - a file under it that does not parse, or a path that is not there "+
				"because %s was renamed or removed without forkOwnedMixedRoots following. Fix the "+
				"file or the list: a root this guard cannot read is a root it is not checking, and "+
				"CI's gofmt step names the same root.",
				bin, path, err, stderr, root)
			continue
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

// ── Issue #578: a test with no runner is a test nobody has ──────────────

// This section is #164's shape one layer up. #164 was "a fork-owned package
// the workflow's glob never named"; this is "a test the glob DOES name,
// inside a package CI runs, that skips itself every time".
//
// tools/terralith-gen's TestValidateGeneratedTerralith - the only automated
// check that `terraform validate` accepts the synthetic estate the whole
// migration story is told against - was gated on TF_ACC/TF_FLOCI_TEST.
// Neither is set by ci.yml's fast tier, nor by the justfile's `ci` recipe;
// the single place CI sets TF_FLOCI_TEST is gauntlet.yml, on one step
// running one test in a different package. So the glob ran the package, the
// package reported ok, and the test inside it had never once executed.
//
// Its gate is now the terraform binary's presence, and ci.yml grew a
// validate-generated-terralith job to supply the binary. The two checks
// below are what stop that job from quietly going away or going quiet: one
// asserts the job exists, is wired to this test by name, and fails on a
// SKIP rather than accepting a zero exit; the other asserts the workflow
// still runs on ordinary pushes and pull requests, because a job moved
// behind workflow_dispatch is the same hole with a different lid.

// validateJobName is the ci.yml job that owns the generator's validate
// coverage.
const validateJobName = "validate-generated-terralith"

// workflowJob returns the body of a named job in a GitHub Actions workflow:
// the lines indented under `  <name>:` up to the next line at that same
// two-space indent. Deliberately textual, like justRecipe above - these
// guards are about what a human reading the file would see, and a YAML
// round-trip would let a semantically-equal rewrite pass while the comments
// explaining the job were deleted.
func workflowJob(doc, name string) (string, bool) {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if strings.TrimRight(line, " \t") != "  "+name+":" {
			continue
		}
		var body []string
		for _, l := range lines[i+1:] {
			if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "   ") {
				break
			}
			body = append(body, l)
		}
		return strings.Join(body, "\n"), true
	}
	return "", false
}

// TestCIRunsTheGeneratedTerralithValidation is issue #578's defect-3 guard.
func TestCIRunsTheGeneratedTerralithValidation(t *testing.T) {
	data, err := os.ReadFile(ciWorkflowRel)
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflowRel, err)
	}
	job, ok := workflowJob(string(data), validateJobName)
	if !ok {
		t.Fatalf("%s has no `%s:` job.\n"+
			"tools/terralith-gen's TestValidateGeneratedTerralith skips itself unless terraform is on PATH, "+
			"and this job is the only thing in CI that puts it there. Without the job the test is invisible "+
			"in every automated run, which is exactly the state issue #578 found it in.",
			ciWorkflowRel, validateJobName)
	}

	for _, want := range []struct {
		substr string
		why    string
	}{
		{"hashicorp/setup-terraform", "without a terraform binary the test skips, and a skip exits zero"},
		{"TestValidateGeneratedTerralith", "the job must run the validate test by name, not a glob that may stop matching it"},
		{"-v", "the pass line the job greps for is only printed under -v"},
		{"--- PASS: TestValidateGeneratedTerralith", "the job must fail on a SKIP; a zero exit does not distinguish the two, and that is the whole defect"},
		{"terralith-gen -scale 4", "the canonical-formatting check must cover scale 4, not only the smallest tier"},
		{`-fmt-bin ""`, "asserting canonical output with the generator's own formatting pass turned off is the strong form of the claim (#578 defect 1)"},
		{"fmt -check -recursive", "-recursive is the defect; -check because -diff writes the files it reports on"},
	} {
		if !strings.Contains(job, want.substr) {
			t.Errorf("the %s job in %s does not contain %q: %s", validateJobName, ciWorkflowRel, want.substr, want.why)
		}
	}

	// Commands only. The comments in that job talk ABOUT `fmt -diff` in
	// order to explain why it must not be used, and a substring scan over
	// the whole block would flag the explanation.
	for _, line := range strings.Split(job, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "fmt -diff") {
			t.Errorf("the %s job runs `fmt -diff`, which REWRITES the files it reports on. "+
				"A -check run after it passes unconditionally and proves nothing.\n  %s",
				validateJobName, strings.TrimSpace(line))
		}
	}
}

// TestCIValidationRunsOnOrdinaryEvents keeps the job above on the ordinary
// path. A check that only a human can trigger is the same as no check; #578's
// acceptance says it in as many words - "it must execute on an ordinary CI
// run with no special environment".
func TestCIValidationRunsOnOrdinaryEvents(t *testing.T) {
	data, err := os.ReadFile(ciWorkflowRel)
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflowRel, err)
	}
	doc := string(data)
	if _, ok := workflowJob(doc, validateJobName); !ok {
		t.Skipf("no %s job; TestCIRunsTheGeneratedTerralithValidation reports that", validateJobName)
	}

	// The trigger block is everything before the first job.
	triggers := doc
	if i := strings.Index(doc, "\njobs:"); i > 0 {
		triggers = doc[:i]
	}
	for _, event := range []string{"push:", "pull_request:"} {
		if !strings.Contains(triggers, event) {
			t.Errorf("%s no longer runs on %s, so %s does not run on an ordinary change",
				ciWorkflowRel, strings.TrimSuffix(event, ":"), validateJobName)
		}
	}
	if strings.Contains(triggers, "workflow_dispatch") && !strings.Contains(triggers, "pull_request") {
		t.Errorf("%s is dispatch-only; %s would then run only when a human asks, which is the state #578 found",
			ciWorkflowRel, validateJobName)
	}
}
