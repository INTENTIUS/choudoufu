// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This file is issue #1158's guard, the third sighting of one pattern: an
// assertion fails, and its failure branch greps a summary line out of the
// captured output and prints only that, discarding everything
// StatelessPlanHuman.Foreign (internal/command/views/live_plan.go) printed
// beneath it - the itemized list of type, live id, tags and reason. A
// reader gets "N is not what we wanted" and never "which N".
//
// PR #1129 (terralith-scale's day2_remove) and PR #1157
// (corpus-ec2-instance-complete's two foreign-object assertions) are the
// fix shape this guard protects: on failure, print the WHOLE captured
// output via live/e2e/lib/gauntlet.sh's gauntlet_print_evidence, never a
// `grep` of it. #1137's own worker then found 23 more live/e2e/*/run.sh
// scripts sharing the exact same `grep -E '^Foreign resources:'`-discards-
// the-list defect (PR #1157's body has the full list); #1158 fixed those.
//
// Scoped narrowly to that one literal pattern on purpose, not to every
// grep that precedes a fail() call in these scripts. live/e2e/*/run.sh is
// full of failure branches that grep a NARROWER pattern than "everything"
// and are still adequate evidence for the assertion they serve - e.g.
// `grep -qE '^  # .+ will be (created|updated|destroyed)' <<< "$PLAN_OUT"
// && { grep -E '^  # .+ will be' <<< "$PLAN_OUT"; fail ... }`, which shows
// every resource-action line there is to show for "the plan proposes no
// change" and hides no separate section the way `Foreign resources:`
// hides the itemized list beneath it. corpus-ec2-instance-complete - the
// very file PR #1157 fixed - still has dozens of that shape, deliberately
// untouched. A guard that flagged every grep-before-fail would immediately
// fail on code the maintainer has already accepted and never go green; see
// the PR body for #1158 for the reasoning kept here in one place rather
// than re-litigated per file.
//
// foreignResourcesPrintGrep matches a NON-QUIET grep whose pattern mentions
// "Foreign resources:" - i.e. one that PRINTS matching lines rather than
// merely testing for one. It deliberately does not match `grep -qE
// '^Foreign resources: ...'` (the assertion itself, which prints nothing):
// the literal text "grep -E '" does not occur in "grep -qE '" (that is one
// token, "-qE", not "-E" preceded by a space), so the boolean check never
// trips this pattern.
var foreignResourcesPrintGrep = regexp.MustCompile(`grep -E '[^']*Foreign resources:[^']*'`)

// TestNoFailureBranchDiscardsTheForeignResourcesList scans every
// live/e2e/*/run.sh for a printing `Foreign resources:` grep that sits
// right next to a fail() call - same line after a `;`, or the next
// non-blank line - with nothing in between that prints the RAW captured
// output. That adjacency is what turns "assertion failed" into "and here
// is only the one line you already knew was wrong."
//
// A script that assigns the grep's output to a helper (gauntlet_print_evidence
// or a local wrapper around printf '%s\n') before calling fail is not
// flagged: the point is not "no grep may appear near fail", it is "the
// evidence a reader needs must be printed before fail exits", and
// gauntlet_print_evidence prints the whole variable, not a filtered slice
// of it.
func TestNoFailureBranchDiscardsTheForeignResourcesList(t *testing.T) {
	scripts, err := filepath.Glob(filepath.Join("e2e", "*", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) < 20 {
		t.Fatalf("found %d live/e2e/*/run.sh scripts, expected at least 20 - this guard is looking in the wrong place", len(scripts))
	}
	sort.Strings(scripts)

	var violations []string
	for _, script := range scripts {
		body, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			loc := foreignResourcesPrintGrep.FindStringIndex(line)
			if loc == nil {
				continue
			}
			rest := line[loc[1]:]
			sameLineFail := strings.Contains(rest, "fail")
			nextLineFail := false
			if i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				nextLineFail = strings.HasPrefix(next, "fail ") || strings.HasPrefix(next, `fail"`)
			}
			if sameLineFail || nextLineFail {
				violations = append(violations, fmt.Sprintf("%s:%d: %s", script, i+1, strings.TrimSpace(line)))
			}
		}
	}
	if len(violations) > 0 {
		t.Errorf("%d failure branch(es) grep a \"Foreign resources:\" summary line out of their own captured plan output and print only that, discarding the itemized list StatelessPlanHuman.Foreign (internal/command/views/live_plan.go) prints beneath it - issue #1158, the same defect PR #1129 and PR #1157 fixed elsewhere. Call live/e2e/lib/gauntlet.sh's gauntlet_print_evidence with the WHOLE captured output instead of grepping it:\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}
