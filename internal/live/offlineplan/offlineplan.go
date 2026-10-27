// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package offlineplan runs live-plan's second identity pass without a cloud,
// for the callers that measure a configuration offline: the live-check
// command and the two corpus tools (GitHub issue #2297).
//
// live-plan's liveResolve (internal/command/live_plan.go) resolves identities
// once, and when that pass refuses for a value only the provider knows - the
// ACM/Route53 validation records reading
// aws_acm_certificate.domain_validation_options are the shape it was built
// for - it asks each provider to plan a create ([projection.PlanInstancesIn])
// and resolves again with those values in hand. internal/live/check stays
// provider-free on purpose ([check.Context.ManagedResults]), so before #2297
// the offline instruments never ran that second pass and refused sites that
// live-plan resolves (choudoufu#2292's research: complete-alb, complete-nlb
// and mutual-auth-alb).
//
// This package is the caller-side half. [ManagedResults] makes the same
// decision liveResolve makes, over the same two passes, and returns the
// planned values only when liveResolve would keep the second pass. The caller
// hands them to [check.Analyze] as [check.Context.ManagedResults].
//
// # Offline means no cloud call
//
// A plan for a create needs a configured provider, not a credentialed one
// (see [pluginschema.Session]). [Providers] configures each provider from
// [ConfigValue], never from the configuration's own provider block: the
// block may name real credentials or read data sources, and this pass must
// not reach a cloud on anyone's behalf. For hashicorp/aws that value carries
// placeholder keys, every skip_* switch that stops the provider calling STS
// or the instance metadata service while it configures, and a proxy at a
// closed local port, so a resource whose plan would make an API call fails
// that call on this machine instead of reaching AWS. Every other provider is
// configured with an empty configuration; one that requires arguments fails
// to configure, and its resources are simply absent from the planned values,
// which is [projection.PlanInstancesIn]'s own rule for anything it cannot
// plan.
package offlineplan

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// DeadProxy is the proxy [ConfigValue] gives hashicorp/aws: a closed port on
// the loopback interface, so any request the provider makes fails locally.
const DeadProxy = "http://127.0.0.1:9"

// awsProvider is hashicorp/aws, the one provider [ConfigValue] fills in.
var awsProvider = addrs.NewDefaultProvider("aws")

// awsOffline is the hashicorp/aws configuration [ConfigValue] sets, by
// argument name. An argument the provider's schema does not declare is
// skipped, so an older or newer release still configures.
var awsOffline = map[string]cty.Value{
	"region":                      cty.StringVal("us-east-1"),
	"access_key":                  cty.StringVal("offline-plan"),
	"secret_key":                  cty.StringVal("offline-plan"),
	"skip_credentials_validation": cty.True,
	"skip_requesting_account_id":  cty.True,
	"skip_metadata_api_check":     cty.StringVal("true"),
	"skip_region_validation":      cty.True,
	"max_retries":                 cty.NumberIntVal(1),
	"http_proxy":                  cty.StringVal(DeadProxy),
	"https_proxy":                 cty.StringVal(DeadProxy),
}

// ConfigValue is the provider configuration this package configures provider
// with: the schema's empty value, plus [awsOffline] for hashicorp/aws. See
// the package doc for why the configuration's own provider block is never
// read.
func ConfigValue(provider addrs.Provider, block *configschema.Block) cty.Value {
	if block == nil {
		block = &configschema.Block{}
	}
	empty := block.EmptyValue()
	if provider != awsProvider || !empty.Type().IsObjectType() {
		return empty
	}
	vals := empty.AsValueMap()
	if vals == nil {
		vals = map[string]cty.Value{}
	}
	for name, want := range awsOffline {
		attr, ok := block.Attributes[name]
		if !ok {
			continue
		}
		got, err := convert.Convert(want, attr.ImpliedType())
		if err != nil {
			continue
		}
		vals[name] = got
	}
	return cty.ObjectVal(vals)
}

// Source starts providers. [plugins.ProviderManager] satisfies it, which is
// what the live-check command passes; the corpus tools pass a [Sessions]
// view.
type Source interface {
	GetProviderSchema(ctx context.Context, addr addrs.Provider) (providers.ProviderSchema, tfdiags.Diagnostics)
	NewConfiguredProvider(ctx context.Context, addr addrs.Provider, cfgVal cty.Value) (providers.Configured, tfdiags.Diagnostics)
}

// Providers is a [projection.Providers] that configures every provider with
// [ConfigValue]. One configured instance serves every configuration of a
// provider type (aliases and module-level blocks included), because the
// offline value does not depend on which block asked. Close stops what it
// started.
type Providers struct {
	src Source

	mu     sync.Mutex
	cache  map[addrs.Provider]providers.Interface
	failed map[addrs.Provider]error
}

var _ projection.Providers = (*Providers)(nil)

// New returns a [Providers] over src.
func New(src Source) *Providers {
	return &Providers{
		src:    src,
		cache:  map[addrs.Provider]providers.Interface{},
		failed: map[addrs.Provider]error{},
	}
}

// ConfiguredProvider implements [projection.Providers].
func (p *Providers) ConfiguredProvider(ctx context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if prov, ok := p.cache[addr.Provider]; ok {
		return prov, nil
	}
	if err, ok := p.failed[addr.Provider]; ok {
		return nil, err
	}
	schema, diags := p.src.GetProviderSchema(ctx, addr.Provider)
	if diags.HasErrors() {
		err := fmt.Errorf("reading the schema of %s: %w", addr.Provider, diags.Err())
		p.failed[addr.Provider] = err
		return nil, err
	}
	configured, diags := p.src.NewConfiguredProvider(ctx, addr.Provider, ConfigValue(addr.Provider, schema.Provider.Block))
	if diags.HasErrors() {
		err := fmt.Errorf("configuring %s offline: %w", addr.Provider, diags.Err())
		p.failed[addr.Provider] = err
		return nil, err
	}
	prov, ok := configured.(providers.Interface)
	if !ok {
		err := fmt.Errorf("configuring %s offline: the provider does not implement providers.Interface", addr.Provider)
		p.failed[addr.Provider] = err
		return nil, err
	}
	p.cache[addr.Provider] = prov
	return prov, nil
}

// Close stops every provider this started.
func (p *Providers) Close(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for addr, prov := range p.cache {
		if err := prov.Close(ctx); err != nil {
			log.Printf("[TRACE] offlineplan: closing %s: %s", addr, err)
		}
	}
	p.cache = map[addrs.Provider]providers.Interface{}
}

// Outcome says how [ManagedResults] decided, for a caller that reports it.
type Outcome string

const (
	// OutcomeNotNeeded: the first pass refused nothing a managed value
	// could settle, so no provider was started.
	OutcomeNotNeeded Outcome = "not-needed"
	// OutcomeNoValues: the providers planned nothing.
	OutcomeNoValues Outcome = "no-values"
	// OutcomeKeptFirst: the second pass refused as many sites or more, or
	// demoted an instance to needs-discovery, so live-plan would keep the
	// first pass and so does this.
	OutcomeKeptFirst Outcome = "kept-first-pass"
	// OutcomeSecondPass: the planned values are returned.
	OutcomeSecondPass Outcome = "second-pass"
)

// ManagedResults is liveResolve's second pass, offline. ictx is the
// identity context the caller's [check.Analyze] call will resolve with
// (Cloud and Schemas; ManagedResults is ignored, and PendingDataReads is set
// as [check.Analyze] sets it, so both passes are compared the way the check
// will resolve). It returns the planned values when liveResolve would keep
// the second pass, and nil otherwise.
//
// The decision is liveResolve's, rule for rule: no second pass when the
// first resolved cleanly or demanded no managed read
// ([identity.DemandedManagedReads]); and the second pass is kept only when
// it raises strictly fewer errors and demotes no instance to
// [identity.ClassNeedsDiscovery]. See liveResolve's doc comment for why each
// rule exists.
func ManagedResults(ctx context.Context, cfg *configs.Config, ictx identity.Context, provs projection.Providers) (map[string]cty.Value, Outcome) {
	if cfg == nil || provs == nil {
		return nil, OutcomeNotNeeded
	}
	ictx.ManagedResults = nil
	ictx.PendingDataReads = true
	first, firstDiags := identity.ResolveWith(ctx, cfg, ictx)
	if !firstDiags.HasErrors() || len(identity.DemandedManagedReads(first, firstDiags)) == 0 {
		return nil, OutcomeNotNeeded
	}

	planned, planDiags := projection.PlanInstances(ctx, cfg, provs)
	for _, d := range planDiags {
		log.Printf("[TRACE] offlineplan: planning managed values for the second resolution pass: %s", d.Description().Summary)
	}
	if len(planned) == 0 {
		return nil, OutcomeNoValues
	}

	ictx.ManagedResults = planned
	second, secondDiags := identity.ResolveWith(ctx, cfg, ictx)
	if errorCount(secondDiags) >= errorCount(firstDiags) || demoted(first, second) {
		return nil, OutcomeKeptFirst
	}
	return planned, OutcomeSecondPass
}

func errorCount(diags tfdiags.Diagnostics) int {
	n := 0
	for _, d := range diags {
		if d.Severity() == tfdiags.Error {
			n++
		}
	}
	return n
}

// demoted reports whether second classifies as needs-discovery an instance
// first resolved some other way: liveResolve's downgradedToDiscovery.
func demoted(first, second *identity.Result) bool {
	if first == nil || second == nil {
		return false
	}
	was := make(map[string]identity.Class, first.Len())
	for _, r := range first.All() {
		was[r.Addr.String()] = r.Class
	}
	for _, r := range second.All() {
		if r.Class != identity.ClassNeedsDiscovery {
			continue
		}
		if prior, ok := was[r.Addr.String()]; ok && prior != identity.ClassNeedsDiscovery {
			return true
		}
	}
	return false
}
