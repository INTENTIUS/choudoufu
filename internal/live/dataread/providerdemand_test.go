// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// TestProviderConfigDemandReadsThroughBothHops is issue #313's whole chain,
// through this phase's own entry points rather than managedproj's or
// liveModuleEvaluator's directly: a PROVIDER BLOCK's own argument demands a
// data source ([providerConfigDataDemand]'s own walk, never probed by
// identity resolution because nothing about a provider block is an
// identity-bearing position), that data source's own argument crosses a
// child module's output, and that output's own expression reads a managed
// resource attribute no literal argument covers.
//
// Without a live read, AnalyzeProviderConfigs must still leave the source
// ineligible - a provider block existing is not license to invent what it
// needs. With one supplied, both AnalyzeProviderConfigs and
// ReadProviderConfigs resolve it, and the value ReadProviderConfigs hands
// back is asserted by cty equality against what the live read supplied.
func TestProviderConfigDemandReadsThroughBothHops(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "provider-config-demand"), nil)

	demand := providerConfigDataDemand(cfg)
	if len(demand) != 1 || demand[0].resource.Type != "aws_zone" || demand[0].resource.Name != "of_cluster" {
		t.Fatalf("providerConfigDataDemand = %#v, want exactly data.aws_zone.of_cluster, demanded by the provider block", demand)
	}
	if demand[0].neededBy != `provider "clusterauth"` {
		t.Fatalf("neededBy = %q, want the provider block's own name", demand[0].neededBy)
	}

	baseline := AnalyzeProviderConfigs(context.Background(), cfg, Options{})
	src, ok := baseline.SourceFor(addrs.RootModule, dataAddr("aws_zone", "of_cluster"))
	if !ok {
		t.Fatalf("data.aws_zone.of_cluster was not classified at all")
	}
	if !baseline.Scoped() {
		t.Fatalf("AnalyzeProviderConfigs must produce a SCOPED analysis, matching AnalyzeRootOutputs' own contract")
	}
	if src.Eligible {
		t.Fatalf("aws_eks_cluster.this.id is provider-assigned and no live values were supplied; the baseline must still refuse")
	}

	live := map[string]cty.Value{
		"module.child.aws_eks_cluster.this": cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("prod-cluster"),
		}),
	}

	analysis := AnalyzeProviderConfigs(context.Background(), cfg, Options{LiveManagedResults: live})
	src, ok = analysis.SourceFor(addrs.RootModule, dataAddr("aws_zone", "of_cluster"))
	if !ok {
		t.Fatalf("data.aws_zone.of_cluster was not classified at all with live values supplied")
	}
	if !src.Eligible {
		t.Fatalf("a real live read of module.child.aws_eks_cluster.this covers id, which covers module.child.cluster_id; refused: %s", src.ReasonDetail)
	}

	var sawNames []string
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			name := req.Config.GetAttr("name")
			if name.IsNull() || !name.IsKnown() {
				t.Fatalf("aws_zone was read with an unknown name: %#v", name)
			}
			sawNames = append(sawNames, name.AsString())
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"name":    name,
				"zone_id": cty.StringVal("Z-" + name.AsString()),
			})}
		},
	}

	results, diags := ReadProviderConfigs(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}
	if len(sawNames) != 1 || sawNames[0] != "prod-cluster" {
		t.Fatalf("the provider was read with names %v, want [prod-cluster] - the live value carried through both hops", sawNames)
	}
	got, ok := results["data.aws_zone.of_cluster"]
	if !ok {
		t.Fatalf("no result under data.aws_zone.of_cluster; keys: %v", keysOf(results))
	}
	want := cty.StringVal("Z-prod-cluster")
	if zoneID := got.GetAttr("zone_id"); !zoneID.RawEquals(want) {
		t.Fatalf("zone_id is %#v, want %#v - the value that fed the provider block's own argument", zoneID, want)
	}
}

// TestProviderConfigDemandIsScopedNotFatal: a provider block this phase
// cannot read costs the one provider configuration that wanted it, never
// the whole run - internal/command's own "Provider unavailable" diagnostic,
// unchanged, is what reports it when something actually tries to configure
// that provider. read() must skip it in silence, the same contract
// ReadForOutputs gives root-output demand.
func TestProviderConfigDemandIsScopedNotFatal(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "provider-config-demand"), nil)
	analysis := AnalyzeProviderConfigs(context.Background(), cfg, Options{})

	_, diags := ReadProviderConfigs(context.Background(), cfg, analysis, &fakeProviders{provider: &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
	}})
	if diags.HasErrors() {
		t.Fatalf("an ineligible source under the provider-config demand class must be skipped, not refused: %s", diags.Err())
	}
}

// TestManagedRefusalsFeedIdentityDemandedManagedReads proves the claim this
// whole class rests on: [identity.DemandedManagedReads] needs no dataread-
// specific adapter, because [Analysis.ManagedRefusals] carries the exact
// [configs.RefusedReference]-tagged diagnostics identity's own resolution
// raises for the identical shape. Feeding it dataread's diagnostics
// directly, alongside a resolution that has already settled
// aws_eks_cluster.this's own identity, must name exactly that one instance
// as a complete demand.
func TestManagedRefusalsFeedIdentityDemandedManagedReads(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "provider-config-demand"), nil)

	analysis := AnalyzeProviderConfigs(context.Background(), cfg, Options{})
	refusals := analysis.ManagedRefusals()
	if len(refusals) == 0 {
		t.Fatalf("AnalyzeProviderConfigs recorded no managed refusal for a source that needs aws_eks_cluster.this.id")
	}

	// resolveDiags is expected to carry errors here: aws_cloudwatch_log_group.
	// marker's own name argument reads data.aws_zone.of_cluster too, which a
	// bare ResolveWith with no DataResults cannot cover any more than
	// AnalyzeProviderConfigs's own baseline could. What matters for this
	// test is that aws_eks_cluster.this's OWN identity - independent of that
	// failure - still resolved into the partial result, which is exactly
	// the shape identity.DemandedManagedReads's own doc comment says a
	// caller may hand it ("result may be nil - a pass that failed outright
	// still names its demand").
	resolutions, _ := identity.ResolveWith(context.Background(), cfg, identity.Context{})

	demand := identity.DemandedManagedReads(resolutions, refusals)
	if len(demand) != 1 {
		t.Fatalf("identity.DemandedManagedReads(resolutions, analysis.ManagedRefusals()) = %d entries, want exactly 1: %#v", len(demand), demand)
	}
	d := demand[0]
	if d.Resource.Type != "aws_eks_cluster" || d.Resource.Name != "this" {
		t.Fatalf("demanded resource = %s, want aws_eks_cluster.this", d.Resource.String())
	}
	if !d.Module.Equal(addrs.Module{"child"}) {
		t.Fatalf("demanded module = %s, want module.child", d.Module.String())
	}
	if !d.Complete {
		t.Fatalf("demand not marked complete: %#v", d)
	}
	if len(d.Instances) != 1 {
		t.Fatalf("demand names %d instances, want exactly 1 (aws_eks_cluster.this has no count/for_each): %#v", len(d.Instances), d.Instances)
	}
}
