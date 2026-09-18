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
		return func(_ string, _ []string) ([]string, error) { return paths, nil }
	}
	failing := func(msg string) pathDiff {
		return func(_ string, _ []string) ([]string, error) { return nil, errors.New(msg) }
	}
	cases := []struct {
		name      string
		row       EstateResult
		diff      pathDiff
		want      string
		changed   []string
		shared    []string
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
			// #1292: the shared protocol library is outside every estate's
			// own directory, so before it was watched this row read
			// `current` while the functions its every stage calls had
			// moved under it.
			name: "only the shared protocol library changed",
			row:  rowWithRun("reference-k8s-stateful", script, "56e04ebca6"),
			diff: diffOf(SharedLibDir + "/gauntlet.sh"),
			want: ScriptChanged, shared: []string{SharedLibDir + "/gauntlet.sh"},
		},
		{
			name: "both sides changed",
			row:  rowWithRun("reference-k8s-stateful", script, "56e04ebca6"),
			diff: diffOf(SharedLibDir+"/gauntlet.sh", script),
			want: ScriptChanged, changed: []string{script}, shared: []string{SharedLibDir + "/gauntlet.sh"},
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
			if strings.Join(got.Shared, ",") != strings.Join(tc.shared, ",") {
				t.Errorf("shared = %v, want %v - the two sides are kept apart so the note can say which one moved", got.Shared, tc.shared)
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
	// The shared protocol library is part of the watched set, so it has to
	// exist BEFORE the commit the row records - otherwise every arm below
	// would read as changed because the library was added afterwards.
	commitTestFile(t, root, SharedLibDir+"/gauntlet.sh", "gauntlet_stage() { :; }\n", "the protocol library as measured")
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

// TestSharedProtocolLibraryChangeBadgesTheRowsThatSourceIt is #1292's first
// arm, against a real repository rather than an injected diff: edit the
// shared library and every row goes stale, with the note saying it was the
// SHARED side that moved and not the estate's own files.
//
// Driven red before it was green: with watchedDirs returning only the
// estate's own directory (the state of this file before #1292), both
// sub-tests report ScriptCurrent - the exact failure the issue describes,
// 31 rows reading current while the functions all their stages call have
// changed underneath.
func TestSharedProtocolLibraryChangeBadgesTheRowsThatSourceIt(t *testing.T) {
	root := initTestRepo(t)
	const lib = SharedLibDir + "/gauntlet.sh"
	commitTestFile(t, root, lib, "gauntlet_record_count() { ls \"$1\" | wc -l; }\n", "the protocol library as measured")
	const aScript = "live/e2e/estate-a/run.sh"
	const bScript = "live/e2e/estate-b/run.sh"
	at := commitTestFile(t, root, aScript, "#!/usr/bin/env bash\nsource ../lib/gauntlet.sh\n", "estate a")
	bt := commitTestFile(t, root, bScript, "#!/usr/bin/env bash\nsource ../lib/gauntlet.sh\n", "estate b")
	a := rowWithRun("estate-a", aScript, at)
	b := rowWithRun("estate-b", bScript, bt)

	t.Run("nothing has moved yet", func(t *testing.T) {
		for _, r := range []EstateResult{a, b} {
			if got := scriptStaleness(r, gitPathDiff(root)); got.State != ScriptCurrent {
				t.Fatalf("%s: state = %q (why %q, changed %v, shared %v) before any edit, want %q; the arms below would prove nothing",
					r.Name, got.State, got.Why, got.Changed, got.Shared, ScriptCurrent)
			}
		}
	})

	// #1291's own change, in miniature: gauntlet_record_count stops counting
	// and starts refusing. Neither estate directory is touched.
	commitTestFile(t, root, lib, "gauntlet_record_count() { echo 'refusing a record store root' >&2; return 1; }\n", "#1291: the guard every row calls")

	t.Run("both rows go stale on the shared side", func(t *testing.T) {
		for _, r := range []EstateResult{a, b} {
			got := scriptStaleness(r, gitPathDiff(root))
			if got.State != ScriptChanged {
				t.Fatalf("%s: state = %q (why %q), want %q - the library every stage calls changed under this row", r.Name, got.State, got.Why, ScriptChanged)
			}
			if len(got.Shared) != 1 || got.Shared[0] != lib {
				t.Errorf("%s: shared = %v, want [%s]", r.Name, got.Shared, lib)
			}
			if len(got.Changed) != 0 {
				t.Errorf("%s: changed = %v, want empty - this estate's own files did not move, and saying they did sends the reader to the wrong file", r.Name, got.Changed)
			}
			note := scriptStaleNote(got, EstateDir(r))
			if !strings.Contains(note, "shared protocol library") || !strings.Contains(note, lib) {
				t.Errorf("%s: note = %q, want it to name the shared library as what moved", r.Name, note)
			}
			if strings.Contains(note, "own files have changed") {
				t.Errorf("%s: note = %q, blames this estate's own files for a change that is not theirs", r.Name, note)
			}
		}
	})

	t.Run("the banner says how many are shared-only", func(t *testing.T) {
		art := &Artifact{Schema: 1, Estates: []EstateResult{a, b}}
		st := AllScriptStaleness(root, art)
		banner := scriptStaleBanner(art, st)
		if !strings.Contains(banner, "2 of 2 rows") {
			t.Errorf("banner = %q, want the count", banner)
		}
		if !strings.Contains(banner, "For 2 of them nothing in their own directory moved") {
			t.Errorf("banner = %q, want it to say the change was in the shared library, not in 2 estate directories", banner)
		}
	})

	t.Run("an estate edit on top still reads as its own", func(t *testing.T) {
		// Both sides moved: the note must say both rather than pick one,
		// or a reader re-runs the estate and never looks at the library.
		commitTestFile(t, root, aScript, "#!/usr/bin/env bash\nsource ../lib/gauntlet.sh\necho two\n", "estate a edits its own script")
		got := scriptStaleness(a, gitPathDiff(root))
		if len(got.Changed) != 1 || len(got.Shared) != 1 {
			t.Fatalf("changed = %v, shared = %v, want one of each", got.Changed, got.Shared)
		}
		note := scriptStaleNote(got, EstateDir(a))
		if !strings.Contains(note, "own files have changed") || !strings.Contains(note, "shared protocol library") {
			t.Errorf("note = %q, want both sides named", note)
		}
	})
}

// TestTheWatchedSetStopsWhereItSays is #1292's second arm, and the one that
// keeps the set honest. Widening staleness until it catches everything is a
// real failure here: a badge lit on every merge is read by nobody, and that
// is worse than the gap, because a gap is visible the first time someone
// looks and noise never is.
//
// So this drives the four candidates the derivation turned up and
// deliberately left out (SharedLibDir's doc comment says why for each), plus
// the product half #1288 is about, and asserts the row does NOT badge.
//
// Driven red before it was green: adding "live" to watchedDirs - the obvious
// over-wide version, which is how "it sources things out of live/" would be
// implemented by someone not counting the cost - makes every sub-test here
// fail, and makes every row in the real artifact badge on any commit under
// live/.
func TestTheWatchedSetStopsWhereItSays(t *testing.T) {
	root := initTestRepo(t)
	const script = "live/e2e/fixture-estate/run.sh"
	commitTestFile(t, root, SharedLibDir+"/gauntlet.sh", "gauntlet_stage() { :; }\n", "the protocol library")
	at := commitTestFile(t, root, script, "#!/usr/bin/env bash\nsource ../lib/gauntlet.sh\n", "the script as measured")
	row := rowWithRun("fixture-estate", script, at)

	if got := scriptStaleness(row, gitPathDiff(root)); got.State != ScriptCurrent {
		t.Fatalf("state = %q (why %q) before any edit, want %q", got.State, got.Why, ScriptCurrent)
	}

	// Each of these is a real out-of-directory reference a crossing script
	// makes, with the reason it is nonetheless outside the watched set.
	outside := []struct{ path, content, why string }{
		{
			"live/floci-image", "ghcr.io/lex00/floci@sha256:deadbeef\n",
			"the emulator pin has a stronger check already: the row records last_run.emulator and emulatorNote compares that VALUE against the pin now",
		},
		{
			"live/oracle-versions.json", "{\"terraform_version\":\"9.9.9\"}\n",
			"oracleNote compares the recorded terraform and tofu versions by value; the file's remaining content is a 40-line prose comment, and badging 31 rows for a comment edit is the noise this arm exists to refuse (aws_provider_version is #1253's recording gap, not a diffing one)",
		},
		{
			"live/gauntlet/estates.json", "{\"estates\":[{\"name\":\"other\"}]}\n",
			"one file holds all 31 estates' manifest data, so a path diff badges every row when one estate's pre_apply list moves; honest coverage needs a per-estate subtree comparison",
		},
		{
			"internal/live/projection/projection.go", "package projection // changed\n",
			"the product half (#1288): almost every commit touches internal/, so diffing it badges every row on every merge",
		},
		{
			"live/e2e/other-estate/run.sh", "#!/usr/bin/env bash\necho other\n",
			"another estate's script is not this row's evidence",
		},
		{
			"live/GAUNTLET.md", "# changed\n",
			"rendered prose is not read by any run",
		},
	}
	for _, o := range outside {
		t.Run(o.path, func(t *testing.T) {
			commitTestFile(t, root, o.path, o.content, "change "+o.path)
			got := scriptStaleness(row, gitPathDiff(root))
			if got.State != ScriptCurrent {
				t.Errorf("changing %s badged the row (state %q, changed %v, shared %v).\nIt is outside the watched set on purpose: %s.\nIf the set really should grow, change SharedLibDir's doc comment and this table together - never one of them alone.",
					o.path, got.State, got.Changed, got.Shared, o.why)
			}
		})
	}
}

// TestEveryRowSourcesTheSharedLibrary keeps watchedDirs' blanket honest.
// The shared half is watched for every row rather than only for rows whose
// script can be shown to source it, because over-watching costs a badge a
// re-run clears while under-watching is the #1292 failure itself. That
// choice is only free while every row really does source the library, and
// this is what says so.
//
// Its red arm is a fixture estate that sources nothing: without it the scan
// would report "clean" whether or not it can see anything.
func TestEveryRowSourcesTheSharedLibrary(t *testing.T) {
	root := testRoot(t)
	a, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Estates) == 0 {
		t.Fatal("no rows to check; the guard would pass vacuously")
	}
	var missing []string
	for _, r := range a.Estates {
		if EstateDir(r) == "" {
			continue
		}
		if !sourcesSharedLib(root, r.Script) {
			missing = append(missing, r.Script)
		}
	}
	if len(missing) > 0 {
		t.Errorf("these rows' scripts do not source `%s`, yet watchedDirs badges them when it changes:\n  %s\n"+
			"Either convert them to the protocol, or make the shared half of the watched set per-row (#1292).",
			SharedLibDir, strings.Join(missing, "\n  "))
	}

	fixture := t.TempDir()
	dir := filepath.Join(fixture, "live", "e2e", "sources-nothing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/usr/bin/env bash\necho standalone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sourcesSharedLib(fixture, "live/e2e/sources-nothing/run.sh") {
		t.Error("a script that sources nothing was reported as sourcing the shared library; a detector that only ever says yes proves nothing above")
	}
}

// sourcesSharedLib reports whether the crossing script at the repo-relative
// path names the shared protocol library, outside a full-line comment. Not
// a path resolution - the scripts reach the library through
// `$(dirname "${BASH_SOURCE[0]}")/..` as often as through `$ROOT` - a
// reference test, which is all the guard above needs.
func sourcesSharedLib(root, script string) bool {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(script)))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			continue
		}
		if strings.Contains(line, "lib/gauntlet.sh") || strings.Contains(line, SharedLibDir+"/") {
			return true
		}
	}
	return false
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
	// The shared protocol library is in the watched set too (#1292), so the
	// inert rule has to hold there as well: a markdown file under
	// live/e2e/lib/ that gauntlet.sh actually read would be treated as
	// unreadable by every row at once.
	dirs := []string{SharedLibDir}
	for _, r := range a.Estates {
		if d := EstateDir(r); d != "" {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) < 2 {
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
