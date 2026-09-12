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
	k8sLaneRun     = regexp.MustCompile(`go run \./tools/gauntlet run ([a-z0-9 -]+) \| tee gauntlet-run-k8s\.log`)
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
	var lane []string
	for _, e := range m.Estates {
		if e.Lane == "kubernetes" {
			lane = append(lane, e.Name)
		}
	}
	if len(lane) == 0 {
		t.Fatal("the manifest has no kubernetes-lane estate; this guard is checking nothing")
	}
	run := k8sLaneRun.FindStringSubmatch(string(wf))
	if run == nil {
		t.Fatalf("gauntlet.yml has no kubernetes-lane run step (go run ./tools/gauntlet run <estates> | tee gauntlet-run-k8s.log)")
	}
	named := strings.Fields(run[1])
	sort.Strings(named)
	sort.Strings(lane)
	if strings.Join(named, ",") != strings.Join(lane, ",") {
		t.Errorf("gauntlet.yml's kubernetes step runs %v; the manifest's kubernetes lane is %v. A lane estate the nightly never runs goes stale against every repin with nothing re-measuring it.", named, lane)
	}
	if !strings.Contains(string(wf), "gauntlet-run-k8s.log") || strings.Count(string(wf), "gauntlet-run-k8s.log") < 2 {
		t.Errorf("gauntlet.yml does not keep gauntlet-run-k8s.log as an artifact; the lane's per-estate lines would be unreadable after the run")
	}
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
