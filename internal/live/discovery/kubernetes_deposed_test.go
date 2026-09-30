// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1683, part 2: the Kubernetes two-claimant collision
// (#1641) consults the estate's deposed record the way the AWS collision
// does (#361, matchDeposedClaimant).
//
// A create_before_destroy rename interrupted after creating cfg-b and
// before destroying cfg-a leaves both objects carrying the block's address
// annotation. The interrupted apply's write-back records cfg-a as the
// address's deposed object (projection's diffDeposedForWrite, through
// LocatedRecordFrom, which renders "ns/cfg-a" for kubernetes_config_map:
// see projection's TestKubernetesDeposedRecordCarriesTheObjectsIdentity).
// Where neither object is at a declared, listed key - the node reads the
// name (#1539's shape), or the configuration moved on to a third name -
// the record says which claimant is the deposed half: that one is folded
// in as the address's deposed object, the other binds, and nothing is
// refused or filed as an orphan.
//
// Every other shape stays #1641's collision: no record, a record naming
// an object neither claimant is, a record carrying no identity at all (a
// type whose record renders none, kubernetes_config_map_v1 among them),
// and three claimants where the record settles only one.

const k8sDeposedProvider = `provider["registry.opentofu.org/hashicorp/kubernetes"]`

func deposedCfgA(importID string) map[string]projection.DeposedRecord {
	return map[string]projection.DeposedRecord{"00000001": {ImportID: importID, Provider: k8sDeposedProvider}}
}

// k8sDeposedSweep runs the Kubernetes leg over objects, all claiming cfg,
// with the record and the configuration shape the case names.
func k8sDeposedSweep(t *testing.T, cfg addrs.AbsResourceInstance, objects []kubesweep.Object, resolutions []identity.Resolution, refused map[string]bool, deposed map[string]map[string]projection.DeposedRecord) (*Result, tfdiags.Diagnostics) {
	t.Helper()
	sweeper := &stubSweeper{kinds: []kubesweep.Kind{configMapKind()}, objects: map[string][]kubesweep.Object{"ConfigMap": objects}}
	req := Request{
		Estate:         "m1116",
		Sweepers:       []Sweeper{KubernetesSweep{Client: sweeper, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
		Resolutions:    resolutions,
		NodeRefused:    refused,
		DeposedRecords: deposed,
	}
	res := &Result{}
	res.Resolutions = append(res.Resolutions, req.Resolutions...)
	return res, sweepKubernetes(context.Background(), req, res)
}

func hasCollision(diags tfdiags.Diagnostics) bool {
	for _, d := range diags {
		if d.Severity() == tfdiags.Error && d.Description().Summary == problemSummaries[ProblemCollision] {
			return true
		}
	}
	return false
}

// kubernetesDeposedShapes is the two positions #1641's collision covers:
// the node reads the name, or the configuration names a third object.
func kubernetesDeposedShapes(cfg addrs.AbsResourceInstance) []struct {
	name        string
	resolutions []identity.Resolution
	refused     map[string]bool
} {
	return []struct {
		name        string
		resolutions []identity.Resolution
		refused     map[string]bool
	}{
		{
			name:    "the node reads the name (#1539's shape)",
			refused: map[string]bool{cfg.String(): true},
		},
		{
			name:        "the configuration names a third object",
			resolutions: []identity.Resolution{{Addr: cfg, Class: identity.ClassConcrete, ImportID: "ns/cfg-c", IdentityValues: map[string]string{"name": "cfg-c", "namespace": "ns"}}},
		},
	}
}

func TestKubernetesSweepDeposedRecordSettlesTheCollision(t *testing.T) {
	cfg := k8sInstance(t, "kubernetes_config_map", "cfg")
	for _, tc := range kubernetesDeposedShapes(cfg) {
		t.Run(tc.name, func(t *testing.T) {
			res, diags := k8sDeposedSweep(t, cfg, []kubesweep.Object{
				labelled("ConfigMap", "ns", "cfg-a", cfg.String()),
				labelled("ConfigMap", "ns", "cfg-b", cfg.String()),
			}, tc.resolutions, tc.refused, map[string]map[string]projection.DeposedRecord{cfg.String(): deposedCfgA("ns/cfg-a")})

			if hasCollision(diags) {
				t.Fatalf("the record names ns/cfg-a as %s's deposed object, and the sweep still refuses: %v", cfg, diags.ErrWithWarnings())
			}
			if len(res.Orphans) != 0 {
				t.Errorf("orphans = %v; the deposed object is destroyed as the address's deposed half, and the other is the block's", res.Orphans)
			}
			if len(res.DeposedBindings) != 1 {
				t.Fatalf("deposed bindings = %+v, want exactly ns/cfg-a at %s", res.DeposedBindings, cfg)
			}
			db := res.DeposedBindings[0]
			if db.Addr.String() != cfg.String() || db.ImportID != "ns/cfg-a" || string(db.DeposedKey) != "00000001" || db.Provider.Provider.Type != "kubernetes" {
				t.Errorf("deposed binding = %+v, want ns/cfg-a under key 00000001 at %s through the kubernetes provider", db, cfg)
			}
			if len(res.Bindings) != 1 || res.Bindings[0].Addr.String() != cfg.String() || res.Bindings[0].ImportID != "ns/cfg-b" {
				t.Fatalf("bindings = %+v, want ns/cfg-b bound at %s", res.Bindings, cfg)
			}
			if !res.KubernetesAddressBound[cfg.String()] {
				t.Errorf("KubernetesAddressBound = %v, want %s", res.KubernetesAddressBound, cfg)
			}
			found := false
			for _, r := range res.Resolutions {
				if r.Addr.String() == cfg.String() {
					found = true
					if r.Class != identity.ClassConcrete || r.ImportID != "ns/cfg-b" {
						t.Errorf("resolution at %s = %+v, want concrete ns/cfg-b", cfg, r)
					}
				}
			}
			if !found {
				t.Errorf("no resolution at %s", cfg)
			}
		})
	}
}

// TestKubernetesSweepDeposedRecordControls: every shape in which the record
// does not settle which claimant is the deposed one keeps #1641's collision,
// binds nothing and files no orphan.
func TestKubernetesSweepDeposedRecordControls(t *testing.T) {
	cfg := k8sInstance(t, "kubernetes_config_map", "cfg")
	two := []kubesweep.Object{
		labelled("ConfigMap", "ns", "cfg-a", cfg.String()),
		labelled("ConfigMap", "ns", "cfg-b", cfg.String()),
	}
	three := append(append([]kubesweep.Object{}, two...), labelled("ConfigMap", "ns", "cfg-x", cfg.String()))
	for _, ctl := range []struct {
		name    string
		objects []kubesweep.Object
		deposed map[string]map[string]projection.DeposedRecord
	}{
		{name: "no deposed record", objects: two},
		{name: "a deposed record naming a different object", objects: two, deposed: map[string]map[string]projection.DeposedRecord{cfg.String(): deposedCfgA("ns/cfg-z")}},
		{name: "a deposed record for another address", objects: two, deposed: map[string]map[string]projection.DeposedRecord{"kubernetes_config_map.other": deposedCfgA("ns/cfg-a")}},
		{name: "a deposed record carrying no identity", objects: two, deposed: map[string]map[string]projection.DeposedRecord{cfg.String(): {"00000001": {Provider: k8sDeposedProvider}}}},
		{name: "three claimants, the record settles one", objects: three, deposed: map[string]map[string]projection.DeposedRecord{cfg.String(): deposedCfgA("ns/cfg-a")}},
	} {
		for _, shape := range kubernetesDeposedShapes(cfg) {
			t.Run(ctl.name+"/"+shape.name, func(t *testing.T) {
				res, diags := k8sDeposedSweep(t, cfg, ctl.objects, shape.resolutions, shape.refused, ctl.deposed)
				if !hasCollision(diags) {
					t.Errorf("want an error %q, got: %v", problemSummaries[ProblemCollision], diags.ErrWithWarnings())
				}
				if len(res.Orphans) != 0 || len(res.Bindings) != 0 || len(res.DeposedBindings) != 0 || len(res.KubernetesAddressBound) != 0 {
					t.Errorf("orphans %v, bindings %v, deposed %v, bound %v; the record does not settle which object is the block's, so nothing may be proposed", res.Orphans, res.Bindings, res.DeposedBindings, res.KubernetesAddressBound)
				}
			})
		}
	}
}

// TestKubernetesAddressBindingsAgreesOnADeposedRecord: live-ls asks the same
// rule. With the record settling the collision it reports the survivor bound
// and the deposed object not bound (it is the address's deposed half, not
// its object); with no record, neither, as before.
func TestKubernetesAddressBindingsAgreesOnADeposedRecord(t *testing.T) {
	cfg := k8sInstance(t, "kubernetes_config_map", "cfg")
	undeclared := []UndeclaredObject{
		{Kind: configMapKind(), TypeName: "kubernetes_config_map", Object: labelled("ConfigMap", "ns", "cfg-a", cfg.String())},
		{Kind: configMapKind(), TypeName: "kubernetes_config_map", Object: labelled("ConfigMap", "ns", "cfg-b", cfg.String())},
	}
	listed := ListedObjects{}
	listed.Add("ConfigMap", "ns/cfg-a")
	listed.Add("ConfigMap", "ns/cfg-b")
	declared := DeclaredKubernetesObjects(nil, k8sTypes(), "kubernetes_manifest")
	req := Request{Estate: "m1116", NodeRefused: map[string]bool{cfg.String(): true}, DeposedRecords: map[string]map[string]projection.DeposedRecord{cfg.String(): deposedCfgA("ns/cfg-a")}}
	got := KubernetesAddressBindings(req, "kubernetes_manifest", declared, listed, undeclared)
	if len(got) != 1 || got[1].String() != cfg.String() {
		t.Fatalf("live-ls binds %v, want index 1 (ns/cfg-b) alone at %s", got, cfg)
	}
}
