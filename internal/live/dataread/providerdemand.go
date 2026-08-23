// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the phase's THIRD demand class, issue #313's boundary: data
// sources a PROVIDER BLOCK's own arguments reach, rather than data sources
// an identity or a root output reaches.
//
// The wall it closes: "provider.kubernetes { host = data.aws_eks_cluster.
// cluster.endpoint }" is refused today not by anything in this package, but
// by internal/command's statelessProviders.providerConfigValue decoding the
// block through the module's bare StaticEvaluator - no data lookup, no
// module-output lookup, nothing this phase already built for every OTHER
// static-context caller. This class makes the same demand-then-read
// pipeline outputs.go's own class runs available to that call site: analyze
// what a provider block's arguments reach, read it, hand the result to
// [StaticEvaluator.WithDataResults] the same way [liveModuleEvaluator]
// already does for a data source's own arguments.
//
// It shares every offline eligibility rule (analyze.go's
// [analyzer.classify]) and the whole read machinery (read.go) with the
// other two classes, and differs from [AnalyzeRootOutputs] in only one way,
// which is why this is a separate entry point mirroring that one rather
// than a parameter to it:
//
//   - Demand is derived by reading the provider blocks' own argument
//     expressions across every module in the tree - a provider block is
//     not restricted to the root the way a root output is - using the
//     identical four-hop walk [rootOutputDataDemand] already performs
//     (locals, module outputs in either direction, data sources), rooted
//     at a provider block's arguments instead of an output's value.
//
// Fatality is the SAME as [AnalyzeRootOutputs], for the same reason: a
// source this class cannot read is SCOPED, never fatal. A provider whose
// configuration cannot be resolved this way is not a new failure mode -
// internal/command's statelessProviders.ConfiguredProvider already reports
// "Provider unavailable" for it, unchanged, the moment something tries to
// use it. Making THIS phase fatal over the same gap would only turn one
// clear diagnostic into two.
func AnalyzeProviderConfigs(ctx context.Context, cfg *configs.Config, opts Options) *Analysis {
	a := &Analysis{sources: make(map[string]*Source), projectManaged: !opts.SkipManagedProjection, scoped: true, liveManaged: opts.LiveManagedResults}
	if cfg == nil || cfg.Module == nil || cfg.Module.StaticEvaluator == nil {
		return a
	}
	an := &analyzer{ctx: ctx, cfg: cfg, analysis: a, schemas: opts.Schemas, scope: opts.Scope, visiting: make(map[string]bool)}
	if a.projectManaged {
		an.proj = newManagedProjector(ctx, cfg, false, opts.LiveManagedResults)
	}

	for _, want := range providerConfigDataDemand(cfg) {
		if _, seen := a.sources[sourceKey(want.module, want.resource)]; seen {
			continue
		}
		an.classify(want.module, want.resource, want.neededBy)
	}
	an.confineToBoundary(cfg, opts)
	return a
}

// ReadProviderConfigs performs the reads of a SCOPED analysis built by
// [AnalyzeProviderConfigs]. See [ReadForOutputs] for the shared contract:
// an ineligible source is skipped in silence and a failed read is skipped
// with a warning, because the only thing either can cost is the one
// provider configuration that wanted it, which then fails to configure
// exactly as it does today.
func ReadProviderConfigs(ctx context.Context, cfg *configs.Config, analysis *Analysis, provs Providers) (map[string]cty.Value, tfdiags.Diagnostics) {
	return read(ctx, cfg, analysis, provs)
}

// providerConfigDataDemand walks every provider block declared anywhere in
// the module tree - [configs.Module.ProviderConfigs], root and every
// descendant, the same population [statelessProviders.providerConfigValue]
// itself may decode a block from - and returns every data resource its own
// arguments can reach, module and neededBy included. Deterministic: modules
// in path order, provider configs within a module sorted by local name then
// alias.
func providerConfigDataDemand(cfg *configs.Config) []outputDemand {
	var out []outputDemand
	walkProviderConfigDemand(cfg, cfg, &out)
	return out
}

func walkProviderConfigDemand(root, node *configs.Config, out *[]outputDemand) {
	if node == nil || node.Module == nil {
		return
	}

	keys := make([]string, 0, len(node.Module.ProviderConfigs))
	for k := range node.Module.ProviderConfigs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		pc := node.Module.ProviderConfigs[k]
		displayName := pc.Name
		if pc.Alias != "" {
			displayName = displayName + "." + pc.Alias
		}
		for _, expr := range providerConfigExpressions(pc) {
			w := &demandWalk{cfg: root, seen: make(map[string]bool)}
			w.expr(node, expr, 0)
			for _, f := range w.found {
				*out = append(*out, outputDemand{
					module:   f.module,
					resource: f.resource,
					neededBy: fmt.Sprintf("provider %q", displayName),
				})
			}
		}
	}

	childNames := make([]string, 0, len(node.Children))
	for name := range node.Children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)
	for _, name := range childNames {
		walkProviderConfigDemand(root, node.Children[name], out)
	}
}

// providerConfigExpressions collects every attribute expression a provider
// block's own body carries, at any nesting depth - a provider block has no
// count, for_each, provider or depends_on meta-argument of its own to skip,
// unlike [collectBodyExpressions]' resource-block callers, so this is its
// own, simpler walk rather than a reuse that would carry that filtering by
// accident. A body this phase cannot enumerate (anything but native syntax)
// contributes nothing - "cannot analyze" must never read as "nothing to
// read", but for THIS demand class nothing-found costs the demand nothing
// (see this file's own doc comment on fatality), so an empty result here is
// the safe direction rather than a special case to raise.
func providerConfigExpressions(pc *configs.Provider) []hcl.Expression {
	body, ok := pc.Config.(*hclsyntax.Body)
	if !ok {
		return nil
	}
	var out []hcl.Expression
	collectProviderBodyExpressions(body, &out)
	return out
}

func collectProviderBodyExpressions(body *hclsyntax.Body, out *[]hcl.Expression) {
	names := make([]string, 0, len(body.Attributes))
	for name := range body.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		*out = append(*out, body.Attributes[name].Expr)
	}
	for _, block := range body.Blocks {
		collectProviderBodyExpressions(block.Body, out)
	}
}
