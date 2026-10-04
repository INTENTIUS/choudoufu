// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// sigpipe_test.go: epic #1885's guard against `cmd | grep -q PATTERN` in a
// script that sets `-o pipefail`.
//
// grep -q exits on its first match. If cmd (tofu, terraform, choudoufu,
// kubectl) writes anything after that, the write hits a closed pipe, cmd
// dies of SIGPIPE (141), and pipefail makes the whole pipeline fail even
// though the pattern was found. Measured in the #1885 test phase: 261/300
// failures with saturated CPUs, 0/1000 idle. A positive check turns a pass
// into a random fail; a negated one (`cmd | grep -q X && fail`) turns a
// fail into a false pass. #1895 and #1899 fixed two scripts; the kind sweep
// fixed the rest by capturing the output first and grepping the variable.
//
// The writer is allowed when it is printf, echo, or `cat <<<`: those print
// a variable that is already in memory, in one write that has finished
// before grep can exit. Everything else, including a grep reading a
// here-string, can still be writing when grep -q leaves.

// grepQ matches a single (not ||) pipe into a grep whose short options
// include q.
var grepQ = regexp.MustCompile(`(?:^|[^|])\|\s*grep((?:\s+-[A-Za-z]+)+)`)

// sigpipeCeiling holds scripts outside the kind sweep that still carry the
// hazard, with the number of sites each had when this guard landed. A file
// may go down, never up; anything not listed must have none.
var sigpipeCeiling = map[string]int{
	// fixed by its own PR (#1899); listed so this guard does not race it
	"live/e2e/corpus-cloud-platform-components/run.sh": 10,
	// #1895 fixed its cold deploy; the crash-recovery reads were out of scope
	"live/e2e/reference-k8s-platform-app/run.sh": 2,
	// floci/AWS estates, not on the kind substrate this sweep covered
	"live/e2e/corpus-autoscaling-complete/run.sh":    1,
	"live/e2e/corpus-ec2-instance-complete/run.sh":   1,
	"live/e2e/corpus-hongbomiao-storage/run.sh":      1,
	"live/e2e/corpus-security-group-complete/run.sh": 1,
	"live/e2e/provisioner-taint/run.sh":              1,
}

// pipeWriter returns the command feeding the pipe that ends prefix: the text
// after the last unquoted separator (|, ;, &, (, {, !) or $(. Single quotes
// hide everything; double quotes hide everything except $(.
func pipeWriter(prefix string) string {
	start := 0
	inS, inD := false, false
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		switch {
		case inS:
			if c == '\'' {
				inS = false
			}
		case c == '\\':
			i++
		case c == '$' && i+1 < len(prefix) && prefix[i+1] == '(':
			start = i + 2
			i++
		case inD:
			if c == '"' {
				inD = false
			}
		case c == '\'':
			inS = true
		case c == '"':
			inD = true
		case strings.IndexByte("|;&({!", c) >= 0:
			start = i + 1
		}
	}
	return strings.TrimSpace(prefix[start:])
}

var safeWriter = regexp.MustCompile(`^(?:printf|echo)(?:\s|$)|^cat\s+<<<`)

// sigpipeSites returns "line: text" for every grep -q fed by a command that
// can still be writing when grep exits. Backslash continuations are joined
// so a pipeline split over lines is read whole. A pipe inside an `sh -c`
// string runs in a child shell without pipefail and is skipped.
func sigpipeSites(src string) []string {
	var out []string
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		n := i + 1
		line := lines[i]
		for strings.HasSuffix(strings.TrimRight(line, " \t"), "\\") && i+1 < len(lines) {
			line = strings.TrimSuffix(strings.TrimRight(line, " \t"), "\\") + " " + lines[i+1]
			i++
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range grepQ.FindAllStringSubmatchIndex(line, -1) {
			opts := line[m[2]:m[3]]
			quiet := false
			for _, o := range strings.Fields(opts) {
				if strings.Contains(o[1:], "q") {
					quiet = true
				}
			}
			if !quiet {
				continue
			}
			pipe := strings.LastIndexByte(line[:m[2]], '|')
			if strings.Contains(line[:pipe], "sh -c ") {
				continue
			}
			if safeWriter.MatchString(pipeWriter(line[:pipe])) {
				continue
			}
			out = append(out, fmt.Sprintf("%d: %s", n, strings.TrimSpace(line)))
		}
	}
	return out
}

// TestNoPipefailScriptFeedsGrepQFromACommand (#1885): no live/e2e run.sh or
// shared library that sets pipefail may pipe a command into grep -q.
func TestNoPipefailScriptFeedsGrepQFromACommand(t *testing.T) {
	checkSigpipe(t, testRoot(t))
}

// checkSigpipe is the guard's body over a checkout rooted at root, split out
// so it can be pointed at a copy of the pre-sweep tree to prove it red.
func checkSigpipe(t *testing.T, root string) {
	t.Helper()
	var paths []string
	for _, pat := range []string{
		filepath.Join(root, "live", "e2e", "run.sh"),
		filepath.Join(root, "live", "e2e", "*", "run.sh"),
		filepath.Join(root, "live", "e2e", "lib", "*.sh"),
	} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, m...)
	}
	sort.Strings(paths)
	scanned := 0
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "pipefail") {
			continue
		}
		scanned++
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		sites := sigpipeSites(string(b))
		if ceiling, ok := sigpipeCeiling[rel]; ok {
			if len(sites) > ceiling {
				t.Errorf("%s: %d grep -q site(s) fed by a command, up from %d; capture the output first:\n\t%s", rel, len(sites), ceiling, strings.Join(sites, "\n\t"))
			}
			continue
		}
		for _, s := range sites {
			t.Errorf("%s:%s\n\tunder pipefail, grep -q exits on its first match and the writer dies of SIGPIPE, failing the pipeline at random (#1885). Capture the output into a variable and grep that with <<<, keeping any negation and printing the captured output on the failure path", rel, s)
		}
	}
	if scanned < 10 {
		t.Fatalf("scanned only %d pipefail scripts under live/e2e; the globs no longer describe the tree and this guard proves nothing", scanned)
	}
}

// TestSigpipeSitesSeesTheShapesItGuards drives the scanner over lines taken
// from the scripts before the #1885 sweep and after it, so the guard is
// proven able to fail as well as pass.
func TestSigpipeSitesSeesTheShapesItGuards(t *testing.T) {
	bad := []string{
		`( stock_b apply -auto-approve -input=false -no-color 2>&1 | grep -qF "Apply complete! Resources: 7 added" ) || fail "stock cold deploy failed on B"`,
		`( cd "$ADOPTED" && "$TOFU" apply -auto-approve -input=false -no-color 2>&1 | grep -qF "0 added, 0 changed, 1 destroyed" ) || fail "x"`,
		"kca get secret -n \"$NS\" -l \"tofu-estate=$ESTATE\" -o name 2>/dev/null | grep -qx \"secret/crash-first\" \\\n  || fail \"x\"",
		`  kca get secrets -n "$ns" -o name 2>/dev/null | grep -q 'secret/dex-client-' && fail "x"`,
		`  grep -E '^[[:space:]]*# .* will be' <<< "$R_PLAN" | grep -q 'crash_first\|crash-first' && return 1`,
		`  kca get statefulset redis -n "$NS" -o jsonpath='{.spec.volumeClaimTemplates[0].metadata.labels.app}' | grep -qx "redis-data" || fail "x"`,
		`X="$(kubectl get pods | grep -qE 'a|b')"`,
	}
	good := []string{
		`{ APPLY_OUT="$(stock_b apply -auto-approve -input=false -no-color 2>&1)" && grep -qF "Apply complete! Resources: 7 added" <<< "$APPLY_OUT"; } || { printf '%s\n' "$APPLY_OUT" | tail -20; fail "x"; }`,
		`printf '%s\n' "$OUT" | grep -qF "x" || fail "y"`,
		`echo "$OUT" | grep -q x`,
		`cat <<< "$OUT" | grep -qx y`,
		`[ "$A" = "deleted" ] || grep -qF 'NotFound' <<< "$A" || fail "x"`,
		`kca get pods | grep -c Running`,
		`kca get pods | grep -E 'x|y' | tail -1`,
		`  sh -c "kubectl get pvc web-cache -o jsonpath='{.status.phase}' | grep -qx Bound" || fail "x"`,
		`# kca get pods | grep -q x`,
	}
	for _, l := range bad {
		if got := sigpipeSites(l); len(got) != 1 {
			t.Errorf("want one site, got %d for:\n\t%s", len(got), l)
		}
	}
	for _, l := range good {
		if got := sigpipeSites(l); len(got) != 0 {
			t.Errorf("want no site, got %v for:\n\t%s", got, l)
		}
	}
}
