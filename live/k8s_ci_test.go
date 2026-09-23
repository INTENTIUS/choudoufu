// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The Kubernetes CI story (GitHub issue #1080): every k8s-* smoke scenario
// runs on a kind cluster in .github/workflows/k8s-smoke.yml, with its
// BREAK=1 control, and every kubernetes-lane estate in the manifest runs in
// the nightly gauntlet's kubernetes step. Both workflows pin kind and
// kubectl to the same releases. Before this the three Kubernetes claims
// and the lane's rows were measured only on the maintainer's machine.
//
// Proving it red: add a live/smoke/scenarios/k8s-x.sh with no matrix
// entry, or a kubernetes-lane estate the gauntlet step does not name, or
// change one workflow's kind pin; each fails a different check below.

const (
	k8sSmokeWorkflow  = "../.github/workflows/k8s-smoke.yml"
	gauntletWorkflow  = "../.github/workflows/gauntlet.yml"
	k8sScenarioPrefix = "k8s-"
)

var (
	kindActionLine = regexp.MustCompile(`uses: helm/kind-action@(v[0-9]+\.[0-9]+\.[0-9]+)`)
	kindVersion    = regexp.MustCompile(`\n\s+version: (v[0-9]+\.[0-9]+\.[0-9]+)`)
	kubectlVersion = regexp.MustCompile(`\n\s+kubectl_version: (v[0-9]+\.[0-9]+\.[0-9]+)`)
	matrixEntry    = regexp.MustCompile(`\n\s+- (k8s-[a-z0-9-]+)`)
	// The shard matrix is computed, never typed: a `gauntlet estates` step
	// in the plan job, read back through fromJSON into `matrix:` (#1550).
	matrixFromManifest = regexp.MustCompile(`(?s)go run \./tools/gauntlet estates.*matrix:\s*\n\s*estate: \$\{\{ fromJSON\(needs\.plan\.outputs\.estates\) \}\}`)
)

func TestKubernetesSmokesRunInCIWithTheirControls(t *testing.T) {
	wf, err := os.ReadFile(k8sSmokeWorkflow)
	if err != nil {
		t.Fatalf("read %s: %v", k8sSmokeWorkflow, err)
	}
	entries, err := os.ReadDir(filepath.Join("smoke", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []string
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".sh")
		if strings.HasPrefix(name, k8sScenarioPrefix) && strings.HasSuffix(e.Name(), ".sh") {
			onDisk = append(onDisk, name)
		}
	}
	if len(onDisk) < 3 {
		t.Fatalf("found %d k8s-* scenarios on disk, expected at least 3; this guard is looking in the wrong place", len(onDisk))
	}
	var inMatrix []string
	for _, m := range matrixEntry.FindAllStringSubmatch(string(wf), -1) {
		inMatrix = append(inMatrix, m[1])
	}
	sort.Strings(onDisk)
	sort.Strings(inMatrix)
	if strings.Join(onDisk, ",") != strings.Join(inMatrix, ",") {
		t.Errorf("k8s-smoke.yml's matrix is %v; the k8s-* scenarios on disk are %v. Every Kubernetes claim runs in CI or the site's proven cell is a laptop's word.", inMatrix, onDisk)
	}
	if !strings.Contains(string(wf), "BREAK: \"1\"") {
		t.Errorf("k8s-smoke.yml runs no BREAK=1 control; a scenario whose failure is never demonstrated is scenery")
	}
	if !strings.Contains(string(wf), "install_only: true") {
		t.Errorf("k8s-smoke.yml lets the action create the cluster; the scenario's own cluster_up must own it, or two clusters race")
	}
}

// TestKubernetesSmokesGetTheEmulatorToolsTheyNeed: a Kubernetes scenario
// that starts the pinned floci emulator needs Docker and the AWS CLI on the
// runner as well as kind, and k8s-smoke.yml has to check for them. Claim 27
// gained such a step in #1394 - its label removal measured from a second
// working directory, over a record store the two share - and a runner
// without the AWS CLI would have failed it halfway through, or, worse, a
// scenario written to skip the step there would have reported the same PASS
// as one that ran it.
//
// Proving it red: delete the "aws --version" line from the workflow, or the
// stack_up call from k8s-a-label-is-a-change.sh. Both were run on
// 2026-09-19.
func TestKubernetesSmokesGetTheEmulatorToolsTheyNeed(t *testing.T) {
	wf, err := os.ReadFile(k8sSmokeWorkflow)
	if err != nil {
		t.Fatalf("read %s: %v", k8sSmokeWorkflow, err)
	}
	entries, err := os.ReadDir(filepath.Join("smoke", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	var needEmulator []string
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".sh")
		if !strings.HasPrefix(name, k8sScenarioPrefix) || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("smoke", "scenarios", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range smokeExecutableLines(string(raw)) {
			if line != "" && smokeStackUp.MatchString(line) {
				needEmulator = append(needEmulator, name)
				break
			}
		}
	}
	if len(needEmulator) == 0 {
		// Never a skip: a guard that disables itself when the thing it
		// guards disappears is one nobody notices going green.
		t.Fatalf("no k8s-* scenario starts the emulator any more, so this guard is checking nothing; claim 27's shared-record-store step (#1394) is where it came from")
	}
	for _, want := range []string{"docker info", "aws --version"} {
		if !strings.Contains(string(wf), want) {
			t.Errorf("%v start the floci emulator, and k8s-smoke.yml never checks for %q; the job would fail inside a scenario, or a scenario written to skip the step there would report the same PASS as one that ran it", needEmulator, want)
		}
	}
}

// TestKubernetesLaneRunsNightly: the nightly still measures every
// kubernetes-lane estate, now that the board is sharded one estate per job
// (#1550).
//
// It used to run the lane in a step naming its four estates by hand, and
// this test held that list to the manifest. The list is gone: gauntlet.yml
// builds its matrix from `gauntlet estates`, which computes the set's own
// selection plus the whole kubernetes lane (tools/gauntlet/shards.go, and
// TestShardEstatesCarriesTheKubernetesLaneOfThisRepository, which is where
// the lane-membership half of this claim now lives - it can call the
// function rather than read a workflow).
//
// What is checkable here is that the workflow names no estate at all. A
// hand-written list is what an estate drops out of, so the guard is the
// absence of every single manifest name from the file, not the presence of
// the right ones.
func TestKubernetesLaneRunsNightly(t *testing.T) {
	wf, err := os.ReadFile(gauntletWorkflow)
	if err != nil {
		t.Fatalf("read %s: %v", gauntletWorkflow, err)
	}
	raw, err := os.ReadFile(filepath.Join("gauntlet", "estates.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Estates []struct {
			Name string `json:"name"`
			Lane string `json:"lane"`
		} `json:"estates"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var lane, all []string
	for _, e := range m.Estates {
		all = append(all, e.Name)
		if e.Lane == "kubernetes" {
			lane = append(lane, e.Name)
		}
	}
	if len(lane) == 0 {
		t.Fatal("the manifest has no kubernetes-lane estate; this guard is checking nothing")
	}
	sort.Strings(lane)

	// Comments name estates freely and always have (the CGO_ENABLED note
	// names corpus-eks-basic because that estate is why the flag is
	// there). What must carry no name is what the runner executes.
	executable := workflowWithoutComments(string(wf))

	if !matrixFromManifest.MatchString(string(wf)) {
		t.Errorf("gauntlet.yml's shard matrix is not computed from the manifest (expected a `go run ./tools/gauntlet estates` step feeding `matrix:` through fromJSON); a list the workflow carries itself is a list an estate drops out of")
	}
	for _, name := range all {
		if strings.Contains(executable, name) {
			t.Errorf("gauntlet.yml names estate %q. The matrix comes from `gauntlet estates`; a name typed into the workflow is either a duplicate run or an estate list that will not follow the manifest.", name)
		}
	}
	// The lane's per-estate lines still have to be readable after the run:
	// each shard keeps its own log, and the collect job keeps the lot.
	for _, want := range []string{"gauntlet-run-${{ matrix.estate }}.log", "shards/"} {
		if !strings.Contains(executable, want) {
			t.Errorf("gauntlet.yml does not keep %s; a shard's per-estate lines would be unreadable after the run", want)
		}
	}
}

// workflowWithoutComments drops whole-line YAML comments, which is where
// this repository's workflows keep their reasoning - and where an estate
// name is a citation rather than an instruction.
func workflowWithoutComments(wf string) string {
	var kept []string
	for _, line := range strings.Split(wf, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func TestKubernetesWorkflowsPinOneKind(t *testing.T) {
	pins := map[string][3]string{}
	for _, wf := range []string{k8sSmokeWorkflow, gauntletWorkflow} {
		raw, err := os.ReadFile(wf)
		if err != nil {
			t.Fatal(err)
		}
		action := kindActionLine.FindStringSubmatch(string(raw))
		kv := kindVersion.FindStringSubmatch(string(raw))
		kc := kubectlVersion.FindStringSubmatch(string(raw))
		if action == nil || kv == nil || kc == nil {
			t.Fatalf("%s does not pin helm/kind-action, kind and kubectl each to an exact vX.Y.Z (found action=%v kind=%v kubectl=%v)", wf, action != nil, kv != nil, kc != nil)
		}
		pins[wf] = [3]string{action[1], kv[1], kc[1]}
	}
	if pins[k8sSmokeWorkflow] != pins[gauntletWorkflow] {
		t.Errorf("the two workflows pin different kind toolchains: k8s-smoke %v, gauntlet %v; a claim and a lane row measured on different API servers are two different measurements", pins[k8sSmokeWorkflow], pins[gauntletWorkflow])
	}
}
