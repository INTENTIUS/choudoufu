// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// TestManifestEntryChangeMarksOnlyItsOwnRow is #1295's second half.
// live/gauntlet/estates.json holds every estate's pre_apply list in one
// file, so a path diff either badges every row or none. The comparison is
// per entry: the row's own entry at last_run.commit against the entry now.
// Changing one estate's pre_apply marks that row stale and leaves its
// neighbour, whose entry did not move, current. Prose fields (source,
// reason) do not change what a run does and do not badge.
func TestManifestEntryChangeMarksOnlyItsOwnRow(t *testing.T) {
	root := initTestRepo(t)
	commitTestFile(t, root, SharedLibDir+"/gauntlet.sh", "gauntlet_stage() { :; }\n", "protocol library")
	commitTestFile(t, root, "live/e2e/alpha/run.sh", "#!/usr/bin/env bash\n", "alpha script")
	commitTestFile(t, root, "live/e2e/beta/run.sh", "#!/usr/bin/env bash\n", "beta script")
	manifest := func(alphaPreApply, betaReason string) string {
		return `{"estates": [
  {"name": "alpha", "source": "a", "lane": "reference", "set": "growing", "pre_apply": [` + alphaPreApply + `]},
  {"name": "beta", "source": "b", "lane": "reference", "set": "core", "reason": "` + betaReason + `"}
]}
`
	}
	measured := commitTestFile(t, root, ManifestPath, manifest(`"aws_iam_role.a"`, "first"), "manifest as measured")
	a := &Artifact{Estates: []EstateResult{
		rowWithRun("alpha", "live/e2e/alpha/run.sh", measured),
		rowWithRun("beta", "live/e2e/beta/run.sh", measured),
	}}

	if got := AllScriptStaleness(root, a); got["alpha"].State != ScriptCurrent || got["beta"].State != ScriptCurrent {
		t.Fatalf("before any change: alpha %q, beta %q; want both current", got["alpha"].State, got["beta"].State)
	}

	commitTestFile(t, root, ManifestPath, manifest(`"aws_iam_role.a", "aws_iam_policy.b"`, "reworded"), "alpha gains a pre_apply address; beta's reason is reworded")
	got := AllScriptStaleness(root, a)
	if got["alpha"].State != ScriptChanged {
		t.Errorf("alpha: state %q, changed %v; want %q after its own pre_apply moved", got["alpha"].State, got["alpha"].Changed, ScriptChanged)
	} else if want := ManifestPath + "[alpha]"; len(got["alpha"].Changed) != 1 || got["alpha"].Changed[0] != want {
		t.Errorf("alpha: changed %v, want [%s] so the note names the entry, not the whole file", got["alpha"].Changed, want)
	}
	if got["beta"].State != ScriptCurrent {
		t.Errorf("beta: state %q, changed %v; want %q - only its reason prose moved", got["beta"].State, got["beta"].Changed, ScriptCurrent)
	}
	if note := scriptStaleNote(got["alpha"], "live/e2e/alpha"); !strings.Contains(note, ManifestPath+"[alpha]") {
		t.Errorf("alpha's page note does not name its manifest entry: %q", note)
	}
}
