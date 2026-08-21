// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TargetedResources reports which of a configuration's resource blocks a
// -target / -exclude run still evaluates: the blocks the plan graph keeps
// once [TargetingTransformer] has run over it, keyed by
// [addrs.ConfigResource.String].
//
// It exists for the fork's stateless pipeline (GitHub issue #352), which does
// its identity resolution, data reads and marker work in front of the plan
// rather than inside it, and so has no graph of its own to prune. Before this,
// every one of those passes walked the whole configuration regardless of
// -target, and a resource the run was never going to act on could refuse the
// whole run on its own identity arguments - while stock OpenTofu, asked the
// same question, removed that resource from the graph before anything
// evaluated it.
//
// The answer is the graph's rather than a re-derivation of it, deliberately.
// Targeting is not "the addresses the user typed": it is those plus every
// object they depend on ([TargetingTransformer.selectTargetedNodes] adds each
// targeted vertex's ancestors), computed over the same reference edges
// [ReferenceTransformer] builds, with the same generalization of an instance
// or module address down to a [addrs.ConfigResource] that [nodeIsTarget]
// applies. A second implementation of that in the live layer would be a
// second set of semantics to keep in step, and the failure mode when the two
// drift is a plan that proposes creating something that already exists.
//
// Both resource modes are reported: [ConfigTransformer] adds data resources
// as ordinary [GraphNodeConfigResource] vertices too, so a data source that
// only an untargeted resource demanded is absent here just as its demander
// is.
//
// prevRunState may be an empty state. The set this returns is about which
// CONFIGURATION blocks survive; orphan instance vertices come from state and
// have no configuration block to report.
//
// Callers must not call this when neither Targets nor Excludes is set: with
// no targeting the transformer is a no-op and every block is in the answer,
// which is a whole graph build to learn nothing. [PlanOpts] is taken rather
// than the two slices so that a caller passes the same options object it will
// plan with, and cannot target one thing here and another there.
func (c *Context) TargetedResources(ctx context.Context, config *configs.Config, prevRunState *states.State, opts *PlanOpts) (map[string]addrs.ConfigResource, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if len(opts.Targets) == 0 && len(opts.Excludes) == 0 {
		return nil, diags
	}
	if prevRunState == nil {
		prevRunState = states.NewState()
	}

	graph, _, moreDiags := c.planGraph(ctx, config, prevRunState, opts, make(ProviderFunctionMapping))
	diags = diags.Append(moreDiags)
	if diags.HasErrors() {
		return nil, diags
	}

	out := make(map[string]addrs.ConfigResource)
	for _, v := range graph.Vertices() {
		rn, ok := v.(GraphNodeConfigResource)
		if !ok {
			continue
		}
		addr := rn.ResourceAddr()
		out[addr.String()] = addr
	}
	return out, diags
}
