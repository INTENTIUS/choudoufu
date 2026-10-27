// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/tools/statpages"
)

// A Go test proof (go_test, GitHub issue #2288) cites a test in this
// repository as the proof of a cell, where a smoke scenario's claim was
// already held by a Go test and the scenario cost a maintainer minutes for
// no more than the test shows. Its dated run is a CI run at a recorded
// commit for a test CI's fast tier runs, or a maintainer's run with its log
// for one behind an environment gate. tools/gotest-record writes both, one
// file per test under live/smoke/evidence/go-test/, and nothing else does.
//
// This file holds a Go test proof to the code it names: the package is a
// directory, the test is a function in it, the command is the one that runs
// exactly that test, a gate names an environment variable the package's
// tests read and a recipe that sets it, and a recorded run says what the
// proof says. Proving it red: rename TestBucketContract in claims.json, set
// a gate on an ungated proof, or change a record's test name; each fails a
// different check below.

// goTestProof is one go_test proof with the cell it proves.
type goTestProof struct {
	Claim    int
	Provider string
	Proof    smokeProof
}

func (g goTestProof) String() string {
	return fmt.Sprintf("claim %d (%s) Go test %s", g.Claim, g.Provider, g.Proof.GoTest)
}

func goTestProofs(f smokeClaimsFile) []goTestProof {
	var out []goTestProof
	for _, c := range f.Claims {
		for _, p := range f.ProviderOrder {
			for _, pr := range c.Providers[p].Proofs {
				if pr.GoTest != "" {
					out = append(out, goTestProof{Claim: c.ID, Provider: p, Proof: pr})
				}
			}
		}
	}
	return out
}

// goTestRunPattern is the -run pattern that selects exactly test: each
// level anchored, as `go test` splits the pattern on slashes.
func goTestRunPattern(test string) string {
	parts := strings.Split(test, "/")
	for i, p := range parts {
		parts[i] = "^" + regexp.QuoteMeta(p) + "$"
	}
	return strings.Join(parts, "/")
}

// goTestCommand is the command an ungated Go test proof states: the one
// that runs that test and nothing else.
func goTestCommand(pkg, test string) string {
	return fmt.Sprintf("go test %s -run '%s'", pkg, goTestRunPattern(test))
}

var goTestName = regexp.MustCompile(`^Test[A-Z0-9_][A-Za-z0-9_]*(/[A-Za-z0-9_]+)*$`)

// packageTestSource is every _test.go file in a package directory, joined.
func packageTestSource(t *testing.T, pkg string) (string, bool) {
	t.Helper()
	dir := filepath.Join("..", filepath.FromSlash(strings.TrimPrefix(pkg, "./")))
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil || len(files) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String(), true
}

// justRecipeBody is the body of a justfile recipe, or false when the
// justfile has none of that name.
func justRecipeBody(justfile, recipe string) (string, bool) {
	lines := strings.Split(justfile, "\n")
	head := regexp.MustCompile(`^` + regexp.QuoteMeta(recipe) + `(\s[^:]*)?:`)
	for i, l := range lines {
		if !head.MatchString(l) {
			continue
		}
		var body []string
		for _, b := range lines[i+1:] {
			if b != "" && !strings.HasPrefix(b, " ") && !strings.HasPrefix(b, "\t") {
				break
			}
			body = append(body, b)
		}
		return strings.Join(body, "\n"), true
	}
	return "", false
}

// TestGoTestProofsNameTestsThatExist is the rule above over claims.json.
func TestGoTestProofsNameTestsThatExist(t *testing.T) {
	f := readSmokeClaims(t)
	proofs := goTestProofs(f)
	if len(proofs) == 0 {
		t.Fatalf("%s carries no go_test proof; this guard is checking nothing", smokeClaimsPath)
	}
	justfile, err := os.ReadFile("../justfile")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]goTestProof{}
	for _, g := range proofs {
		pr := g.Proof
		if prev, dup := seen[pr.GoTest]; dup {
			t.Errorf("%s is also %s; a Go test proves one cell, as a scenario does", g, prev)
		}
		seen[pr.GoTest] = g
		if !goTestName.MatchString(pr.GoTest) {
			t.Errorf("%s: %q is not a Go test name (Test..., a subtest as Test.../Sub)", g, pr.GoTest)
			continue
		}
		if !strings.HasPrefix(pr.GoPackage, "./") {
			t.Errorf("%s: go_package %q is not a ./ package path", g, pr.GoPackage)
			continue
		}
		src, ok := packageTestSource(t, pr.GoPackage)
		if !ok {
			t.Errorf("%s: go_package %s holds no _test.go file", g, pr.GoPackage)
			continue
		}
		top := strings.Split(pr.GoTest, "/")[0]
		if !regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(top) + `\(t \*testing\.T\)`).MatchString(src) {
			t.Errorf("%s: %s has no func %s(t *testing.T)", g, pr.GoPackage, top)
		}
		if strings.TrimSpace(pr.Note) == "" {
			t.Errorf("%s: no note; say what the test asserts that the claim needs", g)
		}
		if pr.Minutes != 0 || pr.BreakMode != "" || pr.NeedsGo || pr.NeedsEmulator || len(pr.Evidence) > 0 || pr.LastRun != nil {
			t.Errorf("%s carries minutes, a break mode, a needs_ flag, evidence or a last_run; those describe a scenario, and a Go test's run is recorded by tools/gotest-record", g)
		}
		if pr.Gate == "" {
			if want := goTestCommand(pr.GoPackage, pr.GoTest); pr.Command != want {
				t.Errorf("%s: command is %q, want %q", g, pr.Command, want)
			}
			if pr.RealService {
				t.Errorf("%s: real_service with no gate; a test CI runs on every commit reaches no real account", g)
			}
			continue
		}
		if !strings.Contains(src, `"`+pr.Gate+`"`) {
			t.Errorf("%s: gate %s is read by no test in %s, so the test does not skip without it and needs no gate", g, pr.Gate, pr.GoPackage)
		}
		recipe, ok := strings.CutPrefix(pr.Command, "just ")
		if !ok || strings.Contains(recipe, " ") {
			t.Errorf("%s: a gated proof's command is the `just <recipe>` that sets %s, got %q", g, pr.Gate, pr.Command)
			continue
		}
		body, ok := justRecipeBody(string(justfile), recipe)
		if !ok {
			t.Errorf("%s: the justfile has no recipe %s", g, recipe)
			continue
		}
		script := body
		if m := regexp.MustCompile(`bash (scripts/[a-z0-9-]+\.sh)`).FindStringSubmatch(body); m != nil {
			raw, err := os.ReadFile(filepath.Join("..", m[1]))
			if err != nil {
				t.Errorf("%s: recipe %s runs %s: %v", g, recipe, m[1], err)
				continue
			}
			script = string(raw)
		}
		if !strings.Contains(script, pr.Gate) {
			t.Errorf("%s: recipe %s never sets %s", g, recipe, pr.Gate)
		}
		if pr.RealService && !strings.Contains(pr.Note, "maintainer-run") {
			t.Errorf("%s: a real-service proof's note must say it is maintainer-run", g)
		}
	}
}

// goTestRecordProblems checks one recorded run against its proof. root is
// the repository root.
func goTestRecordProblems(root string, g goTestProof, rec statpages.GoTestRecord) []string {
	var out []string
	pr := g.Proof
	if rec.Test != pr.GoTest || rec.Package != pr.GoPackage {
		out = append(out, fmt.Sprintf("records %s in %s, the proof names %s in %s", rec.Test, rec.Package, pr.GoTest, pr.GoPackage))
	}
	if rec.Status != "pass" {
		out = append(out, fmt.Sprintf("status %q; a recorded run is a pass", rec.Status))
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(rec.Commit) {
		out = append(out, fmt.Sprintf("commit %q is not a full sha", rec.Commit))
	}
	if !provenCellEvidenceDate.MatchString(rec.Date) {
		out = append(out, fmt.Sprintf("date %q is not YYYY-MM-DD", rec.Date))
	}
	if pr.Gate == "" {
		if !strings.HasPrefix(rec.CIRun, "https://github.com/INTENTIUS/choudoufu/actions/runs/") || rec.Log != "" {
			out = append(out, fmt.Sprintf("a test CI runs is recorded from a CI run; got ci_run %q, log %q", rec.CIRun, rec.Log))
		}
		return out
	}
	if rec.CIRun != "" || !strings.HasPrefix(rec.Log, statpages.GoTestEvidenceDir+"/") {
		out = append(out, fmt.Sprintf("a gated test is recorded from a maintainer run's log under %s; got ci_run %q, log %q", statpages.GoTestEvidenceDir, rec.CIRun, rec.Log))
	} else if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rec.Log))); err != nil {
		out = append(out, fmt.Sprintf("its log %s: %v", rec.Log, err))
	}
	return out
}

// TestGoTestRecordsSayWhatTheirProofsSay: every file under
// live/smoke/evidence/go-test/ is a run of a proof claims.json lists, and
// says what that proof says. A record no proof names is a run of nothing;
// a log no record names is kept for nothing.
func TestGoTestRecordsSayWhatTheirProofsSay(t *testing.T) {
	f := readSmokeClaims(t)
	byRel := map[string]goTestProof{}
	for _, g := range goTestProofs(f) {
		byRel[statpages.GoTestEvidenceRel(g.Proof.GoTest)] = g
	}
	dir := filepath.Join("..", filepath.FromSlash(statpages.GoTestEvidenceDir))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		t.Logf("%s does not exist yet; every Go test proof is waiting", statpages.GoTestEvidenceDir)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	logs := map[string]bool{}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	recorded := 0
	for _, name := range names {
		rel := statpages.GoTestEvidenceDir + "/" + name
		switch {
		case strings.HasSuffix(name, ".log"):
			if _, ok := logs[rel]; !ok {
				logs[rel] = false
			}
			continue
		case !strings.HasSuffix(name, ".json"):
			t.Errorf("%s is neither a record nor a log", rel)
			continue
		}
		g, ok := byRel[rel]
		if !ok {
			t.Errorf("%s is the record of no go_test proof in %s", rel, smokeClaimsPath)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var rec statpages.GoTestRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		recorded++
		for _, p := range goTestRecordProblems("..", g, rec) {
			t.Errorf("%s (%s): %s", rel, g, p)
		}
		if rec.Log != "" {
			logs[rec.Log] = true
		}
	}
	for rel, named := range logs {
		if !named {
			t.Errorf("%s is the log of no record", rel)
		}
	}
	t.Logf("%d of %d Go test proofs carry a recorded run", recorded, len(byRel))
}

// TestGoTestRecordRuleCatches proves the record check red.
func TestGoTestRecordRuleCatches(t *testing.T) {
	ci := goTestProof{Claim: 1, Provider: "aws", Proof: smokeProof{GoTest: "TestA", GoPackage: "./pkg/x"}}
	gated := goTestProof{Claim: 1, Provider: "aws", Proof: smokeProof{GoTest: "TestB", GoPackage: "./pkg/x", Gate: "X"}}
	good := statpages.GoTestRecord{Test: "TestA", Package: "./pkg/x", Status: "pass",
		Commit: "289d16bad4cb3910600e0a9e60be85452eae47eb", Date: "2026-10-27",
		CIRun: "https://github.com/INTENTIUS/choudoufu/actions/runs/1"}
	if got := goTestRecordProblems(t.TempDir(), ci, good); len(got) != 0 {
		t.Fatalf("an exact CI record was reported: %v", got)
	}
	with := func(edit func(*statpages.GoTestRecord)) statpages.GoTestRecord { r := good; edit(&r); return r }
	for _, tc := range []struct {
		name string
		g    goTestProof
		rec  statpages.GoTestRecord
	}{
		{"another test's run", ci, with(func(r *statpages.GoTestRecord) { r.Test = "TestOther" })},
		{"a failed run", ci, with(func(r *statpages.GoTestRecord) { r.Status = "fail" })},
		{"a short sha", ci, with(func(r *statpages.GoTestRecord) { r.Commit = "289d16bad4" })},
		{"a typed date", ci, with(func(r *statpages.GoTestRecord) { r.Date = "27 Oct" })},
		{"a CI test recorded from a log", ci, with(func(r *statpages.GoTestRecord) { r.CIRun = ""; r.Log = "x.log" })},
		{"a gated test recorded from CI", gated, with(func(r *statpages.GoTestRecord) { r.Test = "TestB" })},
		{"a gated test whose log is gone", gated, with(func(r *statpages.GoTestRecord) {
			r.Test, r.CIRun, r.Log = "TestB", "", statpages.GoTestEvidenceDir+"/gone.log"
		})},
	} {
		if got := goTestRecordProblems(t.TempDir(), tc.g, tc.rec); len(got) == 0 {
			t.Errorf("%s: the check reported nothing", tc.name)
		}
	}
}
