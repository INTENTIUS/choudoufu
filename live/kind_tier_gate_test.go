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

// Issue #1596, the Kubernetes-side instance of the #591/#623/#691 class:
// every CHOUDOUFU_K8S_*-gated test read its own environment variable
// directly and skipped, so nothing said whether the tier ran at all -
// internal/live/projection/fenced_live_test.go:135's
// TestFencedIdentityOnACluster in particular was invoked by nothing, on any
// schedule, anywhere. internal/live/k8stest gives every such test a shared
// switch (k8stest.Gate, beside internal/live/flocitest.Gate for the AWS
// side); this file is what live/floci_tier_gate_test.go is for that switch.
//
// TestFencedIdentityOnACluster is checked first and unconditionally, so a
// tree with no kind-tier.yml at all - today's tree - fails naming exactly
// that test rather than on a derivation error further down.
//
// The rest of the roster is derived, never hand-listed: every package
// containing a k8stest.Gate call site. live/nightly_watch_test.go
// separately holds nightly-watch.yml to every workflow file that carries a
// cron, kind-tier.yml included, so a red kind-tier night cannot go
// unnoticed the way seventeen red floci-tier nights did (#1316).
func TestKindTierCoversEveryGatedPackage(t *testing.T) {
	root := repoRoot(t)

	wfPath := filepath.Join(root, ".github", "workflows", "kind-tier.yml")
	wfBytes, readErr := os.ReadFile(wfPath)
	wfs := string(wfBytes)

	if !strings.Contains(wfs, "TestFencedIdentityOnACluster") {
		t.Errorf("no workflow names TestFencedIdentityOnACluster (reading %s: %v); issue #1596 exists because that test - internal/live/projection/fenced_live_test.go - is invoked by nothing, on any schedule, anywhere", wfPath, readErr)
	}
	if readErr != nil {
		t.Fatalf(".github/workflows/kind-tier.yml is missing (%v); the Kubernetes live tier gates nothing without a workflow that sets k8stest.EnvVar and runs it (issue #1596)", readErr)
	}

	if !strings.Contains(wfs, "schedule:") && !strings.Contains(wfs, "cron:") {
		t.Error("kind-tier.yml has no schedule/cron; a workflow that only runs on workflow_dispatch still depends on a human remembering to type it")
	}
	if !strings.Contains(wfs, "go test") {
		t.Error("kind-tier.yml never runs `go test`; it does not appear to run the gated roster at all")
	}
	if !strings.Contains(wfs, "k8stest.EnvVar") && !strings.Contains(wfs, "CHOUDOUFU_K8S_TEST") {
		t.Error("kind-tier.yml never sets CHOUDOUFU_K8S_TEST (k8stest.EnvVar); without it every k8stest.Gate call skips and the workflow measures nothing")
	}

	// The pattern requires the call's open paren, so this file - whose prose
	// names k8stest.Gate without calling it - cannot match itself. Built the
	// same way live/floci_tier_gate_test.go builds its own pattern: the
	// runtime string is a single backslash before the dot, but the source
	// that produces it needs two, so this file's own bytes never contain the
	// literal sequence being searched for.
	out, err := exec.Command("git", "-C", root, "grep", "-l", "k8stest\\.Gate(", "--", "*_test.go").Output()
	if err != nil {
		// git grep exits 1 (not an execution failure) when it simply finds no
		// match, which is itself a finding worth failing on below (via the
		// pkgs-count check) rather than treating as a broken command.
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			t.Fatalf("enumerating k8stest.Gate call sites: %v", err)
		}
	}
	pkgs := map[string]bool{}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f == "" {
			continue
		}
		pkgs[filepath.Dir(f)] = true
	}
	if len(pkgs) < 2 {
		t.Fatalf("found only %d packages calling k8stest.Gate; want at least internal/live/staterecord and internal/live/projection (issue #1596 asks that every CHOUDOUFU_K8S_*-gated test use it)", len(pkgs))
	}

	for pkg := range pkgs {
		// A package appears in the workflow either as an explicit `./pkg/...`
		// test target or as the bare import path inside a wider `go test`
		// invocation (e.g. `./internal/live/...`); either way its path string,
		// or a parent directory's `.../...` pattern, must appear in the file.
		if strings.Contains(wfs, pkg) {
			continue
		}
		shortened := pkg
		found := false
		for shortened != "." && shortened != string(filepath.Separator) {
			shortened = filepath.Dir(shortened)
			if strings.Contains(wfs, "./"+filepath.ToSlash(shortened)+"/...") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("package %s calls k8stest.Gate, but kind-tier.yml names neither it nor a parent %s/... pattern; this package's gated tests can never run in the tier", pkg, pkg)
		}
	}
}
