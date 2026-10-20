// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A proof's last_run in live/smoke/claims.json is a measured result carried
// across releases, so it may never be typed by hand. Each one must say
// exactly what git says: claims.json at last_run.commit lists the scenario
// in claim last_run.claim's cell for the same provider, and that cell reads
// last_run.status; last_run.date is that commit's committer day;
// last_run.version is the release tag it follows (git describe --tags
// --abbrev=0); and the commit is an ancestor of HEAD, so a squash cannot
// orphan it.
//
// The entries restored on 2026-10-19 come from 958bcf7f2d, the last commit
// before #2028's dated-run rule turned 21 cells unproven. A dated evidence
// file written by a later hand run supersedes a last_run; it does not need
// to edit one.

// claimsHistory is the part of git the guard reads, so the red test can
// feed it a fixture.
type claimsHistory interface {
	claimsAt(commit string) ([]byte, error)
	commitDay(commit string) (string, error)
	describe(commit string) (string, error)
	isAncestorOfHEAD(commit string) (bool, error)
}

type gitClaimsHistory struct{ root string }

func (g gitClaimsHistory) git(args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", g.root}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func (g gitClaimsHistory) claimsAt(commit string) ([]byte, error) {
	out, err := exec.Command("git", "-C", g.root, "show", commit+":live/smoke/claims.json").Output()
	return out, err
}

func (g gitClaimsHistory) commitDay(commit string) (string, error) {
	return g.git("log", "-1", "--format=%cs", commit)
}

func (g gitClaimsHistory) describe(commit string) (string, error) {
	return g.git("describe", "--tags", "--abbrev=0", commit)
}

func (g gitClaimsHistory) isAncestorOfHEAD(commit string) (bool, error) {
	err := exec.Command("git", "-C", g.root, "merge-base", "--is-ancestor", commit, "HEAD").Run()
	if err == nil {
		return true, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// lastRunHistoryProblems checks every last_run in f against h.
func lastRunHistoryProblems(f smokeClaimsFile, h claimsHistory) []string {
	var out []string
	past := map[string]*smokeClaimsFile{}
	for _, c := range f.Claims {
		for _, provider := range f.ProviderOrder {
			for _, p := range c.Providers[provider].Proofs {
				lr := p.LastRun
				if lr == nil {
					continue
				}
				where := fmt.Sprintf("claim %d (%s) %s last_run", c.ID, provider, filepath.Base(p.Scenario))
				if p.Scenario == "" {
					out = append(out, where+": only a scenario proof carries a last_run")
					continue
				}
				if ok, err := h.isAncestorOfHEAD(lr.Commit); err != nil {
					out = append(out, fmt.Sprintf("%s: commit %s does not resolve: %v", where, lr.Commit, err))
					continue
				} else if !ok {
					out = append(out, fmt.Sprintf("%s: commit %s is not an ancestor of HEAD", where, lr.Commit))
				}
				if day, err := h.commitDay(lr.Commit); err != nil || day != lr.Date {
					out = append(out, fmt.Sprintf("%s: date %q, but commit %s is dated %q (%v)", where, lr.Date, lr.Commit, day, err))
				}
				if v, err := h.describe(lr.Commit); err != nil || v != lr.Version {
					out = append(out, fmt.Sprintf("%s: version %q, but commit %s follows %q (%v)", where, lr.Version, lr.Commit, v, err))
				}
				then, ok := past[lr.Commit]
				if !ok {
					raw, err := h.claimsAt(lr.Commit)
					if err == nil {
						var pf smokeClaimsFile
						if err = json.Unmarshal(raw, &pf); err == nil {
							then = &pf
						}
					}
					if err != nil {
						out = append(out, fmt.Sprintf("%s: claims.json at %s does not read: %v", where, lr.Commit, err))
					}
					past[lr.Commit] = then
				}
				if then == nil {
					continue
				}
				found := false
				for _, pc := range then.Claims {
					if pc.ID != lr.Claim {
						continue
					}
					cell := pc.Providers[provider]
					for _, pp := range cell.Proofs {
						if pp.Scenario == p.Scenario {
							found = true
						}
					}
					if found && cell.Status != lr.Status {
						out = append(out, fmt.Sprintf("%s: status %q, but claim %d (%s) read %q at %s", where, lr.Status, lr.Claim, provider, cell.Status, lr.Commit))
					}
				}
				if !found {
					out = append(out, fmt.Sprintf("%s: claims.json at %s lists no %s in claim %d (%s)", where, lr.Commit, p.Scenario, lr.Claim, provider))
				}
			}
		}
	}
	return out
}

// TestClaimsLastRunMatchesHistory holds every last_run to git.
func TestClaimsLastRunMatchesHistory(t *testing.T) {
	h := gitClaimsHistory{root: ".."}
	if shallow, err := h.git("rev-parse", "--is-shallow-repository"); err != nil {
		t.Fatalf("git rev-parse --is-shallow-repository: %v", err)
	} else if shallow == "true" {
		t.Fatal("this checkout is shallow; a last_run cannot be checked against history without it - fetch full history (fetch-depth: 0) rather than let this guard skip")
	}
	f := readSmokeClaims(t)
	n := 0
	for _, c := range f.Claims {
		for _, cell := range c.Providers {
			for _, p := range cell.Proofs {
				if p.LastRun != nil {
					n++
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("no proof carries a last_run; this guard is checking nothing")
	}
	problems := lastRunHistoryProblems(f, h)
	for _, p := range problems {
		t.Error(p)
	}
	if len(problems) == 0 {
		t.Logf("%d last_run entries match git history", n)
	}
}

// fakeClaimsHistory is one commit, its day, its tag and its claims.json.
type fakeClaimsHistory struct {
	commit, day, tag string
	claims           []byte
	ancestor         bool
}

func (f fakeClaimsHistory) claimsAt(c string) ([]byte, error) {
	if c != f.commit {
		return nil, fmt.Errorf("no such commit")
	}
	return f.claims, nil
}
func (f fakeClaimsHistory) commitDay(string) (string, error)      { return f.day, nil }
func (f fakeClaimsHistory) describe(string) (string, error)       { return f.tag, nil }
func (f fakeClaimsHistory) isAncestorOfHEAD(string) (bool, error) { return f.ancestor, nil }

// TestClaimsLastRunHistoryCatches proves the guard red: each case is a
// last_run that does not say what git says.
func TestClaimsLastRunHistoryCatches(t *testing.T) {
	const sha = "958bcf7f2dc7fd956ecccce8bcc017a9ef81c04a"
	past := []byte(`{"provider_order":["aws"],"claims":[
		{"id":1,"providers":{"aws":{"status":"proven","proofs":[{"scenario":"live/smoke/scenarios/a.sh"}]}}},
		{"id":2,"providers":{"aws":{"status":"open","proofs":[{"scenario":"live/smoke/scenarios/b.sh"}]}}}]}`)
	h := fakeClaimsHistory{commit: sha, day: "2026-10-11", tag: "v0.26.0", claims: past, ancestor: true}
	file := func(scenario string, lr smokeLastRun) smokeClaimsFile {
		return smokeClaimsFile{ProviderOrder: []string{"aws"}, Claims: []smokeClaim{
			{ID: 1, Providers: map[string]smokeClaimProviderCell{"aws": {Status: "proven", Proofs: []smokeProof{
				{Scenario: "live/smoke/scenarios/" + scenario + ".sh", LastRun: &lr}}}}},
		}}
	}
	good := smokeLastRun{Status: "proven", Claim: 1, Commit: sha, Date: "2026-10-11", Version: "v0.26.0"}
	if got := lastRunHistoryProblems(file("a", good), h); len(got) != 0 {
		t.Fatalf("an exact last_run was reported: %v", got)
	}
	with := func(edit func(*smokeLastRun)) smokeLastRun { lr := good; edit(&lr); return lr }
	for _, tc := range []struct {
		name string
		f    smokeClaimsFile
		h    claimsHistory
	}{
		{"a date typed by hand", file("a", with(func(l *smokeLastRun) { l.Date = "2026-10-18" })), h},
		{"a newer version than the commit follows", file("a", with(func(l *smokeLastRun) { l.Version = "v0.30.0" })), h},
		{"a status the cell never read", file("b", with(func(l *smokeLastRun) { l.Claim = 2 })), h},
		{"a scenario the claim never listed", file("c", good), h},
		{"a commit git does not have", file("a", with(func(l *smokeLastRun) { l.Commit = "0000000000000000000000000000000000000000" })), h},
		{"a commit off HEAD's history", file("a", good), fakeClaimsHistory{commit: sha, day: "2026-10-11", tag: "v0.26.0", claims: past}},
	} {
		if got := lastRunHistoryProblems(tc.f, tc.h); len(got) == 0 {
			t.Errorf("%s: the guard reported nothing", tc.name)
		}
	}
}
