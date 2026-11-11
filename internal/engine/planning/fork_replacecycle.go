// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planning

// choudoufu fork port (#2380): remove when upstream #4714 lands. The whole
// file is the planning engine's side of package replacecycle.

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport/replacecycle"
	"github.com/intentius/choudoufu/internal/engine/internal/execgraph"
	"github.com/intentius/choudoufu/internal/resources"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// forkReplaceCycle is the port's work at the end of planning, while it is
// on. It records each refreshed object's effective replace order in the
// refreshed state as CreateBeforeDestroy, as the old runtime does, so that
// an object orphaned later is still replaced and destroyed in the order it
// was created in; the apply engine starts from this state. And it refuses
// an execution graph with a cycle, which would otherwise surface only part
// way through the apply.
func forkReplaceCycle(refreshed *states.State, objs *resourceInstanceObjects, orders addrs.Map[addrs.AbsResourceInstanceObject, resources.ReplaceOrder], graph *execgraph.Graph) tfdiags.Diagnostics {
	if !replacecycle.Enabled() {
		return nil
	}
	for addr, obj := range objs.All() {
		order, ok := orders.GetOk(addr)
		if !ok {
			continue
		}
		is := refreshed.ResourceInstance(obj.PrevRoundAddr.InstanceAddr)
		if is == nil {
			continue
		}
		src := is.Current
		if obj.PrevRoundAddr.IsDeposed() {
			src = is.Deposed[obj.PrevRoundAddr.DeposedKey]
		}
		if src != nil {
			src.CreateBeforeDestroy = order == resources.ReplaceCreateFirst
		}
	}
	return replacecycle.CycleError(graph.ForkCycles())
}
