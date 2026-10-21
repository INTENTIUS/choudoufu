// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/gcpsweep"
	"github.com/intentius/choudoufu/internal/live/mv"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// live-mv's GCP destination search (GitHub issue #1119): before a GCP move
// rewrites a tofu-address label, internal/live/mv asks Cloud Asset
// Inventory whether anything else in the destination estate carries the
// new value. The client is the one a plan's GCP sweep leg builds
// ([projectionProviders.assetSearchLeg]), from the provider block's own
// credential arguments.
var _ mv.AssetSearchers = (*projectionProviders)(nil)

// AssetSearcher implements [mv.AssetSearchers]. An error is the reason no
// client exists, which mv reports in its refusal; it never answers with a
// nil client and no error.
func (p *projectionProviders) AssetSearcher(_ context.Context, addr addrs.AbsProviderConfig) (gcpsweep.AddressSearcher, string, error) {
	sub, ok := substrate.ForProvider(addr.Provider.Type)
	if !ok || sub.Sweep() != substrate.SweepAssetSearch {
		return nil, "", fmt.Errorf("provider %s is not searched through Cloud Asset Inventory", addr)
	}
	p.mu.Lock()
	val, configured := p.configVals[providerCacheKey(addr)]
	p.mu.Unlock()
	built, err := sub.NewSweeper(val, configured)
	if err != nil {
		return nil, "", err
	}
	client, isAsset := built.(substrate.AssetSearchSweeper)
	if !isAsset || client.Client == nil {
		return nil, "", fmt.Errorf("provider family %s built no Cloud Asset Inventory client", sub.Name())
	}
	searcher, isAddr := client.Client.(gcpsweep.AddressSearcher)
	if !isAddr {
		return nil, "", fmt.Errorf("provider family %s built a client that cannot search by address", sub.Name())
	}
	return searcher, client.Project, nil
}
