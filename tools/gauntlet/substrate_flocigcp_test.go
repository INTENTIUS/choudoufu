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

// The floci-gcp substrate (#1119): the gcp lane's estates run on the
// lex00/floci-gcp emulator, pinned in live/floci-gcp-image. The lane decides
// it; its rows have a bar of their own and count toward neither AWS bar;
// they record the floci-gcp digest and the hashicorp/google version and are
// judged against those pins; and every stage says how it reads there.

func flociGCPManifest() *Manifest {
	return &Manifest{Estates: []Estate{
		{Name: "aws-one", Source: "s", URL: "u", Pin: "p", Lane: "terraform-popular", Set: SetCore, Reason: "r"},
		{Name: "gcp-one", Source: "s", Lane: LaneGCP, Set: SetGrowing},
		{Name: "k8s-one", Source: "s", Lane: LaneKubernetes, Set: SetGrowing},
	}}
}

func TestGCPLaneRunsOnFlociGCP(t *testing.T) {
	if err := flociGCPManifest().Validate(); err != nil {
		t.Fatalf("a gcp-lane estate kept in this repository, with no url or pin, was refused: %v", err)
	}
	e := Estate{Lane: LaneGCP}
	if got := e.Substrate(); got != SubstrateFlociGCP {
		t.Errorf("a gcp-lane estate's substrate is %q, want %q", got, SubstrateFlociGCP)
	}
	if RunsOnFloci(SubstrateFlociGCP) {
		t.Error("RunsOnFloci(floci-gcp) is true; the gcp lane never launches floci and must not be judged against its pin or count toward the AWS bars")
	}
	if !UsesGoogleProvider(SubstrateFlociGCP) || UsesKubernetesProvider(SubstrateFlociGCP) {
		t.Error("a floci-gcp row must be judged against hashicorp/google and not hashicorp/kubernetes")
	}
	for _, sub := range []string{SubstrateFloci, SubstrateKind, SubstrateFlociEKS} {
		if UsesGoogleProvider(sub) {
			t.Errorf("UsesGoogleProvider(%q) is true", sub)
		}
	}
	err := (&Manifest{Estates: []Estate{{Name: "x", Source: "s", Lane: LaneGCP, Set: SetGrowing, SubstrateOverride: SubstrateFlociEKS}}}).Validate()
	if err == nil || !strings.Contains(err.Error(), "AWS-lane") {
		t.Errorf("floci-eks declared on a gcp-lane estate: Validate() = %v, want the AWS-lane refusal", err)
	}
}

func TestGCPRowHasABarOfItsOwn(t *testing.T) {
	a := &Artifact{Estates: []EstateResult{
		{Name: "aws-one", Protocol: ProtocolGauntlet, Stages: passEverything()},
		{Name: "gcp-one", Protocol: ProtocolGauntlet, Stages: passEverything()},
		{Name: "k8s-one", Protocol: ProtocolGauntlet, Stages: passEverything()},
	}}
	a.Rebuild(flociGCPManifest(), &BehaviorIndex{}, "img", OracleVersions{}, ProviderVersions{}, "", "")
	gcp, ok := a.Result("gcp-one")
	if !ok {
		t.Fatal("gcp-one has no row")
	}
	if gcp.Substrate != SubstrateFlociGCP {
		t.Errorf("gcp-one carries substrate %q, want %q", gcp.Substrate, SubstrateFlociGCP)
	}
	for id, v := range gcp.Stages {
		if v == VerdictNA {
			t.Errorf("gcp-one/%s reads n/a; no stage is n/a on floci-gcp", id)
		}
	}
	if !gcp.Clear {
		t.Errorf("gcp-one is not clear with every headline stage passed: %v", gcp.Stages)
	}
	if a.Sets["all"].Estates != 1 {
		t.Errorf("the all bar counts %d estates, want 1 (aws-one): a floci-gcp row is in neither AWS bar", a.Sets["all"].Estates)
	}
	if a.Lanes[LaneGCP].Estates != 1 {
		t.Errorf("lanes.gcp counts %d estates, want 1", a.Lanes[LaneGCP].Estates)
	}
}

func TestEveryStageHasAFlociGCPNote(t *testing.T) {
	for _, s := range registeredStages() {
		note, ok := s.Substrates[SubstrateFlociGCP]
		if !ok || strings.TrimSpace(note) == "" {
			t.Errorf("stage %s has no floci-gcp note; flociGCPNotes must name every stage, so an AWS-shaped oracle is marked rather than skipped", s.ID)
			continue
		}
		if _, na := s.NotApplicable(SubstrateFlociGCP); na {
			t.Errorf("stage %s is n/a on floci-gcp; GCP has a create_before_destroy replacement and a crash window, and every stage applies", s.ID)
		}
	}
	for id := range flociGCPNotes {
		if _, ok := StageByID(id); !ok {
			t.Errorf("flociGCPNotes names %q, which is not a registered stage", id)
		}
	}
	cold, _ := StageByID("cold_deploy")
	for _, gap := range []string{"lex00/floci-gcp#6", "lex00/floci-gcp#7"} {
		if !strings.Contains(cold.Substrates[SubstrateFlociGCP], gap) {
			t.Errorf("cold_deploy's floci-gcp note does not name the emulator gap %s", gap)
		}
	}
}

// TestFlociGCPRowIsJudgedAgainstItsOwnPins: a floci-gcp row's image is read
// against live/floci-gcp-image (Artifact.EmulatorGCP), not floci's or
// kind's, and its provider against hashicorp/google's pin only.
//
// Proving it red: return to IsSubstrateStale's old two-way switch, which
// sent every non-kind row to IsStale, and the current floci-gcp row reads
// stale against the floci digest.
func TestFlociGCPRowIsJudgedAgainstItsOwnPins(t *testing.T) {
	const gcpPin = "ghcr.io/lex00/floci-gcp@sha256:current"
	a := &Artifact{Emulator: "ghcr.io/lex00/floci@sha256:aws", EmulatorGCP: gcpPin, Providers: ProviderVersions{AWS: "6.63.0", Google: "8.6.0"}}
	row := func(img, google string) EstateResult {
		return EstateResult{Substrate: SubstrateFlociGCP, Protocol: ProtocolGauntlet, LastRun: &LastRun{Commit: "c", SubstrateImage: img, GoogleProviderVersion: google}}
	}
	if IsSubstrateStale(row(gcpPin, "8.6.0"), a, "kind-pin") {
		t.Error("a floci-gcp row measured against the current floci-gcp pin reads stale")
	}
	if !IsSubstrateStale(row("ghcr.io/lex00/floci-gcp@sha256:old", "8.6.0"), a, "kind-pin") {
		t.Error("a floci-gcp row measured against an older floci-gcp image reads current")
	}
	if got := ProviderStaleReasons(row(gcpPin, "8.6.0"), a.Providers); len(got) != 0 {
		t.Errorf("a current floci-gcp row is provider-stale: %q (it records no hashicorp/aws version and must not be judged against that pin)", got)
	}
	got := ProviderStaleReasons(row(gcpPin, "8.5.0"), a.Providers)
	if len(got) != 1 || !strings.Contains(got[0], "hashicorp/google 8.5.0") {
		t.Errorf("ProviderStaleReasons = %q, want one reason naming hashicorp/google 8.5.0", got)
	}
	note, _ := lastRunNote(row("ghcr.io/lex00/floci-gcp@sha256:old", "8.6.0"), a, "kind-pin")
	if !strings.Contains(note, "**Stale**: the current pin is `"+gcpPin+"`") {
		t.Errorf("lastRunNote for a stale floci-gcp row = %q, want it to name the floci-gcp pin", note)
	}
}

// TestRunEstatesRecordsTheFlociGCPImage is #1594's rule for the gcp lane: a
// floci-gcp row leaves last_run.emulator empty and records live/
// floci-gcp-image in substrate_image.
func TestRunEstatesRecordsTheFlociGCPImage(t *testing.T) {
	root := t.TempDir()
	const gcpImage = "ghcr.io/lex00/floci-gcp@sha256:testdigest"
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "live", "floci-gcp-image"), []byte(gcpImage+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("live", "e2e", "gcp-one", "run.sh")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\nprintf 'GAUNTLET protocol=1\\n'\nprintf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=1\\n'\n"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Estates: []Estate{{Name: "gcp-one", Source: "s", Lane: LaneGCP, Set: SetGrowing, Script: rel}}}
	a := &Artifact{Schema: 1}
	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"gcp-one"}, Stdout: &out}, "c", "ghcr.io/lex00/floci@sha256:aws"); err != nil {
		t.Fatal(err)
	}
	r, ok := a.Result("gcp-one")
	if !ok || r.LastRun == nil {
		t.Fatal("gcp-one has no last_run")
	}
	if r.LastRun.Emulator != "" || r.LastRun.AWSProviderVersion != "" {
		t.Errorf("gcp-one recorded floci provenance (emulator %q, hashicorp/aws %q); it never launches floci", r.LastRun.Emulator, r.LastRun.AWSProviderVersion)
	}
	if r.LastRun.SubstrateImage != gcpImage {
		t.Errorf("gcp-one last_run.substrate_image = %q, want %q (live/floci-gcp-image's pin)", r.LastRun.SubstrateImage, gcpImage)
	}
}

// TestReferenceGCPIsTheGCPLanesEstate: the lane's first estate is
// registered on it and its script exists.
func TestReferenceGCPIsTheGCPLanesEstate(t *testing.T) {
	root := repoRootForTest(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := m.ByName("reference-gcp")
	if !ok {
		t.Fatal("reference-gcp is not in live/gauntlet/estates.json")
	}
	if e.Lane != LaneGCP || e.Substrate() != SubstrateFlociGCP {
		t.Errorf("reference-gcp is on lane %q, substrate %q; want %q on %q", e.Lane, e.Substrate(), LaneGCP, SubstrateFlociGCP)
	}
	if _, err := os.Stat(filepath.Join(root, e.ScriptPath())); err != nil {
		t.Errorf("reference-gcp's crossing script: %v", err)
	}
}
