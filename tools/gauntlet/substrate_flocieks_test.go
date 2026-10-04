// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// The floci-eks substrate (#1113): an AWS-lane estate whose configuration
// also manages the cluster its own aws_eks_cluster creates, run against
// floci's EKS real mode. It is declared, never inferred; it counts toward
// the AWS bars because it runs on the emulator; it is judged against both
// provider pins; and every stage says how it reads there.
//
// Written under the maintainer's no-testing ruling for #1113 and not run by
// the change that added it.

func flociEKSManifest() *Manifest {
	return &Manifest{Estates: []Estate{
		{Name: "aws-one", Source: "s", URL: "u", Pin: "p", Lane: "terraform-popular", Set: SetCore, Reason: "r"},
		{Name: "eks-one", Source: "s", URL: "u", Pin: "p", Lane: "terraform-popular", Set: SetCore, Reason: "r", SubstrateOverride: SubstrateFlociEKS},
		{Name: "k8s-one", Source: "s", Lane: LaneKubernetes, Set: SetGrowing},
	}}
}

func TestFlociEKSIsDeclaredAndValidated(t *testing.T) {
	if err := flociEKSManifest().Validate(); err != nil {
		t.Fatalf("a floci-eks declaration on an AWS lane was refused: %v", err)
	}
	for _, tc := range []struct {
		name string
		e    Estate
		want string
	}{
		{"on the kubernetes lane", Estate{Name: "x", Source: "s", Lane: LaneKubernetes, Set: SetGrowing, SubstrateOverride: SubstrateFlociEKS}, "AWS-lane"},
		{"the lane's own substrate", Estate{Name: "x", Source: "s", Lane: "reference", Set: SetGrowing, SubstrateOverride: SubstrateFloci}, "not one an estate may declare"},
		{"kind on an AWS lane", Estate{Name: "x", Source: "s", Lane: "reference", Set: SetGrowing, SubstrateOverride: SubstrateKind}, "not one an estate may declare"},
		{"an unknown value", Estate{Name: "x", Source: "s", Lane: "reference", Set: SetGrowing, SubstrateOverride: "eks"}, "not one an estate may declare"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Manifest{Estates: []Estate{tc.e}}).Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want an error containing %q", err, tc.want)
			}
		})
	}

	// The lane rule is untouched for an estate that declares nothing.
	for _, e := range flociEKSManifest().Estates {
		want := map[string]string{"aws-one": SubstrateFloci, "eks-one": SubstrateFlociEKS, "k8s-one": SubstrateKind}[e.Name]
		if got := e.Substrate(); got != want {
			t.Errorf("%s.Substrate() = %q, want %q", e.Name, got, want)
		}
	}
}

func TestFlociEKSRowCountsTowardTheAWSBars(t *testing.T) {
	a := &Artifact{Estates: []EstateResult{
		{Name: "aws-one", Protocol: ProtocolGauntlet, Stages: passEverything()},
		{Name: "eks-one", Protocol: ProtocolGauntlet, Stages: passEverything()},
		{Name: "k8s-one", Protocol: ProtocolGauntlet, Stages: passEverything()},
	}}
	a.Rebuild(flociEKSManifest(), &BehaviorIndex{}, "img", OracleVersions{}, ProviderVersions{}, "", "")

	var eks EstateResult
	for _, r := range a.Estates {
		if r.Name == "eks-one" {
			eks = r
		}
	}
	if eks.Substrate != SubstrateFlociEKS {
		t.Errorf("eks-one carries substrate %q, want %q", eks.Substrate, SubstrateFlociEKS)
	}
	for id, v := range eks.Stages {
		if v == VerdictNA {
			t.Errorf("eks-one/%s reads n/a; every stage applies on floci-eks", id)
		}
	}
	if !eks.Clear {
		t.Errorf("eks-one is not clear with every headline stage passed: %v", eks.Stages)
	}
	// aws-one and eks-one in both AWS bars; k8s-one in neither.
	if a.Sets["core"].Estates != 2 || a.Sets["all"].Estates != 2 {
		t.Errorf("sets count core=%d all=%d, want 2 and 2 (a floci-eks row runs on the emulator)", a.Sets["core"].Estates, a.Sets["all"].Estates)
	}
}

func TestEveryStageHasAFlociEKSNote(t *testing.T) {
	for _, s := range registeredStages() {
		note, ok := s.Substrates[SubstrateFlociEKS]
		if !ok || strings.TrimSpace(note) == "" {
			t.Errorf("stage %s has no floci-eks note; flociEKSNotes must name every stage", s.ID)
			continue
		}
		if _, na := s.NotApplicable(SubstrateFlociEKS); na {
			t.Errorf("stage %s is n/a on floci-eks; the substrate is the emulator plus a cluster, and every stage applies", s.ID)
		}
	}
	for id := range flociEKSNotes {
		if _, ok := StageByID(id); !ok {
			t.Errorf("flociEKSNotes names %q, which is not a registered stage", id)
		}
	}
	// What k3s cannot stand in for is stated, once, where a reader starts.
	cold, _ := StageByID("cold_deploy")
	for _, gap := range []string{"IRSA", "Pod Identity", "VPC CNI", "EBS CSI", "access-entry", "add-ons"} {
		if !strings.Contains(cold.Substrates[SubstrateFlociEKS], gap) {
			t.Errorf("cold_deploy's floci-eks note does not name %q among what k3s cannot stand in for", gap)
		}
	}
}

func TestFlociEKSRowIsJudgedAgainstBothProviderPins(t *testing.T) {
	pins := ProviderVersions{AWS: "6.59.0", Kubernetes: "3.1.0"}
	row := func(sub, aws, k8s string) EstateResult {
		return EstateResult{Substrate: sub, LastRun: &LastRun{AWSProviderVersion: aws, KubernetesProviderVersion: k8s}}
	}
	for _, tc := range []struct {
		name    string
		r       EstateResult
		reasons []string
	}{
		{"floci, current", row("", "6.59.0", ""), nil},
		{"floci ignores the kubernetes pin", row("", "6.59.0", "0.0.1"), nil},
		{"kind ignores the aws pin", row(SubstrateKind, "", "3.1.0"), nil},
		{"floci-eks, both current", row(SubstrateFlociEKS, "6.59.0", "3.1.0"), nil},
		{"floci-eks, kubernetes stale", row(SubstrateFlociEKS, "6.59.0", "3.0.0"), []string{"hashicorp/kubernetes 3.0.0"}},
		{"floci-eks, aws stale", row(SubstrateFlociEKS, "6.58.0", "3.1.0"), []string{"hashicorp/aws 6.58.0"}},
		{"floci-eks, both stale", row(SubstrateFlociEKS, "6.58.0", ""), []string{"hashicorp/aws 6.58.0", "hashicorp/kubernetes unrecorded"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ProviderStaleReasons(tc.r, pins)
			if len(got) != len(tc.reasons) {
				t.Fatalf("ProviderStaleReasons = %q, want %d reason(s) naming %q", got, len(tc.reasons), tc.reasons)
			}
			for i, want := range tc.reasons {
				if !strings.Contains(got[i], want) {
					t.Errorf("reason %d = %q, want it to name %q", i, got[i], want)
				}
			}
			if IsProviderStale(tc.r, pins) != (len(tc.reasons) > 0) {
				t.Errorf("IsProviderStale disagrees with ProviderStaleReasons")
			}
		})
	}
}

// TestCorpusEKSBasicRunsOnFlociEKS: the lane's first mixed estate is
// declared on the substrate it has run on all along (#1113's design: floci's
// real mode already started the k3s cluster its aws-auth verdict reads).
func TestCorpusEKSBasicRunsOnFlociEKS(t *testing.T) {
	m, err := LoadManifest(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := m.ByName("corpus-eks-basic")
	if !ok {
		t.Fatal("corpus-eks-basic is not in the manifest")
	}
	if got := e.Substrate(); got != SubstrateFlociEKS {
		t.Errorf("corpus-eks-basic runs on %q, want %q", got, SubstrateFlociEKS)
	}
}
