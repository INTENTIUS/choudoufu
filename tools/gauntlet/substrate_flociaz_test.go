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
	a.Rebuild(flociAzManifest(), &BehaviorIndex{}, "img", OracleVersions{}, ProviderVersions{}, "", SubstratePins{})

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

// A floci-az row is judged against live/floci-az-image (#1120) the way a
// kind row is judged against live/kind-node-image: stamped with the current
// pin it reads current and counts verified in the azure lane, stamped with
// another digest it reads stale, is carried rather than verified, and says
// so on the board and in `next`. It read stale whatever it recorded until
// the pin was threaded through, so no floci-az row could ever be verified.
// An unmeasured one is not stale, as for every substrate, and a floci-az
// row is never held to the kind pin or the floci emulator pin.
func TestFlociAzRowIsJudgedAgainstItsPin(t *testing.T) {
	const (
		emu   = "ghcr.io/lex00/floci@sha256:current"
		pin   = "ghcr.io/lex00/floci-az@sha256:current"
		other = "ghcr.io/lex00/floci-az@sha256:old"
		eng   = "1.13.0"
	)
	// The kind pin is set to the floci-az pin on purpose: a row compared
	// against the wrong field would read current in the stale case.
	pins := SubstratePins{Kind: other, FlociAz: pin}
	oracle := OracleVersions{Terraform: "1.16.1", Tofu: "1.13.0"}
	row := func(name, img string) EstateResult {
		r := EstateResult{Name: name, Protocol: ProtocolGauntlet, Stages: passEverything()}
		oc := oracle
		r.LastRun = &LastRun{Commit: "c", Date: "2026-10-20", Oracle: &oc, UpstreamVersion: eng, SubstrateImage: img}
		return r
	}

	if IsSubstrateStale(EstateResult{Substrate: SubstrateFlociAz}, emu, pins) {
		t.Error("a floci-az row with no run reads stale")
	}
	current, stale := row("az-current", pin), row("az-stale", other)
	current.Substrate, stale.Substrate = SubstrateFlociAz, SubstrateFlociAz
	if IsSubstrateStale(current, emu, pins) {
		t.Error("a floci-az row stamped with the current live/floci-az-image pin reads stale")
	}
	if !IsSubstrateStale(stale, emu, pins) {
		t.Error("a floci-az row stamped with another digest reads current")
	}
	if got := ProviderStaleReasons(current, ProviderVersions{AWS: "6.63.0", Kubernetes: "3.2.1"}); len(got) != 0 {
		t.Errorf("a floci-az row is judged against %q; it configures neither hashicorp/aws nor hashicorp/kubernetes", got)
	}

	m := &Manifest{Estates: []Estate{
		{Name: "az-current", Source: "s", Lane: LaneAzure, Set: SetGrowing},
		{Name: "az-stale", Source: "s", Lane: LaneAzure, Set: SetGrowing},
	}}
	a := &Artifact{Estates: []EstateResult{row("az-current", pin), row("az-stale", other)}}
	a.Rebuild(m, &BehaviorIndex{}, emu, oracle, ProviderVersions{}, eng, pins)
	if got := a.Lanes[LaneAzure]; got.Clear != 2 || got.Verified != 1 || got.Carried != 1 {
		t.Errorf("lanes.azure: clear %d, verified %d, carried %d; want 2, 1, 1", got.Clear, got.Verified, got.Carried)
	}

	for _, r := range a.Estates {
		note, _ := lastRunNote(r, a, pins)
		if gotStale, wantStale := strings.Contains(note, "**Stale**"), r.Name == "az-stale"; gotStale != wantStale {
			t.Errorf("%s's board note is %q; stale marker %v, want %v", r.Name, note, gotStale, wantStale)
		}
		if r.Name == "az-stale" && !strings.Contains(note, pin) {
			t.Errorf("the stale row's note does not name the current pin: %q", note)
		}
	}

	var reasons []string
	for _, u := range NextUnits(a, "all", pins) {
		if u.Estate == "az-current" {
			t.Errorf("next lists az-current, measured against the current pin: %+v", u)
		}
		if u.Estate == "az-stale" {
			reasons = append(reasons, u.Detail)
		}
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], other) || !strings.Contains(reasons[0], pin) {
		t.Errorf("next's reasons for az-stale are %q; want one naming %s and the current pin %s", reasons, other, pin)
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
	_, legacy := lastRunNote(r, &Artifact{}, SubstratePins{})
	if strings.Contains(legacy, "by hand") || !strings.Contains(legacy, "No run is recorded") {
		t.Errorf("a never-run row's note is %q", legacy)
	}
	r.Stages["migrate"] = VerdictPass
	_, legacy = lastRunNote(r, &Artifact{}, SubstratePins{})
	if !strings.Contains(legacy, "by hand") {
		t.Errorf("a legacy row with a recorded verdict lost its by-hand note: %q", legacy)
	}
}
