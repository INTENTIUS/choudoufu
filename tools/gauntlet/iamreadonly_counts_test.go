// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// iamReadOnlyScript is the estate whose test_apply passed on
// "genuine no-op: 0 objects before, 0 after" (#1275).
const iamReadOnlyScript = "live/e2e/corpus-iam-read-only-policy/run.sh"

// testApplyCountVerdictFn lifts test_apply_count_verdict out of the
// committed script, the way greenfieldVerdictFn does for #1204, so the arms
// a passing run never reaches are driven here directly. It refuses rather
// than skips when the script is missing or no longer routes the stage
// through the function: either would leave this test driving dead code.
func testApplyCountVerdictFn(t *testing.T) string {
	t.Helper()
	path := filepath.Join(testRoot(t), filepath.FromSlash(iamReadOnlyScript))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	script := string(b)
	if !strings.Contains(script, `TA_VERDICT="$(test_apply_count_verdict "$BEFORE_N" "$AFTER_N" 1)" \`) {
		t.Fatalf("%s no longer composes test_apply's verdict through test_apply_count_verdict; this test would be driving dead code", path)
	}
	if !strings.Contains(script, `gauntlet_stage test_apply pass "$TA_VERDICT`) {
		t.Fatalf("%s no longer reports test_apply's pass detail from test_apply_count_verdict's sentence", path)
	}
	lines := strings.Split(script, "\n")
	start := -1
	for i, ln := range lines {
		if ln == "test_apply_count_verdict() {" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s does not define test_apply_count_verdict() at column 0", path)
	}
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			return strings.Join(lines[start:i+1], "\n") + "\n"
		}
	}
	t.Fatalf("%s: test_apply_count_verdict has no closing brace at column 0", path)
	return ""
}

// driveTestApplyCountVerdict runs the lifted function and returns its
// sentence and whether it passed. A bash error other than the function's own
// return 1 fails the test.
func driveTestApplyCountVerdict(t *testing.T, before, after, want string) (string, bool) {
	t.Helper()
	script := fmt.Sprintf("set -uo pipefail\n%s\nout=\"$(test_apply_count_verdict %q %q %q)\"; rc=$?\nprintf '%%s\\nrc=%%s\\n' \"$out\" \"$rc\"\n",
		testApplyCountVerdictFn(t), before, after, want)
	out, err := runBash(script)
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	s := strings.TrimRight(string(out), "\n")
	i := strings.LastIndex(s, "\nrc=")
	if i < 0 {
		t.Fatalf("no rc line in:\n%s", s)
	}
	return s[:i], s[i+len("\nrc="):] == "0"
}

// TestTestApplyVerdictRefusesAZeroCount is #1275, written from what the
// stage promises - that a no-op apply was observed over the objects migrate
// stamped - and not from the function's implementation.
func TestTestApplyVerdictRefusesAZeroCount(t *testing.T) {
	t.Run("the real case: one object on both sides passes and says so", func(t *testing.T) {
		got, ok := driveTestApplyCountVerdict(t, "1", "1", "1")
		if !ok {
			t.Fatalf("one stamped object seen before and after is a no-op and the verdict failed it:\n%s", got)
		}
		if got != "genuine no-op: 1 objects before, 1 after" {
			t.Errorf("unexpected pass sentence: %q", got)
		}
	})

	t.Run("zero on both sides is the defect, not a no-op", func(t *testing.T) {
		got, ok := driveTestApplyCountVerdict(t, "0", "0", "1")
		if ok {
			t.Fatalf("0 = 0 passed - this is #1275, the committed row's own sentence:\n%s", got)
		}
		if strings.Contains(got, "genuine no-op") {
			t.Errorf("a zero count is spoken as a no-op observation:\n%s", got)
		}
		if !strings.Contains(got, "counted 0") || !strings.Contains(got, "migrate stamped 1") {
			t.Errorf("the verdict does not say what it failed to see:\n%s", got)
		}
	})

	t.Run("the BREAK_BEFORE shape: 0 before, 1 after", func(t *testing.T) {
		got, ok := driveTestApplyCountVerdict(t, "0", "1", "1")
		if ok || !strings.Contains(got, "counted 0") {
			t.Errorf("a zero BEFORE count did not fail on the zero:\n%s", got)
		}
	})

	t.Run("the BREAK_NOOP shape: 1 before, 0 after", func(t *testing.T) {
		got, ok := driveTestApplyCountVerdict(t, "1", "0", "1")
		if ok {
			t.Fatalf("a marker lost across the apply passed:\n%s", got)
		}
		if !strings.Contains(got, "changed across a no-op apply: 1 -> 0") {
			t.Errorf("the verdict does not name the change:\n%s", got)
		}
	})

	t.Run("a count that is not what migrate stamped fails even when equal", func(t *testing.T) {
		got, ok := driveTestApplyCountVerdict(t, "2", "2", "1")
		if ok || !strings.Contains(got, "expected 1") {
			t.Errorf("2 = 2 against 1 stamped passed or did not say why:\n%s", got)
		}
	})

	t.Run("an empty or non-numeric count is a read that never ran", func(t *testing.T) {
		for _, c := range [][2]string{{"", "1"}, {"1", ""}, {"unknown", "unknown"}} {
			got, ok := driveTestApplyCountVerdict(t, c[0], c[1], "1")
			if ok || !strings.Contains(got, "not a number") {
				t.Errorf("before=%q after=%q passed or did not say the read never ran:\n%s", c[0], c[1], got)
			}
		}
	})
}
