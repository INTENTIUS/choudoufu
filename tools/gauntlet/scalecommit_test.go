// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// offMainScaleCommit is a scale record whose commit is not an ancestor of
// HEAD, written down with what the run actually measured so the provenance
// can still be checked from main. GitHub issue #1744 item 5: #1733 merged
// a real-AWS scale-128 record naming 686ac8a958, a commit on a local branch
// that was never pushed (`gh api .../commits/686ac8a958` returns 422), and
// nothing checked that a scale record's commit resolves.
//
// An entry binds WHAT it allows, not only WHO: the record's whole key and
// its exact commit. Base must be an ancestor of HEAD, and so must
// Equivalent when set, so CI checks those on every run. Where the commit's
// object is present (the machine that measured it), the test also checks
// that its first parent is Base and that its diff from Base is either the
// same patch as Equivalent (a rebased-away pre-image) or touches only
// MeasuredArtifacts (a commit that recorded a measurement and changed no
// code), so the measured tree is Base's, or Base plus Equivalent's patch.
type offMainScaleCommit struct {
	Estate, Target string
	Scale          int
	Commit         string
	Base           string
	Equivalent     string
	Why            string
}

// MeasuredArtifacts are the files a measurement-only commit may touch for
// its tree to count as its parent's.
var scaleMeasuredArtifacts = map[string]bool{
	"live/gauntlet.json":       true,
	"live/gauntlet-scale.json": true,
}

var offMainScaleCommits = []offMainScaleCommit{
	{
		Estate: "terralith-scale", Target: "aws", Scale: 4,
		Commit:     "420d460c047132624e7a565fd8dc8803fd372ee2",
		Base:       "c02ea7aa88acf482b46231958b0b1193d3335ec9",
		Equivalent: "54ba20623f",
		Why:        "the pre-rebase image of 54ba20623f (#588's plan-timing change to live/live-cert/terralith-scale.sh), the #509 shape: the run measured c02ea7aa88 plus that one patch, which reached main as 54ba20623f",
	},
	{
		Estate: "terralith-scale", Target: "aws", Scale: 128,
		Commit: "686ac8a9583617eaec25277a61f7a97d7c22f53b",
		Base:   "3db8029364",
		Why:    "a commit on the unpushed local branch runs that recorded the scale-50 real-AWS result in live/gauntlet.json and live/gauntlet-scale.json and changed nothing else; the scale-128 run (#1733) measured 3db8029364's tree",
	},
}

func scaleKeyString(estate, target string, scale int) string {
	return fmt.Sprintf("(%s, %s, %d)", estate, target, scale)
}

// checkScaleRecordCommits is the logic behind
// TestEveryScaleRecordCommitResolves, factored out so its red case can be
// shown without leaving a failing test committed.
func checkScaleRecordCommits(root, head string, records []ScaleRecord, offMain []offMainScaleCommit) []string {
	var problems []string
	type pointer struct{ key, what, commit string }
	var ptrs []pointer
	for _, r := range records {
		k := scaleKeyString(r.Estate, r.Target, r.Scale)
		ptrs = append(ptrs, pointer{k, "commit", r.Commit})
		for i, s := range r.Supersedes {
			ptrs = append(ptrs, pointer{k, fmt.Sprintf("supersedes[%d].commit", i), s.Commit})
		}
	}
	excused := map[string]offMainScaleCommit{}
	for _, e := range offMain {
		excused[scaleKeyString(e.Estate, e.Target, e.Scale)+" "+e.Commit] = e
	}
	used := map[string]bool{}
	for _, p := range ptrs {
		if p.commit == "" {
			problems = append(problems, fmt.Sprintf("%s: %s is empty", p.key, p.what))
			continue
		}
		ok, err := isAncestor(root, p.commit, head)
		if err == nil && ok {
			continue
		}
		id := p.key + " " + p.commit
		e, listed := excused[id]
		if !listed {
			problems = append(problems, fmt.Sprintf("%s: %s %s is not an ancestor of HEAD (%s) and offMainScaleCommits does not say what it measured; a provenance pointer nobody can follow (#1744)", p.key, p.what, p.commit, head))
			continue
		}
		used[id] = true
		problems = append(problems, checkOffMainEntry(root, head, e)...)
	}
	var stale []string
	for id := range excused {
		if !used[id] {
			stale = append(stale, id)
		}
	}
	sort.Strings(stale)
	for _, id := range stale {
		problems = append(problems, fmt.Sprintf("offMainScaleCommits lists %s, which no off-main scale record names; remove the entry", id))
	}
	return problems
}

func checkOffMainEntry(root, head string, e offMainScaleCommit) []string {
	var problems []string
	k := scaleKeyString(e.Estate, e.Target, e.Scale)
	if e.Why == "" {
		problems = append(problems, fmt.Sprintf("%s: offMainScaleCommits entry for %s has no Why", k, e.Commit))
	}
	for _, c := range []struct{ name, sha string }{{"Base", e.Base}, {"Equivalent", e.Equivalent}} {
		if c.sha == "" {
			if c.name == "Base" {
				problems = append(problems, fmt.Sprintf("%s: offMainScaleCommits entry for %s has no Base", k, e.Commit))
			}
			continue
		}
		if ok, err := isAncestor(root, c.sha, head); err != nil || !ok {
			problems = append(problems, fmt.Sprintf("%s: offMainScaleCommits entry for %s: %s %s is not an ancestor of HEAD (%v)", k, e.Commit, c.name, c.sha, err))
		}
	}
	if len(problems) > 0 {
		return problems
	}
	if _, err := gitOutput(root, "cat-file", "-e", e.Commit+"^{commit}"); err != nil {
		// The object is not in this clone (CI's, which fetched origin). The
		// Base and Equivalent checks above are what can be checked here.
		return nil
	}
	parent, err := gitOutput(root, "rev-parse", e.Commit+"^1")
	if err != nil {
		return []string{fmt.Sprintf("%s: %s has no first parent: %v", k, e.Commit, err)}
	}
	base, err := gitOutput(root, "rev-parse", e.Base)
	if err != nil {
		return []string{fmt.Sprintf("%s: Base %s does not resolve: %v", k, e.Base, err)}
	}
	if parent != base {
		return []string{fmt.Sprintf("%s: %s's first parent is %s, not Base %s", k, e.Commit, parent, base)}
	}
	if e.Equivalent != "" {
		a, err := patchID(root, "diff", e.Base, e.Commit)
		if err != nil {
			return []string{fmt.Sprintf("%s: patch-id of %s: %v", k, e.Commit, err)}
		}
		b, err := patchID(root, "show", "--format=", e.Equivalent)
		if err != nil {
			return []string{fmt.Sprintf("%s: patch-id of %s: %v", k, e.Equivalent, err)}
		}
		if a == "" || a != b {
			return []string{fmt.Sprintf("%s: %s's patch over Base is not %s's (patch-id %q vs %q)", k, e.Commit, e.Equivalent, a, b)}
		}
		return nil
	}
	names, err := gitOutput(root, "diff", "--name-only", e.Base, e.Commit)
	if err != nil {
		return []string{fmt.Sprintf("%s: git diff %s %s: %v", k, e.Base, e.Commit, err)}
	}
	for _, n := range strings.Fields(names) {
		if !scaleMeasuredArtifacts[n] {
			problems = append(problems, fmt.Sprintf("%s: %s changes %s over Base %s, which is not a measured artifact, so the run did not measure Base's tree; name an Equivalent or re-run", k, e.Commit, n, e.Base))
		}
	}
	return problems
}

func patchID(root string, args ...string) (string, error) {
	diff := exec.Command("git", args...)
	diff.Dir = root
	out, err := diff.Output()
	if err != nil {
		return "", err
	}
	pid := exec.Command("git", "patch-id", "--stable")
	pid.Dir = root
	pid.Stdin = strings.NewReader(string(out))
	got, err := pid.Output()
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(got))
	if len(f) == 0 {
		return "", nil
	}
	return f[0], nil
}

// TestEveryScaleRecordCommitResolves is #1744 item 5's guard: every commit a
// scale record (or one it superseded) names is an ancestor of HEAD, or
// offMainScaleCommits says, checkably, what tree the run measured. A shallow
// checkout fails rather than skips, as TestEveryLastRunCommitIsAnAncestorOfHEAD
// does.
func TestEveryScaleRecordCommitResolves(t *testing.T) {
	root := testRoot(t)
	shallow, err := isShallowRepo(root)
	if err != nil {
		t.Fatalf("git rev-parse --is-shallow-repository: %v", err)
	}
	if shallow {
		t.Fatal("this checkout is shallow; scale-record commit ancestry cannot be checked without full history")
	}
	head, err := headCommit(root)
	if err != nil {
		t.Fatal(err)
	}
	a, err := LoadScaleArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Records) == 0 {
		t.Fatalf("%s has no records; the guard would check nothing", ScaleRecordsPath)
	}
	for _, p := range checkScaleRecordCommits(root, head, append(append([]ScaleRecord{}, a.Records...), a.Refusals...), offMainScaleCommits) {
		t.Error(p)
	}
}

// TestEveryScaleRecordCommitResolvesCatchesAnUnreachableCommit shows the
// guard red: a record naming a commit that resolves nowhere, and an
// allowlist entry no record uses, are both reported.
func TestEveryScaleRecordCommitResolvesCatchesAnUnreachableCommit(t *testing.T) {
	root := testRoot(t)
	head, err := headCommit(root)
	if err != nil {
		t.Fatal(err)
	}
	bogus := strings.Repeat("0", 40)
	recs := []ScaleRecord{{Estate: "e", Target: "aws", Scale: 1, Commit: bogus}}
	got := checkScaleRecordCommits(root, head, recs, nil)
	if len(got) != 1 || !strings.Contains(got[0], bogus) {
		t.Fatalf("an unresolvable commit was not reported: %q", got)
	}
	got = checkScaleRecordCommits(root, head, nil, []offMainScaleCommit{{Estate: "e", Target: "aws", Scale: 1, Commit: bogus, Base: head, Why: "x"}})
	if len(got) != 1 || !strings.Contains(got[0], "remove the entry") {
		t.Fatalf("a stale allowlist entry was not reported: %q", got)
	}
	got = checkScaleRecordCommits(root, head, recs, []offMainScaleCommit{{Estate: "e", Target: "aws", Scale: 1, Commit: bogus, Base: bogus, Why: "x"}})
	if len(got) == 0 {
		t.Fatal("an entry whose Base is not on HEAD's history was accepted")
	}
}
