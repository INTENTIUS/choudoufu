// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1860: claim 9, "unchanged is free", on Kubernetes - the
// projection half, counting the requests the draft
// live/smoke/drafts/k8s-unchanged-is-free.sh counts on the wire.
//
// The hit rule itself is provider-agnostic ([builder.cacheHit]); what was
// missing on Kubernetes was the vouch, which the sweep now records
// (discovery's TestKubernetesSweepVouchesTheDeclaredObjectsItLists). These
// tests pin the other end: a kubernetes_config_map the sweep vouched for,
// with a fresh cache beside it, costs no provider call under
// -refresh=false, and each way of turning that off - no vouch (the leg
// before #1860), reads = "full" - pays the import and the read again
// without changing the projected object.

// k8sCountingCluster is [k8sConflictCluster] with the provider calls a
// cache hit exists to avoid counted.
type k8sCountingCluster struct {
	mu      sync.Mutex
	objects map[string]cty.Value
	imports int
	reads   int
}

func (c *k8sCountingCluster) provider() (addrs.AbsProviderConfig, *tofu.MockProvider) {
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{configMapTestType: configMapTypeSchema()},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.imports++
		obj, ok := c.objects[r.Target.ID]
		if !ok {
			return providers.ImportResourceStateResponse{}
		}
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{TypeName: r.TypeName, State: obj}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.reads++
		obj, ok := c.objects[r.PriorState.GetAttr("id").AsString()]
		if !ok {
			return providers.ReadResourceResponse{NewState: cty.NullVal(r.PriorState.Type())}
		}
		return providers.ReadResourceResponse{NewState: obj}
	}
	return provAddr, p
}

const k8sCacheConfig = `
resource "kubernetes_config_map" "app" {
  metadata {
    name      = "app-config"
    namespace = "smoke-k8s"
  }
  data = {
    greeting = "hello"
  }
}
`

// k8sCachedState is the cache the previous apply wrote: the same object
// the cluster holds, which is what "unchanged" means.
func k8sCachedState(t *testing.T, addr addrs.AbsResourceInstance, provAddr addrs.AbsProviderConfig, obj cty.Value) *states.State {
	t.Helper()
	src, err := (&states.ResourceInstanceObject{Value: obj, Status: states.ObjectReady}).Encode(configMapTypeSchema().Block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding the cached object: %s", err)
	}
	s := states.NewState()
	s.RootModule().SetResourceInstanceCurrent(addr.Resource, src, provAddr, addrs.NoKey)
	return s
}

// TestKubernetesUnchangedPlanIsServedFromTheCache is the claim's three
// arms through [BuildWith], each counting the provider calls the plan
// made.
func TestKubernetesUnchangedPlanIsServedFromTheCache(t *testing.T) {
	const id = "smoke-k8s/app-config"
	addr := mustAddr(t, `kubernetes_config_map.app`)
	live := k8sLiveConfigMap(map[string]string{markers.TagEstate: policyEstate})

	type run struct {
		res            *Result
		hits           int
		imports, reads int
	}
	plan := func(t *testing.T, verified, servesReads bool) run {
		t.Helper()
		cfg := refSeedConfig(t, k8sCacheConfig)
		cluster := &k8sCountingCluster{objects: map[string]cty.Value{id: live}}
		provAddr, p := cluster.provider()
		own := &Ownership{Estate: policyEstate}
		if verified {
			// What discovery's MarkerVerified carries since #1860: the
			// sweep's label-selected list returned this object at the
			// natural key the block declares.
			own.Verified = map[string]bool{addr.String(): true}
		}
		res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
			{Addr: addr, Class: identity.ClassConcrete, ImportID: id},
		}, SingleProvider(provAddr, p), Options{
			Ownership:        own,
			StateCache:       k8sCachedState(t, addr, provAddr, live),
			CacheServesReads: servesReads,
		})
		assertNoErrors(t, diags)
		if !res.Has(addr) {
			t.Fatalf("the ConfigMap is missing from the projection:\n%s", res)
		}
		return run{res: res, hits: res.CacheHits(), imports: cluster.imports, reads: cluster.reads}
	}

	selective := plan(t, true, true)
	full := plan(t, true, false)
	unvouched := plan(t, false, true)

	if selective.hits != 1 || selective.imports != 0 || selective.reads != 0 {
		t.Errorf("vouched, -refresh=false: %d hits, %d imports, %d reads; want 1, 0, 0 - unchanged was not free", selective.hits, selective.imports, selective.reads)
	}
	if full.hits != 0 || full.imports == 0 || full.reads == 0 {
		t.Errorf("reads = \"full\": %d hits, %d imports, %d reads; want 0 hits and the import and read paid - the off switch does not switch off", full.hits, full.imports, full.reads)
	}
	if unvouched.hits != 0 || unvouched.imports == 0 || unvouched.reads == 0 {
		t.Errorf("not vouched (the Kubernetes sweep before #1860): %d hits, %d imports, %d reads; want 0 hits and the import and read paid - the cache served what nothing vouched for", unvouched.hits, unvouched.imports, unvouched.reads)
	}
	if selective.imports+selective.reads >= full.imports+full.reads {
		t.Errorf("the selective plan cost %d calls against the full plan's %d: no saving", selective.imports+selective.reads, full.imports+full.reads)
	}

	// The price may change, the answer may not: the object the plan
	// diffs against is the same whether the cache or the cluster
	// supplied it.
	ty := configMapTypeSchema().Block.ImpliedType()
	decode := func(t *testing.T, r run) cty.Value {
		t.Helper()
		is := r.res.State.ResourceInstance(addr)
		if is == nil || is.Current == nil {
			t.Fatal("no current object in the projection")
		}
		obj, err := is.Current.Decode(ty)
		if err != nil {
			t.Fatalf("decoding the projected object: %s", err)
		}
		return obj.Value
	}
	got, want := decode(t, selective), decode(t, full)
	for _, attr := range []string{"id", "data", "metadata"} {
		if !got.GetAttr(attr).RawEquals(want.GetAttr(attr)) {
			t.Errorf("%s differs between the cache-served and the read plan:\n  cache: %#v\n  read:  %#v", attr, got.GetAttr(attr), want.GetAttr(attr))
		}
	}
}
