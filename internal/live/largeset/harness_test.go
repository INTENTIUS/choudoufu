// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package largeset

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeChoudoufu stands in for the binary: init always succeeds, apply prints
// what a real apply prints unless the working directory is the estate named
// by LARGESET_FAKE_FAIL (exit 1) or LARGESET_FAKE_SILENT (exit 0 with no
// "Apply complete!" line).
const fakeChoudoufu = `#!/usr/bin/env bash
here="$(basename "$PWD")"
case "$1" in
  init) echo "Initializing..."; exit 0 ;;
  apply)
    if [ "$here" = "${LARGESET_FAKE_FAIL:-}" ]; then echo "Error: the emulator said no" >&2; exit 1; fi
    if [ "$here" = "${LARGESET_FAKE_SILENT:-}" ]; then echo "nothing to see"; exit 0; fi
    echo "Apply complete! Resources: 4 added, 0 changed, 0 destroyed."
    exit 0 ;;
esac
exit 2
`

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// runApply runs live/large-set/apply.sh over a fresh N=5 fixture with the
// fake binary and an endpoint that is never dialled, and returns its stdout.
func runApply(t *testing.T, env ...string) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not on PATH")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fixture")
	if _, err := Write(fixture, Options{Estates: 5, ModuleVersion: VersionA}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "choudoufu")
	if err := os.WriteFile(bin, []byte(fakeChoudoufu), 0o755); err != nil { //nolint:gosec // an executable test double
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "live", "large-set", "apply.sh"), fixture) //nolint:gosec // fixed script path
	cmd.Env = append(os.Environ(),
		"CHOUDOUFU_BIN="+bin,
		// Never dialled: the fake binary makes no calls, and setting it is
		// what keeps the script from starting a container.
		"LARGESET_ENDPOINT=http://localhost:1",
		"LARGESET_LOG_DIR="+filepath.Join(dir, "logs"),
	)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run() // the verdict line is the result, not the exit status
	t.Logf("stderr:\n%s", stderr.String())
	return stdout.String()
}

// verdictLine asserts out is exactly one line and returns it.
func verdictLine(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("apply.sh must print exactly one verdict line on stdout, printed %d:\n%s", len(lines), out)
	}
	return lines[0]
}

// TestApplyHarnessVerdictIsRedWhenAnApplyFails is #1750's harness criterion:
// the one verdict line goes red, naming the estate, when an apply fails - and
// the estates after it are not attempted, since a consumer cannot apply
// before its producer.
func TestApplyHarnessVerdictIsRedWhenAnApplyFails(t *testing.T) {
	line := verdictLine(t, runApply(t, "LARGESET_FAKE_FAIL=e03"))
	want := "LARGESET-APPLY: red - e03 failed at apply after 2/5 applied"
	if !strings.HasPrefix(line, want) {
		t.Fatalf("an apply that failed in e03 printed\n  %s\nwant a line starting\n  %s", line, want)
	}
}

// TestApplyHarnessVerdictIsRedOnASilentApply: an apply that exits 0 without
// printing "Apply complete!" is not one the harness can vouch for.
func TestApplyHarnessVerdictIsRedOnASilentApply(t *testing.T) {
	line := verdictLine(t, runApply(t, "LARGESET_FAKE_SILENT=e04"))
	want := "LARGESET-APPLY: red - e04 exited 0 from apply but printed no 'Apply complete!' after 3/5 applied"
	if !strings.HasPrefix(line, want) {
		t.Fatalf("a silent apply in e04 printed\n  %s\nwant a line starting\n  %s", line, want)
	}
}

// TestApplyHarnessVerdictIsGreenWhenEveryApplySucceeds is the control: the
// two tests above would pass against a script that is always red.
func TestApplyHarnessVerdictIsGreenWhenEveryApplySucceeds(t *testing.T) {
	line := verdictLine(t, runApply(t))
	want := "LARGESET-APPLY: green - 5/5 estates applied (source local, module A) against http://localhost:1"
	if line != want {
		t.Fatalf("a run where every apply succeeded printed\n  %s\nwant\n  %s", line, want)
	}
}
