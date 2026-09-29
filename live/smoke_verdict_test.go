// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The smoke verdict (GitHub issue #1439). A scenario step that exits
// non-zero under smoke.sh's `set -euo pipefail` used to end the run with no
// PASS line and no FAIL line, and the CI smoke jobs read the step's exit
// code, which CLAUDE.md says never to do. Now smoke.sh ends every run on
// one verdict line, a control run on a `-> caught` line as well, and both
// workflows read the run's log for those lines through
// live/smoke/ci-run.sh.
//
// live/smoke/selftest-verdict.sh drives the shipped smoke.sh, lib.sh and
// ci-run.sh over throwaway scenarios, and TestSmokeVerdictSelftestPasses
// runs it here, in the `go test ./live/` tier, the way
// live/smoke_teardown_test.go runs selftest-teardown.sh. The two tests
// after it read the workflows: every step that runs a scenario goes through
// the wrapper, and a control held to one line names a line its scenario
// prints.
//
// Proving it red: run the selftest with SMOKE_SRC pointing at live/smoke as
// of main before #1439 (21 FAIL lines on 2026-09-21); replace a workflow
// step's `ci-run.sh` with `smoke.sh`; or change the --caught text.

const smokeVerdictSelftest = "smoke/selftest-verdict.sh"

// smokeVerdictBound is how long the selftest gets. Measured at 6s on a
// 2026-09-21 laptop: seventeen runs of smoke.sh over stub binaries (fourteen
// cases, two through ci-run.sh, one mutant), no container, no network, no
// sleep beyond ci-run.sh's two-second tail grace.
const smokeVerdictBound = 120 * time.Second

func TestSmokeVerdictSelftestPasses(t *testing.T) {
	if _, err := os.Stat(smokeVerdictSelftest); err != nil {
		t.Fatalf("%s is missing: %v\nIt is the only thing that measures how a smoke run ends (#1439); "+
			"if it was renamed, rename it here too rather than dropping the wiring.", smokeVerdictSelftest, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), smokeVerdictBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", smokeVerdictSelftest)
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("%s did not finish within %s and was killed.\nIts output up to the kill:\n%s",
			smokeVerdictSelftest, smokeVerdictBound, out.String())
	}
	text := out.String()
	if err != nil {
		t.Fatalf("%s failed (%v). Its own output says which way of ending a run has no verdict:\n%s",
			smokeVerdictSelftest, err, text)
	}
	if strings.Contains(text, "  FAIL:") {
		t.Errorf("%s exited 0 while printing a FAIL line:\n%s", smokeVerdictSelftest, text)
	}
	if !strings.Contains(text, "PASS: selftest-verdict") {
		t.Errorf("%s exited 0 without printing its PASS line, so nothing here shows it measured anything:\n%s",
			smokeVerdictSelftest, text)
	}
	if !strings.Contains(text, "passes against a smoke.sh with no verdict") {
		t.Errorf("%s did not run its own mutation case, so nothing shows these checks can go red:\n%s",
			smokeVerdictSelftest, text)
	}
}

// smokeWorkflows are the two that run scenarios in CI.
var smokeWorkflows = []string{bucketSmokeWorkflow, k8sSmokeWorkflow}

// smokeRunStep matches the one way a workflow step may run a scenario: the
// wrapper, the matrix entry, a log under smoke-logs, and optionally the one
// proof line the control is held to.
var smokeRunStep = regexp.MustCompile(`^bash live/smoke/ci-run\.sh \$\{\{ matrix\.scenario \}\} "\$RUNNER_TEMP/smoke-logs/[a-z-]+\.log"( --caught '([^']+)')?$`)

// workflowSteps splits a workflow into its steps, each as its own text,
// on the six-space `- name:` both files use.
func workflowSteps(wf string) []string {
	return strings.Split(wf, "\n      - name:")[1:]
}

func TestSmokeWorkflowStepsReadTheVerdictLine(t *testing.T) {
	for _, path := range smokeWorkflows {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		runs := 0
		for _, step := range workflowSteps(string(raw)) {
			if !strings.Contains(step, "matrix.scenario }}") || !strings.Contains(step, "run:") {
				continue
			}
			name := strings.TrimSpace(strings.SplitN(step, "\n", 2)[0])
			runs++
			// The run line is the one that names the matrix entry.
			var runLine string
			for _, line := range strings.Split(step, "\n") {
				line = strings.TrimSpace(line)
				if strings.Contains(line, "${{ matrix.scenario }}") {
					runLine = strings.TrimPrefix(line, "run: ")
					break
				}
			}
			if strings.Contains(runLine, "smoke.sh") {
				t.Errorf("%s step %q runs smoke.sh directly (%q); the step passes on an exit code then, and a run that ends with no verdict line exits 0 in at least one shape. Run it through live/smoke/ci-run.sh, which reads the log for the PASS line", path, name, runLine)
				continue
			}
			if !smokeRunStep.MatchString(runLine) {
				t.Errorf("%s step %q runs %q; want exactly `bash live/smoke/ci-run.sh ${{ matrix.scenario }} \"$RUNNER_TEMP/smoke-logs/<name>.log\"` with an optional --caught '<line>', so the verdict is read from a log the artifact step uploads", path, name, runLine)
			}
			if !strings.Contains(step, "shell: bash") {
				t.Errorf("%s step %q does not set `shell: bash`; the default shell runs with -e and no pipefail, and the wrapper's own settings should be the only ones in play", path, name)
			}
		}
		if runs < 2 {
			t.Errorf("%s has %d step(s) running a scenario, want at least the scenario and its BREAK=1 control; this guard is looking in the wrong place", path, runs)
		}
		if !strings.Contains(string(raw), "smoke-logs") || !strings.Contains(string(raw), "upload-artifact") {
			t.Errorf("%s does not upload the smoke-logs directory the wrapper writes; the FAIL line names that log as the place to read", path)
		}
	}
}

// workflowNamedControl matches a workflow step setting a control variable
// other than BREAK itself, the way both workflows spell it.
var workflowNamedControl = regexp.MustCompile(`(BREAK_[A-Z0-9_]+): "1"`)

// TestSmokeControlsHeldToALineTheScenarioPrints: a `--caught '<text>'` in a
// workflow is a claim about the scenario, and a stale one is a control that
// can never pass. Every such text must be the start of a proof line the
// scenario prints, and every control the scenario reads under its own name
// (BREAK_<NAME>) must be held to a line, which is #1430's reason.
func TestSmokeControlsHeldToALineTheScenarioPrints(t *testing.T) {
	held := 0
	for _, path := range smokeWorkflows {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, step := range workflowSteps(string(raw)) {
			name := strings.TrimSpace(strings.SplitN(step, "\n", 2)[0])
			cond := regexp.MustCompile(`matrix\.scenario == '([a-z0-9-]+)'`).FindStringSubmatch(step)
			var text string
			for _, line := range strings.Split(step, "\n") {
				if m := smokeRunStep.FindStringSubmatch(strings.TrimPrefix(strings.TrimSpace(line), "run: ")); m != nil {
					text = m[2]
				}
			}
			named := workflowNamedControl.FindStringSubmatch(step)
			if named != nil && text == "" {
				t.Errorf("%s step %q sets %s and passes no --caught '<line>'; a control run under its own name is held to the one line it exists to print (#1430)", path, name, named[1])
			}
			if text == "" {
				continue
			}
			held++
			if cond == nil {
				t.Errorf("%s step %q passes --caught %q but is not conditioned on one matrix.scenario, so the text cannot be checked against a scenario", path, name, text)
				continue
			}
			body, err := os.ReadFile(filepath.Join("smoke", "scenarios", cond[1]+".sh"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `proof "`+text) {
				t.Errorf("%s step %q holds %s to the line %q, and %s.sh prints no `proof \"%s...\"`; the control can never pass", path, name, cond[1], text, cond[1], text)
			}
		}
	}
	if held == 0 {
		t.Errorf("no workflow step passes --caught, and claim 31's BREAK_CROSSCHECK step is known to; this guard is looking in the wrong place")
	}
}

// TestSmokeCIRunReadsALog: the wrapper's --check form against logs written
// here, so the reader the workflows depend on is measured in this tier and
// not only inside the selftest. Bash is what the workflows run it with.
func TestSmokeCIRunReadsALog(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	none := write("none.log", "=== 1. a step ===\n")
	pass := write("pass.log", "PASS: smoke scenario 'foo' - every claim held (smoke v0)\n")
	caught := write("caught.log", "  -> caught - the break was seen\nPASS: smoke scenario 'foo' - the control (BREAK) caught what it broke, 1 proof line(s) (smoke v0)\n")
	cases := []struct {
		name string
		env  []string
		args []string
		ok   bool
		says string
	}{
		{"no PASS line", nil, []string{"foo", none}, false, "no PASS line"},
		{"PASS line", nil, []string{"foo", pass}, true, ""},
		{"PASS line for another scenario", nil, []string{"bar", pass}, false, "no PASS line"},
		{"control with no caught line", []string{"BREAK=1"}, []string{"foo", pass}, false, "no '-> caught' line"},
		{"named control with no caught line", []string{"BREAK_CROSSCHECK=1"}, []string{"foo", pass}, false, "no '-> caught' line"},
		{"control with its caught line", []string{"BREAK=1"}, []string{"foo", caught}, true, ""},
		{"held to the line it printed", nil, []string{"foo", caught, "--caught", "caught - the break was seen"}, true, ""},
		{"held to a line it did not print", nil, []string{"foo", caught, "--caught", "caught - another line"}, false, "no '-> caught - another line' line"},
	}
	for _, c := range cases {
		cmd := exec.Command("bash", append([]string{"smoke/ci-run.sh", "--check"}, c.args...)...)
		cmd.Env = append(os.Environ(), c.env...)
		out, err := cmd.CombinedOutput()
		if (err == nil) != c.ok {
			t.Errorf("%s: ci-run.sh --check %v exited ok=%v, want ok=%v:\n%s", c.name, c.args, err == nil, c.ok, out)
		}
		if c.says != "" && !strings.Contains(string(out), c.says) {
			t.Errorf("%s: ci-run.sh --check %v does not say %q:\n%s", c.name, c.args, c.says, out)
		}
	}
}
