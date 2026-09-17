// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// scopeExcluding is a [identity.Scope] that keeps everything except the
// named blocks, written the way [statelessTargetScope] writes one: a
// lookup in a set of [addrs.ConfigResource] strings.
func scopeExcluding(out ...string) identity.Scope {
	gone := make(map[string]bool, len(out))
	for _, s := range out {
		gone[s] = true
	}
	return func(addr addrs.ConfigResource) bool { return !gone[addr.String()] }
}

// TestKubernetesSweepDoesNotRefuseAManifestTargetingExcludes is GitHub
// issue #1176. #1079's fourth ruling refuses a manifest block whose kind
// the cluster does not serve, and it was written from the estate sweep,
// which looks at the WHOLE configuration because finding what nothing
// declares is its job. Under -target that view is the wrong one: a block
// the plan graph no longer holds is never planned, the provider never
// asks the cluster for its schema, and the block cannot fail this run
// whatever the cluster serves - so the refusal names a resource the
// operator excluded on purpose and cannot act on.
//
// This is reference-k8s-cert-manager's greenfield stage exactly: 47
// bundle addresses go up first with -target, the three custom resources
// that need the CRDs those 47 install are excluded, and all three were
// refused before the pre-apply could start.
//
// The scope is not an off-switch. The second half of this test keeps one
// unserved block IN scope and requires it to be refused as before, so a
// change that simply stopped asking would fail here.
func TestKubernetesSweepDoesNotRefuseAManifestTargetingExcludes(t *testing.T) {
	cfg := loadConfig(t, "testdata/manifest-unserved")
	cm := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map_v1"}}

	// The same five resolutions the un-targeted test uses: crontab and the
	// two instances of the for_each block many name the unserved kind, cm
	// and plain do not.
	resolutions := func() []identity.Resolution {
		return []identity.Resolution{
			{Addr: k8sInstance(t, "kubernetes_manifest", "crontab"), Class: identity.ClassConcrete, ImportID: "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=my-crontab"},
			{Addr: k8sKeyedInstance(t, "kubernetes_manifest", "many", "a"), Class: identity.ClassConcrete, ImportID: "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=a"},
			{Addr: k8sKeyedInstance(t, "kubernetes_manifest", "many", "b"), Class: identity.ClassConcrete, ImportID: "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=b"},
			{Addr: k8sInstance(t, "kubernetes_manifest", "cm"), Class: identity.ClassConcrete, ImportID: "apiVersion=v1,kind=ConfigMap,namespace=smoke-crd,name=via-manifest"},
			{Addr: k8sInstance(t, "kubernetes_config_map_v1", "plain"), Class: identity.ClassConcrete, ImportID: "smoke-crd/plain"},
		}
	}
	newReq := func(sweeper kubesweep.Sweeper, scope identity.Scope) Request {
		return Request{
			Estate:                 "smoke-crd",
			Config:                 cfg,
			Kubernetes:             sweeper,
			KubernetesTypes:        []string{"kubernetes_config_map_v1", "kubernetes_manifest"},
			KubernetesManifestType: "kubernetes_manifest",
			Resolutions:            resolutions(),
			Scope:                  scope,
		}
	}

	t.Run("excluded", func(t *testing.T) {
		sweeper := &stubSweeper{kinds: []kubesweep.Kind{cm}, notServed: map[string]bool{"stable.example.com/v1 CronTab": true}}
		req := newReq(sweeper, scopeExcluding("kubernetes_manifest.crontab", "kubernetes_manifest.many"))
		diags := sweepKubernetes(context.Background(), req, &Result{})
		for _, d := range diags {
			t.Errorf("a -target run raised %s %q over a block it excludes: %s", d.Severity(), d.Description().Summary, d.Description().Detail)
		}
		// Silence, not a warning: the estate this was found on takes the
		// targeted route on every run, so a warning would be permanent.
		if sweeper.asked != 1 {
			t.Errorf("Serves asked %d times, want 1 - only the in-scope ConfigMap pair; the cluster should not be asked about a kind on behalf of a block this run cannot plan", sweeper.asked)
		}
	})

	t.Run("one still in scope", func(t *testing.T) {
		sweeper := &stubSweeper{kinds: []kubesweep.Kind{cm}, notServed: map[string]bool{"stable.example.com/v1 CronTab": true}}
		req := newReq(sweeper, scopeExcluding("kubernetes_manifest.many"))
		diags := sweepKubernetes(context.Background(), req, &Result{})
		var refusals int
		for _, d := range diags {
			if d.Description().Summary == SummaryKubernetesKindNotServed {
				refusals++
				if !strings.Contains(d.Description().Detail, "kubernetes_manifest.crontab declares") {
					t.Errorf("the surviving refusal is not the in-scope block's: %s", d.Description().Detail)
				}
			} else if d.Severity() == tfdiags.Error {
				t.Errorf("unexpected error %q: %s", d.Description().Summary, d.Description().Detail)
			}
		}
		if refusals != 1 {
			t.Fatalf("refusals = %d, want 1: excluding one of the two unserved blocks must not silence the other", refusals)
		}
	})

	t.Run("nil scope is unchanged", func(t *testing.T) {
		sweeper := &stubSweeper{kinds: []kubesweep.Kind{cm}, notServed: map[string]bool{"stable.example.com/v1 CronTab": true}}
		diags := sweepKubernetes(context.Background(), newReq(sweeper, nil), &Result{})
		var refusals int
		for _, d := range diags {
			if d.Description().Summary == SummaryKubernetesKindNotServed {
				refusals++
			}
		}
		if refusals != 2 {
			t.Fatalf("refusals = %d, want 2: an untargeted run must behave exactly as it did before #1176", refusals)
		}
	})
}

// TestClassifyOrphansWithholdsARemovalTargetingExcludes is GitHub issue
// #1176's second half, and the ruling recorded in [classifyOrphans]: the
// sweep's removal leg runs under -target - its listing is also what
// verifies declared instances and fills the unclaimed inventory - but it
// proposes a removal only for an orphan whose own block the run's
// targeting keeps. Proposing to destroy a live object during a run the
// operator deliberately narrowed is the manifest refusal's shape in the
// destructive direction.
//
// The withheld orphan still appears in the result with a reason, so the
// operator is told what a full run would propose; only the removal is
// suppressed.
func TestClassifyOrphansWithholdsARemovalTargetingExcludes(t *testing.T) {
	const kept, excluded = "aws_subnet.kept", "aws_subnet.excluded"

	orphans := func() []OwnedResource {
		return []OwnedResource{
			{TypeName: "aws_subnet", ImportID: "subnet-0kept", Marker: kept, Normalized: kept, Swept: true},
			{TypeName: "aws_subnet", ImportID: "subnet-0excluded", Marker: excluded, Normalized: excluded, Swept: true},
		}
	}
	removals := func(t *testing.T, scope identity.Scope) map[string]OwnedResource {
		t.Helper()
		res := &Result{Verdicts: Verdicts{Orphans: orphans()}}
		if diags := classifyOrphans(t.Context(), Request{Estate: "target-scope", Scope: scope}, listclient.Schemas{}, res); diags.HasErrors() {
			t.Fatalf("classifyOrphans raised errors: %s", diags.Err())
		}
		out := map[string]OwnedResource{}
		for _, o := range res.Orphans {
			out[o.Normalized] = o
		}
		if len(out) != 2 {
			t.Fatalf("got %d orphans back, want 2", len(out))
		}
		return out
	}

	t.Run("nil scope proposes both", func(t *testing.T) {
		got := removals(t, nil)
		for _, n := range []string{kept, excluded} {
			if !got[n].Removal {
				t.Errorf("an untargeted run did not propose the removal of %s (withheld: %q); this test's baseline is wrong", n, got[n].Withheld)
			}
		}
	})

	t.Run("targeted proposes only the in-scope one", func(t *testing.T) {
		got := removals(t, scopeExcluding(excluded))
		if !got[kept].Removal {
			t.Errorf("the in-scope orphan was not proposed for removal (withheld: %q); -target must not silence the removals it keeps", got[kept].Withheld)
		}
		o := got[excluded]
		if o.Removal {
			t.Fatal("a -target run proposed destroying a live object whose block the run excludes")
		}
		if !strings.Contains(o.Withheld, "-target") {
			t.Errorf("the withholding reason does not say targeting caused it: %q", o.Withheld)
		}
		if !strings.Contains(o.Withheld, excluded) {
			t.Errorf("the withholding reason does not name the block: %q", o.Withheld)
		}
	})
}
