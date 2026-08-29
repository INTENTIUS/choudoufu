// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mergeTestRepo builds a throwaway git repo (initTestRepo, notes_test.go)
// and writes a manifest straight to the working tree, untracked by git -
// exactly like the real repo's live/gauntlet/estates.json is read straight
// off disk by LoadManifest. MergeArtifact never reads the manifest through
// git, only the current working tree, so leaving it untracked means a
// `git checkout` between commits never touches it.
func mergeTestRepo(t *testing.T, estates []Estate) string {
	t.Helper()
	root := initTestRepo(t)
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(ManifestPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(root, &Manifest{Estates: estates}); err != nil {
		t.Fatal(err)
	}
	return root
}

// commitArtifact writes a and commits it at ArtifactPath, returning the new
// commit's SHA.
func commitArtifact(t *testing.T, root string, a *Artifact, message string) string {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return commitTestFile(t, root, ArtifactPath, string(b), message)
}

// gitCheckout moves HEAD to rev (detached is fine - these tests never need
// a named branch), so the next commitArtifact/commitTestFile call builds a
// sibling of rev rather than a descendant of whatever HEAD was before.
func gitCheckout(t *testing.T, root, rev string) {
	t.Helper()
	cmd := exec.Command("git", "checkout", "-q", rev)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout %s: %v\n%s", rev, err, out)
	}
}

func stagesAllPass() map[string]string {
	m := map[string]string{}
	for _, s := range HeadlineStages() {
		m[s.ID] = VerdictPass
	}
	return m
}

func stagesAllNotRun() map[string]string {
	m := map[string]string{}
	for _, s := range HeadlineStages() {
		m[s.ID] = VerdictNotRun
	}
	return m
}

// TestMergeArtifactMergesDistinctEstatesAndAggregateMatchesRows is #488's
// core claim: two branches adding different estates' rows merge cleanly,
// and the REBUILT AGGREGATE - not just the individual rows - equals the
// count of clear rows. Asserting the aggregate is the point: it is exactly
// the value that was silently wrong on PR #502's clean git auto-merge
// (documented on #488 - the auto-merged aggregate read "core 3/25" while
// the rows underneath it, recomputed by hand, said 4).
func TestMergeArtifactMergesDistinctEstatesAndAggregateMatchesRows(t *testing.T) {
	if len(HeadlineStages()) == 0 {
		t.Skip("no active headline stages")
	}
	estates := []Estate{
		{Name: "alpha", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "beta", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
	}
	root := mergeTestRepo(t, estates)

	base := &Artifact{Estates: []EstateResult{
		{Name: "alpha", Stages: stagesAllNotRun()},
		{Name: "beta", Stages: stagesAllNotRun()},
	}}
	baseSHA := commitArtifact(t, root, base, "base")

	// ours: alpha clears. Measured at baseSHA (an ancestor of both ours and
	// theirs), the same way a real run stamps the commit it ran at, which
	// is always an ancestor of whatever later commit carries the artifact.
	ours := &Artifact{Estates: []EstateResult{
		{Name: "alpha", Stages: stagesAllPass(), LastRun: &LastRun{Commit: baseSHA, Date: "2026-08-29T00:00:00Z", ExitCode: 0}},
		{Name: "beta", Stages: stagesAllNotRun()},
	}}
	oursSHA := commitArtifact(t, root, ours, "ours: alpha clears")

	// theirs: a sibling of ours, also a child of base - beta clears.
	gitCheckout(t, root, baseSHA)
	theirs := &Artifact{Estates: []EstateResult{
		{Name: "alpha", Stages: stagesAllNotRun()},
		{Name: "beta", Stages: stagesAllPass(), LastRun: &LastRun{Commit: baseSHA, Date: "2026-08-29T00:00:00Z", ExitCode: 0}},
	}}
	theirsSHA := commitArtifact(t, root, theirs, "theirs: beta clears")

	merged, err := MergeArtifact(root, baseSHA, oursSHA, theirsSHA)
	if err != nil {
		t.Fatalf("MergeArtifact: %v", err)
	}

	clearRows := 0
	for _, r := range merged.Estates {
		if r.Clear {
			clearRows++
		}
	}
	if clearRows != 2 {
		t.Fatalf("recomputed clear rows = %d, want 2 (alpha and beta both clear); rows: %+v", clearRows, merged.Estates)
	}
	core := merged.Sets["core"]
	if core.Clear != clearRows {
		t.Errorf("sets.core.clear = %d, but %d rows actually read clear:true - exactly PR #502's silent-wrong-aggregate shape", core.Clear, clearRows)
	}
	if core.Clear != 2 || core.Estates != 2 {
		t.Errorf("sets.core = %+v, want Clear:2 Estates:2", core)
	}
}

// TestMergeArtifactRefusesWhenBothSidesChangeSameEstate is #488's case 2:
// guessing which side's measurement is current is how a wrong verdict
// lands, so the merge must refuse rather than pick one, and must name the
// estate.
func TestMergeArtifactRefusesWhenBothSidesChangeSameEstate(t *testing.T) {
	estates := []Estate{{Name: "gamma", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"}}
	root := mergeTestRepo(t, estates)

	base := &Artifact{Estates: []EstateResult{{Name: "gamma", Stages: map[string]string{"cold_deploy": VerdictNotRun}}}}
	baseSHA := commitArtifact(t, root, base, "base")

	ours := &Artifact{Estates: []EstateResult{{Name: "gamma",
		Stages:  map[string]string{"cold_deploy": VerdictPass},
		LastRun: &LastRun{Commit: baseSHA, Date: "2026-08-29T00:00:00Z"},
	}}}
	oursSHA := commitArtifact(t, root, ours, "ours changes gamma")

	gitCheckout(t, root, baseSHA)
	theirs := &Artifact{Estates: []EstateResult{{Name: "gamma",
		Stages:  map[string]string{"cold_deploy": VerdictFail},
		LastRun: &LastRun{Commit: baseSHA, Date: "2026-08-29T01:00:00Z"},
	}}}
	theirsSHA := commitArtifact(t, root, theirs, "theirs changes gamma differently")

	_, err := MergeArtifact(root, baseSHA, oursSHA, theirsSHA)
	if err == nil {
		t.Fatal("expected a conflict error, got nil")
	}
	var conflict *MergeConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *MergeConflictError, got %T: %v", err, err)
	}
	if conflict.Estate != "gamma" {
		t.Errorf("conflict.Estate = %q, want %q", conflict.Estate, "gamma")
	}
	if !strings.Contains(err.Error(), "gamma") {
		t.Errorf("error does not name the estate: %v", err)
	}
}

// TestMergeArtifactRefusesNonAncestorCommit is issue #509's class: a row
// stamped with a commit that is real but not an ancestor of the branch
// that claims to have produced it - #509's corpus-iam-read-only-policy
// defect, where a rebase replayed the diff but left the old, now-dangling
// commit hash baked into last_run.commit.
func TestMergeArtifactRefusesNonAncestorCommit(t *testing.T) {
	estates := []Estate{{Name: "delta", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"}}
	root := mergeTestRepo(t, estates)

	base := &Artifact{Estates: []EstateResult{{Name: "delta", Stages: map[string]string{"cold_deploy": VerdictNotRun}}}}
	baseSHA := commitArtifact(t, root, base, "base")

	// A commit that is real, but a sibling of ours/theirs rather than their
	// ancestor - never reachable from either.
	gitCheckout(t, root, baseSHA)
	rogueSHA := commitTestFile(t, root, "rogue.txt", "rogue\n", "an unrelated, disconnected commit")

	gitCheckout(t, root, baseSHA)
	ours := &Artifact{Estates: []EstateResult{{Name: "delta",
		Stages:  map[string]string{"cold_deploy": VerdictPass},
		LastRun: &LastRun{Commit: rogueSHA, Date: "2026-08-29T00:00:00Z"},
	}}}
	oursSHA := commitArtifact(t, root, ours, "ours: delta clears, stamped with a dangling commit")

	// theirs: identical to base, so baseSHA itself stands in as "the
	// unchanged side" - no separate commit needed (an empty commit is
	// exactly what git commit refuses to create).
	_, err := MergeArtifact(root, baseSHA, oursSHA, baseSHA)
	if err == nil {
		t.Fatal("expected a refusal for a non-ancestor last_run.commit, got nil")
	}
	if !strings.Contains(err.Error(), "delta") || !strings.Contains(err.Error(), rogueSHA) {
		t.Errorf("error does not name the estate and the dangling commit: %v", err)
	}
}

// TestMergeArtifactRefusesNonzeroExitWithNoFailingStage is #413's class,
// re-checked against the MERGED output: a nonzero last_run.exit_code with
// no stage reading fail anywhere is never legitimate (TestNonzeroExitCodeImpliesAFailingStage,
// gauntlet_test.go).
func TestMergeArtifactRefusesNonzeroExitWithNoFailingStage(t *testing.T) {
	estates := []Estate{{Name: "epsilon", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"}}
	root := mergeTestRepo(t, estates)

	base := &Artifact{Estates: []EstateResult{{Name: "epsilon", Stages: map[string]string{"cold_deploy": VerdictNotRun}}}}
	baseSHA := commitArtifact(t, root, base, "base")

	ours := &Artifact{Estates: []EstateResult{{Name: "epsilon",
		Stages:  map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictNotRun},
		LastRun: &LastRun{Commit: baseSHA, Date: "2026-08-29T00:00:00Z", ExitCode: 1},
	}}}
	oursSHA := commitArtifact(t, root, ours, "ours: nonzero exit, no failing stage - stale-carry-forward shape")

	// theirs: identical to base, so baseSHA itself stands in as "the
	// unchanged side".
	_, err := MergeArtifact(root, baseSHA, oursSHA, baseSHA)
	if err == nil {
		t.Fatal("expected a refusal for exit_code != 0 with no failing stage, got nil")
	}
	if !strings.Contains(err.Error(), "epsilon") {
		t.Errorf("error does not name the estate: %v", err)
	}
}

// TestMergeArtifactRefusesWhenProductCodeDiffers is the safety
// precondition: this merge is only valid when no product code moved
// between a row's measurement and the merge. PRs #502 and #503 both
// genuinely needed a re-run for exactly this reason - tools/gauntlet/run.go's
// recording path changed underneath them.
func TestMergeArtifactRefusesWhenProductCodeDiffers(t *testing.T) {
	estates := []Estate{{Name: "zeta", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"}}
	root := mergeTestRepo(t, estates)

	base := &Artifact{Estates: []EstateResult{{Name: "zeta", Stages: map[string]string{"cold_deploy": VerdictNotRun}}}}
	baseSHA := commitArtifact(t, root, base, "base")

	// ours also touches a product file alongside its row - the #502/#503
	// shape.
	commitTestFile(t, root, "tools/gauntlet/run.go", "// changed\n", "ours: also touches run.go")
	ours := &Artifact{Estates: []EstateResult{{Name: "zeta",
		Stages:  map[string]string{"cold_deploy": VerdictPass},
		LastRun: &LastRun{Commit: baseSHA, Date: "2026-08-29T00:00:00Z"},
	}}}
	oursSHA := commitArtifact(t, root, ours, "ours: zeta clears")

	// theirs: identical to base, so baseSHA itself stands in as "the
	// unchanged side".
	_, err := MergeArtifact(root, baseSHA, oursSHA, baseSHA)
	if err == nil {
		t.Fatal("expected a refusal for product code differing, got nil")
	}
	if !strings.Contains(err.Error(), "tools/gauntlet/run.go") {
		t.Errorf("error does not name the changed file: %v", err)
	}
}

// TestMergeArtifactAllowedPath is a fast, git-free unit test of the
// allowlist itself: the artifact, its Hugo copy, the rendered progress
// docs, and an estate's own run.sh are allowed to differ without a re-run;
// everything else - most importantly the gauntlet tool's own recording
// path - is not.
func TestMergeArtifactAllowedPath(t *testing.T) {
	allowed := []string{
		ArtifactPath,
		SiteDataPath,
		"site/content/docs/progress/_index.md",
		"site/content/docs/progress/add-an-estate.md",
		"live/e2e/corpus-alb-complete/run.sh",
		"live/e2e/reference-ec2-vpc/run.sh",
	}
	for _, p := range allowed {
		if !mergeArtifactAllowedPath(p) {
			t.Errorf("mergeArtifactAllowedPath(%q) = false, want true", p)
		}
	}
	disallowed := []string{
		"tools/gauntlet/run.go",
		"tools/gauntlet/artifact.go",
		"internal/command/apply.go",
		"live/e2e/lib/gauntlet.sh",
		"live/gauntlet/estates.json",
		"live/floci-image",
	}
	for _, p := range disallowed {
		if mergeArtifactAllowedPath(p) {
			t.Errorf("mergeArtifactAllowedPath(%q) = true, want false", p)
		}
	}
}
