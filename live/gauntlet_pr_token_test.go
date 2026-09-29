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
// a stubbed gh AND a stubbed git.
//
// The case that matters most is "authenticates, push refused". The first
// version of the script decided usable from the token's LOGIN's permission
// on the repository, a login with admin answered "admin", and runs
// 35684452776 and 35705549497 (2026-09-22) were refused on the push anyway.
// That case could not fail under the old check; here it has to.
func TestGauntletPRTokenCheckNeverBlessesATokenThatCannotPush(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "gauntlet-pr-token-check.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("%s: %v", script, err)
	}

	const secret = "github_pat_SECRETVALUE0123456789"
	cases := []struct {
		name       string
		pat        string
		userOK     bool
		pushOK     bool
		wantUsable string
		wantReason string
		wantProbe  bool
	}{
		{"unset", "", false, false, "false", "not set", false},
		{"does not authenticate", secret, false, false, "false", "does not authenticate", false},
		{"authenticates, push refused (the 2026-09-22 case)", secret, true, false, "false", "refuses it a push", true},
		{"authenticates, push authorized", secret, true, true, "true", "authorizes a push", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gitLog := filepath.Join(dir, "git.log")
			ghStub := `#!/usr/bin/env bash
case "$1 $2" in
  "api user")
    if [ "$STUB_USER_OK" = 1 ]; then echo lex00; else echo "HTTP 401: Bad credentials" >&2; exit 1; fi ;;
  *) echo "unexpected gh call: $*" >&2; exit 2 ;;
esac
`
			// The refusal text is GitHub's own, with the token echoed back
			// in it, so the redaction is exercised too.
			gitStub := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GIT_LOG"
if [ "$STUB_PUSH_OK" = 1 ]; then exit 0; fi
echo "remote: Permission to INTENTIUS/choudoufu.git denied to lex00." >&2
echo "fatal: unable to access 'https://x-access-token:$PAT_ECHO@github.com/INTENTIUS/choudoufu.git/': The requested URL returned error: 403" >&2
exit 128
`
			for name, body := range map[string]string{"gh": ghStub, "git": gitStub} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil { //nolint:gosec // a test stub
					t.Fatal(err)
				}
			}
			outFile := filepath.Join(dir, "output")
			flag := func(b bool) string {
				if b {
					return "1"
				}
				return "0"
			}
			cmd := exec.Command("bash", script)
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"PAT="+tc.pat,
				"PAT_ECHO="+tc.pat,
				"STUB_USER_OK="+flag(tc.userOK),
				"STUB_PUSH_OK="+flag(tc.pushOK),
				"GIT_LOG="+gitLog,
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
			if tc.pat != "" && (strings.Contains(string(got), tc.pat) || strings.Contains(string(out), tc.pat)) {
				t.Errorf("the token's value reached the outputs or the log:\n%s\n%s", got, out)
			}

			calls, _ := os.ReadFile(gitLog)
			if !tc.wantProbe {
				if len(calls) != 0 {
					t.Errorf("git was run although the check could stop earlier: %s", calls)
				}
				return
			}
			c := string(calls)
			for _, want := range []string{"push --dry-run", "-c http.https://github.com/.extraheader= ", "-c credential.helper= ", "refs/heads/gauntlet/token-probe"} {
				if !strings.Contains(c, want) {
					t.Errorf("the probe must be a dry-run push that resets the checkout's auth header and credential helper; git was called as:\n%s\nmissing %q", c, want)
				}
			}
			if strings.Count(c, "push") != strings.Count(c, "push --dry-run") {
				t.Errorf("a push that was not a dry run: %s", c)
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
