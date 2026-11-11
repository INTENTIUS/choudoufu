// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"log"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/states"
)

// newEngineInstanceStateUpdate is [applying.ApplyOpts.InstanceStateUpdate]
// (#2256 U5, #2341): the per-instance state report the old runtime makes
// through [InstanceStateHook] from updateStateHook. The marker verifier and
// writer need no wiring here: the engine finds them on the
// ConfigValueAdjuster U4 already passes (context_temp_runtime_adjust.go
// holds the drift checks).
//
// It calls every [InstanceStateHook] with one instance's state, with
// updateStateHook's semantics: the first error stops the walk and fails the
// instance's apply step, and HookActionHalt stops the walk without one. Unlike the tracer's hooks (eachHook), an error here
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
