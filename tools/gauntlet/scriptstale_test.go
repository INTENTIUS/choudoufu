// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rowWithRun is a minimal gauntlet-protocol row for these tests.
func rowWithRun(name, script, commit string) EstateResult {
	return EstateResult{
		Name: name, Script: script, Set: SetGrowing, Protocol: ProtocolGauntlet,
		Stages:  map[string]string{"cold_deploy": VerdictPass},
		LastRun: &LastRun{Commit: commit, Date: "2026-09-16T09:50:45Z"},
	}
}

// TestScriptStalenessThreeStates: the whole rule, over an injected diff, in
// the direction that matters. The dangerous case is not the one #1264 was
// found through (a fail that had been fixed) but its mirror - a script edit
// that breaks a stage while the cell still reads pass - so every branch
// that cannot answer must land on ScriptUnknown, never on ScriptCurrent.
func TestScriptStalenessThreeStates(t *testing.T) {
	const script = "live/e2e/reference-k8s-stateful/run.sh"
	diffOf := func(paths ...string) pathDiff {
		return func(_, _ string) ([]string, error) { return paths, nil }
	}
	failing := func(msg string) pathDiff {
		return func(_, _ string) ([]string, error) { return nil, errors.New(msg) }
	}
	cases := []struct {
		name      string
		row       EstateResult
		diff      pathDiff
		want      string
		changed   []string
		inert     []string
		whyHas    string
		wantNoDir bool
	}{
		{
			name: "the script changed since the run",
			row:  rowWithRun("reference-k8s-stateful", script, "56e04ebca6"),
			diff: diffOf(script),
			want: ScriptChanged, changed: []string{script},
		},
		{
			name: "nothing under the directory changed",
			row:  rowWithRun("reference-k8s-stateful", script, "56e04ebca6"),
			diff: diffOf(),
			want: ScriptCurrent,
		},
		{
			name: "only the README changed",
			row:  rowWithRun("reference-k8s-cert-manager", "live/e2e/reference-k8s-cert-manager/run.sh", "abc1234567"),
			diff: diffOf("live/e2e/reference-k8s-cert-manager/README.md"),
			want: ScriptCurrent, inert: []string{"live/e2e/reference-k8s-cert-manager/README.md"},
		},
		{
			name: "the README changed and so did the script",
			row:  rowWithRun("reference-k8s-cert-manager", "live/e2e/reference-k8s-cert-manager/run.sh", "abc1234567"),
			diff: diffOf("live/e2e/reference-k8s-cert-manager/README.md", "live/e2e/reference-k8s-cert-manager/run.sh"),
			want: ScriptChanged,
			changed: []string{
				"live/e2e/reference-k8s-cert-manager/run.sh",
			},
			inert: []string{"live/e2e/reference-k8s-cert-manager/README.md"},
		},
		{
			name:   "git cannot answer",
			row:    rowWithRun("reference-k8s-stateful", script, "56e04ebca6"),
			diff:   failing("commit `56e04ebca6` is not in this checkout"),
			want:   ScriptUnknown,
			whyHas: "not in this checkout",
		},
		{
			name:   "the row has never run",
			row:    EstateResult{Name: "new", Script: script, Stages: map[string]string{}},
			diff:   diffOf(script),
			want:   ScriptUnknown,
			whyHas: "recorded no run",
		},
		{
			name:   "the run recorded no commit",
			row:    rowWithRun("legacy", script, ""),
			diff:   diffOf(script),
			want:   ScriptUnknown,
			whyHas: "recorded no commit",
		},
		{
			name:   "the row names no script",
			row:    rowWithRun("nameless", "", "56e04ebca6"),
			diff:   diffOf(),
			want:   ScriptUnknown,
			whyHas: "no crossing script",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scriptStaleness(tc.row, tc.diff)
			if got.State != tc.want {
				t.Errorf("state = %q, want %q (why: %s)", got.State, tc.want, got.Why)
			}
			if strings.Join(got.Changed, ",") != strings.Join(tc.changed, ",") {
				t.Errorf("changed = %v, want %v", got.Changed, tc.changed)
			}
			if strings.Join(got.Inert, ",") != strings.Join(tc.inert, ",") {
				t.Errorf("inert = %v, want %v", got.Inert, tc.inert)
			}
			if tc.whyHas != "" && !strings.Contains(got.Why, tc.whyHas) {
				t.Errorf("why = %q, want it to carry %q - the reason is what a reader acts on", got.Why, tc.whyHas)
			}
			if tc.want == ScriptUnknown && got.Why == "" {
				t.Error("unknown with no reason: a third state that cannot say why it cannot tell is indistinguishable from a bug")
			}
		})
	}
}

// TestScriptStalenessAgainstARealCheckout drives gitPathDiff against a real
// repository, because the injected-diff test above proves the rule and
// nothing about the git invocation. Every commit here is one git actually
// made: a fixture whose shas are strings would pass while the real command
// was wrong.
func TestScriptStalenessAgainstARealCheckout(t *testing.T) {
	root := initTestRepo(t)
	const script = "live/e2e/fixture-estate/run.sh"
	first := commitTestFile(t, root, script, "#!/usr/bin/env bash\necho one\n", "the script as measured")
	commitTestFile(t, root, "live/e2e/fixture-estate/README.md", "# fixture\n", "documentation only")
	row := rowWithRun("fixture-estate", script, first)

	t.Run("a README-only commit leaves the row current", func(t *testing.T) {
		got := scriptStaleness(row, gitPathDiff(root))
		if got.State != ScriptCurrent {
			t.Fatalf("state = %q (why %q, changed %v), want %q", got.State, got.Why, got.Changed, ScriptCurrent)
		}
		if len(got.Inert) != 1 || !strings.HasSuffix(got.Inert[0], "README.md") {
			t.Errorf("inert = %v, want the README named so the page can say which documentation moved", got.Inert)
		}
	})

	t.Run("an uncommitted edit already reads as changed", func(t *testing.T) {
		// The working tree, not HEAD: a render must be able to mark the row
		// stale in the same commit that makes it stale, or the board can
		// never be produced in one pass.
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(script)), []byte("#!/usr/bin/env bash\necho two\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := scriptStaleness(row, gitPathDiff(root))
		if got.State != ScriptChanged {
			t.Fatalf("state = %q (why %q), want %q for an edit that is on disk but not committed", got.State, got.Why, ScriptChanged)
		}
		if len(got.Changed) != 1 || got.Changed[0] != script {
			t.Errorf("changed = %v, want [%s]", got.Changed, script)
		}
	})

	t.Run("the same edit committed reads the same", func(t *testing.T) {
		commitTestFile(t, root, script, "#!/usr/bin/env bash\necho two\n", "change the script")
		got := scriptStaleness(row, gitPathDiff(root))
		if got.State != ScriptChanged {
			t.Fatalf("state = %q (why %q), want %q - the answer must not move when the edit is committed", got.State, got.Why, ScriptChanged)
		}
	})

	t.Run("a change and its revert is not a change", func(t *testing.T) {
		commitTestFile(t, root, script, "#!/usr/bin/env bash\necho one\n", "revert it")
		got := scriptStaleness(row, gitPathDiff(root))
		if got.State != ScriptCurrent {
			t.Fatalf("state = %q (changed %v), want %q: this is a content comparison, not a commit count", got.State, got.Changed, ScriptCurrent)
		}
	})

	t.Run("a commit that is not in this checkout cannot be told", func(t *testing.T) {
		absent := rowWithRun("fixture-estate", script, "0123456789abcdef0123456789abcdef01234567")
		got := scriptStaleness(absent, gitPathDiff(root))
		if got.State != ScriptUnknown {
			t.Fatalf("state = %q, want %q for a commit this checkout does not have", got.State, ScriptUnknown)
		}
		if !strings.Contains(got.Why, "shallow") {
			t.Errorf("why = %q, want it to name the shallow-clone/rebased-away case", got.Why)
		}
	})

	t.Run("a commit outside HEAD's history cannot be told", func(t *testing.T) {
		// An orphan branch is a commit git can read perfectly well and that
		// is still not a version of this branch - the rebased-away case.
		head := gitHEAD(t, root)
		gitCheckout(t, root, "--orphan=sidelined")
		side := commitTestFile(t, root, script, "#!/usr/bin/env bash\necho sidelined\n", "a run recorded on a branch that never landed")
		gitCheckout(t, root, head)
		if now := gitHEAD(t, root); now != head {
			t.Fatalf("HEAD is %s, want %s back: the fixture never returned to the branch the comparison is against", now, head)
		}
		got := scriptStaleness(rowWithRun("fixture-estate", script, side), gitPathDiff(root))
		if got.State != ScriptUnknown {
			t.Fatalf("state = %q, want %q for a commit that is not an ancestor of HEAD", got.State, ScriptUnknown)
		}
		if !strings.Contains(got.Why, "not an ancestor") {
			t.Errorf("why = %q, want it to say the commit is not an ancestor of HEAD", got.Why)
		}
	})
}

// TestBoardRendersScriptStaleness: the board carries the fact, in the words
// #1069 already uses for a stale cell, and carries NOTHING when the caller
// had no checkout to read - a board built without git must not read as
// "every row is current", which is the failure this whole issue is about
// one level up.
func TestBoardRendersScriptStaleness(t *testing.T) {
	changed := rowWithRun("changed-estate", "live/e2e/changed-estate/run.sh", "aaaaaaaaaa")
	fine := rowWithRun("fine-estate", "live/e2e/fine-estate/run.sh", "bbbbbbbbbb")
	unknown := rowWithRun("unknown-estate", "live/e2e/unknown-estate/run.sh", "cccccccccc")
	a := &Artifact{Schema: 1, Estates: []EstateResult{changed, fine, unknown}}
	m := &Manifest{}
	st := map[string]ScriptStaleness{
		"changed-estate": {State: ScriptChanged, Changed: []string{"live/e2e/changed-estate/run.sh"}},
		"fine-estate":    {State: ScriptCurrent},
		"unknown-estate": {State: ScriptUnknown, Why: "commit `cccccccccc` is not an ancestor of HEAD"},
	}

	b := buildBoard(m, a, st)
	byName := map[string]BoardEstate{}
	for _, e := range b.Estates {
		byName[e.Name] = e
	}
	if got := byName["changed-estate"]; got.ScriptStale != ScriptChanged || !strings.Contains(got.ScriptNote, "**Stale**") || !strings.Contains(got.ScriptNote, "run.sh") {
		t.Errorf("changed row: script_stale=%q note=%q, want the changed badge and a **Stale** sentence naming the file", got.ScriptStale, got.ScriptNote)
	}
	if got := byName["fine-estate"]; got.ScriptStale != "" || got.ScriptNote != "" {
		t.Errorf("current row: script_stale=%q note=%q, want both empty - the quiet case stays quiet", got.ScriptStale, got.ScriptNote)
	}
	if got := byName["unknown-estate"]; got.ScriptStale != ScriptUnknown || !strings.Contains(got.ScriptNote, "**Unverified**") {
		t.Errorf("unknown row: script_stale=%q note=%q, want the third state said out loud, not folded into either other one", got.ScriptStale, got.ScriptNote)
	}
	if !strings.Contains(b.ScriptBanner, "1 of 3 rows") || !strings.Contains(b.ScriptBanner, "changed-estate") {
		t.Errorf("banner = %q, want the count and the row named", b.ScriptBanner)
	}
	if !strings.Contains(b.ScriptBanner, "unknown-estate") {
		t.Errorf("banner = %q, want the unverifiable row named too; a row nobody can check is not a row that is fine", b.ScriptBanner)
	}

	// The nil-map arm: no checkout was read, so the board says nothing.
	blank := buildBoard(m, a, nil)
	if blank.ScriptBanner != "" {
		t.Errorf("banner = %q with no staleness map, want empty: silence, never a claim that every row is current", blank.ScriptBanner)
	}
	for _, e := range blank.Estates {
		if e.ScriptStale != "" || e.ScriptNote != "" {
			t.Errorf("%s: script_stale=%q note=%q with no staleness map, want both empty", e.Name, e.ScriptStale, e.ScriptNote)
		}
	}
}

// TestScriptStalenessDoesNotFailTheRenderedDocsGuard is #1264's ruling in
// force: a board that differs ONLY in script staleness is reported, never
// returned as stale. Failing on it would turn every estate-script pull
// request into a re-render (and every history-less checkout into a red
// guard), which is the option the issue rejects.
//
// The second half is the guard that keeps the first half honest: any other
// difference in the same file is still stale.
func TestScriptStalenessDoesNotFailTheRenderedDocsGuard(t *testing.T) {
	a := &Artifact{Schema: 1, Stages: Stages(), Estates: []EstateResult{rowWithRun("e", "live/e2e/e/run.sh", "aaaaaaaaaa")}}
	m := &Manifest{}
	current, err := buildBoard(m, a, map[string]ScriptStaleness{"e": {State: ScriptCurrent}}).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	drifted, err := buildBoard(m, a, map[string]ScriptStaleness{
		"e": {State: ScriptChanged, Changed: []string{"live/e2e/e/run.sh"}},
	}).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if string(current) == string(drifted) {
		t.Fatal("the two boards are byte-identical, so this test proves nothing about telling them apart")
	}
	if !boardsDifferOnlyInScriptStaleness(current, drifted) {
		t.Error("a board differing only in script staleness was not recognised as such; it would fail the rendered-docs guard, which #1264 rules out")
	}

	// A verdict moved as well: that is a real stale render and must stay one.
	b := *a
	b.Estates = []EstateResult{rowWithRun("e", "live/e2e/e/run.sh", "aaaaaaaaaa")}
	b.Estates[0].Stages = map[string]string{"cold_deploy": VerdictFail}
	moved, err := buildBoard(m, &b, map[string]ScriptStaleness{
		"e": {State: ScriptChanged, Changed: []string{"live/e2e/e/run.sh"}},
	}).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if boardsDifferOnlyInScriptStaleness(current, moved) {
		t.Error("a board whose verdict cell also moved was waved through as script-staleness-only; that is how a real stale render lands unnoticed")
	}
	if !json.Valid(moved) {
		t.Error("the board is not valid JSON")
	}
}

// TestEstateScriptsReadNoMarkdown holds isInertEstatePath's one assumption
// to the tree: no crossing script reads a markdown file out of its own
// estate directory, so a README-only change really cannot move a verdict.
//
// The second half runs the scan against a fixture that DOES read its
// README. Without it this guard would print "clean" whether or not it can
// see anything at all.
func TestEstateScriptsReadNoMarkdown(t *testing.T) {
	root := testRoot(t)
	a, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, r := range a.Estates {
		if d := EstateDir(r); d != "" {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) == 0 {
		t.Fatal("no estate directories to scan; the guard would pass vacuously")
	}
	hits, err := estateMarkdownReads(root, dirs)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Errorf("a crossing script names a markdown file from its own estate directory outside a comment:\n  %s\n"+
			"If that is a prose reference, move it onto a comment line. If the script really reads the file, "+
			"markdown is not inert for this estate and isInertEstatePath must stop treating it as inert (#1264).",
			strings.Join(hits, "\n  "))
	}

	fixture := t.TempDir()
	dir := filepath.Join(fixture, "live", "e2e", "reads-its-readme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("expected: 12\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/usr/bin/env bash\n# see README.md for the contract\nexpected=$(grep expected README.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	red, err := estateMarkdownReads(fixture, []string{"live/e2e/reads-its-readme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(red) != 1 {
		t.Fatalf("the scan found %v on a script that reads its own README on one line and mentions it in a comment on another; want exactly the reading line. A guard that cannot fire proves nothing when it is quiet", red)
	}
}

// TestFormatScriptStalenessNamesEveryRowThatNeedsRemeasuring: `gauntlet
// check`'s live answer counts only what it can prove, names the rows, and
// says what to do about them.
func TestFormatScriptStalenessNamesEveryRowThatNeedsRemeasuring(t *testing.T) {
	a := &Artifact{Schema: 1, Estates: []EstateResult{
		rowWithRun("zeta", "live/e2e/zeta/run.sh", "aaaaaaaaaa"),
		rowWithRun("alpha", "live/e2e/alpha/run.sh", "bbbbbbbbbb"),
		rowWithRun("beta", "live/e2e/beta/run.sh", "cccccccccc"),
	}}
	out := formatScriptStaleness(a, map[string]ScriptStaleness{
		"zeta":  {State: ScriptChanged, Changed: []string{"live/e2e/zeta/run.sh"}},
		"alpha": {State: ScriptCurrent},
		"beta":  {State: ScriptUnknown, Why: "commit `cccccccccc` is not an ancestor of HEAD"},
	})
	if !strings.Contains(out, "1 of 3 rows") {
		t.Errorf("output does not count the changed rows:\n%s", out)
	}
	if !strings.Contains(out, "zeta") || !strings.Contains(out, "live/e2e/zeta/run.sh") {
		t.Errorf("output does not name the changed row and its file:\n%s", out)
	}
	if !strings.Contains(out, "beta") || !strings.Contains(out, "not an ancestor") {
		t.Errorf("output does not name the row it could not check, or why:\n%s", out)
	}
	if strings.Contains(out, "alpha") {
		t.Errorf("output names a current row; only work belongs in this list:\n%s", out)
	}
	clean := formatScriptStaleness(a, map[string]ScriptStaleness{
		"zeta": {State: ScriptCurrent}, "alpha": {State: ScriptCurrent}, "beta": {State: ScriptCurrent},
	})
	if !strings.Contains(clean, "every one of 3 rows") {
		t.Errorf("the clean case does not say so plainly:\n%s", clean)
	}
}

// gitHEAD is the fixture repository's current commit.
func gitHEAD(t *testing.T, root string) string {
	t.Helper()
	out, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return out
}
