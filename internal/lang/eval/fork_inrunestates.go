// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package eval

// choudoufu fork port (#2370): remove when upstream #4058 lands. The whole
// file: internal/engine/forkport/inrunestates answers an in-run estate read
// from a module's outputs, which only the evaluator holds.

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang/eval/internal/configgraph"
	"github.com/intentius/choudoufu/internal/lang/eval/internal/evalglue"
)

// ForkInRunModule evaluates the outputs of the module instance addr and
// plans every resource instance under it, returning the outputs object and
// the addresses of those instances. A module instance that does not exist
// returns cty.NilVal. Diagnostics are not returned: the evaluator reports
// them from its own pass over the configuration.
func (o *PlanningOracle) ForkInRunModule(ctx context.Context, addr addrs.ModuleInstance) (cty.Value, []addrs.AbsResourceInstance) {
	mi := evalglue.ModuleInstance(ctx, o.root, addr)
	if mi == nil {
		return cty.NilVal, nil
	}
	var resources []addrs.AbsResourceInstance
	var visit func(m evalglue.CompiledModuleInstance)
	visit = func(m evalglue.CompiledModuleInstance) {
		for ri := range m.ResourceInstances(ctx) {
			_, _ = ri.Value(ctx)
			resources = append(resources, ri.Addr)
		}
		for _, child := range m.ChildModuleInstances(ctx) {
			visit(child)
		}
	}
	visit(mi)
	return forkModuleOutputs(ctx, mi), resources
}

// ForkModuleOutputs returns the outputs object of the module instance addr
// during the apply, or cty.NilVal if there is no such module instance.
func (o *ApplyOracle) ForkModuleOutputs(ctx context.Context, addr addrs.ModuleInstance) cty.Value {
	mi := evalglue.ModuleInstance(ctx, o.root, addr)
	if mi == nil {
		return cty.NilVal
	}
	return forkModuleOutputs(ctx, mi)
}

func forkModuleOutputs(ctx context.Context, mi evalglue.CompiledModuleInstance) cty.Value {
	v, _ := mi.ResultValuer(ctx).Value(ctx)
	return configgraph.PrepareOutgoingValue(v)
}
