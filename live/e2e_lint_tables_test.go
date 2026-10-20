// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/lint"
)

// e2eRunScript is the harness whose lint-rejects step this file reads.
const e2eRunScript = "e2e/run.sh"

// e2eLintTodo is the one table of run.sh's lint-rejects step that holds
// fixtures no lint rule catches yet, each asserted there to be refused by
// something else. Any fixture outside it must be in LINT_ENFORCED or
// LINT_WARNED.
var e2eLintTodo = map[string]string{
	"duplicate-identity":    "refused at identity resolution, not by lint",
	"module-provider-block": "GitHub issue #201: admitted at lint, refused later",
}

// shellAssignment is a NAME="..." assignment in run.sh, possibly spanning
// several lines.
func shellAssignment(t *testing.T, script, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*` + name + `="([^"]*)"`)
	m := re.FindAllStringSubmatch(script, -1)
	if len(m) != 1 {
		t.Fatalf("%s assigns %s %d times, want once", e2eRunScript, name, len(m))
	}
	return m[0][1]
}

// lintTable reads a dir:rule table, one pair per line.
func lintTable(t *testing.T, script, name string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, line := range strings.Split(shellAssignment(t, script, name), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		dir, rule, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("%s: %s line %q is not dir:rule", e2eRunScript, name, line)
		}
		if _, dup := out[dir]; dup {
			t.Errorf("%s: %s lists %s twice", e2eRunScript, name, dir)
		}
		out[dir] = rule
	}
	return out
}

// TestE2ELintStageRunsEveryLimitsFixture (#2133 finding 9): every
// directory under live/e2e/limits is run by live/e2e/run.sh's lint-rejects
// step, in LINT_ENFORCED with an error-severity rule or in LINT_WARNED with
// a warning-severity one, or is one of the two LINT_TODO fixtures no rule
// catches. The step's completeness check takes its list from exactly
// those three tables, so no other variable can mark a fixture as listed
// and skip it, the way LINT_OTHER_PLATFORM once skipped the two Azure
// fixtures.
//
// Proving it red: move policy-overwrite-at-key out of LINT_ENFORCED, list
// arm-tag-limit in LINT_ENFORCED, or add a variable to the LINT_LISTED
// line.
func TestE2ELintStageRunsEveryLimitsFixture(t *testing.T) {
	raw, err := os.ReadFile(e2eRunScript)
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	enforced := lintTable(t, script, "LINT_ENFORCED")
	warned := lintTable(t, script, "LINT_WARNED")
	todo := strings.Fields(shellAssignment(t, script, "LINT_TODO"))

	gotTodo := append([]string(nil), todo...)
	sort.Strings(gotTodo)
	var wantTodo []string
	for dir := range e2eLintTodo {
		wantTodo = append(wantTodo, dir)
	}
	sort.Strings(wantTodo)
	if strings.Join(gotTodo, " ") != strings.Join(wantTodo, " ") {
		t.Errorf("%s: LINT_TODO is %v, want %v; a fixture a rule catches belongs in LINT_ENFORCED or LINT_WARNED", e2eRunScript, gotTodo, wantTodo)
	}

	// The completeness check's list is built from the three tables and
	// nothing else.
	listed := regexp.MustCompile(`(?m)^\s*LINT_LISTED="\$LINT_LISTED (\$LINT_[^"]*)"`).FindAllStringSubmatch(script, -1)
	if len(listed) != 1 || strings.TrimSpace(listed[0][1]) != "$LINT_TODO" {
		t.Errorf("%s: the completeness check adds %v to LINT_LISTED beside the dir:rule tables, want only $LINT_TODO; any other list there skips its fixtures", e2eRunScript, listed)
	}
	for _, done := range []string{`done <<< "$LINT_ENFORCED"`, `done <<< "$LINT_WARNED"`} {
		if strings.Count(script, done) < 2 {
			t.Errorf("%s: %s is not read both by the completeness check and by a loop that runs it", e2eRunScript, done)
		}
	}

	entries, err := os.ReadDir("e2e/limits")
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		dirs[dir] = true
		rule, isEnforced := enforced[dir]
		wrule, isWarned := warned[dir]
		_, isTodo := e2eLintTodo[dir]
		switch {
		case isEnforced && isWarned:
			t.Errorf("%s: %s is in both LINT_ENFORCED and LINT_WARNED", e2eRunScript, dir)
		case isEnforced:
			if lint.Rule(rule).Severity() != lint.SeverityError {
				t.Errorf("%s: %s is in LINT_ENFORCED under %s, a warning rule; it belongs in LINT_WARNED", e2eRunScript, dir, rule)
			}
		case isWarned:
			if lint.Rule(wrule).Severity() != lint.SeverityWarning {
				t.Errorf("%s: %s is in LINT_WARNED under %s, an error rule; it belongs in LINT_ENFORCED", e2eRunScript, dir, wrule)
			}
		case isTodo:
		default:
			t.Errorf("%s: live/e2e/limits/%s is in neither LINT_ENFORCED nor LINT_WARNED, so the e2e lint stage never runs it", e2eRunScript, dir)
		}
	}
	for _, table := range []map[string]string{enforced, warned} {
		for dir := range table {
			if !dirs[dir] {
				t.Errorf("%s names live/e2e/limits/%s, which does not exist", e2eRunScript, dir)
			}
		}
	}
}
