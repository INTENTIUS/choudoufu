// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Issue #691, the third instance of the #591/#623 class: a test tier that
// exists but gates nothing. The staterecord SSM defect shipped through a
// conformance suite whose against-a-real-service run was gated on
// TF_FLOCI_TEST=1 and reachable only by a human typing `just test-floci` -
// no workflow set the variable, and the nightly scoped it to one package.
// While writing this guard a second gap surfaced: seven gated files live
// under tools/, which `make test-floci`'s ./internal/live/... scope never
// ran even when a human did type it.
//
// The roster is derived, never hand-listed: every package containing a
// flocitest.Gate call site. Two assertions close the class:
//
//   - every roster package is matched by a package pattern in the
//     Makefile's test-floci recipe, so the human-typed tier covers the
//     whole roster;
//   - at least one workflow under .github/workflows runs test-floci, so
//     the tier gates something automatically rather than depending on a
//     human remembering it exists.
func TestFlociTierCoversEveryGatedPackage(t *testing.T) {
	root := repoRoot(t)

	// The pattern requires the call's open paren, so this file - whose
	// prose and error strings name flocitest.Gate without calling it -
	// cannot match itself. A condition that can match itself is the
	// while-pgrep-matches-its-own-command-line bug wearing a test's
	// clothes (CLAUDE.md records six workers lost to that one).
	out, err := exec.Command("git", "-C", root, "grep", "-l", "flocitest\\.Gate(", "--", "*_test.go").Output()
	if err != nil {
		t.Fatalf("enumerating flocitest.Gate call sites: %v", err)
	}
	pkgs := map[string]bool{}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f == "" {
			continue
		}
		pkgs[filepath.Dir(f)] = true
	}
	if len(pkgs) < 5 {
		t.Fatalf("found only %d gated packages; the roster extraction is broken rather than the tier being small", len(pkgs))
	}

	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	recipe := ""
	for _, block := range strings.Split(string(mk), "\n\n") {
		if strings.Contains(block, "test-floci:") && strings.Contains(block, "TF_FLOCI_TEST=1") {
			recipe = block
		}
	}
	if recipe == "" {
		t.Fatal("Makefile has no test-floci recipe setting TF_FLOCI_TEST=1; the tier's one entry point is gone")
	}
	var patterns []string
	for _, field := range strings.Fields(recipe) {
		if strings.HasPrefix(field, "./") && strings.HasSuffix(field, "/...") {
			patterns = append(patterns, strings.TrimSuffix(strings.TrimPrefix(field, "./"), "/..."))
		}
	}
	for pkg := range pkgs {
		covered := false
		for _, pat := range patterns {
			if pkg == pat || strings.HasPrefix(pkg, pat+"/") {
				covered = true
			}
		}
		if !covered {
			t.Errorf("package %s has flocitest.Gate tests but no test-floci pattern (%v) covers it; its against-a-real-service tier can never run", pkg, patterns)
		}
	}

	wfDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		t.Fatalf("reading %s: %v", wfDir, err)
	}
	automated := false
	for _, e := range entries {
		b, rerr := os.ReadFile(filepath.Join(wfDir, e.Name()))
		if rerr != nil {
			t.Fatalf("reading workflow %s: %v", e.Name(), rerr)
		}
		if strings.Contains(string(b), "test-floci") {
			automated = true
		}
	}
	if !automated {
		t.Error("no workflow under .github/workflows runs test-floci; the against-a-real-service tier gates nothing without a human remembering to type it (issue #691)")
	}
}

// Issue #1280, the wiring gap the test above could not see. It asserts that
// the Makefile recipe covers the whole gated roster and that a workflow runs
// that recipe. Both were true for seventeen consecutive nights during which
// the tier measured nothing, because the workflow ran the recipe without
// installing the binaries the recipe's own tests drive: every gated test
// failed at 0.00s on flocitest.RequireBinary and not one executed its body.
//
// So the roster's requirements are derived from the tests themselves - every
// RequireBinary argument in every roster package, with identifier arguments
// (the `terraformBin` constant 28 files declare) resolved against their own
// package - and each one is checked against what the workflow provides.
//
// The provider side is the one hand-written table here, and it is small on
// purpose: two of the five binaries need no step because the runner image
// ships them, and the other three have exactly one installer action each.
// Getting that table wrong fails loudly rather than silently, because a
// binary with no entry is reported as unprovidable rather than skipped -
// which is how `tofu` surfaced. internal/live/statefulcost requires it and
// floci-tier.yml installed it no more than it installed terraform, so fixing
// only the reported failure would have bought an eighteenth blind night for
// that one test.
func TestFlociTierWorkflowInstallsWhatTheRosterRequires(t *testing.T) {
	root := repoRoot(t)

	// The roster, derived the same way the test above derives it.
	out, err := exec.Command("git", "-C", root, "grep", "-l", "flocitest\\.Gate(", "--", "*_test.go").Output()
	if err != nil {
		t.Fatalf("enumerating flocitest.Gate call sites: %v", err)
	}
	pkgs := map[string]bool{}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f != "" {
			pkgs[filepath.Dir(f)] = true
		}
	}
	if len(pkgs) < 5 {
		t.Fatalf("found only %d gated packages; the roster extraction is broken", len(pkgs))
	}

	// Every RequireBinary argument in the roster, as written: a quoted
	// literal, or an identifier to resolve.
	reqRe := regexp.MustCompile(`RequireBinary\(t,\s*(?:"([^"]+)"|([A-Za-z_][A-Za-z0-9_]*))\)`)
	// `terraformBin = "terraform"`, const or var, so an identifier argument
	// resolves to the binary it actually names rather than being skipped.
	bindRe := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]+)"`)

	required := map[string]string{} // binary -> a file that requires it
	for pkg := range pkgs {
		dir := filepath.Join(root, pkg)
		entries, rerr := os.ReadDir(dir)
		if rerr != nil {
			t.Fatalf("reading roster package %s: %v", pkg, rerr)
		}
		// Bindings are package-scoped and not always test-scoped, so they
		// are collected from every .go file in the package before any
		// identifier in it is resolved. tools/estate-gen and tools/survey-gen
		// are why: their tests require `defaultInitBin`, which main.go
		// declares as "terraform". Reading only _test.go files reported
		// those nine call sites as unresolvable.
		binds := map[string]string{}
		var files []string
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			b, ferr := os.ReadFile(filepath.Join(dir, e.Name()))
			if ferr != nil {
				t.Fatalf("reading %s: %v", e.Name(), ferr)
			}
			if strings.HasSuffix(e.Name(), "_test.go") {
				files = append(files, string(b))
			}
			for _, m := range bindRe.FindAllStringSubmatch(string(b), -1) {
				binds[m[1]] = m[2]
			}
		}
		for i, src := range files {
			for _, m := range reqRe.FindAllStringSubmatch(src, -1) {
				name := m[1]
				if name == "" {
					resolved, ok := binds[m[2]]
					if !ok {
						// Not a failure: an identifier this test cannot
						// resolve is reported so the gap is visible rather
						// than silently dropping a requirement.
						t.Errorf("%s: RequireBinary(t, %s) names an identifier this guard cannot resolve to a "+
							"binary; either bind it as `%s = \"<name>\"` in the package or pass the literal, so the "+
							"workflow check below can see the requirement (issue #1280)", pkg, m[2], m[2])
						continue
					}
					name = resolved
				}
				if _, seen := required[name]; !seen {
					required[name] = fmt.Sprintf("%s (file %d)", pkg, i)
				}
			}
		}
	}
	if len(required) == 0 {
		t.Fatal("derived no required binaries from the gated roster; the extraction is broken rather than the tier needing nothing")
	}

	// What makes each binary available in a job. "" means the ubuntu-latest
	// runner image ships it and no step is needed.
	providedBy := map[string]string{
		"docker":    "",
		"aws":       "",
		"go":        "actions/setup-go",
		"terraform": "hashicorp/setup-terraform",
		"tofu":      "opentofu/setup-opentofu",
	}

	wfDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		t.Fatalf("reading %s: %v", wfDir, err)
	}
	checked := 0
	for _, e := range entries {
		b, rerr := os.ReadFile(filepath.Join(wfDir, e.Name()))
		if rerr != nil {
			t.Fatalf("reading workflow %s: %v", e.Name(), rerr)
		}
		wf := string(b)
		if !strings.Contains(wf, "test-floci") {
			continue
		}
		checked++
		// Full-line comments are stripped before anything structural is
		// asserted, because a workflow's comments explain the very settings
		// being checked and so contain them verbatim. The first draft of
		// this guard matched `terraform_wrapper: false` in the comment that
		// justifies it, which made that assertion unfailable - it stayed
		// green through a deliberate removal of the real setting.
		var code []string
		for _, line := range strings.Split(wf, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			code = append(code, line)
		}
		wfCode := strings.Join(code, "\n")
		for bin, requirer := range required {
			action, known := providedBy[bin]
			if !known {
				t.Errorf("%s runs the floci tier, whose roster requires %q (%s), but this guard has no entry for "+
					"how a workflow provides %q. Add it to providedBy - with \"\" if the runner image ships it - "+
					"rather than leaving the requirement unchecked (issue #1280)", e.Name(), bin, requirer, bin)
				continue
			}
			if action == "" {
				continue
			}
			if !strings.Contains(wfCode, "uses: "+action) {
				t.Errorf("%s runs the floci tier, but installs no %s. The roster requires %q (%s calls "+
					"flocitest.RequireBinary for it), so every gated test in this workflow fails at 0.00s on "+
					"\"%s is required by this test but is not on PATH\" without executing a line of its body - "+
					"a red nightly that measures nothing. This is issue #1280, seventeen consecutive nights of it.",
					e.Name(), action, bin, requirer, bin)
			}
		}
		// terraform specifically: the version is pinned from
		// live/oracle-versions.json, never a literal, for #544's reason (root
		// cause of #498). A nightly on a different terraform than CI is a
		// second source of divergence, so the tier reads the same file the
		// fast job, the gauntlet, contribute, k8s-smoke and live-cert read.
		if strings.Contains(wfCode, "uses: hashicorp/setup-terraform") {
			if !strings.Contains(wfCode, "oracle-versions.json") {
				t.Errorf("%s installs terraform but does not read live/oracle-versions.json; the stock oracle must "+
					"be pinned from that one file rather than floating or hard-coded (issue #544)", e.Name())
			}
			if m := regexp.MustCompile(`(?m)^\s*terraform_version:\s*["']?\d`).FindString(wfCode); m != "" {
				t.Errorf("%s pins terraform to a literal version (%q); read it from live/oracle-versions.json so a "+
					"bump moves every workflow at once (issue #544)", e.Name(), strings.TrimSpace(m))
			}
			// The wrapper this action installs by default is a shell script
			// around the real binary, and these tests read its exit code and
			// its stderr.
			if !regexp.MustCompile(`(?m)^\s*terraform_wrapper:\s*false\s*$`).MatchString(wfCode) {
				t.Errorf("%s installs terraform without terraform_wrapper: false; the wrapper sits between the "+
					"tests and the binary whose exit code and stderr they read", e.Name())
			}
		}
	}
	if checked == 0 {
		t.Error("no workflow under .github/workflows runs test-floci; the against-a-real-service tier gates nothing (issue #691)")
	}
}
