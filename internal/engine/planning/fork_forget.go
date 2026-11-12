// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planning

// choudoufu fork port (#2409): remove when upstream #4682, #4686 and #4693
// land. The whole file is the planning engine's side of package forget: which
// unwanted objects are forgotten instead of deleted, and what a declared
// object records as destroy = false.

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// forkSkipDestroyDesired records that the configuration declares addr and
// returns refreshed with the destroy = false its resource block sets, as the
// default runtime's refresh rewrites it, so that the plan's prior state (and
// so the apply's starting state) records it even for a no-op. refreshed is
// returned unchanged, never modified, with the port off or nothing to change.
func (p *planGlue) forkSkipDestroyDesired(addr addrs.AbsResourceInstance, refreshed *states.ResourceInstanceObjectFullSrc) (*states.ResourceInstanceObjectFullSrc, tfdiags.Diagnostics) {
	port := p.planCtx.forget
	if port == nil {
		return refreshed, nil
	}
	skip, diags := port.Declared(addr)
	if refreshed == nil || refreshed.SkipDestroy == skip {
		return refreshed, diags
	}
	updated := *refreshed
	updated.SkipDestroy = skip
	return &updated, diags
}

// forkForgetUnwanted returns the forget change for the unwanted object addr,
// planned at currentRunAddr after any move, when the port forgets it, and
// nil when it is to be deleted as before. prior is its refreshed value and
// private its provider's private data. reason is the reason the engine
// gives the object's delete, which the port keeps where the default runtime
// gives a forget the same one.
func (p *planGlue) forkForgetUnwanted(addr addrs.AbsResourceInstanceObject, currentRunAddr addrs.AbsResourceInstance, stateSrc *states.ResourceInstanceObjectFullSrc, providerAddr addrs.AbsProviderConfig, prior cty.Value, private []byte, reason plans.ResourceInstanceChangeActionReason) (*plans.ResourceInstanceChange, tfdiags.Diagnostics) {
	stateSkip := stateSrc != nil && stateSrc.SkipDestroy
	forget, reason, diags := p.planCtx.forget.Unwanted(currentRunAddr.Object(addr.DeposedKey), addr.InstanceAddr, stateSkip, reason)
	if !forget || diags.HasErrors() {
		return nil, diags
	}
	return &plans.ResourceInstanceChange{
		Addr:         currentRunAddr,
		PrevRunAddr:  addr.InstanceAddr,
		DeposedKey:   addr.DeposedKey,
		ProviderAddr: providerAddr,
		Private:      private,
		Action:       plans.Forget,
		Before:       prior,
		After:        cty.NullVal(prior.Type()),
		ActionReason: reason,
	}, diags
}
