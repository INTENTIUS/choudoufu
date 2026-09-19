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
	"strings"
	"testing"
	"time"
)

// examples/ci-pipelines/scripts/selftest-oidc-bootstrap.sh is what measures
// the three IAM policies oidc-bootstrap.sh writes for the plan, adopt and
// apply roles: it stubs `aws` and `gh` on PATH, runs the bootstrap under
// --dry-run, and reads the policy documents it composed. Nothing invoked it.
//
// That is the shape #1267 closed for live/live-cert/'s selftests and #1378
// closed for the smoke teardowns, in the words #691 gave the class: a test
// tier that exists but gates nothing. GitHub issue #1370 added the
// assertions that the plan and adopt roles carry the renderer's --read-only
// output and no write to the bucket, and those assertions are only a guard
// if something runs them.
//
// Nothing here reaches AWS or GitHub. The stubs answer every read the
// bootstrap makes and refuse every write, and --dry-run prints the writes
// rather than making them; a stub that is reached by a call it does not
// expect exits non-zero and reddens this test. The script needs bash, jq and
// the repository, all of which this package already needs for the IAM
// renderer tests, so there is deliberately no t.Skip: a skip here would
// leave the guard permanently green on the machine that was missing one.
const oidcBootstrapSelftest = "../examples/ci-pipelines/scripts/selftest-oidc-bootstrap.sh"

// oidcBootstrapSelftestBound is how long the selftest gets before it is
// killed and this test fails. Measured at 1.6s on a 2026-09-19 laptop: it
// runs the bootstrap a dozen times over stubs and waits on nothing, so a run
// near this bound is a hang rather than a slow machine.
const oidcBootstrapSelftestBound = 120 * time.Second

func TestOIDCBootstrapSelftestPasses(t *testing.T) {
	if _, err := os.Stat(oidcBootstrapSelftest); err != nil {
		t.Fatalf("%s is missing: %v\nIt is the only thing that measures the three role policies "+
			"oidc-bootstrap.sh writes; if it was renamed, rename it here too rather than dropping the wiring.",
			oidcBootstrapSelftest, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), oidcBootstrapSelftestBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", oidcBootstrapSelftest)
	cmd.WaitDelay = 5 * time.Second
	// The stubs go on PATH inside the script itself. What is cleared here is
	// anything that could make the bootstrap authenticate for real if a stub
	// were ever bypassed.
	cmd.Env = append(os.Environ(), "AWS_PROFILE=", "AWS_ACCESS_KEY_ID=", "AWS_SECRET_ACCESS_KEY=", "AWS_SESSION_TOKEN=")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := out.String()
	if ctx.Err() != nil {
		t.Fatalf("%s did not finish within %s and was killed. It stubs every command it runs and waits on "+
			"nothing.\nIts output up to the kill:\n%s", oidcBootstrapSelftest, oidcBootstrapSelftestBound, text)
	}
	if err != nil {
		t.Fatalf("%s failed (%v). Its own output says which assertion:\n%s", oidcBootstrapSelftest, err, text)
	}

	// The verdict is the script's own lines, not its exit code. Its EXIT trap
	// re-raises a saved status and it prints a FAIL line of its own when it
	// stops before its last case, so both are required to agree.
	if strings.Contains(text, "FAIL") {
		t.Errorf("%s exited 0 while printing a FAIL line:\n%s", oidcBootstrapSelftest, text)
	}
	if !strings.Contains(text, "PASS: ") {
		t.Errorf("%s exited 0 without printing its PASS line, so nothing here shows it measured anything:\n%s",
			oidcBootstrapSelftest, text)
	}
	// Named cases, so a selftest whose body was gutted down to the trust
	// policy cannot pass this by printing PASS over nothing. These two are
	// the record store halves: #1346's (the apply role's policy is the
	// renderer's) and #1370's (the plan and adopt roles' is its --read-only
	// output).
	for _, want := range []string{
		"case: each role's record store policy is the renderer's",
		"record store write actions allowed",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s printed no line containing %q, so the record store cases did not run:\n%s",
				oidcBootstrapSelftest, want, text)
		}
	}
}
