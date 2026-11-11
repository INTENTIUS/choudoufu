// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

// This file is a choudoufu fork addition (#2256 U5, #2341): ownership
// markers and per-instance records at apply time on the new runtime. It is
// kept in its own file so that an upstream sync touches it only when an
// upstream signature it calls changes. The patches to upstream's own files
// are the ApplyOpts parameter of [ApplyPlannedChanges], the opts field of
// execOperations, the managedApply rename and the one call to
// appliedMarkers in it, in operations_resource_managed.go.

import (
	"context"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// ApplyOpts carries the fork's apply-time extension points into the apply
// engine. The zero value, and a nil *ApplyOpts, change nothing: the engine
// then behaves as upstream's does.
type ApplyOpts struct {
	// Adjuster is internal/tofu's ConfigValueAdjuster value, or nil. It is
	// typed any because this package must not import internal/tofu: the
	// engine only ever looks at it through type assertions to the optional
	// capabilities declared in this package ([AppliedMarkerVerifier],
	// [AppliedMarkerWriter]), whose method sets are identical to
	// internal/tofu's interfaces of the same names, so one value satisfies
	// both.
	Adjuster any

	// InstanceStateUpdate, when set, is called once for each managed
	// resource instance object the engine applies, after the tracer has
	// reported the apply finished (the Hook API's PostApply) and with the
	// instance's state as the apply left it: a copy of its current and
	// deposed objects (nil when the instance is gone) and its resource's
	// provider configuration (nil when the resource is gone). It is
	// internal/tofu's InstanceStateHook (GitHub issue #1944) on this
	// runtime, which a live run uses to write each instance's record as it
	// returns. An error fails that instance's apply step.
	InstanceStateUpdate InstanceStateUpdate
}

// InstanceStateUpdate is the type of [ApplyOpts.InstanceStateUpdate].
type InstanceStateUpdate func(addr addrs.AbsResourceInstance, inst *states.ResourceInstance, provider *addrs.AbsProviderConfig) error

// AppliedMarkerVerifier is internal/tofu's AppliedMarkerVerifier (GitHub
// issue #1192), restated here so the engine can find it on
// [ApplyOpts.Adjuster] without importing internal/tofu. See that interface
// for the contract. It is never called for a delete.
type AppliedMarkerVerifier interface {
	VerifyAppliedMarkers(ctx context.Context, addr addrs.AbsResourceInstance, action plans.Action, planned, applied cty.Value, schema providers.Schema) tfdiags.Diagnostics
}

// AppliedMarkerWriter is internal/tofu's AppliedMarkerWriter (GitHub issue
// #1084), restated here for the same reason as [AppliedMarkerVerifier]. It
// is called only for a create that has succeeded so far, and the value it
// returns is the one stored in state.
type AppliedMarkerWriter interface {
	WriteAppliedMarkers(ctx context.Context, addr addrs.AbsResourceInstance, provider addrs.AbsProviderConfig, action plans.Action, applied cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics)
}

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
	if ops.opts == nil || ops.opts.Adjuster == nil || schema == nil {
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

	if v, ok := ops.opts.Adjuster.(AppliedMarkerVerifier); ok {
		ret = ret.Append(v.VerifyAppliedMarkers(ctx, addr, action, planned, applied, *schema))
	}
	if w, ok := ops.opts.Adjuster.(AppliedMarkerWriter); ok && action == plans.Create && !ret.HasErrors() {
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
	if plan == nil || ops.opts == nil || ops.opts.InstanceStateUpdate == nil || ops.workingState == nil {
		return ret, diags
	}
	if err := ops.workingState.reportInstance(plan.Addr.InstanceAddr, ops.opts.InstanceStateUpdate); err != nil {
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
