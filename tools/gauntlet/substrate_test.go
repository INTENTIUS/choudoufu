// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// The kind substrate (#1067): a kubernetes-lane estate runs on a kind
// cluster, is counted in its own lane bar and in neither AWS bar, and a
// stage whose Substrates note says it does not apply there reads n/a -
// neutral for clear and for `next` - rather than an eternal not_run.
//
// Proving it red: drop the "n/a:" prefix from day2_replace's kind note, or
// count substrate rows into Sets["all"] again; each fails a check below.

func kindManifest() *Manifest {
	return &Manifest{Estates: []Estate{
		{Name: "aws-one", Source: "s", URL: "u", Pin: "p", Lane: "terraform-popular", Set: SetCore, Reason: "r"},
		{Name: "k8s-one", Source: "s", Lane: LaneKubernetes, Set: SetGrowing},
	}}
}

// passEverything is every active stage passed, as a script would report.
func passEverything() map[string]string {
	out := map[string]string{}
	for _, s := range ActiveStages() {
		out[s.ID] = VerdictPass
	}
	return out
}

func TestKindSubstrateStagesReadNAAndStayNeutral(t *testing.T) {
	naOnKind := 0
	for _, s := range Stages() {
		if _, na := s.NotApplicable(SubstrateKind); na {
			naOnKind++
		}
		if _, na := s.NotApplicable(SubstrateFloci); na {
			t.Errorf("stage %s is n/a on the floci substrate; every stage applies on the emulator", s.ID)
		}
	}
	if naOnKind == 0 {
		t.Fatal("no stage is n/a on the kind substrate; day2_replace and day2_crash should be (a Kubernetes name is unique per namespace)")
	}

	m := kindManifest()
	a := &Artifact{Estates: []EstateResult{
		{Name: "aws-one", Protocol: ProtocolGauntlet, Stages: passEverything()},
		{Name: "k8s-one", Protocol: ProtocolGauntlet, Stages: passEverything()},
	}}
	// The kind row's script never reports the n/a stages; they arrive as
	// not_run and must not cost it clear.
	for _, s := range Stages() {
		if _, na := s.NotApplicable(SubstrateKind); na {
			delete(a.Estates[1].Stages, s.ID)
		}
	}
	a.Rebuild(m, &BehaviorIndex{}, "img", OracleVersions{})

	var aws, k8s EstateResult
	for _, r := range a.Estates {
		switch r.Name {
		case "aws-one":
			aws = r
		case "k8s-one":
			k8s = r
		}
	}
	if aws.Substrate != "" {
		t.Errorf("aws-one carries substrate %q, want empty (the emulator is the default and is not written)", aws.Substrate)
	}
	if k8s.Substrate != SubstrateKind {
		t.Errorf("k8s-one carries substrate %q, want %q", k8s.Substrate, SubstrateKind)
	}
	for _, s := range Stages() {
		_, na := s.NotApplicable(SubstrateKind)
		if got := k8s.Stages[s.ID]; na && got != VerdictNA {
			t.Errorf("k8s-one/%s reads %q, want %q (the stage does not apply on kind)", s.ID, got, VerdictNA)
		} else if !na && got == VerdictNA {
			t.Errorf("k8s-one/%s reads n/a but the stage applies on kind", s.ID)
		}
		if got := aws.Stages[s.ID]; got == VerdictNA {
			t.Errorf("aws-one/%s reads n/a; nothing is n/a on the emulator", s.ID)
		}
	}
	if !k8s.Clear {
		t.Errorf("k8s-one is not clear with every applicable headline stage passed and the rest n/a: %v", k8s.Stages)
	}
	if !aws.Clear {
		t.Errorf("aws-one is not clear with every headline stage passed: %v", aws.Stages)
	}

	// The bars: aws-one in both AWS sets, k8s-one in neither; the
	// kubernetes lane holds k8s-one alone.
	if a.Sets["core"].Estates != 1 || a.Sets["all"].Estates != 1 {
		t.Errorf("sets count core=%d all=%d estates, want 1 and 1 (the kind row is in neither)", a.Sets["core"].Estates, a.Sets["all"].Estates)
	}
	lane, ok := a.Lanes[LaneKubernetes]
	if !ok {
		t.Fatalf("artifact has no %s lane summary: %v", LaneKubernetes, a.Lanes)
	}
	if lane.Estates != 1 || lane.Clear != 1 {
		t.Errorf("%s lane reads %d/%d clear, want 1/1", LaneKubernetes, lane.Clear, lane.Estates)
	}
	if got := lane.Stages["day2_replace"].NA; got != 1 {
		t.Errorf("%s lane tallies day2_replace n/a=%d, want 1", LaneKubernetes, got)
	}
	if got := a.Sets["all"].Stages["day2_replace"].NA; got != 0 {
		t.Errorf("the all set tallies day2_replace n/a=%d, want 0", got)
	}

	// `next` never picks an n/a stage as work.
	a.Estates[1].Stages["migrate"] = VerdictFail
	a.Rebuild(m, &BehaviorIndex{}, "img", OracleVersions{})
	for _, u := range NextUnits(a, "all") {
		if u.Estate != "k8s-one" {
			continue
		}
		if u.Stage != "migrate" {
			t.Errorf("next picked k8s-one/%s, want migrate (the failing stage), never an n/a one", u.Stage)
		}
		if u.Remaining != 1 {
			t.Errorf("k8s-one has %d remaining, want 1: n/a stages are not remaining work", u.Remaining)
		}
	}
}

func TestKubernetesLaneEstateMayBeKeptInRepo(t *testing.T) {
	m := kindManifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("a kubernetes-lane estate with neither url nor pin should validate: %v", err)
	}
	m.Estates[1].URL = "https://example.com/x.git"
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "url and pin") {
		t.Errorf("a kubernetes-lane estate with a url and no pin should be refused, got %v", err)
	}
	m.Estates[1].Lane = "terraform-popular"
	m.Estates[1].URL = ""
	if err := m.Validate(); err == nil {
		t.Error("a terraform-popular estate with neither url nor pin should be refused")
	}
}

func TestKindSubstrateNotesRenderUnderEveryStage(t *testing.T) {
	m := kindManifest()
	a := &Artifact{}
	a.Rebuild(m, &BehaviorIndex{}, "img", OracleVersions{})
	doc := renderSpec(m, a, TypeIndexTotals{})
	for _, s := range Stages() {
		note, ok := s.Substrates[SubstrateKind]
		if !ok {
			continue
		}
		if reason, na := s.NotApplicable(SubstrateKind); na {
			if !strings.Contains(doc, "not applicable, recorded as `n/a`") || !strings.Contains(doc, reason) {
				t.Errorf("GAUNTLET.md does not say %s is n/a on kind, with its reason", s.ID)
			}
		} else if !strings.Contains(doc, "On the kind substrate: "+note) {
			t.Errorf("GAUNTLET.md does not carry %s's kind note", s.ID)
		}
	}
}
