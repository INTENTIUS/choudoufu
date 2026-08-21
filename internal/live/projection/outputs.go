// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// ApplyRootOutputValues evaluates the configuration's root-level `output`
// blocks against state and records each one's value into state's root
// module, in place. It is GitHub issue #348's fix.
//
// Nothing else in this package ever populates root module output values,
// because there is no state file for a stateless run to read a previous
// run's output values back from - a computed output has no carrier at all
// (see the "DEFER" row of HANDOFF.md's decision matrix). Left unset, every
// declared output looks new to the plan graph's own diff logic
// (NodeApplyableOutput.setValue in internal/tofu/node_output.go treats a
// root output absent from the prior state's OutputValues map as
// plans.Create), so live-plan showed every root output as "+" on every run,
// regardless of whether the underlying resources actually changed.
//
// The fix is to do what a normal OpenTofu refresh does before diffing prior
// output values against planned ones: evaluate the output expressions
// against the resource state. core.Eval is the same building block "tofu
// console" uses for exactly that - it builds and walks the ordinary
// variable/local/output evaluation graph (see graph_builder_eval.go, which
// includes OutputTransformer) against the given state, so a reference like
// aws_lambda_function.this.arn resolves from state's own instance objects
// with no provider read and no network call. That is exactly what live-plan
// needs: state here is the projection this package already built from live
// reads, which stands in for what a real refresh would have produced.
//
// A configuration with no root-level outputs costs nothing extra beyond
// building the (small) eval graph: config.Module.Outputs is empty and the
// loop below never runs.
//
// variables must be the same set the caller is about to hand the plan
// (or already handed the apply) - an output that reads var.* needs the
// same values a real plan would evaluate it with, or its "prior" value
// would not match what the plan computes for "planned", and every run
// would show a spurious diff instead of none.
//
// dataValues is GitHub issue #349's sub-problem 2: the values
// [dataread.ReadForOutputs] read live for the data sources these outputs
// reach, keyed by absolute resource instance address, in the same shape
// identity.Context.DataResults uses. They are seeded into the evaluation's
// own copy of state (see [withOutputEvalSeeds]) and never into the caller's,
// exactly like the zero-instance husks. Empty or nil is the ordinary case
// and costs nothing.
//
// This function does not report. It returns diagnostics because it is called
// where diagnostics are collected, and in practice it returns none: an
// output it cannot evaluate is left unset and nothing is raised about it.
// The reason is written out at the eval call below, and it is load-bearing
// rather than tidiness - a pre-plan probe over a deliberately partial state
// must never be the thing that refuses an estate.
func ApplyRootOutputValues(ctx context.Context, core *tofu.Context, config *configs.Config, state *states.State, variables tofu.InputValues, dataValues map[string]cty.Value) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if core == nil || config == nil || config.Module == nil || len(config.Module.Outputs) == 0 || state == nil {
		return diags
	}

	// The probe's own diagnostics are discarded, all of them, and this is
	// the blast-radius rule for the whole function: an expression that will
	// not evaluate against the projection costs THAT output its prior value
	// and nothing else. Nothing here may fail a run.
	//
	// The projection is a partial state by construction - it carries what
	// the live system could be read for and nothing about a resource that
	// does not exist yet - so an expression can fail here that a real plan
	// evaluates without complaint. And everything this evaluation could
	// legitimately say, the plan says for itself a moment later and says
	// better: the plan graph evaluates every one of these same output
	// expressions again, for the "after" side of the same diff, against its
	// own fully expanded graph, from the node that owns the output
	// (internal/tofu/node_output.go). Raising it here as well would report a
	// real problem twice and an artifact of partiality once - and, before
	// this was written down, would refuse a whole estate over one output
	// whose absence renders as an ordinary "+ name = ..." line. That is the
	// exact risk GitHub issue #349's scoping named for the data-source half
	// of this, and it was already live for the evaluation half.
	scope, _ := core.Eval(ctx, config, withOutputEvalSeeds(ctx, core, config, state, dataValues), addrs.RootModuleInstance, &tofu.EvalOpts{SetVariables: variables})
	if scope == nil {
		return diags
	}

	root := state.RootModule()
	for name, output := range config.Module.Outputs {
		val, valDiags := scope.EvalExpr(ctx, output.Expr, cty.DynamicPseudoType)
		if valDiags.HasErrors() {
			continue
		}
		// A not-wholly-known value means this output reads a resource that
		// is not yet materialized in state - most often because the
		// resource itself is about to be created, and every attribute an
		// unmaterialized resource's config-derived node can offer is
		// unknown. Leaving the output unset here is deliberate, not a gap:
		// [Module.SetOutputValue] would otherwise record an unknown value
		// as the diff's "prior" side, and the real plan graph's own
		// NodeApplyableOutput.setValue (internal/tofu/node_output.go)
		// assumes a prior output value is always wholly known - the same
		// assumption a persisted state file's output values always satisfy
		// - and panics comparing a known "after" against an unknown
		// "before" rather than handling it. An output with no prior value
		// at all takes plan's ordinary "this output is new" path instead,
		// which is also the right answer: an output whose resource is
		// itself about to be created cannot be a no-op.
		if !val.IsWhollyKnown() {
			continue
		}
		root.SetOutputValue(name, val, output.Sensitive, output.Deprecated)
	}
	return diags
}

// withZeroInstanceBlocks returns the state [ApplyRootOutputValues]
// evaluates against: the projection, plus an empty resource husk for every
// block that provably produces no instances this run. It is GitHub issue
// #349's fix, and it never modifies the state it is given - the plan runs
// against that one a moment later, and a husk is bookkeeping for an
// evaluation, not a fact about the estate.
//
// # What a husk buys
//
// internal/tofu/evaluate.go's GetResource decides what a whole-resource
// reference is worth by looking the resource up in the state it was handed.
// A resource that is ABSENT falls into the "walk that has not expanded the
// configuration" branch, which answers cty.DynamicVal for anything
// count-gated or for_each-gated: honest, because on that branch "no
// instances" and "not read yet" genuinely are the same observation. A
// resource that is PRESENT with no instances takes the far end of the same
// function instead, where count answers cty.EmptyTupleVal and for_each
// answers cty.EmptyObjectVal - a real, empty collection.
//
// That difference is the whole of #349. Indexing an empty tuple is an HCL
// error, and try() recovers from errors, so
// `try(aws_lambda_layer_version.this[0].arn, "")` finally reaches the ""
// that a real plan reaches - a real plan's own graph having expanded the
// configuration and known all along that instance 0 does not exist.
// Indexing an unknown is not an error, so before this the same expression
// stayed unknown, the output was left unset, and it rendered as newly
// created on every run.
//
// # Why this is not the evaluator's fix to make
//
// The alternative was to teach GetResource's own fallback branch to consult
// count and for_each. That branch is stock OpenTofu, shared with `tofu
// console` and the validate walk, and its DynamicVal is correct for both:
// neither has expanded anything. Seeding the husks here changes what
// choudoufu's own output evaluation is told and leaves every stock walk
// answering exactly as upstream does.
//
// # The soundness rule
//
// Only a block whose expansion RESOLVED, to no keys, gets a husk.
// [identity.ZeroInstanceBlocks] holds that line and its doc says why a
// looser reading is unsafe here: a husk for a block whose count could not
// be evaluated would turn an unknown output into a confidently wrong one,
// and a wrong value that renders as a clean diff is worse than the honest
// "+" it replaces.
// withOutputEvalSeeds returns the state [ApplyRootOutputValues] evaluates
// against: the projection, plus the two kinds of seed the evaluation needs
// and the plan must not see - the zero-instance husks of
// [withZeroInstanceBlocks], and the live data-source values of
// [seedDataValues]. It copies at most once, and returns the caller's own
// state untouched when there is nothing to seed.
func withOutputEvalSeeds(ctx context.Context, core *tofu.Context, config *configs.Config, state *states.State, dataValues map[string]cty.Value) *states.State {
	seeded := withZeroInstanceBlocks(ctx, config, state)
	if len(dataValues) == 0 {
		return seeded
	}
	if seeded == state {
		seeded = state.DeepCopy()
	}
	seedDataValues(ctx, core, config, seeded, dataValues)
	return seeded
}

// seedDataValues writes one data resource instance object into seeded for
// every value [dataread.ReadForOutputs] read, so that a root output
// referencing data.t.n resolves from the provider's own answer instead of
// from cty.DynamicVal.
//
// This is the same device as the husks one file over, with a real value in
// it: internal/tofu/evaluate.go's GetResource answers a whole-resource
// reference out of the state the walk was handed, and it does that for a
// data resource exactly as it does for a managed one. A data source absent
// from state answers unknown on the eval walk, which is what left
// corpus-lambda-simple's lambda_function_arn_static rendering as newly
// created on every run even though the three data sources behind it - a
// partition, a region and a caller identity - had been readable all along.
//
// Everything here is best-effort and silent by design, because this whole
// path is: a value that cannot be placed leaves its output with no prior
// value, which is what the output had before this existed. Refusing a run
// over one is precisely the blast radius #349's scoping warned against, so
// an unparseable address, a data block no longer in the configuration, a
// provider whose schema will not load and a value that will not encode
// against that schema are each skipped, not raised.
func seedDataValues(ctx context.Context, core *tofu.Context, config *configs.Config, seeded *states.State, dataValues map[string]cty.Value) {
	schemas, schemaDiags := core.Schemas(ctx, config, seeded)
	if schemaDiags.HasErrors() || schemas == nil {
		return
	}
	for addrStr, val := range dataValues {
		addr, parseDiags := addrs.ParseAbsResourceInstanceStr(addrStr)
		if parseDiags.HasErrors() || addr.Resource.Resource.Mode != addrs.DataResourceMode {
			continue
		}
		node := config.Descendent(addr.Module.Module())
		if node == nil || node.Module == nil {
			continue
		}
		rc := node.Module.DataResources[addr.Resource.Resource.String()]
		if rc == nil {
			continue
		}
		pcAddr := rc.ProviderConfigAddr()
		provider := addrs.AbsProviderConfig{
			Module:   addrs.RootModule,
			Provider: node.Module.ProviderForLocalConfig(pcAddr),
			Alias:    pcAddr.Alias,
		}
		schema, version := schemas.ResourceTypeConfig(provider.Provider, addrs.DataResourceMode, addr.Resource.Resource.Type)
		if schema == nil || schema.Block == nil {
			continue
		}
		obj := &states.ResourceInstanceObject{Value: val, Status: states.ObjectReady}
		src, err := obj.Encode(schema.Block.ImpliedType(), version, 0)
		if err != nil {
			continue
		}
		seeded.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, provider, addrs.NoKey)
	}
}

func withZeroInstanceBlocks(ctx context.Context, config *configs.Config, state *states.State) *states.State {
	blocks := identity.ZeroInstanceBlocks(ctx, config)
	if len(blocks) == 0 {
		return state
	}
	seeded := state.DeepCopy()
	for _, block := range blocks {
		if seeded.Resource(block.Addr) != nil {
			// The projection already knows this address. It should not be
			// possible for a block with no instances, but if some other
			// pass ever puts one there, that pass's answer is the one built
			// from live reads and this one must not overwrite it.
			continue
		}
		seeded.EnsureModule(block.Addr.Module).SetResourceProvider(block.Addr.Resource, block.Provider)
	}
	return seeded
}
