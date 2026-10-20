// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// The azure lane on the floci-az substrate (#1120, live/ADDING-A-PLATFORM.md
// requirement 14): the lane decides the substrate, the lane's estates count
// in a bar of their own and toward neither AWS bar, a floci-az row is never
// judged against the floci pin or hashicorp/aws, and every stage says how it
// reads there.
//
// Written for #1120 unit 15. No azure-lane estate has run yet.

func flociAzManifest() *Manifest {
	return &Manifest{Estates: []Estate{
		{Name: "aws-one", Source: "s", URL: "u", Pin: "p", Lane: "terraform-popular", Set: SetCore, Reason: "r"},
		{Name: "az-one", Source: "s", Lane: LaneAzure, Set: SetGrowing},
		{Name: "k8s-one", Source: "s", Lane: LaneKubernetes, Set: SetGrowing},
	}}
}

func TestAzureLaneRunsOnFlociAz(t *testing.T) {
	if err := flociAzManifest().Validate(); err != nil {
		t.Fatalf("an in-repo azure-lane estate with neither url nor pin was refused: %v", err)
	}
	for _, e := range flociAzManifest().Estates {
		want := map[string]string{"aws-one": SubstrateFloci, "az-one": SubstrateFlociAz, "k8s-one": SubstrateKind}[e.Name]
		if got := e.Substrate(); got != want {
			t.Errorf("%s.Substrate() = %q, want %q", e.Name, got, want)
		}
	}
	if RunsOnFloci(SubstrateFlociAz) {
		t.Error("RunsOnFloci(floci-az) is true; floci-az is not the floci emulator, so its rows would be stamped with the floci pin and counted in the AWS bars")
	}
	if UsesKubernetesProvider(SubstrateFlociAz) {
		t.Error("UsesKubernetesProvider(floci-az) is true; an azure-lane row configures no hashicorp/kubernetes")
	}
	for _, tc := range []struct {
		name string
		e    Estate
		want string
	}{
		{"floci-eks on the azure lane", Estate{Name: "x", Source: "s", Lane: LaneAzure, Set: SetGrowing, SubstrateOverride: SubstrateFlociEKS}, "AWS-lane"},
		{"floci-az declared", Estate{Name: "x", Source: "s", Lane: "reference", Set: SetGrowing, SubstrateOverride: SubstrateFlociAz}, "not one an estate may declare"},
		{"an azure estate in core", Estate{Name: "x", Source: "s", Lane: LaneAzure, Set: SetCore, Reason: "r"}, "core estate must come from"},
		{"a published azure root without a pin", Estate{Name: "x", Source: "s", URL: "u", Lane: LaneAzure, Set: SetGrowing}, "url and pin are required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Manifest{Estates: []Estate{tc.e}}).Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestFlociAzRowCountsInItsOwnLane(t *testing.T) {
	a := &Artifact{Estates: []EstateResult{
		{Name: "aws-one", Protocol: ProtocolGauntlet, Stages: passEverything()},
		{Name: "az-one", Protocol: ProtocolGauntlet, Stages: passEverything()},
		{Name: "k8s-one", Protocol: ProtocolGauntlet, Stages: passEverything()},
	}}
	a.Rebuild(flociAzManifest(), &BehaviorIndex{}, "img", OracleVersions{}, ProviderVersions{}, "", "")

	var az EstateResult
	for _, r := range a.Estates {
		if r.Name == "az-one" {
			az = r
		}
	}
	if az.Substrate != SubstrateFlociAz {
		t.Errorf("az-one carries substrate %q, want %q", az.Substrate, SubstrateFlociAz)
	}
	for id, v := range az.Stages {
		if v == VerdictNA {
			t.Errorf("az-one/%s reads n/a; no stage is n/a on floci-az (flociAzNotes)", id)
		}
	}
	if a.Sets["core"].Estates != 1 || a.Sets["all"].Estates != 1 {
		t.Errorf("sets count core=%d all=%d, want 1 and 1: an azure-lane row is in neither AWS bar", a.Sets["core"].Estates, a.Sets["all"].Estates)
	}
	if got := a.Lanes[LaneAzure]; got.Estates != 1 || got.Clear != 1 {
		t.Errorf("lanes.azure = %d estates, %d clear; want 1 and 1", got.Estates, got.Clear)
	}
}

// A measured floci-az row is never read as current: nothing compares it
// with live/floci-az-image yet, and the safe answer is stale rather than a
// verified row nobody checked. An unmeasured one is not stale, as for every
// substrate.
func TestFlociAzRowIsNeverReadAsCurrent(t *testing.T) {
	unmeasured := EstateResult{Substrate: SubstrateFlociAz}
	if IsSubstrateStale(unmeasured, "floci", "kind") {
		t.Error("a floci-az row with no run reads stale")
	}
	measured := EstateResult{Substrate: SubstrateFlociAz, LastRun: &LastRun{SubstrateImage: "ghcr.io/lex00/floci-az@sha256:x", Emulator: "floci"}}
	if !IsSubstrateStale(measured, "floci", "kind") {
		t.Error("a measured floci-az row reads current; it would be counted verified against a pin nothing compared")
	}
	if got := ProviderStaleReasons(measured, ProviderVersions{AWS: "6.63.0", Kubernetes: "3.2.1"}); len(got) != 0 {
		t.Errorf("a floci-az row is judged against %q; it configures neither hashicorp/aws nor hashicorp/kubernetes", got)
	}
}

func TestEveryStageHasAFlociAzNote(t *testing.T) {
	for _, s := range registeredStages() {
		note, ok := s.Substrates[SubstrateFlociAz]
		if !ok || strings.TrimSpace(note) == "" {
			t.Errorf("stage %s has no floci-az note; flociAzNotes must name every stage", s.ID)
			continue
		}
		if _, na := s.NotApplicable(SubstrateFlociAz); na {
			t.Errorf("stage %s is n/a on floci-az; an emulator gap fails a stage by name, it does not make the stage neutral", s.ID)
		}
	}
	for id := range flociAzNotes {
		if _, ok := StageByID(id); !ok {
			t.Errorf("flociAzNotes names %q, which is not a registered stage", id)
		}
	}
	// Where a stage leans on what floci-az does not serve, its note says
	// so, so a first run's failure there reads as the emulator's gap.
	for id, gap := range map[string]string{"test_plan": "Resource Graph", "test_apply": "Resource Graph", "no_local_state": "Resource Graph", "plan_approval": "Policy", "cold_deploy": "storage accounts"} {
		s, _ := StageByID(id)
		if !strings.Contains(s.Substrates[SubstrateFlociAz], gap) {
			t.Errorf("stage %s's floci-az note does not name %q, which floci-az lacks and the stage needs", id, gap)
		}
	}
}

// A row Rebuild added for an estate that has never run is not described
// as verdicts recorded by hand.
func TestNeverRunRowIsNotCalledHandRecorded(t *testing.T) {
	r := EstateResult{Name: "az-one", Protocol: ProtocolLegacy, Substrate: SubstrateFlociAz, Stages: map[string]string{"cold_deploy": VerdictNotRun, "migrate": VerdictNotRun}}
	_, legacy := lastRunNote(r, &Artifact{}, "")
	if strings.Contains(legacy, "by hand") || !strings.Contains(legacy, "No run is recorded") {
		t.Errorf("a never-run row's note is %q", legacy)
	}
	r.Stages["migrate"] = VerdictPass
	_, legacy = lastRunNote(r, &Artifact{}, "")
	if !strings.Contains(legacy, "by hand") {
		t.Errorf("a legacy row with a recorded verdict lost its by-hand note: %q", legacy)
	}
}
