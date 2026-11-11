// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// parallelProviderManager gives the new runtime the old one's -parallelism
// (#2256 end phase). internal/engine has no concurrency limit of its own, so
// with the toggle on, -parallelism was silently ignored and every resource
// operation ran at once, which is what the limit exists to stop: CPU
// pressure and provider rate limits. The old runtime holds the Context's
// semaphore for each graph node; here each resource operation a configured
// provider performs holds it, so no internal/engine patch is needed.
type parallelProviderManager struct {
	plugins.ProviderManager
	sem Semaphore
}

func (m parallelProviderManager) NewConfiguredProvider(ctx context.Context, addr addrs.Provider, cfgVal cty.Value) (providers.Configured, tfdiags.Diagnostics) {
	p, diags := m.ProviderManager.NewConfiguredProvider(ctx, addr, cfgVal)
	if p == nil {
		return p, diags
	}
	return &parallelProvider{Configured: p, sem: m.sem}, diags
}

type parallelProvider struct {
	providers.Configured
	sem Semaphore
}

func (p *parallelProvider) ReadResource(ctx context.Context, req providers.ReadResourceRequest) providers.ReadResourceResponse {
	p.sem.Acquire()
	defer p.sem.Release()
	return p.Configured.ReadResource(ctx, req)
}

func (p *parallelProvider) PlanResourceChange(ctx context.Context, req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
	p.sem.Acquire()
	defer p.sem.Release()
	return p.Configured.PlanResourceChange(ctx, req)
}

func (p *parallelProvider) ApplyResourceChange(ctx context.Context, req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
	p.sem.Acquire()
	defer p.sem.Release()
	return p.Configured.ApplyResourceChange(ctx, req)
}

func (p *parallelProvider) ImportResourceState(ctx context.Context, req providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
	p.sem.Acquire()
	defer p.sem.Release()
	return p.Configured.ImportResourceState(ctx, req)
}

func (p *parallelProvider) ReadDataSource(ctx context.Context, req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
	p.sem.Acquire()
	defer p.sem.Release()
	return p.Configured.ReadDataSource(ctx, req)
}
