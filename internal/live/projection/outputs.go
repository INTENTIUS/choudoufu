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
func ApplyRootOutputValues(ctx context.Context, core *tofu.Context, config *configs.Config, state *states.State, variables tofu.InputValues) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if core == nil || config == nil || config.Module == nil || len(config.Module.Outputs) == 0 || state == nil {
		return diags
	}

	scope, evalDiags := core.Eval(ctx, config, state, addrs.RootModuleInstance, &tofu.EvalOpts{SetVariables: variables})
	diags = diags.Append(evalDiags)
	if scope == nil {
		return diags
	}

	root := state.RootModule()
	for name, output := range config.Module.Outputs {
		val, valDiags := scope.EvalExpr(ctx, output.Expr, cty.DynamicPseudoType)
		diags = diags.Append(valDiags)
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
