// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os/exec"
	"strings"
	"testing"
)

// operationalBrief is the tracked record of how to work on this fork: the
// test invocation and its symlink trap, the doc cache path, the cohort
// ownership split, the worktree rules, and the adversarial-audit shapes.
const operationalBrief = ".claude/agents/live-markers.md"

// trackedInstructions is every path under .claude/ that carries shared
// written instruction rather than per-machine state, and that therefore has
// to survive a fresh clone.
//
// The skill is here for the reason a62c892276 gave when it widened
// TestLocalAgentStateStaysUntracked to admit skills/: a skill has exactly
// the standing of the brief. That commit widened the exclusion half and left
// the inclusion half naming only the brief, so re-narrowing the .gitignore
// would have silently dropped the skill while both tests stayed green - the
// #165 state again, one directory over.
//
// agent-progress.sh is here for the same reason, one directory further:
// checking whether a background subagent is stuck or progressing was an ad
// hoc dance re-derived by hand each time this session, at the cost of
// pulling raw transcript into context just to answer "is it still writing".
// Scripted, it is shared written instruction with exactly the standing of
// the brief and the skill - it does not survive a fresh clone if ignored,
// which is the #165 state again.
var trackedInstructions = []string{
	operationalBrief,
	".claude/skills/measuring-choudoufu/SKILL.md",
	".claude/scripts/agent-progress.sh",
}

// TestOperationalBriefIsTracked is issue #165's guard.
//
// HANDOFF.md carried this material and was tracked. Retiring it
// (099193d189) sent the work items to the issue tracker, which was right,
// and the operational half to .claude/agents/, which .gitignore excluded
// wholesale. So for a while the only written record of how to work here
// lived in an untracked directory on one machine: absent from a fresh
// clone, absent from any backup, invisible to a second contributor.
//
// The ignore rule is now /.claude/* with an exception for agents/, which
// keeps settings.local.json and worktrees/ out while letting the brief in.
// That is a narrow exception and an easy one to lose while adjusting the
// surrounding rules, so it is checked rather than trusted.
func TestOperationalBriefIsTracked(t *testing.T) {
	for _, path := range trackedInstructions {
		out, err := exec.Command("git", "-C", "..", "ls-files", "--error-unmatch", path).CombinedOutput()
		if err != nil {
			t.Errorf("%s is not tracked by git (%v: %s)\n"+
				"It is written instruction on how to work on this repository. Untracked, it does not "+
				"survive a fresh clone and no second contributor or agent can read it - which is exactly "+
				"the state issue #165 was filed about. Check the /.claude/* exception in .gitignore still "+
				"re-includes /.claude/agents/, /.claude/skills/ and /.claude/scripts/.",
				path, err, strings.TrimSpace(string(out)))
		}
	}
}

// TestLocalAgentStateStaysUntracked is the other half, and the reason the
// exception is written narrowly. .claude/ also holds settings.local.json,
// which is per-machine, and worktrees/, which is scratch for concurrent
// agents. Re-including the whole directory to rescue the brief would commit
// both.
//
// skills/ and scripts/ were added to the allowed set deliberately, not to
// make a commit pass. The rule this test enforces is about PER-MACHINE
// state; a skill or a script like agent-progress.sh is shared written
// instruction with exactly the standing of the brief above, and it does not
// survive a fresh clone if it is ignored - which is the state issue #165
// was filed about. settings.local.json and worktrees/ stay excluded, which
// is what the narrowness was for.
func TestLocalAgentStateStaysUntracked(t *testing.T) {
	out, err := exec.Command("git", "-C", "..", "ls-files", ".claude/").Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	for _, path := range strings.Fields(string(out)) {
		if !strings.HasPrefix(path, ".claude/agents/") && !strings.HasPrefix(path, ".claude/skills/") && !strings.HasPrefix(path, ".claude/scripts/") {
			t.Errorf("%s is tracked, but .claude/ outside agents/, skills/ and scripts/ is local state.\n"+
				"settings.local.json is per-machine and worktrees/ is scratch; neither belongs in the "+
				"repository. Narrow the .gitignore exception back to /.claude/agents/, "+
				"/.claude/skills/ and /.claude/scripts/.", path)
		}
	}
}
