// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/armtypes"
	"github.com/intentius/choudoufu/internal/live/azsweep"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staticeval"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The command layer's half of the Azure sweep (GitHub issue #1120, unit 11;
// the leg is discovery.ResourceGraphSweep). The sweep covers every
// subscription the estate's resources resolve to, and a run reaches more
// than one subscription through more than one provider block (an aliased
// azurerm per subscription, or azapi beside azurerm), each with the
// credential the provider itself calls as. So every pass's leg is handed
// the same coverage, a Resource Graph client per subscription a provider
// block names, built once per block and kept on projectionProviders; and
// each subscription is assigned to exactly one pass, because two passes
// filing one orphan is a cross-provider collision to discovery.Merge.

// resourceGraphClient is one provider block's client, the subscription it
// names (empty when it names none), and the error that stood in the way of
// building the client.
type resourceGraphClient struct {
	searcher     azsweep.Searcher
	subscription string
	err          error
}

// subscriptionNamer is the client capability that says which subscription
// the block it was built from names (azsweep.Client.Subscription).
type subscriptionNamer interface {
	Subscription() string
}

// resourceGraphLeg builds the Azure estate sweep for one provider
// configuration's pass. The subscriptions it sweeps are the ones whose
// first naming provider configuration, in the sorted order the passes run
// in, is addr; the first pass also takes every subscription a declared ARM
// id names and no provider block does, which the leg then reports
// uncovered by name. A block whose client cannot be built is one warning
// on its own pass, and its subscription is swept with another block's
// client when one names it too, or reported uncovered when none does.
func (p *projectionProviders) resourceGraphLeg(ctx context.Context, sub substrate.Substrate, addr addrs.AbsProviderConfig) (discovery.Sweeper, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	configs := p.familyProviderConfigs(sub, addr)

	cov := azsweep.Coverage{}
	owner := map[string]string{}
	leg := discovery.ResourceGraphSweep{Coverage: cov, TakesUnnamed: configs[0].String() == addr.String()}
	leg.Untaggable = p.azureUntaggableTypes(ctx, sub)
	for _, c := range configs {
		built := p.resourceGraphClientFor(ctx, sub, c)
		if built.subscription == "" {
			continue
		}
		key := strings.ToLower(built.subscription)
		if _, taken := owner[key]; !taken {
			owner[key] = c.String()
			if c.String() == addr.String() {
				leg.Subscriptions = append(leg.Subscriptions, built.subscription)
			} else {
				leg.Elsewhere = append(leg.Elsewhere, built.subscription)
			}
		}
		if built.searcher != nil && !coverageHas(cov, key) {
			cov[built.subscription] = built.searcher
		}
	}

	own := p.resourceGraphClientFor(ctx, sub, addr)
	switch {
	case own.err != nil:
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, azsweep.SummarySweepUnavailable,
			fmt.Sprintf("No Azure Resource Graph client could be built from provider configuration %s, so the estate sweep does not query as that block's credential: %s. Its subscription is swept with another provider block's credential when one names it, and is otherwise reported as not covered.", addr, own.err)))
	case own.subscription == "":
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, azsweep.SummarySweepUnavailable,
			fmt.Sprintf("Provider configuration %s names no subscription_id, and ARM_SUBSCRIPTION_ID is unset, so the estate sweep cannot tell which subscription its resources are in: a resource the estate marks there whose block was deleted is not proposed for removal unless another provider block names the subscription. Set subscription_id on the block.", addr)))
	}
	return leg, diags
}

// azureUntaggableTypes is [untaggableARMTypes] over this run's
// configuration, with each provider's schemas as the provider manager
// answers them. Every pass is handed the same list; [discovery.Merge]
// keeps one gap per type.
func (p *projectionProviders) azureUntaggableTypes(ctx context.Context, sub substrate.Substrate) []discovery.UntaggableType {
	if p.config == nil || p.mgr == nil {
		return nil
	}
	return untaggableARMTypes(ctx, p.config, sub.ProviderTypes(), func(provider addrs.Provider) map[string]providers.Schema {
		schema, diags := p.mgr.GetProviderSchema(ctx, provider)
		if diags.HasErrors() {
			return nil
		}
		return schema.ResourceTypes
	})
}

// untaggableARMTypes is every managed type of the given provider types,
// declared anywhere in config, that the run knows can carry no ownership
// marker (GitHub issue #2119), sorted by name, for the Resource Graph leg
// to report as TYPE_NOT_TAGGABLE gaps:
//
//   - a type whose schema has no tags map the arm-tags surface writes
//     ([markers.ARMTaggable] false): azurerm_subnet among them, one of
//     the 716 of azurerm 5.10.0's 1106 types with no tags argument;
//   - an azapi block, whose schema always has one, when azapi's own type
//     data ([armtypes.Taggable]) says the ARM type at the block's own API
//     version takes no tags, or does not know that pair. The stamp writes
//     no marker on either (internal/live/projection's armTagTarget).
//
// An azapi block whose type argument does not evaluate statically, or
// does not parse, is left out: the run does not know its type, and the
// stamp already warns about that instance by name. So is a type whose
// provider schema was not read. Classified here, where the leg is built,
// and passed in: the leg's own Sweep method shares its name with every
// other leg's, and the surface guard (markers/seams_test.go) resolves a
// call by name.
func untaggableARMTypes(ctx context.Context, config *configs.Config, providerTypes []string, schemasFor func(addrs.Provider) map[string]providers.Schema) []discovery.UntaggableType {
	family := map[string]bool{}
	for _, t := range providerTypes {
		family[t] = true
	}
	seen := map[string]discovery.UntaggableType{}
	schemas := map[addrs.Provider]map[string]providers.Schema{}
	config.DeepEach(func(c *configs.Config) {
		if c.Module == nil {
			return
		}
		for _, rc := range c.Module.ManagedResources {
			if !family[rc.Provider.Type] {
				continue
			}
			byType, read := schemas[rc.Provider]
			if !read {
				byType = schemasFor(rc.Provider)
				schemas[rc.Provider] = byType
			}
			schema, ok := byType[rc.Type]
			if !ok || schema.Block == nil {
				continue
			}
			if !markers.ARMTaggable(schema.Block) {
				seen[rc.Type] = discovery.UntaggableType{TypeName: rc.Type, Why: "carries no tags"}
				continue
			}
			if !armTypeArgument(schema.Block) {
				continue
			}
			typ, reason := staticeval.Argument(ctx, c.Module, rc, "type")
			if reason != "" {
				continue
			}
			armType, apiVersion, parsed := armtypes.ParseAzapiType(typ)
			if !parsed {
				continue
			}
			name := fmt.Sprintf("%s (%s@%s)", rc.Type, armType, apiVersion)
			switch taggable, known := armtypes.Taggable(armType, apiVersion); {
			case !known:
				seen[strings.ToLower(name)] = discovery.UntaggableType{TypeName: name, Why: "is not known to take tags at that API version in azapi's type data"}
			case !taggable:
				seen[strings.ToLower(name)] = discovery.UntaggableType{TypeName: name, Why: "takes no tags at that API version"}
			}
		}
	})
	out := make([]discovery.UntaggableType, 0, len(seen))
	for _, u := range seen {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TypeName < out[j].TypeName })
	return out
}

// armTypeArgument reports whether block names its ARM resource type in a
// settable string type argument, azapi's "<type>@<api-version>" shape, read
// from the schema rather than a type name.
func armTypeArgument(block *configschema.Block) bool {
	attr, ok := block.Attributes["type"]
	return ok && attr != nil && attr.Type == cty.String && (attr.Required || attr.Optional)
}

func coverageHas(cov azsweep.Coverage, lowered string) bool {
	for k := range cov {
		if strings.ToLower(k) == lowered {
			return true
		}
	}
	return false
}

// familyProviderConfigs is every provider configuration of sub's family the
// passes run over ([liveManagedResourceProviders]), with addr among them,
// sorted as the passes are.
func (p *projectionProviders) familyProviderConfigs(sub substrate.Substrate, addr addrs.AbsProviderConfig) []addrs.AbsProviderConfig {
	types := map[string]bool{}
	for _, t := range sub.ProviderTypes() {
		types[t] = true
	}
	seen := map[string]addrs.AbsProviderConfig{addr.String(): addr}
	if p.config != nil {
		for _, c := range liveManagedResourceProviders(p.config) {
			if types[c.Provider.Type] {
				seen[c.String()] = c
			}
		}
	}
	return sortedProviderConfigs(seen)
}

// resourceGraphClientFor is addr's client through the family's own
// [substrate.Substrate.NewSweeper], taken through the [substrate.Sweeper]
// interface as the Kubernetes client is: it must name its family's sweep
// and query Resource Graph (azsweep.Searcher), whatever its type. Built
// once per provider configuration and kept for every later pass.
func (p *projectionProviders) resourceGraphClientFor(ctx context.Context, sub substrate.Substrate, addr addrs.AbsProviderConfig) resourceGraphClient {
	key := providerCacheKey(addr)
	p.mu.Lock()
	if c, ok := p.resourceGraphClients[key]; ok {
		p.mu.Unlock()
		return c
	}
	p.mu.Unlock()

	val, ok := p.familyProviderValue(ctx, addr)
	c := resourceGraphClient{subscription: substrate.AzureSweepAttrs(val, ok).WithEnv(os.Getenv).SubscriptionID}
	built, err := sub.NewSweeper(val, ok)
	switch {
	case err != nil:
		c.err = err
	case built == nil:
		c.err = fmt.Errorf("provider family %s built no Resource Graph client", sub.Name())
	case built.SweepKind() != sub.Sweep():
		c.err = fmt.Errorf("provider family %s built a client for the %s sweep, not its own %s sweep", sub.Name(), built.SweepKind(), sub.Sweep())
	default:
		searcher, isGraph := built.(azsweep.Searcher)
		if !isGraph {
			c.err = fmt.Errorf("provider family %s built a %s client that cannot query Resource Graph", sub.Name(), built.SweepKind())
			break
		}
		c.searcher = searcher
		if n, ok := built.(subscriptionNamer); ok && n.Subscription() != "" {
			c.subscription = n.Subscription()
		}
	}

	p.mu.Lock()
	if p.resourceGraphClients == nil {
		p.resourceGraphClients = map[string]resourceGraphClient{}
	}
	p.resourceGraphClients[key] = c
	p.mu.Unlock()
	return c
}

// familyProviderValue is addr's evaluated provider block: the value it was
// configured with when this run has configured it (every pass's own
// provider is, before its legs are built), else the block evaluated now,
// without configuring the plugin. ok false when there is neither.
func (p *projectionProviders) familyProviderValue(ctx context.Context, addr addrs.AbsProviderConfig) (cty.Value, bool) {
	p.mu.Lock()
	val, ok := p.configVals[providerCacheKey(addr)]
	p.mu.Unlock()
	if ok || p.config == nil || p.mgr == nil {
		return val, ok
	}
	schema, diags := p.mgr.GetProviderSchema(ctx, addr.Provider)
	if diags.HasErrors() {
		return cty.NilVal, false
	}
	block := schema.Provider.Block
	if block == nil {
		block = &configschema.Block{}
	}
	val, cfgDiags := p.providerConfigValue(ctx, addr, block.DecoderSpec())
	if cfgDiags.HasErrors() {
		return cty.NilVal, false
	}
	return val, true
}
