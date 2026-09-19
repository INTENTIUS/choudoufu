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

// This file is issue #1378.
//
// live/smoke/smoke.sh runs every scenario under `set -euo pipefail`, and
// the real-AWS scenarios tear down in an EXIT trap. A trap body is
// ordinary code there, so the first command that fails ends the whole trap
// and every step after it is skipped with nothing printed:
//
//	set -euo pipefail
//	t() { echo start; x="$(false)"; echo MUST-PRINT; }
//	trap 't; echo cleanup' EXIT
//	exit 1          // prints "start" and nothing else
//
// On claim 37 one throttled list-object-versions took out `just down`, the
// IAM role deletion, the deletion of a created KMS key and the restore of
// a BORROWED key's policy, all silently. Those steps cost money when they
// are skipped, and the key policy is somebody else's.
//
// live/smoke/selftest-teardown.sh drives the shipped teardown bodies
// against a stub `aws` and a stub `just` and reads the stub's call log.
// This test is what runs it, so the guard is in the ordinary `go test
// ./live/` tier rather than being a script somebody has to remember to
// type - the shape live/livecert_selftests_test.go established for
// live/live-cert/'s selftests (#1267).
//
// It deliberately does not skip. The selftest needs bash, python3 and the
// scripts, all of which are in this repository or in CI's image, and a
// t.Skip here would leave the guard permanently green wherever one of them
// was missing.

// smokeTeardownSelftest is the script, relative to this package.
const smokeTeardownSelftest = "smoke/selftest-teardown.sh"

// smokeTeardownBound is how long the selftest gets before it is killed and
// this test fails. Measured at 1.4s on a 2026-09-19 laptop: twelve cases
// over stubs, plus one re-run of a single case against a mutated copy. It
// sleeps, polls and waits nowhere, and it makes no network call, so a run
// anywhere near this bound means something is wrong rather than slow.
const smokeTeardownBound = 120 * time.Second

func TestSmokeTeardownSelftestPasses(t *testing.T) {
	if _, err := os.Stat(smokeTeardownSelftest); err != nil {
		t.Fatalf("%s is missing: %v\nIt is the only thing that measures the smoke teardowns (#1378); "+
			"if it was renamed, rename it here too rather than dropping the wiring.", smokeTeardownSelftest, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), smokeTeardownBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", smokeTeardownSelftest)
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("%s did not finish within %s and was killed. It stubs every command it runs and waits on "+
			"nothing, so a run this long is a hang rather than a slow machine.\nIts output up to the kill:\n%s",
			smokeTeardownSelftest, smokeTeardownBound, out.String())
	}

	// The verdict is the script's own lines, not its exit code. A summary
	// that says PASS over a run that measured nothing is the failure this
	// repository keeps finding, so both are required to agree.
	text := out.String()
	if err != nil {
		t.Fatalf("%s failed (%v). Its own output says which teardown step was not reached:\n%s",
			smokeTeardownSelftest, err, text)
	}
	if strings.Contains(text, "FAIL:") && !strings.Contains(text, "mutant |") {
		t.Errorf("%s exited 0 while printing a FAIL line:\n%s", smokeTeardownSelftest, text)
	}
	if !strings.Contains(text, "PASS: selftest-teardown") {
		t.Errorf("%s exited 0 without printing its PASS line, so nothing here shows it measured anything:\n%s",
			smokeTeardownSelftest, text)
	}
	// Its last case re-runs one of the others against a copy of
	// live/smoke whose `set +e` has been removed, and requires it to fail.
	// Without that line the suite above could be passing over a condition
	// it cannot observe.
	if !strings.Contains(text, "the same case fails against a teardown with no 'set +e'") {
		t.Errorf("%s did not run its own mutation case, so nothing shows these checks can go red:\n%s",
			smokeTeardownSelftest, text)
	}
}

// TestRealAWSSmokeTeardownsAreGuarded is the roster half of #1378, and the
// reason it exists is that the selftest above measures the teardowns it
// names and nothing else. A seventh real-AWS scenario written tomorrow
// with an unguarded trap would leave this package green.
//
// Every scenario that installs an EXIT trap has to turn errexit off in the
// body that runs there, and this reads the scenario files for it. What it
// checks is narrow on purpose: it cannot tell a COULD NOT line that fires
// from one that does not, which is what the selftest is for. It can tell
// that a new trap body has not been thought about at all.
func TestRealAWSSmokeTeardownsAreGuarded(t *testing.T) {
	entries, err := os.ReadDir(smokeScenariosDir)
	if err != nil {
		t.Fatalf("reading %s: %v", smokeScenariosDir, err)
	}
	// bucket-iam.sh is not under scenarios/ and installs a trap of its own.
	files := []string{filepath.Join("smoke", "bucket-iam.sh")}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sh") {
			files = append(files, filepath.Join(smokeScenariosDir, e.Name()))
		}
	}

	checked := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("reading %s: %v", path, err)
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "trap ") || !strings.Contains(trimmed, "EXIT") {
				continue
			}
			// smoke.sh's own `trap cleanup EXIT` shape, and any trap that
			// runs a single named function, still has to say so. The
			// teardown function is whatever the trap names first.
			fn := teardownFuncName(trimmed)
			if fn == "" || fn == "cleanup" {
				continue
			}
			checked++
			if !bodyDisablesErrexit(string(data), fn) {
				t.Errorf("%s: %s runs from an EXIT trap but does not start with `set +e`.\n"+
					"smoke.sh runs scenarios under `set -euo pipefail`, so the first call in there that fails "+
					"ends the whole teardown and skips every step after it, silently - which is issue #1378, and "+
					"on a real-AWS scenario it means a stack, a role or somebody else's KMS key policy left behind.\n"+
					"Put `set +e` (and `set +u` if the body reads variables that may be unset) at the top of %s, "+
					"give every step that can fail its own COULD NOT line naming the resource, and add a case to "+
					"%s for it.", path, fn, fn, smokeTeardownSelftest)
			}
		}
	}
	if checked == 0 {
		t.Errorf("no EXIT trap with a teardown function was found in %s or smoke/bucket-iam.sh at all, "+
			"so this guard passed over nothing. The parsing is broken rather than the scenarios being clean.",
			smokeScenariosDir)
	}
}

// teardownFuncName reads the first function a trap line calls, out of the
// quoted command string a `trap '...' EXIT` line carries.
func teardownFuncName(trapLine string) string {
	open := strings.Index(trapLine, "'")
	if open < 0 {
		return ""
	}
	rest := trapLine[open+1:]
	close := strings.Index(rest, "'")
	if close < 0 {
		return ""
	}
	for _, part := range strings.Split(rest[:close], ";") {
		part = strings.TrimSpace(part)
		switch {
		case part == "", strings.HasPrefix(part, "set "):
			continue
		}
		// A bare call, not a pipeline or a redirection.
		if i := strings.IndexAny(part, " |><&"); i >= 0 {
			part = part[:i]
		}
		return part
	}
	return ""
}

// bodyDisablesErrexit says whether the named shell function turns errexit
// off before it does anything else that could fail.
func bodyDisablesErrexit(src, fn string) bool {
	start := strings.Index(src, fn+"() {")
	if start < 0 {
		// The trap names a function this file does not define. Other files
		// in the roster define it, and their own entry checks it.
		return true
	}
	lines := strings.Split(src[start:], "\n")
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "}" {
			return false
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "set +e") {
			return true
		}
		if strings.HasPrefix(trimmed, "set ") || strings.HasPrefix(trimmed, "local ") {
			continue
		}
		return false
	}
	return false
}
