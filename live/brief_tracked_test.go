// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"errors"
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
// scripts/pickup.sh is here because it is the first thing HANDOFF.md tells
// every session to run, and a procedure that exists only on one machine is
// the #165 state one more time. It lives under scripts/ rather than
// .claude/scripts/ because it is for people as much as for agents, and a
// contributor with no Claude Code at all runs it the same way.
var trackedInstructions = []string{
	operationalBrief,
	".claude/skills/measuring-choudoufu/SKILL.md",
	".claude/scripts/agent-progress.sh",
	"scripts/pickup.sh",
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
//
// This one always failed loudly - unlike the two skips #653 fixed - but it
// failed with the wrong cause. Any error at all read as "not tracked by git"
// and sent the reader to .gitignore, so no git on the machine, or a parent
// that is not a git repository, produced four confident reports about an
// ignore rule that was fine. Exit 1 is the only code `ls-files
// --error-unmatch` uses to mean "this path is not tracked"; anything else is
// git declining to answer, and the finding here is only as good as the answer.
func TestOperationalBriefIsTracked(t *testing.T) {
	bin := gitBin(t)
	for _, path := range trackedInstructions {
		out, err := exec.Command(bin, "-C", "..", "ls-files", "--error-unmatch", path).CombinedOutput()
		if err == nil {
			continue
		}
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			t.Fatalf("`%s -C .. ls-files --error-unmatch %s` did not answer: %v %s\n"+
				"git was found on PATH and exit 1 is how it reports an untracked path, so this is "+
				"neither - most likely the parent of live/ is not inside a git repository. Nothing here "+
				"has been established about .gitignore either way.",
				bin, path, err, strings.TrimSpace(string(out)))
		}
		t.Errorf("%s is not tracked by git (%v: %s)\n"+
			"It is written instruction on how to work on this repository. Untracked, it does not "+
			"survive a fresh clone and no second contributor or agent can read it - which is exactly "+
			"the state issue #165 was filed about. Check the /.claude/* exception in .gitignore still "+
			"re-includes /.claude/agents/, /.claude/skills/ and /.claude/scripts/.",
			path, err, strings.TrimSpace(string(out)))
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
//
// The three ways the `ls-files` call can end are kept apart on purpose. git
// missing, git exiting non-zero, and git naming tracked paths are different
// facts about the machine and the tree, and they used to collapse into one
// blanket `t.Skipf("git ls-files unavailable")` on any error at all - a
// permanent green whenever anything went wrong, under a message that named
// the wrong cause for two of the three.
//
// An empty file list needs no assertion of its own here: .claude/agents/,
// .claude/skills/ and .claude/scripts/ all carry tracked files, so a git
// that answered about the wrong tree and returned nothing would take
// TestOperationalBriefIsTracked above red on the same run.
func TestLocalAgentStateStaysUntracked(t *testing.T) {
	bin := gitBin(t)
	out, err := exec.Command(bin, "-C", "..", "ls-files", ".claude/").Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		t.Fatalf("`%s -C .. ls-files .claude/` exited non-zero: %v %s\n"+
			"git was found on PATH, so this is git refusing to answer rather than a missing tool - "+
			"most likely the parent of live/ is not inside a git repository. The whole of this check "+
			"is the list git returns, so it fails here rather than skipping: with no list it has "+
			"looked at nothing, and a .claude/ full of per-machine state would pass.",
			bin, err, stderr)
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
