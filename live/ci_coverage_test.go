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
var forkOwnedMixedRoots = []string{"internal/command", "internal/engine/applying", "internal/tofu"}

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
