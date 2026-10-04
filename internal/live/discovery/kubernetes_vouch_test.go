// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1860: claim 9, "unchanged is free", on Kubernetes. A
// -refresh=false plan serves an instance from the state cache only when
// [Result.MarkerVerified] holds it, and the Kubernetes leg joined every
// declared object by its kind and natural key and recorded nothing, so an
// unchanged estate's second plan paid every read
// (live/smoke/drafts/k8s-unchanged-is-free.sh).
//
// Red against the leg before #1860: restore
//
//	if _, isDeclared := declared.Declares(k.Kind, key); isDeclared {
//		continue
//	}
//
// and TestKubernetesSweepVouchesTheDeclaredObjectsItLists finds an empty
// VerifiedDeclared.

func vouched(res *Result) []string {
	var out []string
	for a := range res.MarkerVerified() {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// TestKubernetesSweepVouchesTheDeclaredObjectsItLists is the green half:
// every declared object the label-selected list returned is vouched, a
// built-in block's and a manifest block's alike, with or without the
// address annotation (an object an older build made carries none) - and
// the vouch costs no request beyond the one list per kind the sweep was
// already making.
func TestKubernetesSweepVouchesTheDeclaredObjectsItLists(t *testing.T) {
	cm := k8sInstance(t, "kubernetes_config_map_v1", "app")
	legacy := k8sInstance(t, "kubernetes_config_map", "legacy")
	tab := k8sInstance(t, "kubernetes_manifest", "crontab")
	sweeper := &stubSweeper{
		kinds: []kubesweep.Kind{configMapKind(), cronTabKind()},
		objects: map[string][]kubesweep.Object{
			"ConfigMap": {
				labelled("ConfigMap", "ns", "app", markers.EscapeAddress(cm.String())),
				labelled("ConfigMap", "ns", "legacy", ""),
			},
			"CronTab": {labelled("CronTab", "ns", "tab", markers.EscapeAddress(tab.String()))},
		},
	}
	req := Request{
		Estate:   "m1116",
		Sweepers: []Sweeper{KubernetesSweep{Client: sweeper, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
		Resolutions: []identity.Resolution{
			{Addr: cm, Class: identity.ClassConcrete, ImportID: "ns/app"},
			{Addr: legacy, Class: identity.ClassConcrete, ImportID: "ns/legacy"},
			{Addr: tab, Class: identity.ClassConcrete, ImportID: kubesweep.ManifestImportID("stable.example.com/v1", "CronTab", "ns", "tab")},
		},
	}
	res := &Result{}
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	want := []string{cm.String(), legacy.String(), tab.String()}
	sort.Strings(want)
	got := vouched(res)
	if len(got) != len(want) {
		t.Fatalf("vouched = %v, want %v: an unvouched instance is never served from the cache, so an unchanged estate's -refresh=false plan reads it", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("vouched = %v, want %v", got, want)
		}
	}
	if len(res.Orphans) != 0 {
		t.Errorf("orphans = %v, want none: every listed object is declared", res.Orphans)
	}

	// The request count: one list per kind, and nothing else. The vouch
	// is read off the response the sweep already had.
	if len(sweeper.listed) != 2 {
		t.Errorf("list calls = %v, want exactly one per kind (2): the vouch must cost no request", sweeper.listed)
	}
	if sweeper.asked != 1 {
		// The CronTab block's kind is asked about once by
		// refuseUnservedManifests, as before this change.
		t.Errorf("Serves calls = %d, want 1 (the manifest block's kind, unchanged by the vouch)", sweeper.asked)
	}
	if len(sweeper.dryRuns) != 0 {
		t.Errorf("dry runs = %v, want none", sweeper.dryRuns)
	}
}

// TestKubernetesSweepWithholdsTheVouchWhereItCouldBeWrong is the other
// half: each shape where the listed object might not be the declared
// instance's, or might not outlive the plan, takes the ordinary read.
// Every case fails toward reading; none may vouch.
func TestKubernetesSweepWithholdsTheVouchWhereItCouldBeWrong(t *testing.T) {
	cm := k8sInstance(t, "kubernetes_config_map_v1", "app")
	other := k8sInstance(t, "kubernetes_config_map_v1", "other")
	viaManifest := k8sInstance(t, "kubernetes_manifest", "app")

	cases := map[string]struct {
		object      kubesweep.Object
		resolutions []identity.Resolution
	}{
		"annotated for another declared block": {
			object: labelled("ConfigMap", "ns", "app", markers.EscapeAddress(other.String())),
			resolutions: []identity.Resolution{
				{Addr: cm, Class: identity.ClassConcrete, ImportID: "ns/app"},
				{Addr: other, Class: identity.ClassConcrete, ImportID: "ns/other"},
			},
		},
		"annotation does not parse": {
			object: labelled("ConfigMap", "ns", "app", "not an address"),
			resolutions: []identity.Resolution{
				{Addr: cm, Class: identity.ClassConcrete, ImportID: "ns/app"},
			},
		},
		"terminating": {
			object: func() kubesweep.Object {
				o := labelled("ConfigMap", "ns", "app", markers.EscapeAddress(cm.String()))
				o.DeletionTimestamp = "2026-10-03T00:00:00Z"
				o.Finalizers = []string{"example.com/hold"}
				return o
			}(),
			resolutions: []identity.Resolution{
				{Addr: cm, Class: identity.ClassConcrete, ImportID: "ns/app"},
			},
		},
		"declared by two blocks": {
			object: labelled("ConfigMap", "ns", "app", ""),
			resolutions: []identity.Resolution{
				{Addr: cm, Class: identity.ClassConcrete, ImportID: "ns/app"},
				{Addr: viaManifest, Class: identity.ClassConcrete, ImportID: kubesweep.ManifestImportID("v1", "ConfigMap", "ns", "app")},
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sweeper := &stubSweeper{
				kinds:   []kubesweep.Kind{configMapKind()},
				objects: map[string][]kubesweep.Object{"ConfigMap": {tc.object}},
			}
			req := Request{
				Estate:      "m1116",
				Sweepers:    []Sweeper{KubernetesSweep{Client: sweeper, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
				Resolutions: tc.resolutions,
			}
			res := &Result{}
			if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
				t.Fatalf("unexpected errors: %s", diags.Err())
			}
			if len(res.VerifiedDeclared) != 0 {
				t.Errorf("VerifiedDeclared = %v, want none: a vouch here could serve a cached answer for an object that is not this instance's, or is going away", res.VerifiedDeclared)
			}
		})
	}
}

// TestKubernetesSweepVouchesOnlyForItsOwnProviderConfiguration: on a run
// with two kubernetes provider configurations each pass lists its own
// cluster, and a sighting through one says nothing about a block the
// other configuration manages - which may name another cluster entirely.
// Only the owning pass vouches; Merge keeps that vouch.
func TestKubernetesSweepVouchesOnlyForItsOwnProviderConfiguration(t *testing.T) {
	cfg := k8sTwoConfig(t)
	elsewhere := k8sInstance(t, "kubernetes_config_map_v1", "elsewhere")
	resolutions := []identity.Resolution{
		{Addr: elsewhere, Class: identity.ClassConcrete, ImportID: "ns/elsewhere"},
	}
	var passes []Pass
	perPass := map[string][]string{}
	for _, scope := range []addrs.AbsProviderConfig{k8sProvider(""), k8sProvider("other")} {
		sweeper := &stubSweeper{
			kinds:   []kubesweep.Kind{configMapKind()},
			objects: map[string][]kubesweep.Object{"ConfigMap": {labelled("ConfigMap", "ns", "elsewhere", "")}},
		}
		res, diags := Discover(context.Background(), Request{
			Estate:        "m1116",
			Config:        cfg,
			Provider:      newFakeCloud(),
			Resolutions:   resolutions,
			ScopeProvider: scope,
			Sweepers:      []Sweeper{KubernetesSweep{Client: sweeper, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
		})
		if diags.HasErrors() {
			t.Fatalf("pass %s: unexpected errors: %s", scope, diags.Err())
		}
		perPass[scope.String()] = vouched(res)
		passes = append(passes, Pass{Provider: scope, Result: res})
	}
	if got := perPass[k8sProvider("").String()]; len(got) != 0 {
		t.Errorf("the default configuration's pass vouched %v for a block kubernetes.other manages", got)
	}
	if got := perPass[k8sProvider("other").String()]; len(got) != 1 || got[0] != elsewhere.String() {
		t.Errorf("the owning pass vouched %v, want [%s]", got, elsewhere)
	}
	merged, _, diags := Merge("m1116", passes, false)
	if diags.HasErrors() {
		t.Fatalf("merge: unexpected errors: %s", diags.Err())
	}
	if !merged.MarkerVerified()[elsewhere.String()] {
		t.Errorf("Merge dropped the owning pass's vouch for %s", elsewhere)
	}
}
