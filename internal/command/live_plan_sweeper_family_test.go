// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// clusterFamily is a third family that lists a cluster the way the
// Kubernetes family does ([substrate.SweepLabelList]) through a client of
// its own type: not [substrate.LabelListSweeper], but one that names the
// sweep it serves ([substrate.Sweeper.SweepKind]) and can list
// ([kubesweep.Sweeper]). Everything else is Kubernetes', by embedding.
type clusterFamily struct {
	substrate.Substrate
	built substrate.Sweeper
}

func (clusterFamily) Name() string { return "fakecluster" }
func (f clusterFamily) NewSweeper(cty.Value, bool) (substrate.Sweeper, error) {
	return f.built, nil
}

// clusterClient is clusterFamily's client. The embedded kubesweep.Sweeper
// is nil: building the leg never calls it.
type clusterClient struct {
	kubesweep.Sweeper
	kind substrate.Sweep
}

func (c clusterClient) SweepKind() substrate.Sweep { return c.kind }

var (
	_ substrate.Sweeper = clusterClient{}
	_ kubesweep.Sweeper = clusterClient{}
)

// TestSweeperPlugsInThroughTheInterface (GitHub issue #1742 item 3): a
// family's sweep client reaches its leg through [substrate.Sweeper], so a
// third family's own client type plugs in. Before, the only caller of
// NewSweeper asserted the concrete Kubernetes LabelListSweeper, refused
// every other client as "built no cluster client", and SweepKind had no
// caller at all. A client naming a sweep its family does not serve is
// still refused, by name.
func TestSweeperPlugsInThroughTheInterface(t *testing.T) {
	ctx := context.Background()
	addr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("fakecluster")}
	p := newProjectionProviders(nil, plugins.NewLibrary(plugins.ProviderFactories{
		addr.Provider: providers.FactoryFixed(&tofu.MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{},
		}}),
	}, nil))

	client := clusterClient{kind: substrate.SweepLabelList}
	legs, _, diags := p.liveSweepLegs(ctx, clusterFamily{Substrate: substrate.Kubernetes, built: client}, true, addr)
	if len(diags) != 0 {
		t.Errorf("a label-list client of the family's own type: %d diagnostics, first %q: %q", len(diags), diags[0].Description().Summary, diags[0].Description().Detail)
	}
	if len(legs) != 1 {
		t.Fatalf("legs %#v, want the label-list leg", legs)
	}
	leg, ok := legs[0].(discovery.KubernetesSweep)
	if !ok || leg.Client != kubesweep.Sweeper(client) {
		t.Errorf("leg %#v, want the label-list leg listing through the family's own client", legs[0])
	}

	wrong := clusterClient{kind: "graph-query"}
	_, _, diags = p.liveSweepLegs(ctx, clusterFamily{Substrate: substrate.Kubernetes, built: wrong}, true, addr)
	if len(diags) != 1 || !containsAll(diags[0].Description().Detail, "fakecluster", "graph-query", string(substrate.SweepLabelList)) {
		t.Errorf("a client for another sweep: diagnostics %v, want one warning naming the family, the client's sweep and the family's", diags)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
