// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"log"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/applying"
	"github.com/intentius/choudoufu/internal/states"
)

// The engine finds the marker capabilities by type assertion on the same
// value, so a signature that drifts between the two packages would make
// them silently absent on the new runtime. These make the drift a compile
// error instead: any AppliedMarkerVerifier here is one there, and the same
// for the writer.
var (
	_ applying.AppliedMarkerVerifier = AppliedMarkerVerifier(nil)
	_ applying.AppliedMarkerWriter   = AppliedMarkerWriter(nil)
)

// newEngineApplyOpts is the fork's apply-time wiring for the new runtime
// (#2256 U5, #2341): the ConfigValueAdjuster value, for the applied-marker
// verifier and writer, and the per-instance state report that the old
// runtime makes through [InstanceStateHook] from updateStateHook.
//
// refuseLiveOnNewRuntime still stops any run with an adjuster from getting
// here until U6 (#2342) lifts it; this is the wiring that lift relies on.
func (c *Context) newEngineApplyOpts() *applying.ApplyOpts {
	opts := &applying.ApplyOpts{
		InstanceStateUpdate: c.newEngineInstanceStateUpdate,
	}
	if c.configValueAdjuster != nil {
		opts.Adjuster = c.configValueAdjuster
	}
	return opts
}

// newEngineInstanceStateUpdate calls every [InstanceStateHook] with one
// instance's state, with updateStateHook's semantics: the first error stops
// the walk and fails the instance's apply step, and HookActionHalt stops
// the walk without one. Unlike the tracer's hooks (eachHook), an error here
// is not swallowed, because a live run's record write failing must fail
// the apply at that instance, as it does on the old runtime.
func (c *Context) newEngineInstanceStateUpdate(addr addrs.AbsResourceInstance, inst *states.ResourceInstance, provider *addrs.AbsProviderConfig) error {
	for _, h := range c.hooks {
		ih, ok := h.(InstanceStateHook)
		if !ok {
			continue
		}
		action, err := ih.PostInstanceStateUpdate(addr, inst, provider)
		if err != nil {
			return err
		}
		if action == HookActionHalt {
			log.Printf("[WARN] Early exit triggered by hook: %T", h)
			return nil
		}
	}
	return nil
}
