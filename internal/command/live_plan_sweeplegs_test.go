// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// TestEverySubstrateSweepHasALeg (GitHub issue #1580): every family's
// sweep is served by a leg, so no family written into substrate.All plans
// with its removals unswept. Deleting a leg from sweepLegBuilders fails
// here by the family's name.
func TestEverySubstrateSweepHasALeg(t *testing.T) {
	for _, sub := range substrate.All {
		if _, ok := sweepLegBuilders[sub.Sweep()]; !ok {
			t.Errorf("provider family %s asks for the %q sweep and no leg serves it: its plans would list nothing and file a %s gap", sub.Name(), sub.Sweep(), discovery.SweepGapNoSweepLeg)
		}
	}
}

// TestSweepLegsChosenBySweepProperty: the AWS family gets the tagging-index
// legs with Request.Sweep on; a family whose sweep has no leg gets
// discovery.NoSweepLeg naming it, with Request.Sweep off. A provider no
// family claims is TestUnclaimedProviderGetsNoAWSLegs's.
func TestSweepLegsChosenBySweepProperty(t *testing.T) {
	ctx := context.Background()
	p := &statelessProviders{}
	addr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}

	legs, sweep, diags := p.statelessSweepLegs(ctx, substrate.AWS, true, addr)
	if diags.HasErrors() || !sweep || len(legs) != 1 || legs[0].Leg() != substrate.SweepTaggingIndex {
		t.Errorf("AWS family: legs %v, sweep %v, diags %v; want the tagging-index leg with Sweep on", legs, sweep, diags.Err())
	}

	legs, sweep, _ = p.statelessSweepLegs(ctx, graphFamily{substrate.AWS}, true, addr)
	if sweep || len(legs) != 1 {
		t.Fatalf("family with no leg: legs %v, sweep %v; want one NoSweepLeg with Sweep off", legs, sweep)
	}
	got, ok := legs[0].(discovery.NoSweepLeg)
	if !ok || got.Family != "graph" || got.Kind != "graph-query" {
		t.Errorf("family with no leg got %#v, want a NoSweepLeg naming graph and graph-query", legs[0])
	}
}

// graphFamily is a family whose sweep no leg serves.
type graphFamily struct{ substrate.Substrate }

func (graphFamily) Name() string           { return "graph" }
func (graphFamily) Sweep() substrate.Sweep { return "graph-query" }

// TestUnclaimedProviderGetsNoAWSLegs (GitHub issue #1707): a provider no
// family claims used to get the AWS tagging-index legs, which list the
// admission table's AWS and Kubernetes types through a provider that
// serves none of them - 1009 TYPE_NOT_LISTABLE gaps naming AWS types on
// one pass, measured on the discovery fixture. It now gets one named gap
// when its schema has a type a marker is written onto (the node stamp asks
// substrate.SurfaceOf, whatever the provider), since no leg lists those
// objects back; and no leg at all when it has none, since nothing it
// holds carries a marker. Request.Sweep stays on either way: the record
// store's removal legs are not a family's, and a google-only or
// record-only estate finds its deleted blocks through them.
func TestUnclaimedProviderGetsNoAWSLegs(t *testing.T) {
	ctx := context.Background()
	tagged := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name": {Type: cty.String, Required: true},
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}}}
	plain := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name":   {Type: cty.String, Required: true},
		"labels": {Type: cty.Map(cty.String), Optional: true},
	}}}
	azure := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}
	helm := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("helm")}
	schemaOf := func(types map[string]providers.Schema) providers.Factory {
		return providers.FactoryFixed(&tofu.MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: types,
		}})
	}
	p := newStatelessProviders(nil, plugins.NewLibrary(plugins.ProviderFactories{
		azure.Provider: schemaOf(map[string]providers.Schema{"azurerm_resource_group": tagged}),
		helm.Provider:  schemaOf(map[string]providers.Schema{"helm_release": plain}),
	}, nil))

	for _, addr := range []addrs.AbsProviderConfig{azure, helm} {
		if _, known := substrate.ForProvider(addr.Provider.Type); known {
			t.Fatalf("provider %s is claimed by a family; this test needs an unclaimed one", addr.Provider)
		}
	}

	legs, sweep, diags := p.statelessSweepLegs(ctx, nil, false, azure)
	if diags.HasErrors() {
		t.Fatalf("unclaimed provider with a tagged type: %v", diags.Err())
	}
	if !sweep {
		t.Errorf("unclaimed provider with a tagged type: Sweep off; the record store's removal legs would stop running")
	}
	if len(legs) != 1 {
		t.Fatalf("unclaimed provider with a tagged type: legs %#v, want one NoSweepLeg", legs)
	}
	gap, ok := legs[0].(discovery.NoSweepLeg)
	if !ok || gap.Family != azure.Provider.ForDisplay() || gap.Kind != "" {
		t.Errorf("unclaimed provider with a tagged type got %#v, want a NoSweepLeg naming %s and no family sweep", legs[0], azure.Provider.ForDisplay())
	}

	legs, sweep, diags = p.statelessSweepLegs(ctx, nil, false, helm)
	if diags.HasErrors() {
		t.Fatalf("unclaimed provider with no marked type: %v", diags.Err())
	}
	if !sweep {
		t.Errorf("unclaimed provider with no marked type: Sweep off; the record store's removal legs would stop running")
	}
	if legs == nil || len(legs) != 0 {
		t.Errorf("unclaimed provider with no marked type: legs %#v, want an empty, non-nil list (nil means the AWS default to discovery)", legs)
	}
}
