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

// ── issue #1303: a live/e2e destroy step only ever points at the emulator ──
//
// live/live-cert/lib/live-cert.sh used to justify its teardown obligation by
// saying no live/e2e/*/run.sh had a real destroy step, with a grep recipe
// that "returns nothing". By #1303 it returned six files, five of them
// running a real `tofu destroy` / `terraform destroy` (oracle counts in four
// corpus crossings, and terralith-scale's GREEN destroy-then-assert-empty).
//
// The distinction that still holds, and the one live-cert's teardown rests
// on, is narrower: a live/e2e script may destroy, but only against the
// pinned emulator. Every endpoint it can hand a destroy is a loopback floci
// port, so `docker rm -f` remains the backstop. A live-cert script is the
// one that can point at a real account, and that is why it alone carries
// livecert_teardown. This guard holds that distinction instead of the
// parenthetical grep recipe that used to stand in for it.
//
// WHAT IT CHECKS. For every live/e2e/*/run.sh with a non-comment `tofu
// destroy` or `terraform destroy` line: every *ENDPOINT* variable assignment
// in the script is a loopback emulator URL (127.0.0.1, localhost, or
// localhost.floci.io) or a copy of another such variable, and at least one
// loopback assignment exists.
//
// WHAT IT DOES NOT CHECK. A destroy whose endpoint comes from the provider
// block of the directory it runs in, or from the environment the caller
// started the script with, is outside a line-level read of the script. This
// guard pins what the script itself sets.

// e2eDestroyLine matches a non-comment line that invokes a real destroy.
var e2eDestroyLine = regexp.MustCompile(`^[^#]*\b(tofu|terraform) destroy\b`)

// e2eEndpointAssign matches an assignment to a variable whose name contains
// ENDPOINT, optionally exported, capturing the assigned value.
var e2eEndpointAssign = regexp.MustCompile(`^\s*(?:export\s+)?([A-Z_]*ENDPOINT[A-Z_]*)=("[^"]*"|\S*)`)

// e2eLoopbackValue is a value that can only reach the local emulator: a
// loopback floci URL, or a copy of another *ENDPOINT* variable (which this
// same check holds to the same rule).
var e2eLoopbackValue = regexp.MustCompile(`^"?(?:http://(?:127\.0\.0\.1|localhost|localhost\.floci\.io)[:/"]|\$\{?[A-Z_]*ENDPOINT[A-Z_]*\}?"?$)`)

// e2eDestroyEndpointViolations reads one run.sh's text and reports whether
// it destroys, and every endpoint assignment that is not loopback.
func e2eDestroyEndpointViolations(script string) (destroys bool, violations []string) {
	loopback := 0
	for i, line := range strings.Split(script, "\n") {
		if e2eDestroyLine.MatchString(line) {
			destroys = true
		}
		m := e2eEndpointAssign.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if e2eLoopbackValue.MatchString(m[2]) {
			if strings.Contains(m[2], "http://") {
				loopback++
			}
			continue
		}
		violations = append(violations, fmt.Sprintf("line %d: %s=%s is not a loopback emulator endpoint", i+1, m[1], m[2]))
	}
	if destroys && loopback == 0 {
		violations = append(violations, "destroys, but sets no loopback emulator endpoint at all")
	}
	if !destroys {
		return false, nil
	}
	return true, violations
}

// e2eDestroyFloor is the population #1303 measured on f31378d5fa: five
// live/e2e scripts with a real destroy step. A count below it means the
// matcher went blind, not that the scripts stopped destroying.
const e2eDestroyFloor = 5

func TestE2EDestroyStepsTargetOnlyTheEmulator(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("e2e", "*", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no live/e2e/*/run.sh found: the population is empty, so this guard would pass vacuously")
	}
	sort.Strings(paths)
	var destroying []string
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		destroys, violations := e2eDestroyEndpointViolations(string(data))
		if !destroys {
			continue
		}
		destroying = append(destroying, filepath.ToSlash(p))
		for _, v := range violations {
			t.Errorf("live/%s: %s", filepath.ToSlash(p), v)
		}
	}
	if len(destroying) < e2eDestroyFloor {
		t.Errorf("found %d live/e2e scripts with a destroy step, want at least %d (#1303 measured five): %v",
			len(destroying), e2eDestroyFloor, destroying)
	}
	t.Logf("%d of %d live/e2e scripts destroy, all against a loopback emulator endpoint: %v",
		len(destroying), len(paths), destroying)
}

// TestE2EDestroyEndpointCheckCanFail plants the shapes the guard exists to
// refuse, so its green on the real tree means something.
func TestE2EDestroyEndpointCheckCanFail(t *testing.T) {
	cases := []struct {
		name         string
		script       string
		wantDestroys bool
		wantRed      bool
	}{
		{
			name:         "loopback destroy is clean",
			script:       "ENDPOINT=\"http://127.0.0.1:${FLOCI_PORT}\"\nexport AWS_ENDPOINT_URL=\"$ENDPOINT\"\nterraform destroy -auto-approve\n",
			wantDestroys: true,
		},
		{
			name:         "floci hostname destroy is clean",
			script:       "GREEN_ENDPOINT=\"http://localhost.floci.io:${P}\"\nX=$(AWS_ENDPOINT_URL=\"$GREEN_ENDPOINT\" tofu destroy)\n",
			wantDestroys: true,
		},
		{
			name:         "real AWS endpoint is red",
			script:       "ENDPOINT=\"https://ec2.us-east-1.amazonaws.com\"\nterraform destroy -auto-approve\n",
			wantDestroys: true,
			wantRed:      true,
		},
		{
			name:         "destroy with no emulator endpoint is red",
			script:       "terraform destroy -auto-approve\n",
			wantDestroys: true,
			wantRed:      true,
		},
		{
			name:   "a commented destroy is not a destroy",
			script: "# the \"terraform destroy\" alias sets a different view flag\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			destroys, violations := e2eDestroyEndpointViolations(c.script)
			if destroys != c.wantDestroys {
				t.Errorf("destroys = %v, want %v", destroys, c.wantDestroys)
			}
			if red := len(violations) > 0; red != c.wantRed {
				t.Errorf("red = %v (%v), want %v", red, violations, c.wantRed)
			}
		})
	}
}
