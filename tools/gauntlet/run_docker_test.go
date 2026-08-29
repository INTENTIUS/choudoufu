// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestParallelRunLeavesNoContainers is issue #437's leak check, run for
// real against Docker rather than eyeballed: two fixture "estate" scripts
// each start one real container named exactly the way every
// live/e2e/*/run.sh names its floci container - choudoufu-<estate>-$$, its
// own process ID - and tear it down themselves via an EXIT trap, which is
// the same discipline every real crossing script already relies on. It
// asserts, for both a serial run and a -parallel run of the same fixtures:
//
//  1. no container matching the runner's naming convention is left running
//     afterwards (a fresh `docker ps -a` snapshot, diffed against the one
//     taken before the run, per the issue's Accept criteria - not a manual
//     eyeball);
//  2. floci-ecr-registry, the shared persistent registry container other
//     estates and other runs depend on, is untouched: same container ID,
//     same running status, before and after.
//
// Needs Docker, so it is gated the same way every other Docker-dependent
// tier in this repo already is (TF_FLOCI_TEST=1, see Makefile's
// test-floci): `just ci` has no Docker and stays green; this only runs when
// asked, e.g. `TF_FLOCI_TEST=1 go test ./tools/gauntlet/ -run
// TestParallelRunLeavesNoContainers -v`.
func TestParallelRunLeavesNoContainers(t *testing.T) {
	if os.Getenv("TF_FLOCI_TEST") == "" {
		t.Skip("set TF_FLOCI_TEST=1 to run this Docker-dependent check")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker not running")
	}
	// A locally available image, so the check does not depend on network
	// access to a registry; nginx is already pulled in every dev/CI image
	// this repo's e2e tooling has been run on. Any small long-enough-lived
	// image would do - this test is exercising container lifecycle and
	// naming, not floci itself (that is what the real gauntlet equivalence
	// run proves separately).
	image := "nginx:latest"
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Skipf("image %s not available locally and this check does not pull over the network", image)
	}

	registryBefore, haveRegistry := inspectRegistry(t)

	root := t.TempDir()
	const prefix = "choudoufu-leak-"
	names := []string{"leaka", "leakb"}
	for _, name := range names {
		script := fmt.Sprintf(`NAME="%s%s-$$"
trap 'docker rm -f "$NAME" >/dev/null 2>&1 || true' EXIT
printf 'GAUNTLET protocol=1\n'
docker run -d --rm -p "${FLOCI_PORT:-28080}:80" --name "$NAME" %s >/dev/null
sleep 0.3
printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=0\n'
`, prefix, name, image)
		writeFakeEstate(t, root, name, script)
	}
	m := &Manifest{}
	for _, name := range names {
		m.Estates = append(m.Estates, Estate{Name: name, Source: "s", Lane: "reference", Set: SetGrowing})
	}

	check := func(label string, parallel int) {
		before := dockerPSNames(t)
		a := &Artifact{Schema: 1}
		var out bytes.Buffer
		failures, err := RunEstates(root, m, a, RunOptions{Names: names, Parallel: parallel, Stdout: &out}, "c", "e")
		if err != nil {
			t.Fatalf("%s: RunEstates: %v\n%s", label, err, out.String())
		}
		if failures != 0 {
			t.Fatalf("%s: %d estate(s) failed\n%s", label, failures, out.String())
		}
		for _, name := range names {
			r, _ := a.Result(name)
			if r.Stages["cold_deploy"] != VerdictPass {
				t.Errorf("%s: %s: cold_deploy = %q, want pass", label, name, r.Stages["cold_deploy"])
			}
		}

		after := dockerPSNames(t)
		var leaked []string
		for n := range after {
			if before[n] {
				continue
			}
			if strings.HasPrefix(n, prefix) {
				leaked = append(leaked, n)
			}
		}
		if len(leaked) > 0 {
			t.Errorf("%s: %d container(s) leaked (present after the run, matching %q, absent before): %v", label, len(leaked), prefix, leaked)
		}
	}

	check("serial", 1)
	check("parallel-2", 2)

	if haveRegistry {
		registryAfter, stillHave := inspectRegistry(t)
		if !stillHave {
			t.Fatal("floci-ecr-registry existed before this test and is gone afterwards - the shared registry must never be touched")
		}
		if registryAfter != registryBefore {
			t.Errorf("floci-ecr-registry changed: before %q, after %q - the shared registry must never be touched", registryBefore, registryAfter)
		}
	}
}

// dockerPSNames returns every container name docker currently knows about
// (running or not), so a leak check can diff a before/after pair rather
// than assume the set was empty to start with - other estates' and other
// runs' containers may legitimately be present throughout.
func dockerPSNames(t *testing.T) map[string]bool {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	names := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			names[line] = true
		}
	}
	return names
}

// inspectRegistry reports floci-ecr-registry's container ID and running
// status as one comparable string, and whether that container exists at
// all in this environment (it is shared, persistent infrastructure this
// repo's own tooling never creates, so a dev machine or CI runner without
// it configured is not an error - there is simply nothing to protect here).
func inspectRegistry(t *testing.T) (string, bool) {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{.Id}}|{{.State.Status}}", "floci-ecr-registry").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
