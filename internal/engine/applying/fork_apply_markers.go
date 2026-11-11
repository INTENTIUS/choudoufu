// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

// This file is a choudoufu fork addition (#2256 U5, #2341): ownership
// markers and per-instance records at apply time on the new runtime. It is
// kept in its own file so that an upstream sync touches it only when an
// upstream signature it calls changes. The patches to upstream's own files
// are [ApplyOpts.InstanceStateUpdate] and its execOperations field (next to
// U4's adjuster, #2340), the managedApply rename and the one call to
// appliedMarkers in it, in operations_resource_managed.go. The marker
// interfaces live in internal/engine/configadjust with U4's.

import (
	"context"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/configadjust"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// InstanceStateUpdate is the type of [ApplyOpts.InstanceStateUpdate]. It is
// called once for each managed resource instance object the engine applies,
// after the tracer has reported the apply finished (the Hook API's
// PostApply), with the instance's state as the apply left it: a copy of its
// current and deposed objects (nil when the instance is gone) and its
// resource's provider configuration (nil when the resource is gone).
type InstanceStateUpdate func(addr addrs.AbsResourceInstance, inst *states.ResourceInstance, provider *addrs.AbsProviderConfig) error

// appliedMarkers is the old runtime's post-apply marker step
// (NodeAbstractResourceInstance.apply, internal/tofu), called from
// managedApply right after ApplyResourceChange returns an object and before
// the post-create provisioners run and the object is written to state,
// which is the order the old runtime has.
//
// The conditions are the old runtime's: nothing for a delete or for an
// apply that already carries an error; the verifier for a create or an
// update; the writer for a create only, and only while no error has been
// reported. A replace's create leg has a null prior here, as the old
// runtime's simplified Create does, so it is a create to both.
//
// It returns the object to store, which is applied unless the writer wrote
// a marker and returned the object with it.
func (ops *execOperations) appliedMarkers(ctx context.Context, plan *exec.ManagedResourceObjectFinalPlan, planned, applied cty.Value, schema *providers.Schema, diags tfdiags.Diagnostics) (cty.Value, tfdiags.Diagnostics) {
	var ret tfdiags.Diagnostics
	if ops.configValueAdjuster == nil || schema == nil {
		return applied, ret
	}
	if diags.HasErrors() || applied == cty.NilVal || applied.IsNull() || planned.IsNull() || !applied.IsWhollyKnown() {
		// A delete (null planned), a failed apply, or a result the engine
		// is about to report as invalid: the old runtime asks nothing.
		return applied, ret
	}
	action := plans.Update
	if prior := plan.PriorStateVal; prior == cty.NilVal || prior.IsNull() {
		action = plans.Create
	}
	addr := plan.Addr.InstanceAddr

	if v, ok := ops.configValueAdjuster.(configadjust.AppliedMarkerVerifier); ok {
		ret = ret.Append(v.VerifyAppliedMarkers(ctx, addr, action, planned, applied, *schema))
	}
	if w, ok := ops.configValueAdjuster.(configadjust.AppliedMarkerWriter); ok && action == plans.Create && !ret.HasErrors() {
		written, writeDiags := w.WriteAppliedMarkers(ctx, addr, legacyProviderConfigAddr(plan.ProviderInstance), action, applied, *schema)
		ret = ret.Append(writeDiags)
		if !writeDiags.HasErrors() && written != cty.NilVal {
			// The object as it now stands, marker included, so that the
			// state does not contradict the object it describes (#1316).
			applied = written
		}
	}
	return applied, ret
}

// legacyProviderConfigAddr converts the engine's provider instance address
// to the provider configuration address the old runtime hands the marker
// writer, the same lossy way internal/states does for the state's own
// resource-level provider (lossyProviderConfigAddrFromProviderInstanceAddr):
// module instance keys are dropped, because the old runtime does not allow a
// provider configuration inside a multi-instance module.
func legacyProviderConfigAddr(addr addrs.AbsProviderInstanceCorrect) addrs.AbsProviderConfig {
	return addrs.AbsProviderConfig{
		Module:   addr.Config.Module.Module(),
		Provider: addr.Config.Config.Provider,
		Alias:    addr.Config.Config.Alias,
	}
}

// ManagedApply implements [exec.Operations]. It is upstream's apply step
// (managedApply) followed by the fork's per-instance state report. It runs
// after managedApply has returned so that the tracer's end-of-apply event,
// which internal/tofu turns into the PostApply hook, has already fired:
// internal/backend/local's live record hook writes a record only for an
// instance PostApply has marked, the order the old runtime has.
func (ops *execOperations) ManagedApply(
	ctx context.Context,
	plan *exec.ManagedResourceObjectFinalPlan,
	fallback *exec.ResourceInstanceObject,
) (*exec.ResourceInstanceObject, tfdiags.Diagnostics) {
	ret, diags := ops.managedApply(ctx, plan, fallback)
	if plan == nil || ops.instanceStateUpdate == nil || ops.workingState == nil {
		return ret, diags
	}
	if err := ops.workingState.reportInstance(plan.Addr.InstanceAddr, ops.instanceStateUpdate); err != nil {
		log.Printf("[ERROR] apply phase: instance state update for %s failed: %s", plan.Addr.InstanceAddr, err)
		diags = diags.Append(err)
	}
	return ret, diags
}

// reportInstance hands report a snapshot of one resource instance's state
// as it stands in the working state. This is the per-instance write that
// [PersistingState] stands in for until upstream has granular state storage
// (`#4767`): the whole-state persist callback has already run for the same
// change, and this tells the caller which instance it was about.
func (s *PersistingState) reportInstance(addr addrs.AbsResourceInstance, report InstanceStateUpdate) error {
	inst := s.sync.ResourceInstance(addr)
	provider := s.sync.ResourceProvider(addr.ContainingResource())
	return report(addr, inst, provider)
}
