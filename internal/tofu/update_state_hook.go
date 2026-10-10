// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/states"
)

// updateState calls the PostStateUpdate hook with the state modification function
func updateStateHook(evalCtx EvalContext, addr addrs.AbsResourceInstance) error {
	// Call the hook
	if err := evalCtx.Hook(func(h Hook) (HookAction, error) {
		return h.PostStateUpdate(func(s *states.SyncState) {
			provider := evalCtx.State().ResourceProvider(addr.ContainingResource())
			if provider == nil {
				// If there is no provider currently defined for the resource, it has been removed
				// See the documentation of ResourceProvider for more details
				s.RemoveResource(addr.ContainingResource())
			} else {
				// The individual instance may be nil, but that can happen when destroying
				// some but not all instances of a resource (or when that is in-progress).
				// SetResourceInstance handles that nil correctly and updates the state accordingly.
				s.SetResourceInstance(addr, evalCtx.State().ResourceInstance(addr), *provider)
			}
		})
	}); err != nil {
		return err
	}
	// choudoufu fork addition, GitHub issue #1944: see [InstanceStateHook].
	// The instance is copied once and shared by every hook that implements
	// the method, which includes every hook embedding [NilHook].
	var inst *states.ResourceInstance
	var provider *addrs.AbsProviderConfig
	read := false
	return evalCtx.Hook(func(h Hook) (HookAction, error) {
		ih, ok := h.(InstanceStateHook)
		if !ok {
			return HookActionContinue, nil
		}
		if !read {
			inst = evalCtx.State().ResourceInstance(addr)
			provider = evalCtx.State().ResourceProvider(addr.ContainingResource())
			read = true
		}
		return ih.PostInstanceStateUpdate(addr, inst, provider)
	})
}
