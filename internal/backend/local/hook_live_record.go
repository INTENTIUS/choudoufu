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
// # Why two hook points
//
// PostApply is the signal that an apply step returned, but it carries only
// the new value: not the private data, the status or the deposed objects a
// record is written from, and it fires before the state for that step is
// handed to the hooks. The PostStateUpdate that follows it on the same
// goroutine (managedResourceExecute, the destroy and deposed nodes) carries
// the instance's whole state - but only as a closure that copies one
// instance, without saying which. So PostApply marks the instance pending,
// and each PostStateUpdate finds out which pending instance it is for by
// applying the closure to a scratch state seeded with a sentinel per
// pending instance: the one whose sentinel was replaced or removed is the
// one this update wrote. A second application to an empty state gives that
// instance's object and provider. Both are copies; nothing here touches the
// run's real state.
//
// An error from the write is returned from PostStateUpdate, which fails the
// apply at that instance with the conflict named, as [LiveRun.WriteBack]
// does at the end.
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
	pending []addrs.AbsResourceInstance
}

var _ tofu.Hook = (*liveRecordHook)(nil)

// arm enables the hook for the apply that follows.
func (h *liveRecordHook) arm(ctx context.Context, run LiveRun, schemas *tofu.Schemas, replaced []addrs.AbsResourceInstance, deposed []projection.DeposedDestroy) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ctx = ctx
	h.run = run
	h.schemas = schemas
	h.replace = replaced
	h.deposed = deposed
	h.pending = nil
}

func (h *liveRecordHook) PostApply(addr addrs.AbsResourceInstance, _ states.Generation, _ cty.Value, _ error) (tofu.HookAction, error) {
	if addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
		return tofu.HookActionContinue, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.run == nil {
		return tofu.HookActionContinue, nil
	}
	for _, p := range h.pending {
		if p.Equal(addr) {
			return tofu.HookActionContinue, nil
		}
	}
	h.pending = append(h.pending, addr)
	return tofu.HookActionContinue, nil
}

func (h *liveRecordHook) PostStateUpdate(mutate func(*states.SyncState)) (tofu.HookAction, error) {
	h.mu.Lock()
	if h.run == nil || len(h.pending) == 0 {
		h.mu.Unlock()
		return tofu.HookActionContinue, nil
	}

	seeded := states.NewState()
	sentinels := make([]*states.ResourceInstance, len(h.pending))
	for i, addr := range h.pending {
		sentinels[i] = &states.ResourceInstance{}
		seeded.EnsureModule(addr.Module).SetResourceInstance(addr.Resource, sentinels[i], addrs.AbsProviderConfig{})
	}
	mutate(seeded.SyncWrapper())

	var touched []addrs.AbsResourceInstance
	keep := h.pending[:0]
	for i, addr := range h.pending {
		if seeded.ResourceInstance(addr) == sentinels[i] {
			keep = append(keep, addr)
			continue
		}
		touched = append(touched, addr)
	}
	h.pending = keep
	run, ctx, schemas, replaced, deposed := h.run, h.ctx, h.schemas, h.replace, h.deposed
	h.mu.Unlock()

	if len(touched) == 0 {
		return tofu.HookActionContinue, nil
	}

	written := states.NewState()
	mutate(written.SyncWrapper())
	for _, addr := range touched {
		diags := run.WriteInstance(ctx, written, addr, schemas, replaced, deposed)
		if diags.HasErrors() {
			return tofu.HookActionHalt, diags.Err()
		}
	}
	return tofu.HookActionContinue, nil
}
