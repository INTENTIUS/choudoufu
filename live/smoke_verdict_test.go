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
	"strings"
	"testing"
	"time"
)

// The smoke verdict (GitHub issue #1439). A scenario step that exits
// non-zero under smoke.sh's `set -euo pipefail` used to end the run with no
// PASS line and no FAIL line, and the CI smoke jobs read the step's exit
// code, which CLAUDE.md says never to do. Now smoke.sh ends every run on
// one verdict line, a control run on a `-> caught` line as well, and
// live/smoke/ci-run.sh reads the run's log for those lines.
//
// live/smoke/selftest-verdict.sh drives the shipped smoke.sh, lib.sh and
// ci-run.sh over throwaway scenarios, and TestSmokeVerdictSelftestPasses
// runs it here, in the `go test ./live/` tier, the way
// live/smoke_teardown_test.go runs selftest-teardown.sh.
//
// Proving it red: run the selftest with SMOKE_SRC pointing at live/smoke as
// of main before #1439 (21 FAIL lines on 2026-09-21).

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

// TestSmokeCIRunReadsALog: the wrapper's --check form against logs written
// here, so the reader is measured in this tier and not only inside the
// selftest.
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
