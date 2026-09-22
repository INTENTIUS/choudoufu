// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #496: scripts/gauntlet-pr-token-check.sh decides which token the
// nightly's PR step uses. The one thing it must never do is say usable=true
// for a token the push would refuse, so every branch is driven here against
// a stubbed gh: unset, not authenticating, authenticating but unable to
// read the repository (the wrong-resource-owner PAT that cost nine nights),
// read-only, and each of the three permissions that can push.
func TestGauntletPRTokenCheckNeverBlessesATokenThatCannotPush(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "gauntlet-pr-token-check.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("%s: %v", script, err)
	}

	cases := []struct {
		name       string
		pat        string
		userOK     bool
		perm       string // "" makes the permission read fail with 404
		wantUsable string
		wantReason string
	}{
		{"unset", "", false, "", "false", "not set"},
		{"does not authenticate", "ghp_bad", false, "", "false", "does not authenticate"},
		{"wrong resource owner", "github_pat_user_owned", true, "", "false", "cannot read INTENTIUS/choudoufu"},
		{"read only", "github_pat_read", true, "read", "false", "needs write"},
		{"write", "github_pat_write", true, "write", "true", "with 'write'"},
		{"maintain", "github_pat_maintain", true, "maintain", "true", "with 'maintain'"},
		{"admin", "github_pat_admin", true, "admin", "true", "with 'admin'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			stub := filepath.Join(dir, "gh")
			body := `#!/usr/bin/env bash
case "$1 $2" in
  "api user")
    if [ "$STUB_USER_OK" = 1 ]; then echo lex00; else echo "HTTP 401: Bad credentials" >&2; exit 1; fi ;;
  "api repos/INTENTIUS/choudoufu/collaborators/lex00/permission")
    if [ -n "$STUB_PERM" ]; then echo "$STUB_PERM"; else echo "HTTP 404: Not Found" >&2; exit 1; fi ;;
  *) echo "unexpected gh call: $*" >&2; exit 2 ;;
esac
`
			if err := os.WriteFile(stub, []byte(body), 0o755); err != nil { //nolint:gosec // a test stub
				t.Fatal(err)
			}
			outFile := filepath.Join(dir, "output")
			userOK := "0"
			if tc.userOK {
				userOK = "1"
			}
			cmd := exec.Command("bash", script)
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"PAT="+tc.pat,
				"STUB_USER_OK="+userOK,
				"STUB_PERM="+tc.perm,
				"GITHUB_OUTPUT="+outFile,
				"GITHUB_REPOSITORY=INTENTIUS/choudoufu",
				"GITHUB_STEP_SUMMARY=",
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the check must always exit 0 (an unusable token is routed around, not fatal); got %v:\n%s", err, out)
			}
			got, rerr := os.ReadFile(outFile)
			if rerr != nil {
				t.Fatalf("no GITHUB_OUTPUT written: %v\n%s", rerr, out)
			}
			if !strings.Contains(string(got), "usable="+tc.wantUsable+"\n") {
				t.Errorf("want usable=%s, got outputs:\n%s\nstdout:\n%s", tc.wantUsable, got, out)
			}
			if !strings.Contains(string(got), tc.wantReason) {
				t.Errorf("want the reason to mention %q, got outputs:\n%s", tc.wantReason, got)
			}
			if tc.wantUsable == "false" && !strings.Contains(string(out), "::warning") {
				t.Errorf("an unusable token must annotate the run with a warning; stdout:\n%s", out)
			}
		})
	}
}

// TestGauntletWorkflowSelectsItsTokenFromTheCheck holds gauntlet.yml to the
// wiring: the check runs, and the PR step's token is chosen by its output
// rather than by whether the secret merely exists (which is how a
// mis-issued secret was preferred over a working fallback for nine nights).
func TestGauntletWorkflowSelectsItsTokenFromTheCheck(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "gauntlet.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wf := string(b)
	for _, want := range []string{
		"run: scripts/gauntlet-pr-token-check.sh",
		"token: ${{ steps.prtoken.outputs.usable == 'true' && secrets.GAUNTLET_PR_TOKEN || secrets.GITHUB_TOKEN }}",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("gauntlet.yml lacks %q", want)
		}
	}
	if strings.Contains(wf, "token: ${{ secrets.GAUNTLET_PR_TOKEN || secrets.GITHUB_TOKEN }}") {
		t.Error("gauntlet.yml still prefers GAUNTLET_PR_TOKEN whenever it is set, which is the shape that discarded nine nights of verdicts (#496)")
	}
}
