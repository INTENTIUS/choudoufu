// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"
	"sync"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// liveRecordHook is GitHub issue #1944's seam: it writes an instance's
// record as soon as that instance's apply step returns, through
// [LiveRun.WriteInstance], instead of leaving every record to
// [LiveRun.WriteBack] after the whole apply. A run that dies in between
// otherwise leaves a record-carried or record-backed object in the cloud
// and nothing anywhere that names it.
//
// PostApply marks a managed instance as applied, and the fork's
// [tofu.InstanceStateHook] call that follows it from the same node hands
// over the instance's whole state once it is written: the current object
// with its private data and status, and its deposed objects. Only an
// instance PostApply marked is written, so the state updates that are not
// an apply step returning - a data source read, a `forget` - write nothing
// here and stay the final pass's.
//
// An error from the write is returned from PostInstanceStateUpdate, which
// fails the apply at that instance with the conflict named, as
// [LiveRun.WriteBack] does at the end.
//
// The hook is inert until arm is called, which opApply does once the plan
// is approved and the plan's replace and deposed-destroy signals are in
// hand, and inert for a run with no [LiveRun] at all.
type liveRecordHook struct {
	tofu.NilHook

	mu      sync.Mutex
	run     LiveRun
	ctx     context.Context
	schemas *tofu.Schemas
	replace []addrs.AbsResourceInstance
	deposed []projection.DeposedDestroy
	applied map[string]bool
}

var _ tofu.Hook = (*liveRecordHook)(nil)
var _ tofu.InstanceStateHook = (*liveRecordHook)(nil)

// arm enables the hook for the apply that follows.
func (h *liveRecordHook) arm(ctx context.Context, run LiveRun, schemas *tofu.Schemas, replaced []addrs.AbsResourceInstance, deposed []projection.DeposedDestroy) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ctx = ctx
	h.run = run
	h.schemas = schemas
	h.replace = replaced
	h.deposed = deposed
	h.applied = map[string]bool{}
}

func (h *liveRecordHook) PostApply(addr addrs.AbsResourceInstance, _ states.Generation, _ cty.Value, _ error) (tofu.HookAction, error) {
	if addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
		return tofu.HookActionContinue, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.run != nil {
		h.applied[addr.String()] = true
	}
	return tofu.HookActionContinue, nil
}

func (h *liveRecordHook) PostInstanceStateUpdate(addr addrs.AbsResourceInstance, inst *states.ResourceInstance, provider *addrs.AbsProviderConfig) (tofu.HookAction, error) {
	h.mu.Lock()
	key := addr.String()
	if h.run == nil || !h.applied[key] {
		h.mu.Unlock()
		return tofu.HookActionContinue, nil
	}
	delete(h.applied, key)
	run, ctx, schemas, replaced, deposed := h.run, h.ctx, h.schemas, h.replace, h.deposed
	h.mu.Unlock()

	state := states.NewState()
	if inst != nil && provider != nil {
		state.EnsureModule(addr.Module).SetResourceInstance(addr.Resource, inst, *provider)
	}
	diags := run.WriteInstance(ctx, state, addr, schemas, replaced, deposed)
	if diags.HasErrors() {
		return tofu.HookActionHalt, diags.Err()
	}
	return tofu.HookActionContinue, nil
}
