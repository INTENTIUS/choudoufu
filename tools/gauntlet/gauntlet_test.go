// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func testRoot(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Skip("not in a git checkout")
	}
	return root
}

// TestStagesAreWellFormed: unique IDs, contiguous order from 1, every prose
// field present, status in the known set, and at least one active headline
// stage so "clear" can never be vacuously true. A stage's own Proves text
// saying it is "not part of the headline bars" must agree with its Headline
// field, in both directions, so the two never drift apart silently.
func TestStagesAreWellFormed(t *testing.T) {
	stages := Stages()
	seen := map[string]bool{}
	active, headlineActive := 0, 0
	for i, s := range stages {
		if s.Order != i+1 {
			t.Errorf("stage %q has order %d, want %d (orders must be contiguous from 1)", s.ID, s.Order, i+1)
		}
		if seen[s.ID] {
			t.Errorf("stage id %q repeated", s.ID)
		}
		seen[s.ID] = true
		if s.ID == "" || strings.ContainsAny(s.ID, " -") {
			t.Errorf("stage id %q must be a bare snake_case token", s.ID)
		}
		for name, v := range map[string]string{"Title": s.Title, "Proves": s.Proves, "Oracle": s.Oracle, "Break": s.Break} {
			if strings.TrimSpace(v) == "" {
				t.Errorf("stage %q: %s is empty", s.ID, name)
			}
		}
		switch s.Status {
		case StatusActive:
			active++
			if s.Headline {
				headlineActive++
			}
		case StatusPlanned:
		default:
			t.Errorf("stage %q: status %q is not active or planned", s.ID, s.Status)
		}
		saysNonHeadline := strings.Contains(s.Proves, "not part of the headline bars")
		if saysNonHeadline && s.Headline {
			t.Errorf("stage %q: Proves says \"not part of the headline bars\" but Headline is true", s.ID)
		}
		if !saysNonHeadline && !s.Headline {
			t.Errorf("stage %q: Headline is false but Proves does not say \"not part of the headline bars\"", s.ID)
		}
	}
	if active == 0 {
		t.Fatal("no active stage")
	}
	if headlineActive == 0 {
		t.Fatal("no active headline stage; clear would be vacuous")
	}
}

// TestManifestIsCanonical: the committed manifest validates, is byte-identical
// to its canonical encoding, and every entry's script exists and is
// executable.
func TestManifestIsCanonical(t *testing.T) {
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	want, err := m.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, ManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s is not canonical; run `go run ./tools/gauntlet render`", ManifestPath)
	}
	for _, e := range m.Estates {
		p := filepath.Join(root, e.ScriptPath())
		st, err := os.Stat(p)
		if err != nil {
			t.Errorf("estate %q: script %s: %v", e.Name, e.ScriptPath(), err)
			continue
		}
		if st.Mode()&0o111 == 0 {
			t.Errorf("estate %q: script %s is not executable", e.Name, e.ScriptPath())
		}
	}
}

// TestArtifactAgreesWithManifest: every estate in the manifest has a row and
// vice versa, clear is computed per the active-stage rule, and the set
// summaries add up.
func TestArtifactAgreesWithManifest(t *testing.T) {
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	a, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	if a.Schema != 1 {
		t.Errorf("artifact schema %d, want 1", a.Schema)
	}
	names := map[string]bool{}
	for _, e := range m.Estates {
		names[e.Name] = true
		r, ok := a.Result(e.Name)
		if !ok {
			t.Errorf("%s has no row for %q; run `go run ./tools/gauntlet render`", ArtifactPath, e.Name)
			continue
		}
		if r.Clear != isClear(r.Stages) {
			t.Errorf("%q: clear=%v but stages say %v", e.Name, r.Clear, isClear(r.Stages))
		}
		if r.Set != e.Set || r.Lane != e.Lane {
			t.Errorf("%q: artifact set/lane %s/%s differ from manifest %s/%s", e.Name, r.Set, r.Lane, e.Set, e.Lane)
		}
		for id := range r.Stages {
			if _, ok := StageByID(id); !ok {
				t.Errorf("%q: artifact carries unknown stage %q", e.Name, id)
			}
		}
		if !IsValidEstateProtocol(r.Protocol) {
			t.Errorf("%q: protocol %q unknown", e.Name, r.Protocol)
		}
	}
	for _, r := range a.Estates {
		if !names[r.Name] {
			t.Errorf("%s has a row for %q which is not in the manifest", ArtifactPath, r.Name)
		}
	}
	for key := range SetLabels {
		sum, ok := a.Sets[key]
		if !ok {
			t.Errorf("artifact has no set %q", key)
			continue
		}
		n, clear := 0, 0
		for _, r := range a.Estates {
			if key == "core" && r.Set != SetCore {
				continue
			}
			n++
			if r.Clear {
				clear++
			}
		}
		if sum.Estates != n || sum.Clear != clear {
			t.Errorf("set %q: summary %d/%d, recomputed %d/%d", key, sum.Clear, sum.Estates, clear, n)
		}
	}
}

// TestNonzeroExitCodeImpliesAFailingStage: a run that ends non-zero must
// leave visible evidence in the stage table of what failed. If it does not -
// every stage in the row reads pass or not_run, nothing anywhere reads fail -
// there is no way for a nonzero exit to be legitimate: it can only mean the
// exit code reflects something this run never recorded a verdict for, most
// likely the whole row being carried forward untouched from a prior run
// while the script died before its first `GAUNTLET stage=` line (issue
// filed for exactly this shape; see run.go's Spoken-but-empty-Stages branch).
//
// This is deliberately narrower than "exit_code != 0 implies clear == false":
// clear is computed over ACTIVE stages only (isClear), so a script that
// genuinely fails a PLANNED stage this run (day2_count, say) legitimately
// exits non-zero while clear stays true - that combination is real, current,
// correct data, not staleness. reference-ec2-vpc is exactly this case as of
// this writing: exit_code=1, clear=true, day2_count=fail. The guard must
// pass on that row, because it is not the defect; it must fail on a row
// where NOTHING failed anywhere yet the run still exited non-zero.
func TestNonzeroExitCodeImpliesAFailingStage(t *testing.T) {
	root := testRoot(t)
	a, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range a.Estates {
		if r.LastRun == nil || r.LastRun.ExitCode == 0 {
			continue
		}
		hasFail := false
		for _, v := range r.Stages {
			if v == VerdictFail {
				hasFail = true
				break
			}
		}
		if !hasFail {
			t.Errorf("%q: last_run.exit_code=%d but no stage reads %q anywhere in its row; this run's failure left no trace in the stage table, which is the stale-carry-forward shape (a script that spoke the protocol, produced zero stage verdicts, and died) - see run.go's res.Spoken/len(res.Stages)==0 branch", r.Name, r.LastRun.ExitCode, VerdictFail)
		}
	}
}

// checkLastRunCommitAncestry is the shared logic behind
// TestEveryLastRunCommitIsAnAncestorOfHEAD, factored out so the guard's
// exact failure text can be demonstrated against a synthetic fixture
// (TestEveryLastRunCommitIsAnAncestorOfHEADCatchesADanglingCommit) without
// requiring an actually-red test to stay committed to the suite - "prove it
// red" without leaving a permanently failing test behind. It reuses
// isAncestor (mergeartifact.go), the same primitive
// checkProvenanceAncestry's #509 case is built on, so both guards agree on
// what "ancestor" means and on how an unresolvable commit is reported (a
// hard failure, no allowlist - see the TestEveryLastRunCommitIsAnAncestorOfHEAD
// doc comment).
func checkLastRunCommitAncestry(root string, a *Artifact, head string) []string {
	var problems []string
	for _, r := range a.Estates {
		if r.LastRun == nil || r.LastRun.Commit == "" {
			continue
		}
		ok, err := isAncestor(root, r.LastRun.Commit, head)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%q: last_run.commit %s: git merge-base --is-ancestor failed: %v (issue #509's class - an unresolvable provenance pointer)", r.Name, r.LastRun.Commit, err))
			continue
		}
		if !ok {
			problems = append(problems, fmt.Sprintf("%q: last_run.commit %s is not an ancestor of HEAD (%s); a dangling or rebased-away provenance pointer (issue #509's class) - re-run this estate rather than carrying the stamp forward", r.Name, r.LastRun.Commit, head))
		}
	}
	return problems
}

// TestEveryLastRunCommitIsAnAncestorOfHEAD guards issue #511, the #509
// class: every estate row's last_run.commit must be a real ancestor of
// HEAD, never a rebased-away or otherwise dangling object.
//
// #509: PR #500's branch was rebased onto a moved main. `git rebase`
// replays each commit's *diff* onto its new parent; it never re-runs `go
// run ./tools/gauntlet run`, so live/gauntlet.json's embedded "as of this
// commit" pointer kept naming the pre-rebase commit even though that
// commit's own hash changed underneath it on replay. The rebase hit no
// textual conflict, so nothing about it looked wrong - the provenance
// pointer went stale silently anyway, reached main, and was rendered onto
// the public progress page. This is the counterintuitive part worth
// remembering: a clean, conflict-free rebase invalidates a measured row's
// commit pointer just as surely as a hand edit would, because rebase
// replays diffs, not the procedure that produced them.
//
// Checked relation: ancestor of process HEAD, not ancestor of main. On a
// feature branch mid-work, a freshly measured row legitimately names a
// commit on that branch, not on main - HEAD there is the branch's own tip,
// which the row's commit must (and normally does) precede, since the run
// happens before the commit that records it. This is not a divergence from
// mergeartifact.go's checkProvenanceAncestry, which checks each row against
// "the revision that produced it" (base/ours/theirs) rather than a single
// fixed HEAD: that function runs pre-merge, when up to three candidate
// revisions exist and none is yet a descendant of the others. This test
// runs post-checkout against a single tree with exactly one true HEAD, so
// "ancestor of HEAD" is that same rule specialized to the one-candidate
// case - once a real merge lands, HEAD is a descendant of every row's
// source, so this test passing on main is exactly what
// checkProvenanceAncestry already guaranteed would hold.
//
// Shallow clones cannot answer this question at all (isShallowRepo,
// main.go): a shallow checkout has no history to check ancestry against,
// and silently skipping here would mean the guard reports green in CI
// without ever having checked anything - exactly the failure mode #511
// warns against, since actions/checkout defaults to a depth-1 (shallow)
// checkout when a workflow does not set fetch-depth: 0. So this FAILS
// loudly instead of skipping quietly: .github/workflows/ci.yml's "fast"
// job (the one that runs this test) now sets fetch-depth: 0, matching
// contribute.yml's existing choice for the same reason, so this should
// never actually fire from CI - if it does, the checkout config regressed
// and needs fixing, not a bypass here.
//
// An unresolvable or dangling last_run.commit is a hard failure with no
// allowlist, matching mergeartifact.go's checkProvenanceAncestry (#509's
// sibling guard, #516): that function refuses unconditionally rather than
// exempting any row by name, and this test follows the same convention
// rather than inventing a third one.
func TestEveryLastRunCommitIsAnAncestorOfHEAD(t *testing.T) {
	root := testRoot(t)
	shallow, err := isShallowRepo(root)
	if err != nil {
		t.Fatalf("git rev-parse --is-shallow-repository: %v", err)
	}
	if shallow {
		t.Fatal("this checkout is shallow (git rev-parse --is-shallow-repository = true); last_run.commit ancestry cannot be verified without full history - fetch full history (git fetch --unshallow, or a checkout with fetch-depth: 0) rather than let this guard skip")
	}
	head := headCommit(root)
	if head == "" {
		t.Fatal("could not resolve HEAD via git rev-parse")
	}
	a, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range checkLastRunCommitAncestry(root, a, head) {
		t.Error(msg)
	}
}

// TestEveryLastRunCommitIsAnAncestorOfHEADCatchesADanglingCommit is the red
// demonstration #511 requires: a guard never shown failing is not a guard.
// It builds a synthetic repo where a fixture row's last_run.commit names a
// commit that is real but never an ancestor of head - a sibling, not a
// parent, exactly #509's shape (the rebased-away pre-rebase commit still
// exists locally as a dangling object; it is just no longer reachable from
// HEAD) - and calls checkLastRunCommitAncestry directly, the same function
// TestEveryLastRunCommitIsAnAncestorOfHEAD calls against the real artifact,
// so this demonstrates the guard's actual code path failing rather than a
// re-implementation of it.
func TestEveryLastRunCommitIsAnAncestorOfHEADCatchesADanglingCommit(t *testing.T) {
	root := initTestRepo(t)
	baseSHA := commitTestFile(t, root, "base.txt", "base\n", "base")

	gitCheckout(t, root, baseSHA)
	rogueSHA := commitTestFile(t, root, "rogue.txt", "rogue\n", "an unrelated, disconnected commit - #509's dangling pre-rebase shape")

	gitCheckout(t, root, baseSHA)
	headSHA := commitTestFile(t, root, "head.txt", "head\n", "head, a sibling of rogue, not its descendant")

	a := &Artifact{Estates: []EstateResult{{
		Name:    "bogus-provenance",
		LastRun: &LastRun{Commit: rogueSHA, Date: "2026-08-29T00:00:00Z"},
	}}}

	problems := checkLastRunCommitAncestry(root, a, headSHA)
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want 1: %v", len(problems), problems)
	}
	got := problems[0]
	if !strings.Contains(got, "bogus-provenance") || !strings.Contains(got, rogueSHA) || !strings.Contains(got, "#509") {
		t.Errorf("diagnostic missing expected content (estate name, dangling commit, issue reference): %q", got)
	}
	t.Logf("red demonstration - the guard's real failure text: %s", got)
}

// TestBoardBannerMatchesEstateRows guards #414's fix in the spirit of
// #413's TestNonzeroExitCodeImpliesAFailingStage: a claim the rendered page
// makes must be one the artifact's own rows actually support, not a value
// that merely looks like it summarizes them.
//
// #414 was a top-level `commit`/`generated` stamp that no procedure could
// ever advance, so it went stale relative to what the artifact's own
// estates recorded - the page claimed a "measured at" instant that
// predated data it was displaying. What replaced it (boardBanner, computed
// fresh in renderProgressIndex from a.Estates every render) must never be
// able to repeat that shape: a value on the page that reads as evidence
// without being tied to the rows it summarizes.
//
// The independent check does NOT call estateDateRange or boardBanner - it
// walks a.Estates itself, exactly as a reader auditing the page by hand
// would, so a bug that broke boardBanner's internals but left this test
// calling the same broken function would still be caught. See #414 for how
// this was proven load-bearing: the committed page's banner line was
// hand-edited to a wrong date, this test failed, and the change was
// reverted.
func TestBoardBannerMatchesEstateRows(t *testing.T) {
	root := testRoot(t)
	a, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	oldest, newest, ok := "", "", false
	for _, r := range a.Estates {
		if r.LastRun == nil || r.LastRun.Date == "" {
			continue
		}
		d := r.LastRun.Date
		if !ok || d < oldest {
			oldest = d
		}
		if !ok || d > newest {
			newest = d
		}
		ok = true
	}
	if !ok {
		t.Skip("no estate carries a last_run.date; the range claim does not apply to this artifact")
	}

	b, err := os.ReadFile(filepath.Join(root, SiteProgressPage))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	if a.Emulator == "" || !strings.Contains(page, a.Emulator) {
		t.Errorf("%s does not mention the pinned emulator %q anywhere; the one board-wide fact the banner still makes went missing", SiteProgressPage, a.Emulator)
	}
	if oldest == newest {
		if !strings.Contains(page, oldest) {
			t.Errorf("%s's board banner does not carry %q, the only last_run.date every estate row agrees on", SiteProgressPage, oldest)
		}
		return
	}
	if !strings.Contains(page, oldest) {
		t.Errorf("%s's board banner does not carry %q, the oldest last_run.date across a.Estates; the rendered claim has drifted from the rows it summarizes", SiteProgressPage, oldest)
	}
	if !strings.Contains(page, newest) {
		t.Errorf("%s's board banner does not carry %q, the newest last_run.date across a.Estates; the rendered claim has drifted from the rows it summarizes", SiteProgressPage, newest)
	}
}

// TestBoardWideEmulatorClaimMatchesRows guards the same family of defect as
// #414 and TestBoardBannerMatchesEstateRows above, one field over: the
// emulator digest.
//
// a.Emulator is CONFIGURATION - a plain copy of live/floci-image, true of
// the checked-out tree regardless of what has run; it is the pin the NEXT
// `gauntlet run` will use. Each row's own last_run.emulator is EVIDENCE -
// the digest that specific run actually launched against, stamped by
// RunEstates at run time (run.go). boardBanner used to read a.Emulator to
// describe every row's evidence (the pre-fix "Every estate below last ran
// against the pinned emulator image %s" line, built directly from
// a.Emulator with no reference to any row) - true for exactly one instant,
// when a full sweep finishes, and false the moment a single estate re-runs
// against a repinned image while the rest sit unrun, since the artifact is
// updated one estate at a time by construction.
//
// The independent check does NOT call emulatorGroups or boardBanner - it
// walks a.Estates itself and buckets last_run.emulator directly, exactly
// as TestBoardBannerMatchesEstateRows already does for last_run.date, so a
// bug that breaks both the renderer and a test that merely calls it would
// still be caught. See this change's own commit message for how this was
// proven load-bearing: the committed page's banner line was hand-edited to
// claim uniformity over rows that disagree, this test failed, and the
// change was reverted.
func TestBoardWideEmulatorClaimMatchesRows(t *testing.T) {
	root := testRoot(t)
	a, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]int{}
	for _, r := range a.Estates {
		if r.LastRun == nil {
			continue
		}
		seen[r.LastRun.Emulator]++
	}
	if len(seen) == 0 {
		t.Skip("no estate has recorded a run; the emulator claim does not apply yet")
	}

	b, err := os.ReadFile(filepath.Join(root, SiteProgressPage))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)

	if len(seen) == 1 {
		for digest := range seen {
			if digest == "" {
				break // every row ran but recorded no digest; nothing literal to check
			}
			if !strings.Contains(page, digest) {
				t.Errorf("%s: every estate's last_run agrees on emulator %q, but the page never mentions it", SiteProgressPage, digest)
			}
		}
	}

	// Whether rows agree or disagree, the page must never assert uniformity
	// ("every estate ... ran against") once more than one distinct group is
	// present among the rows - a real digest disagreement, an unrecorded
	// row mixed with a recorded one, or an all-unrecorded board.
	distinctReal := 0
	for digest := range seen {
		if digest != "" {
			distinctReal++
		}
	}
	_, hasUnknown := seen[""]
	disagrees := distinctReal > 1 || (hasUnknown && len(seen) > 1) || (hasUnknown && distinctReal == 0)
	claimsUniformity := strings.Contains(page, "Every estate below last ran against")
	if disagrees && claimsUniformity {
		t.Errorf("%s claims every estate ran against a single emulator image, but last_run.emulator disagrees across rows (or is unrecorded for some): %v", SiteProgressPage, seen)
	}
	if len(seen) > 1 {
		// A genuine disagreement: every real digest recorded by at least
		// one row must be named somewhere on the page, not just the winner.
		for digest, n := range seen {
			if digest == "" {
				continue
			}
			if !strings.Contains(page, digest) {
				t.Errorf("%s: %d estate(s) recorded last_run.emulator=%q, but the page never mentions it", SiteProgressPage, n, digest)
			}
		}
	}
}

// TestRenderedDocsAreCurrent: a fresh render equals the committed files.
func TestRenderedDocsAreCurrent(t *testing.T) {
	root := testRoot(t)
	stale, err := StaleFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) > 0 {
		t.Errorf("rendered files are stale; run `go run ./tools/gauntlet render`:\n  %s", strings.Join(stale, "\n  "))
	}
}

// TestProtocolParser: the grammar round-trips, including a detail with
// spaces and an equals sign, and malformed lines are errors.
func TestProtocolParser(t *testing.T) {
	in := strings.Join([]string{
		"some other output",
		"GAUNTLET protocol=1",
		"GAUNTLET stage=cold_deploy verdict=pass duration_s=12",
		"GAUNTLET stage=migrate verdict=pass duration_s=43.5 detail=68 added, 41 stamped, 27 skipped",
		"GAUNTLET stage=test_plan verdict=fail detail=Non-static identity argument: x=y",
		"GAUNTLET stage=test_apply verdict=not_run",
		"=== PASS ===",
	}, "\n")
	res, err := ParseProtocol(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Spoken {
		t.Error("protocol line not recognised")
	}
	want := map[string]string{"cold_deploy": "pass", "migrate": "pass", "test_plan": "fail", "test_apply": "not_run"}
	for id, v := range want {
		if res.Stages[id] != v {
			t.Errorf("stage %s = %q, want %q", id, res.Stages[id], v)
		}
	}
	if res.Detail["test_plan"] != "Non-static identity argument: x=y" {
		t.Errorf("detail lost: %q", res.Detail["test_plan"])
	}
	if res.Detail["migrate"] != "68 added, 41 stamped, 27 skipped" {
		t.Errorf("detail lost: %q", res.Detail["migrate"])
	}
	if res.Seconds["cold_deploy"] != 12 {
		t.Errorf("duration_s lost for cold_deploy: %v", res.Seconds["cold_deploy"])
	}
	if res.Seconds["migrate"] != 43.5 {
		t.Errorf("duration_s lost for migrate: %v", res.Seconds["migrate"])
	}
	if _, ok := res.Seconds["test_plan"]; ok {
		t.Errorf("test_plan reported no duration_s but one was recorded: %v", res.Seconds["test_plan"])
	}
	if len(res.Unknown) != 0 {
		t.Errorf("unexpected unknown stages %v", res.Unknown)
	}

	for _, bad := range []string{
		"GAUNTLET stage=cold_deploy verdict=maybe",
		"GAUNTLET verdict=pass",
		"GAUNTLET protocol=2",
		"GAUNTLET stage=cold_deploy verdict=pass duration_s=notanumber",
	} {
		if _, err := ParseProtocol(strings.NewReader(bad)); err == nil {
			t.Errorf("%q parsed without error", bad)
		}
	}
	res, err = ParseProtocol(strings.NewReader("GAUNTLET protocol=1\nGAUNTLET stage=no_such verdict=pass\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unknown) != 1 || res.Unknown[0] != "no_such" {
		t.Errorf("unknown stage not reported: %v", res.Unknown)
	}
}

// TestClearNeedsEveryHeadlineStage: the definition of the headline number.
// A headline stage (active and Headline: true) must pass; a planned stage,
// or an active stage marked non-headline (#482 - "strict" today), must not
// affect clear either way.
func TestClearNeedsEveryHeadlineStage(t *testing.T) {
	all := map[string]string{}
	for _, s := range Stages() {
		all[s.ID] = VerdictPass
	}
	if !isClear(all) {
		t.Fatal("all pass should be clear")
	}
	for _, s := range HeadlineStages() {
		cp := map[string]string{}
		for k, v := range all {
			cp[k] = v
		}
		cp[s.ID] = VerdictNotRun
		if isClear(cp) {
			t.Errorf("not_run on headline stage %q should not be clear", s.ID)
		}
	}
	for _, s := range Stages() {
		if s.Status == StatusActive && s.Headline {
			continue
		}
		cp := map[string]string{}
		for k, v := range all {
			cp[k] = v
		}
		cp[s.ID] = VerdictFail
		if !isClear(cp) {
			t.Errorf("fail on non-headline stage %q (status=%s, headline=%v) must not affect clear", s.ID, s.Status, s.Headline)
		}
	}
}

// TestNonHeadlineActiveStageDoesNotGateOrGetPicked is the guard for #482:
// isClear and NextUnits must ignore a stage that is Status active but
// Headline false, exactly as they ignore a merely-planned one. Built from a
// synthetic two-stage list, independent of whatever real stage in Stages()
// happens to be non-headline today (currently "strict"), so this pins the
// mechanism itself rather than one stage's current spec - it would still
// catch a regression even if "strict" were later made headline or removed.
func TestNonHeadlineActiveStageDoesNotGateOrGetPicked(t *testing.T) {
	headlineStage := Stage{ID: "h1", Order: 1, Title: "Headline stage", Status: StatusActive, Headline: true}
	sideStage := Stage{ID: "nh1", Order: 2, Title: "Side stage (non-headline)", Status: StatusActive, Headline: false}
	headlineOnly := []Stage{headlineStage} // what HeadlineStages() would return for this synthetic pair

	// isClear: a fail on the non-headline stage must not break clear; a fail
	// on the headline stage must.
	if !isClearAgainst(headlineOnly, map[string]string{headlineStage.ID: VerdictPass, sideStage.ID: VerdictFail}) {
		t.Error("a fail on a non-headline active stage broke isClear")
	}
	if isClearAgainst(headlineOnly, map[string]string{headlineStage.ID: VerdictFail, sideStage.ID: VerdictPass}) {
		t.Error("isClear did not gate on the headline stage")
	}

	// NextUnits: an estate that fails only the non-headline stage must be
	// reported clear and never picked as work; one that fails the headline
	// stage must be picked, and never on the non-headline stage's id.
	a := &Artifact{Emulator: "e"}
	sideOnlyFails := EstateResult{
		Name: "side-only-fails", Set: SetCore, Protocol: ProtocolGauntlet,
		Stages: map[string]string{headlineStage.ID: VerdictPass, sideStage.ID: VerdictFail},
	}
	sideOnlyFails.Clear = isClearAgainst(headlineOnly, sideOnlyFails.Stages)
	headlineFails := EstateResult{
		Name: "headline-fails", Set: SetCore, Protocol: ProtocolGauntlet,
		Stages: map[string]string{headlineStage.ID: VerdictFail, sideStage.ID: VerdictPass},
	}
	headlineFails.Clear = isClearAgainst(headlineOnly, headlineFails.Stages)
	if !sideOnlyFails.Clear {
		t.Fatal("an estate failing only a non-headline stage must be Clear")
	}
	if headlineFails.Clear {
		t.Fatal("an estate failing the headline stage must not be Clear")
	}
	a.Estates = []EstateResult{sideOnlyFails, headlineFails}

	units := nextUnitsAgainst(headlineOnly, a, "all")
	for _, u := range units {
		if u.Estate == sideOnlyFails.Name {
			t.Errorf("estate failing only a non-headline stage was selected as next work: %+v", u)
		}
		if u.Stage == sideStage.ID {
			t.Errorf("the non-headline stage surfaced as a unit to fix: %+v", u)
		}
	}
	if len(units) != 1 || units[0].Estate != headlineFails.Name || units[0].Stage != headlineStage.ID {
		t.Errorf("expected exactly one unit, %s/%s, got %+v", headlineFails.Name, headlineStage.ID, units)
	}
}

// TestRebuildIsDeterministic: two rebuilds of the same inputs give the same
// bytes, and a manifest entry with no verdicts appears with every stage
// not_run.
func TestRebuildIsDeterministic(t *testing.T) {
	m := &Manifest{Estates: []Estate{
		{Name: "b", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "a", Source: "s", URL: "u", Pin: "p", Lane: "published-deployment", Set: SetGrowing},
	}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	a := &Artifact{}
	a.Rebuild(m, nil, "img", OracleVersions{})
	b1, _ := a.Canonical()
	a.Rebuild(m, nil, "img", OracleVersions{})
	b2, _ := a.Canonical()
	if !bytes.Equal(b1, b2) {
		t.Error("rebuild is not deterministic")
	}
	if a.Estates[0].Name != "a" || a.Estates[1].Name != "b" {
		t.Errorf("estates not sorted: %v", []string{a.Estates[0].Name, a.Estates[1].Name})
	}
	for _, r := range a.Estates {
		for _, s := range Stages() {
			if r.Stages[s.ID] != VerdictNotRun {
				t.Errorf("%s/%s = %q, want not_run", r.Name, s.ID, r.Stages[s.ID])
			}
		}
	}
	if a.Sets["core"].Estates != 1 || a.Sets["all"].Estates != 2 {
		t.Errorf("set sizes core=%d all=%d", a.Sets["core"].Estates, a.Sets["all"].Estates)
	}
}

// TestLegacyScriptsOnlyGoDown: the count of crossing scripts that do not
// source live/e2e/lib/gauntlet.sh is a burndown. Lower the bound when you
// convert one; never raise it.
func TestLegacyScriptsOnlyGoDown(t *testing.T) {
	const bound = 0 // measured 2026-08-21 at 24, then converted in one pass to 0: every crossing script now speaks the protocol
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	var legacy []string
	for _, e := range m.Estates {
		b, err := os.ReadFile(filepath.Join(root, e.ScriptPath()))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "live/e2e/lib/gauntlet.sh") {
			legacy = append(legacy, e.Name)
		}
	}
	sort.Strings(legacy)
	if len(legacy) > bound {
		t.Errorf("%d scripts do not speak the gauntlet protocol, bound is %d: %v", len(legacy), bound, legacy)
	}
	if len(legacy) < bound {
		t.Errorf("only %d legacy scripts remain; lower the bound in this test to %d", len(legacy), len(legacy))
	}
}

// sentinelBlindFindPattern matches a crossing script's own copy of the
// record-count find live/e2e/*/run.sh used to share before issue #861: a
// find that excludes the store's lock and in-progress-write files but not
// its provisioning sentinel (internal/live/projection/store.go's
// sentinelKeyName, ".store-sentinel" on disk). The fix lives once in
// live/e2e/lib/gauntlet.sh's gauntlet_record_count; a script that still
// spells the find out by hand (the three that predate the shared protocol:
// provisioner-taint, record-located, record-store) must at least carry the
// sentinel exclusion on the same line.
var sentinelBlindFindPattern = regexp.MustCompile(`-type f ! -name '\*\.lock'`)

// TestNoScriptCopiesTheSentinelBlindFind: issue #861. Every live/e2e/*/run.sh
// (the glob does not reach live/e2e/lib/gauntlet.sh, which has no run.sh) is
// scanned line by line; a line that re-derives the lock/tmp exclusion
// without also excluding the sentinel is a stale copy of the bug the
// helper fixed, and would count one extra file per record store it
// touches. Proven red on purpose: reverting any one of this issue's fixes
// (dropping its "! -name '.store-sentinel'" or its call to
// gauntlet_record_count) makes this test fail again.
func TestNoScriptCopiesTheSentinelBlindFind(t *testing.T) {
	root := testRoot(t)
	scripts, err := filepath.Glob(filepath.Join(root, "live", "e2e", "*", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) == 0 {
		t.Fatal("no live/e2e/*/run.sh scripts found - glob is broken")
	}
	var violations []string
	for _, s := range scripts {
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, s)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if sentinelBlindFindPattern.MatchString(line) && !strings.Contains(line, ".store-sentinel") {
				violations = append(violations, fmt.Sprintf("%s:%d", rel, i+1))
			}
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("record-count find(s) with no sentinel exclusion (issue #861):\n%s", strings.Join(violations, "\n"))
	}
}

// TestScriptStubSpeaksProtocol: the stub `add` writes parses as a script that
// reports every stage not_run.
func TestScriptStubSpeaksProtocol(t *testing.T) {
	stub := scriptStub(Estate{Name: "x", Source: "s", URL: "u", Pin: "p", Lane: "terraform-popular", Set: SetGrowing})
	if !strings.Contains(stub, "live/e2e/lib/gauntlet.sh") {
		t.Error("stub does not source the protocol library")
	}
	for _, s := range Stages() {
		if !strings.Contains(stub, "gauntlet_stage "+s.ID+" not_run") {
			t.Errorf("stub does not report stage %s", s.ID)
		}
	}
}

// TestProtocolLibraryMatchesParser: the shell library's output parses, for
// every verdict the parser accepts, with and without a detail.
func TestProtocolLibraryMatchesParser(t *testing.T) {
	root := testRoot(t)
	lib := filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh")
	if _, err := os.Stat(lib); err != nil {
		t.Fatal(err)
	}
	script := "source " + lib + "\ngauntlet_begin\ngauntlet_stage cold_deploy pass\ngauntlet_stage migrate fail 'a detail, with = sign'\ngauntlet_stage test_plan not_run\ngauntlet_end\n"
	out, err := runBash(script)
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	res, err := ParseProtocol(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if !res.Spoken || res.Stages["cold_deploy"] != "pass" || res.Stages["migrate"] != "fail" || res.Stages["test_plan"] != "not_run" {
		t.Errorf("unexpected parse: %+v\n%s", res, out)
	}
	if res.Detail["migrate"] != "a detail, with = sign" {
		t.Errorf("detail: %q", res.Detail["migrate"])
	}
	// The real shell library must emit duration_s on every stage line, not
	// just the parser accepting it when present (#434): a bug that made
	// gauntlet_stage stop printing the field would pass TestProtocolParser
	// (which builds its own GAUNTLET lines by hand) while silently going
	// dark here, where the library itself produces the line.
	for _, id := range []string{"cold_deploy", "migrate", "test_plan"} {
		if _, ok := res.Seconds[id]; !ok {
			t.Errorf("live/e2e/lib/gauntlet.sh did not emit duration_s for stage %q: %+v\n%s", id, res, out)
		}
	}
	// An unknown verdict must make the library exit non-zero.
	if _, err := runBash("source " + lib + "\ngauntlet_begin\ngauntlet_stage x maybe\n"); err == nil {
		t.Error("library accepted verdict 'maybe'")
	}
}

// TestGapFailureIsNotAttributedToThePreviousStage is issue #555's own
// demonstration, at the library layer rather than a real crossing script: a
// script's fail() has always reported against CURRENT_STAGE
// (`if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail
// "$*"; fi`, copied byte-for-byte into all 25 protocol-speaking crossing
// scripts - see fail() below), but CURRENT_STAGE was only ever assigned by
// hand, at the start of each stage's own section, and never cleared at the
// end. The setup between two stages - a docker run, copy_leaf_modules,
// write_root, an oracle's own plan - runs while CURRENT_STAGE still names
// whichever stage happened to be assigned last, so a failure there was
// blamed on a stage that had already finished (or, as here, never actually
// entered its own real verdict-bearing section at all).
//
// corpus-hongbomiao-labelbox/run.sh is the concrete reproduction this test
// mirrors: line 545 sets CURRENT_STAGE=day2_replace for that stage's own
// stock-oracle comparison (STAGE F-ORACLE), which succeeds; lines 566-607
// then build the GREENFIELD estate (copy_leaf_modules, write_root, a second
// `docker run`) with CURRENT_STAGE never reassigned until line 608's own
// CURRENT_STAGE=greenfield - so a docker failure in that window records
// "day2_replace fail" for a stage whose own oracle already passed.
//
// "before" is that exact shape: CURRENT_STAGE assigned by hand and never
// cleared, matching every crossing script as of this writing except the
// ones this issue's own PR converts. "after" is the same shape through
// gauntlet_begin_stage/gauntlet_end_stage (this file's new library
// functions): the same failure, in the same window, is left unattributed
// instead.
func TestGapFailureIsNotAttributedToThePreviousStage(t *testing.T) {
	root := testRoot(t)
	lib := filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh")

	// Copied byte-for-byte from live/e2e/corpus-iam-policy/run.sh (and its
	// 24 siblings): this is the real contract a crossing script's fail()
	// speaks, not a stand-in for it.
	const failFn = `
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
`

	t.Run("before: hand-assigned CURRENT_STAGE leaks into the next stage's setup", func(t *testing.T) {
		script := "source " + lib + failFn + `
CURRENT_STAGE=""
gauntlet_begin
CURRENT_STAGE=day2_replace
log() { :; }
log "day2_replace's own oracle plan succeeds - no gauntlet_stage call yet, its real verdict comes much later in the script"
false || fail "docker run for the greenfield container failed"
`
		out, err := runBash(script)
		if err == nil {
			t.Fatalf("expected the script to exit non-zero\n%s", out)
		}
		res, perr := ParseProtocol(bytes.NewReader(out))
		if perr != nil {
			t.Fatalf("parse: %v\n%s", perr, out)
		}
		if res.Stages["day2_replace"] != VerdictFail {
			t.Fatalf("expected the unfixed shape to misattribute the greenfield setup failure to day2_replace (proving the bug is real, not hypothetical); got %+v\n%s", res.Stages, out)
		}
	})

	t.Run("after: gauntlet_end_stage closes the window, the same failure is unattributed", func(t *testing.T) {
		script := "source " + lib + failFn + `
gauntlet_begin
gauntlet_begin_stage day2_replace
log() { :; }
log "day2_replace's own oracle plan succeeds - no gauntlet_stage call yet, its real verdict comes much later in the script"
gauntlet_end_stage
false || fail "docker run for the greenfield container failed"
`
		out, err := runBash(script)
		if err == nil {
			t.Fatalf("expected the script to exit non-zero\n%s", out)
		}
		res, perr := ParseProtocol(bytes.NewReader(out))
		if perr != nil {
			t.Fatalf("parse: %v\n%s", perr, out)
		}
		if v, ok := res.Stages["day2_replace"]; ok {
			t.Errorf("day2_replace must not carry a verdict from a window neither its own nor any stage's - got %q\n%s", v, out)
		}
		if len(res.Stages) != 0 {
			t.Errorf("no stage should have been reported at all; the failure happened entirely outside any stage's own bracket, got %+v\n%s", res.Stages, out)
		}
	})
}
