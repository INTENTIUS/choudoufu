// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/substrate"
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
// legs with Request.Sweep on; a provider no family claims keeps exactly
// that; a family whose sweep has no leg gets discovery.NoSweepLeg naming
// it, with Request.Sweep off.
func TestSweepLegsChosenBySweepProperty(t *testing.T) {
	ctx := context.Background()
	p := &statelessProviders{}
	addr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}

	legs, sweep, diags := p.statelessSweepLegs(ctx, substrate.AWS, true, addr)
	if diags.HasErrors() || !sweep || len(legs) != 1 || legs[0].Leg() != substrate.SweepTaggingIndex {
		t.Errorf("AWS family: legs %v, sweep %v, diags %v; want the tagging-index leg with Sweep on", legs, sweep, diags.Err())
	}

	legs, sweep, _ = p.statelessSweepLegs(ctx, nil, false, addr)
	if !sweep || len(legs) != 1 || legs[0].Leg() != substrate.SweepTaggingIndex {
		t.Errorf("unclaimed provider: legs %v, sweep %v; want the tagging-index leg with Sweep on, as before families existed", legs, sweep)
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
