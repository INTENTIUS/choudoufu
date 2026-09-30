// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunEstatesRecordsSubstrateImageNotEmulatorForKindLane is issue
// #1594's guard at the schema layer: before this fix, RunEstates stamped
// every row's last_run.emulator with the floci digest unconditionally,
// including a kind-substrate (kubernetes-lane) estate that never launches
// floci at all. A kind-lane row must instead leave Emulator empty and
// record what it actually ran against in SubstrateImage, read from
// live/kind-node-image; a floci-lane row is untouched by this change.
//
// Proving it red: replace the Substrate-conditional stamp in RunEstates
// (run.go) with the old unconditional `Emulator: emulator`, and this
// fails - the kind-lane row would carry the floci digest passed in below
// instead of an empty Emulator and the pinned kind image, and the
// floci-lane row's SubstrateImage would still read empty either way, so
// the kind-lane assertions are what catch the regression.
func TestRunEstatesRecordsSubstrateImageNotEmulatorForKindLane(t *testing.T) {
	root := t.TempDir()
	const kindImage = "kindest/node:v1.37.0@sha256:testdigest0000000000000000000000000000000000000000000000000000"
	const flociImage = "ghcr.io/lex00/floci@sha256:testemulator"

	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "live", "kind-node-image"), []byte(kindImage+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	kindScript := filepath.Join("live", "e2e", "k8s-one", "run.sh")
	flociScript := filepath.Join("live", "e2e", "floci-one", "run.sh")
	script := "#!/usr/bin/env bash\n" +
		"printf 'GAUNTLET protocol=1\\n'\n" +
		"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=1\\n'\n"
	for _, rel := range []string{kindScript, flociScript} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	m := &Manifest{Estates: []Estate{
		{Name: "k8s-one", Source: "s", Lane: LaneKubernetes, Set: SetGrowing, Script: kindScript},
		{Name: "floci-one", Source: "s", Lane: "reference", Set: SetGrowing, Script: flociScript},
	}}
	a := &Artifact{Schema: 1}

	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"k8s-one", "floci-one"}, Stdout: &out}, "c", flociImage); err != nil {
		t.Fatal(err)
	}

	kind, ok := a.Result("k8s-one")
	if !ok || kind.LastRun == nil {
		t.Fatal("k8s-one has no last_run")
	}
	if kind.LastRun.Emulator != "" {
		t.Errorf("k8s-one (kind lane) last_run.emulator = %q, want empty - a kind-substrate estate never launches floci", kind.LastRun.Emulator)
	}
	if kind.LastRun.SubstrateImage != kindImage {
		t.Errorf("k8s-one last_run.substrate_image = %q, want %q (live/kind-node-image's pin)", kind.LastRun.SubstrateImage, kindImage)
	}

	floci, ok := a.Result("floci-one")
	if !ok || floci.LastRun == nil {
		t.Fatal("floci-one has no last_run")
	}
	if floci.LastRun.Emulator != flociImage {
		t.Errorf("floci-one last_run.emulator = %q, want %q (unaffected by #1594)", floci.LastRun.Emulator, flociImage)
	}
	if floci.LastRun.SubstrateImage != "" {
		t.Errorf("floci-one last_run.substrate_image = %q, want empty - it is not a kind-substrate estate", floci.LastRun.SubstrateImage)
	}
}

// TestKindLaneClearRowIsNeverEmulatorStale is the next.go half of #1594:
// nextUnitsAgainst must not treat a clear kind-substrate row's unstamped
// Emulator field as evidence that it is stale against the floci pin - that
// field is never stamped for a kind-lane row (see the test above), so
// IsStale's "empty always reads as stale" rule would otherwise enqueue a
// permanent, meaningless re-verify unit for every clear kind estate. The
// row records the kind node image it ran on, and that matches the pin, so
// nothing about it is stale (#1739 is the test below, where it does not).
func TestKindLaneClearRowIsNeverEmulatorStale(t *testing.T) {
	const kindPin = "kindest/node:v1.37.0@sha256:current"
	m := &Manifest{Estates: []Estate{
		{Name: "k8s-clear", Source: "s", Lane: LaneKubernetes, Set: SetGrowing},
	}}
	a := &Artifact{Estates: []EstateResult{
		{Name: "k8s-clear", Protocol: ProtocolGauntlet, Stages: passEverything(), LastRun: &LastRun{Commit: "c", Date: "2026-01-01T00:00:00Z", SubstrateImage: kindPin}},
	}}
	a.Rebuild(m, &BehaviorIndex{}, "ghcr.io/lex00/floci@sha256:current", OracleVersions{}, ProviderVersions{})

	for _, r := range a.Estates {
		if r.Name == "k8s-clear" && !r.Clear {
			t.Fatalf("k8s-clear is not clear: %v", r.Stages)
		}
	}

	for _, u := range NextUnits(a, "all", kindPin) {
		if u.Estate == "k8s-clear" {
			t.Errorf("k8s-clear surfaced as a unit (%q) despite being clear with an unstamped, irrelevant emulator field: %+v", u.Stage, u)
		}
	}
}

// TestKindLaneClearRowGoesStaleOnANodeImageBump is issue #1739's second
// defect: after live/kind-node-image moves, a clear kind row measured on
// the old image is stale evidence and must surface as a stale_pin unit
// naming both images, the way a floci row does when live/floci-image
// moves. Before #1739, nextUnitsAgainst checked staleness only for
// r.Substrate == "", so this row stayed clear and produced no work.
func TestKindLaneClearRowGoesStaleOnANodeImageBump(t *testing.T) {
	const (
		oldImage = "kindest/node:v1.36.1@sha256:old"
		newImage = "kindest/node:v1.37.0@sha256:new"
	)
	m := &Manifest{Estates: []Estate{
		{Name: "k8s-old", Source: "s", Lane: LaneKubernetes, Set: SetGrowing},
		{Name: "k8s-new", Source: "s", Lane: LaneKubernetes, Set: SetGrowing},
	}}
	a := &Artifact{Estates: []EstateResult{
		{Name: "k8s-old", Protocol: ProtocolGauntlet, Stages: passEverything(), LastRun: &LastRun{Commit: "c", Date: "2026-01-01T00:00:00Z", SubstrateImage: oldImage}},
		{Name: "k8s-new", Protocol: ProtocolGauntlet, Stages: passEverything(), LastRun: &LastRun{Commit: "c", Date: "2026-01-01T00:00:00Z", SubstrateImage: newImage}},
	}}
	a.Rebuild(m, &BehaviorIndex{}, "ghcr.io/lex00/floci@sha256:current", OracleVersions{}, ProviderVersions{})

	var got *Unit
	for _, u := range NextUnits(a, "all", newImage) {
		u := u
		switch u.Estate {
		case "k8s-new":
			t.Errorf("k8s-new ran on the current kind node image and must not be work, got %+v", u)
		case "k8s-old":
			got = &u
		}
	}
	if got == nil {
		t.Fatalf("k8s-old was measured on %s and the pin is now %s; expected a %s unit, got none", oldImage, newImage, StageStalePin)
	}
	if got.Stage != StageStalePin {
		t.Errorf("k8s-old surfaced as %q, want %q", got.Stage, StageStalePin)
	}
	if !strings.Contains(got.Detail, oldImage) || !strings.Contains(got.Detail, newImage) || strings.Contains(got.Detail, "emulator") {
		t.Errorf("k8s-old's stale_pin detail should name both kind node images and no emulator, got %q", got.Detail)
	}
}
