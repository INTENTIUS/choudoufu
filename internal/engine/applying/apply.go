// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/configadjust"
	"github.com/intentius/choudoufu/internal/engine/internal/execgraph"
	"github.com/intentius/choudoufu/internal/engine/plugins"
	"github.com/intentius/choudoufu/internal/lang/eval"
	"github.com/intentius/choudoufu/internal/lang/grapheval"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/shared"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// ApplyPlannedChanges is a temporary placeholder entrypoint for a new approach
// to applying based on an execution graph generated during the planning phase.
//
// The signature here is a little confusing because we're currently reusing
// our old-style plan and state models, even though their shape isn't quite
// right for what we need here. A future version of this function will hopefully
// have a signature more tailored to the needs of the new apply engine, once
// we have a stronger understanding of what those needs are.
func ApplyPlannedChanges(ctx context.Context, plan *plans.Plan, configInst *eval.ConfigInstance, plugins plugins.Plugins, persistState PersistState, opts *ApplyOpts) (*states.State, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	// We'll make the "shared" tracer also available to everything we call.
	tracer := contextTracer(ctx)
	ctx = shared.ContextWithTracer(ctx, &tracer.Tracer)

	ctx = grapheval.ContextWithRequestTracker(ctx, grapheval.NewRequestTracker())

	glue := &evalGlue{
		plugins: plugins,
		// graph field populated below before we actually use the oracle
		// during graph execution.
	}
	oracle, moreDiags := configInst.ApplyOracle(ctx, glue)
	diags = diags.Append(moreDiags)
	if moreDiags.HasErrors() {
		return nil, diags
	}

	execGraph, execOps, moreDiags := compileExecutionGraph(ctx, plan, oracle, plugins, persistState)
	diags = diags.Append(moreDiags)
	if moreDiags.HasErrors() {
		return nil, diags
	}
	glue.graph = execGraph
	glue.ops = execOps
	if opts != nil {
		execOps.configValueAdjuster = opts.ConfigValueAdjuster
		execOps.instanceStateUpdate = opts.InstanceStateUpdate
	}

	moreDiags = execGraph.Execute(ctx)
	diags = diags.Append(moreDiags)

	// We skip updating the output values for the new state when destroying
	// because after destroy output values would be in a confusing state due to
	// referring to resource instance objects that no longer exist.
	newState, moreDiags := execOps.Finish(ctx, !plan.Destroying)
	diags = diags.Append(moreDiags)

	return newState, diags
}

// ApplyOpts are choudoufu's options for [ApplyPlannedChanges]. nil means
// none are set.
type ApplyOpts struct {
	// ConfigValueAdjuster is the ownership-marker seam (#2256 U4): the
	// adjuster [ManagedFinalPlan] applies to the desired configuration value
	// so that the final plan matches the initial one, which the planning
	// engine made from the adjusted value. It is the same value internal/tofu
	// holds as its Context's ConfigValueAdjuster, so the apply-time optional
	// capabilities (AppliedMarkerWriter, AppliedMarkerVerifier) can be found
	// on it with a type assertion.
	ConfigValueAdjuster configadjust.ConfigValueAdjuster

	// InstanceStateUpdate, when set, is called once for each managed
	// resource instance object the engine applies (#2256 U5, #2341). It is
	// internal/tofu's InstanceStateHook (GitHub issue #1944) on this runtime,
	// which a live run uses to write each instance's record as it returns.
	// See [InstanceStateUpdate] and ManagedApply in fork_apply_markers.go
	// for when it runs and what it is given. An error fails that instance's
	// apply step.
	InstanceStateUpdate InstanceStateUpdate
}

type evalGlue struct {
	graph   *execgraph.CompiledGraph
	ops     *execOperations
	plugins plugins.Plugins
}

// ResourceInstanceFinalState implements [eval.ApplyGlue].
func (e *evalGlue) ResourceInstanceFinalState(ctx context.Context, addr addrs.AbsResourceInstance) cty.Value {
	return e.graph.ResourceInstanceValue(ctx, addr)
}
