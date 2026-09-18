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
	"time"
)

// k8swaitall_test.go: issue #1285's guard, at the library layer, over
// live/e2e/lib/gauntlet.sh's gauntlet_k8s_wait_all.
//
// The defect it replaces, measured on kind with kubectl v1.36.1 before the
// fix landed:
//
//	$ kubectl wait --for=condition=Available --timeout=300s deployment --all -n empty-ns
//	error: no matching resources found
//	rc=1 after 0s
//
// reference-k8s-cert-manager's cert_manager_ready read that exit code as
// "the cert-manager Deployments did not become Available within 300s". An
// immediate empty match and a three-hundred-second wait are different
// problems - the first is nearly always a setup bug - and a reader sent to
// the second one loses the time this estate exists to save.
//
// The arms here are driven through a kubectl STUB rather than a cluster, so
// they run in CI with no Docker: what is under test is the shell function's
// branching on what kubectl says, and each arm pins one of kubectl's real,
// separately measured answers. The same function was also driven against a
// real kind cluster by hand (see the pull request for #1285) - that is where
// the stub's canned answers come from, not from reading the implementation.
//
// Both timing arms use a two-second bound so the suite does not pay a real
// timeout; the arm is run rather than asserted, because a fix that only ever
// proves its new message has swapped one untested message for two.

// kubectlStub writes an executable fake kubectl into dir and returns a PATH
// with dir first. The stub answers three subcommands:
//
//	get <kind> -n <ns> -o name   -> the lines in names (empty for an empty set)
//	get namespace <ns>           -> exit 0, or 1 when nsMissing
//	wait ...                     -> waitBehavior: "ok", "timeout", "fastfail"
//
// It writes nothing else, so an arm that reached a branch it should not have
// shows up as a missing or surprising line rather than as silence.
func kubectlStub(t *testing.T, names string, nsMissing bool, waitBehavior string) string {
	t.Helper()
	dir := t.TempDir()
	missing := "0"
	if nsMissing {
		missing = "1"
	}
	// NAMES arrives through a quoted heredoc, not through %q: Go's %q writes
	// a \n escape, and bash does not expand escapes inside double quotes, so
	// a two-object set would arrive as one line with a literal backslash-n -
	// and the count in the message under test would be wrong for a reason
	// that has nothing to do with the library. (Caught by running it.)
	script := fmt.Sprintf(`#!/usr/bin/env bash
# fake kubectl for TestGauntletK8sWaitAllSeparatesAnEmptyMatchFromATimeout
NAMES=$(cat <<'STUB_NAMES_EOF'
%s
STUB_NAMES_EOF
)
NS_MISSING=%q
BEHAVIOR=%q
args=("$@")
# strip the --kubeconfig <path> the library always passes
if [ "${args[0]}" = "--kubeconfig" ]; then args=("${args[@]:2}"); fi
case "${args[0]}" in
  get)
    case "${args[1]}" in
      namespace|namespaces)
        [ "$NS_MISSING" = "1" ] && { echo 'Error from server (NotFound): namespaces "x" not found' >&2; exit 1; }
        echo "namespace/x"; exit 0 ;;
      all)
        echo "No resources found in x namespace." >&2; exit 0 ;;
      pods)
        echo "NAME READY STATUS"; exit 0 ;;
    esac
    [ -n "$NAMES" ] && printf '%%s\n' "$NAMES"
    exit 0 ;;
  wait)
    secs=2
    for a in "${args[@]}"; do case "$a" in --timeout=*) secs="${a#--timeout=}"; secs="${secs%%s}" ;; esac; done
    case "$BEHAVIOR" in
      ok)       echo "deployment.apps/a condition met"; exit 0 ;;
      timeout)  sleep "$secs"
                first="$(printf '%%s' "$NAMES" | head -1)"
                echo "error: timed out waiting for the condition on deployments/${first##*/}" >&2; exit 1 ;;
      fastfail) echo "error: Unable to connect to the server: dial tcp 127.0.0.1:6443: connect: connection refused" >&2; exit 1 ;;
    esac
    exit 9 ;;
esac
echo "fake kubectl: unexpected call: $*" >&2
exit 97
`, names, missing, waitBehavior)
	p := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// callWaitAll sources the real library and calls the real function, with the
// stub's directory first on PATH.
func callWaitAll(t *testing.T, path, ns, timeout string) (out string, rc int, elapsed time.Duration) {
	t.Helper()
	root := testRoot(t)
	lib := filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh")
	if _, err := os.Stat(lib); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`set -euo pipefail
export PATH=%q
source %q
gauntlet_k8s_wait_all /dev/null %q deployment Available %s "cluster A" 2>&1 && echo "WAITALL_RC=0" || echo "WAITALL_RC=$?"
`, path, lib, ns, timeout)
	start := time.Now()
	b, _ := runBash(script)
	elapsed = time.Since(start)
	out = string(b)
	rc = 0
	if !strings.Contains(out, "WAITALL_RC=0") {
		rc = 1
	}
	return out, rc, elapsed
}

func TestGauntletK8sWaitAllSeparatesAnEmptyMatchFromATimeout(t *testing.T) {
	// Arm 1: the namespace exists and holds no Deployments. kubectl's real
	// answer here is `error: no matching resources found` in ~0.05s, which
	// is what the old call site called a 300-second timeout.
	t.Run("empty match is its own failure, fast, and does not claim a timeout", func(t *testing.T) {
		out, rc, elapsed := callWaitAll(t, kubectlStub(t, "", false, "timeout"), "cert-manager", "300")
		if rc == 0 {
			t.Fatalf("an empty set must fail; got success\n%s", out)
		}
		if !strings.Contains(out, "no deployment objects exist in namespace cert-manager on cluster A") {
			t.Errorf("the empty-match message must name the kind, the namespace and where; got:\n%s", out)
		}
		if !strings.Contains(out, "NOT a 300s timeout") {
			t.Errorf("the empty-match message must say it is not a timeout; got:\n%s", out)
		}
		if strings.Contains(out, "TIMEOUT") {
			t.Errorf("an empty match must not be reported as a timeout; got:\n%s", out)
		}
		// Fast: the bound is 300s and the stub's wait leg would sleep it.
		// Anything slow here means the empty set reached the wait.
		if elapsed > 20*time.Second {
			t.Errorf("the empty match took %s; it must not wait", elapsed)
		}
	})

	// The namespace itself being absent looks identical to the caller -
	// `kubectl get deployment -n no-such-ns -o name` is rc=0 with no output
	// and nothing on stderr (measured) - so it gets its own sentence.
	t.Run("a missing namespace says so rather than blaming the objects", func(t *testing.T) {
		out, rc, _ := callWaitAll(t, kubectlStub(t, "", true, "timeout"), "typo-ns", "300")
		if rc == 0 {
			t.Fatalf("a missing namespace must fail; got success\n%s", out)
		}
		if !strings.Contains(out, "namespace typo-ns does not exist on cluster A") {
			t.Errorf("want the missing-namespace message; got:\n%s", out)
		}
		if strings.Contains(out, "TIMEOUT") {
			t.Errorf("a missing namespace must not be reported as a timeout; got:\n%s", out)
		}
	})

	// Arm 2: the objects are there and never reach the condition. The old
	// message is the right one here, and it has to keep working - this is the
	// arm a fix that only proves its new branch silently drops.
	t.Run("a real timeout keeps the timeout message and reports the elapsed time", func(t *testing.T) {
		out, rc, elapsed := callWaitAll(t, kubectlStub(t, "deployment.apps/never-ready", false, "timeout"), "cert-manager", "2")
		if rc == 0 {
			t.Fatalf("a never-ready Deployment must fail; got success\n%s", out)
		}
		if !strings.Contains(out, "TIMEOUT") {
			t.Errorf("a genuine timeout must still say so; got:\n%s", out)
		}
		if !strings.Contains(out, "did not all reach Available within 2s") {
			t.Errorf("the timeout message must name the condition and the bound; got:\n%s", out)
		}
		if !strings.Contains(out, "(waited 2s)") && !strings.Contains(out, "(waited 3s)") {
			t.Errorf("the timeout message must report the real elapsed time; got:\n%s", out)
		}
		if !strings.Contains(out, "deployment.apps/never-ready") {
			t.Errorf("the timeout message must name what it waited on; got:\n%s", out)
		}
		if !strings.Contains(out, "timed out waiting for the condition on deployments/never-ready") {
			t.Errorf("kubectl's own words must be quoted, not dropped into /dev/null; got:\n%s", out)
		}
		if elapsed < 2*time.Second {
			t.Errorf("the wait returned in %s, so the timeout arm never actually waited", elapsed)
		}
	})

	// Arm 3: kubectl fails inside the bound for a reason that is not the
	// bound. Calling that a timeout is the same defect as arm 1.
	t.Run("a failure before the bound is not called a timeout", func(t *testing.T) {
		out, rc, _ := callWaitAll(t, kubectlStub(t, "deployment.apps/a", false, "fastfail"), "cert-manager", "300")
		if rc == 0 {
			t.Fatalf("a failing wait must fail; got success\n%s", out)
		}
		if strings.Contains(out, "TIMEOUT") {
			t.Errorf("a connection error 0s into a 300s bound is not a timeout; got:\n%s", out)
		}
		if !strings.Contains(out, "before the 300s bound") {
			t.Errorf("want the not-a-timeout message; got:\n%s", out)
		}
		if !strings.Contains(out, "connection refused") {
			t.Errorf("kubectl's own words must be quoted; got:\n%s", out)
		}
	})

	// The success path, so none of the above is passing because the function
	// fails for everything.
	t.Run("the success path reports how long it took and returns 0", func(t *testing.T) {
		out, rc, _ := callWaitAll(t, kubectlStub(t, "deployment.apps/a\ndeployment.apps/b", false, "ok"), "cert-manager", "300")
		if rc != 0 {
			t.Fatalf("want success; got:\n%s", out)
		}
		if !strings.Contains(out, "all 2 deployment in namespace cert-manager on cluster A reached Available") {
			t.Errorf("the ready line must count what it waited on; got:\n%s", out)
		}
	})
}

// TestCertManagerReadyDoesNotWaitOnABareAll pins the call site: #1285's
// defect was one `kubectl wait --all` whose failure branch assumed the match
// was non-empty, and the fix is worth nothing if a later edit puts it back.
func TestCertManagerReadyDoesNotWaitOnABareAll(t *testing.T) {
	root := testRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "live", "e2e", "reference-k8s-cert-manager", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for i, ln := range strings.Split(s, "\n") {
		code := strings.TrimSpace(ln)
		if strings.HasPrefix(code, "#") {
			continue
		}
		if strings.Contains(code, "kubectl") && strings.Contains(code, "wait ") && strings.Contains(code, "--all") {
			t.Errorf("live/e2e/reference-k8s-cert-manager/run.sh:%d waits on a bare --all selector, whose empty match kubectl answers in 0.05s with `error: no matching resources found` - call gauntlet_k8s_wait_all so that is not reported as a timeout (#1285):\n\t%s", i+1, code)
		}
	}
	if !strings.Contains(s, "gauntlet_k8s_wait_all") {
		t.Error("reference-k8s-cert-manager no longer calls gauntlet_k8s_wait_all; #1285's empty-match confusion is back unless something else distinguishes it")
	}
}
