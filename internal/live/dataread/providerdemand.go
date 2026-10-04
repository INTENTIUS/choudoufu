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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
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
	if a.projectManaged {
		an.providerManagedDemand(cfg)
	}
	an.confineToBoundary(cfg, opts)
	return a
}

// providerManagedDemand is GitHub issue #1113's addition to this demand
// class: a provider block whose own argument reads a MANAGED value, with no
// data source in between.
//
// Two shapes do it, and every published EKS root built on
// terraform-aws-modules/eks v19 or later writes one of them:
//
//	provider "kubernetes" { host = aws_eks_cluster.this.endpoint }
//	provider "kubernetes" { host = module.eks.cluster_endpoint }
//
// The data-source walk above never sees either, because neither names a
// data source. So each provider block's own expressions are evaluated here
// through the same live evaluator [analyzer.evalRecorded] builds for a data
// source's arguments, in its coverage-only mode, and every managed
// reference the block's own literal arguments and the live values already
// supplied ([Options.LiveManagedResults]) cannot answer is recorded in
// [Analysis.ManagedRefusals] - the list [identity.DemandedManagedReads]
// already reads - and in [Analysis.ProviderManagedRefusals], which is what
// lets the caller treat a failed read of one of these instances as the
// error the ruling makes it.
//
// A refusal one module-output hop away is recorded by [moduleOutputLookup]
// through the callback; a direct one surfaces in the expression's own
// diagnostics and is recorded from there. wanted narrows a module call to
// the outputs the block actually names, for the reason
// [analyzer.evalRecorded] gives: terraform-aws-eks declares dozens of
// outputs, and evaluating every one would demand reads of instances no
// provider block reads.
//
// Fatality is unchanged by this function: it records demand and nothing
// else. Whether a provider whose block refers to a cluster that does not
// exist yet can be configured is decided where it always has been,
// [statelessProviders.providerConfigValue] in internal/command.
func (an *analyzer) providerManagedDemand(cfg *configs.Config) {
	record := func(d *hcl.Diagnostic) {
		if d == nil {
			return
		}
		if ref, isRef := d.Extra.(configs.RefusedReference); !isRef || ref.Category != configs.CategoryManagedResource {
			return
		}
		an.analysis.managedRefusals = an.analysis.managedRefusals.Append(d)
		an.analysis.providerManagedRefusals = an.analysis.providerManagedRefusals.Append(d)
	}
	noDeps := func(addrs.Module, addrs.Resource) {}
	walkProviderBlocks(cfg, func(node *configs.Config, pc *configs.Provider, displayName string) {
		for _, expr := range providerConfigExpressions(pc) {
			var travs []hcl.Traversal
			travs = append(travs, expr.Variables()...)
			eval := liveModuleEvaluator(an.ctx, an.cfg, node.Path, an.lookupFactory(noDeps), false, record, moduleOutputWantsFor(travs))
			if eval == nil {
				continue
			}
			_, _, refused, ok := staticEvalExprRefused(an.ctx, node.Path, "provider "+displayName, "argument", expr, eval)
			if !ok {
				record(refused)
			}
		}
	})
}

// walkProviderBlocks calls fn for every provider block declared anywhere in
// the module tree, in the deterministic order [walkProviderConfigDemand]
// uses: modules in path order, blocks within a module by local name then
// alias.
func walkProviderBlocks(node *configs.Config, fn func(node *configs.Config, pc *configs.Provider, displayName string)) {
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
		fn(node, pc, displayName)
	}
	childNames := make([]string, 0, len(node.Children))
	for name := range node.Children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)
	for _, name := range childNames {
		walkProviderBlocks(node.Children[name], fn)
	}
}

// ProviderConfigEvaluator returns the evaluator a provider block's own
// arguments are decoded through when the block reads a managed value -
// directly, through a local, or through a module output (GitHub issue
// #1113) - or nil when it reaches none, so that every block this fork
// configured before keeps exactly the evaluator it had.
//
// dataResults is the provider-configuration fixpoint's own data-source
// results ([ReadProviderConfigs]' shape, keyed by absolute instance
// address) and liveManaged the managed instances that fixpoint read
// ([Options.LiveManagedResults]' shape). The evaluator answers:
//
//   - a data source from dataResults, as [configs.StaticEvaluator.
//     WithDataResults] already did for this block;
//   - a managed resource from its block's own literal arguments and then
//     from liveManaged, through the same projector a data source's own
//     argument reads a managed value through ([managedProjector], in its
//     materializing mode, so an unknown is refused rather than handed to a
//     provider);
//   - a module output from the child module's own output expression,
//     evaluated the same way ([moduleOutputLookup]).
//
// Anything those cannot answer keeps refusing. A cluster that does not
// exist yet was never read, so `host = aws_eks_cluster.this.endpoint`
// refuses exactly as it did before this function existed, and the caller's
// "provider configuration not evaluable" handling applies unchanged.
//
// The evaluator is pure ([configs.StaticEvaluator.Pure]), like every one
// this package builds: a provider block that calls timestamp() or uuid()
// AND reads a managed value would see an unknown for the impure call. No
// such block is known; it is stated rather than assumed away.
func ProviderConfigEvaluator(ctx context.Context, cfg *configs.Config, module addrs.Module, pc *configs.Provider, dataResults, liveManaged map[string]cty.Value) *configs.StaticEvaluator {
	if cfg == nil || pc == nil {
		return nil
	}
	node := cfg.Descendent(module)
	if node == nil || node.Module == nil {
		return nil
	}
	exprs := providerConfigExpressions(pc)
	w := &demandWalk{cfg: cfg, seen: make(map[string]bool)}
	var travs []hcl.Traversal
	for _, expr := range exprs {
		w.expr(node, expr, 0)
		travs = append(travs, expr.Variables()...)
	}
	if !w.reachesManaged && !w.reachesModuleOutput {
		return nil
	}

	proj := newManagedProjector(ctx, cfg, true, liveManaged)
	var lookup func(addrs.Module) configs.StaticDataLookup
	lookup = func(m addrs.Module) configs.StaticDataLookup {
		data, _ := identity.DataLookupFor(dataResults, m)
		return func(addr addrs.Resource) (cty.Value, bool) {
			if addr.Mode == addrs.ManagedResourceMode {
				return proj.project(m, addr, lookup)
			}
			if data == nil {
				return cty.NilVal, false
			}
			return data(addr)
		}
	}
	return liveModuleEvaluator(ctx, cfg, module, lookup, true, nil, moduleOutputWantsFor(travs))
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
