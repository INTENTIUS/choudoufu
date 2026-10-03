// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// No smoke scenario throws an init's output away on its failure path
// (#1717). `chdf init -input=false -no-color >/dev/null 2>&1 || fail ...
// "init failed"` leaves a job log reading that one line: a provider that
// would not download, a backend that would not configure and a binary that
// would not start all print it, so the failure cannot be told from a
// regression. claims-smoke run 36339857046 lost a BREAK arm's cause exactly
// this way. lib.sh's `logged` (#1521) keeps the output under SMOKE_LOG_DIR
// and prints its tail before the FAIL line; an init goes through it.
//
// An init whose failure is tolerated (`|| true`, a teardown) has no failure
// path to explain, and is allowed to stay quiet.
//
// Proving it red: put `>/dev/null 2>&1` back on any converted init.

// initWord is `init` as a command argument: whitespace before it, and the
// end of a word after it. `present-init`, a log name, is not one.
var initWord = regexp.MustCompile(`(^|\s)init($|[\s;&|)])`)

// discarded is any redirection of a stream to /dev/null: `>/dev/null`,
// `2>/dev/null`, `&>/dev/null`, `>> /dev/null`, `> "/dev/null"`.
var discarded = regexp.MustCompile(`>[>|]?\s*"?/dev/null`)

// messageCmd is a command whose arguments are prose for the reader, where
// the word init describes a step rather than running one.
var messageCmd = regexp.MustCompile(`^(cmd|step|note|explain|proof|echo|printf|evidence)\b`)

// failCall starts fail's arguments, which are a message too.
var failCall = regexp.MustCompile(`\bfail\b`)

var tolerated = regexp.MustCompile(`\|\|\s*(true|:)\s*$`)

// discardedInit reports whether one logical line (continuations joined)
// runs an init and sends its output to /dev/null on a path that fails.
func discardedInit(line string) bool {
	code := strings.TrimSpace(line)
	if code == "" || strings.HasPrefix(code, "#") || messageCmd.MatchString(code) {
		return false
	}
	if i := strings.Index(code, " #"); i >= 0 {
		code = code[:i]
	}
	if tolerated.MatchString(code) {
		return false
	}
	run := code
	if loc := failCall.FindStringIndex(run); loc != nil {
		run = run[:loc[0]]
	}
	return initWord.MatchString(run) && discarded.MatchString(run)
}

// initLogicalLines joins backslash-continued lines, reporting each by the line
// number it starts on, so `init ... >/dev/null \` / `|| fail` is one line.
func initLogicalLines(src string) (lines []string, starts []int) {
	var cur strings.Builder
	start := 0
	for i, l := range strings.Split(src, "\n") {
		if cur.Len() == 0 {
			start = i + 1
		}
		if strings.HasSuffix(l, "\\") && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			cur.WriteString(strings.TrimSuffix(l, "\\"))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(l)
		lines = append(lines, cur.String())
		starts = append(starts, start)
		cur.Reset()
	}
	return lines, starts
}

func TestSmokeScenariosKeepInitOutput(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("smoke", "scenarios", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no smoke scenarios found; the guard would pass on nothing")
	}
	files = append(files, filepath.Join("smoke", "lib.sh"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines, starts := initLogicalLines(string(b))
		for i, line := range lines {
			if discardedInit(line) {
				t.Errorf("%s:%d: an init's output goes to /dev/null, so its failure prints a bare FAIL line with the cause gone; run it through lib.sh's logged: %s", f, starts[i], strings.TrimSpace(line))
			}
		}
	}
}

func TestDiscardedInitPattern(t *testing.T) {
	for _, s := range []string{
		`( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "x" "init failed"`,
		`( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "x" "init failed"`,
		`stock -chdir=/work/m init -input=false -no-color >/dev/null 2>&1 || fail "carve" "stock init failed"`,
		`run_chdf "$d" init >/dev/null || fail "$SCEN" "choudoufu init failed in $d"`,
		`( cd "$d" && "$RUN_BIN" init -input=false 2>/dev/null ) || fail "x" "y"`,
		`terraform init &>/dev/null || fail "x" "y"`,
		`chdf init >> /dev/null 2>&1 || fail "x" "y"`,
		`  ( cd "$A" && no_aws chdf init -input=false -no-color >/dev/null )  || fail "k" "BREAK"`,
		`logged x-init x "init failed" -- chdf init -input=false >/dev/null`,
	} {
		if !discardedInit(s) {
			t.Errorf("pattern misses a discarded init in %q", s)
		}
	}
	for _, s := range []string{
		`logged present-init present "choudoufu init failed" -- chdf -chdir="$SMOKE_WORK" init -input=false -no-color`,
		`logged init-a x "init failed in a" -- in_dir "$SMOKE_WORK/a" chdf init -input=false -no-color`,
		`( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || true`,
		`T_OUT="$(run "$SMOKE_WORK/typo" init -input=false -no-color 2>&1)" || fail "w" "x"`,
		`cmd "rm -rf .terraform* ; choudoufu init >/dev/null ; choudoufu plan"`,
		`step "1. init, and start the apply" >/dev/null`,
		`( cd "$X" && chdf apply >/dev/null 2>&1 ) || fail "x" "apply after init failed"`,
		`# ( cd "$X" && chdf init >/dev/null 2>&1 ) || fail "x" "init failed"`,
		`chdf plan -input=false >/dev/null # then init`,
	} {
		if discardedInit(s) {
			t.Errorf("pattern flags an init that keeps its output, or no init: %q", s)
		}
	}
	lines, starts := initLogicalLines("a\n( cd \"$X\" && terraform init -input=false >/dev/null 2>&1 ) \\\n  || fail \"x\" \"stock init failed\"\nb\n")
	if len(lines) != 4 || starts[1] != 2 || !discardedInit(lines[1]) {
		t.Errorf("a continued init is not joined and flagged: %q %v", lines, starts)
	}
}
