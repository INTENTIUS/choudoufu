// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// estateRouter is the plan-node seam of a composed run (GitHub issue #2059).
// The walk sees one configuration, where every estate is a module; each
// estate's resolver was built for that estate's own root. The router hands
// each call to the resolver of the module the address is in, with the
// module call's step taken off the address, so the resolver sees the
// root-relative address its estate has when it runs alone. That address is
// what the tofu-address marker and the record key are made from.
//
// An address outside every estate (there are none in a valid composed root,
// which holds module calls only) is passed through untouched.
type estateRouter struct {
	by map[string]*projection.NodeResolver
}

var (
	_ tofu.ResourceIdentityResolver  = (*estateRouter)(nil)
	_ tofu.ConfigValueAdjuster       = (*estateRouter)(nil)
	_ tofu.IgnoreChangesAdjuster     = (*estateRouter)(nil)
	_ tofu.AppliedMarkerVerifier     = (*estateRouter)(nil)
	_ tofu.CreateConfigValueAdjuster = (*estateRouter)(nil)
	_ tofu.AppliedMarkerWriter       = (*estateRouter)(nil)
	_ tofu.PriorConfigValueAdjuster  = (*estateRouter)(nil)
)

func (e *estateRouter) route(addr addrs.AbsResourceInstance) (*projection.NodeResolver, addrs.AbsResourceInstance, bool) {
	if len(addr.Module) == 0 {
		return nil, addr, false
	}
	n, ok := e.by[addr.Module[0].Name]
	if !ok || n == nil {
		return nil, addr, false
	}
	out := addr
	out.Module = append(addrs.ModuleInstance(nil), addr.Module[1:]...)
	return n, out, true
}

func (e *estateRouter) ResolveResourceIdentity(ctx context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (providers.ImportTarget, bool, tfdiags.Diagnostics) {
	n, a, ok := e.route(addr)
	if !ok {
		return providers.ImportTarget{}, false, nil
	}
	return n.ResolveResourceIdentity(ctx, a, config, schema)
}

func (e *estateRouter) AdjustConfigValue(ctx context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	n, a, ok := e.route(addr)
	if !ok {
		return config, nil
	}
	return n.AdjustConfigValue(ctx, a, config, schema)
}

func (e *estateRouter) AdjustCreateConfigValue(ctx context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	n, a, ok := e.route(addr)
	if !ok {
		return config, nil
	}
	return n.AdjustCreateConfigValue(ctx, a, config, schema)
}

func (e *estateRouter) AdjustConfigValueToPrior(ctx context.Context, addr addrs.AbsResourceInstance, evaluated, adjusted, prior cty.Value, schema providers.Schema) cty.Value {
	n, a, ok := e.route(addr)
	if !ok {
		return adjusted
	}
	return n.AdjustConfigValueToPrior(ctx, a, evaluated, adjusted, prior, schema)
}

func (e *estateRouter) AdjustIgnoreChanges(ctx context.Context, addr addrs.AbsResourceInstance, schema providers.Schema) []cty.Path {
	n, a, ok := e.route(addr)
	if !ok {
		return nil
	}
	return n.AdjustIgnoreChanges(ctx, a, schema)
}

func (e *estateRouter) VerifyAppliedMarkers(ctx context.Context, addr addrs.AbsResourceInstance, action plans.Action, planned, applied cty.Value, schema providers.Schema) tfdiags.Diagnostics {
	n, a, ok := e.route(addr)
	if !ok {
		return nil
	}
	return n.VerifyAppliedMarkers(ctx, a, action, planned, applied, schema)
}

func (e *estateRouter) WriteAppliedMarkers(ctx context.Context, addr addrs.AbsResourceInstance, provider addrs.AbsProviderConfig, action plans.Action, applied cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	n, a, ok := e.route(addr)
	if !ok {
		return applied, nil
	}
	if len(provider.Module) > 0 {
		provider.Module = append(addrs.Module(nil), provider.Module[1:]...)
	}
	return n.WriteAppliedMarkers(ctx, a, provider, action, applied, schema)
}
