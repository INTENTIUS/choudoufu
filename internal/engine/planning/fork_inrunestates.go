// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planning

// choudoufu fork port (#2370): remove when upstream #4058 lands. The whole
// file is the planning engine's side of package inrunestates: it plans a
// terraform_estate_outputs read, keeps its result in the refreshed state,
// and adds a deferred read to the execution graph.

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport/inrunestates"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/engine/internal/execgraph"
	"github.com/intentius/choudoufu/internal/lang/eval"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// forkPlanEstateOutputs plans a terraform_estate_outputs instance the port
// handles. It returns false, leaving ret alone, for every other data block
// and for an estate_outputs block that is read from the provider as
// upstream reads any data block.
func (p *planGlue) forkPlanEstateOutputs(ctx context.Context, inst *eval.DesiredResourceInstance, meta *exec.ResourceInstanceObjectMeta, providerInstAddr addrs.AbsProviderInstanceCorrect, configVal cty.Value, ret *resourceInstanceObject) (bool, tfdiags.Diagnostics) {
	port := p.planCtx.inRunEstates
	if port == nil || !inrunestates.IsEstateOutputs(inst.Addr) {
		return false, nil
	}
	schema, diags := p.planCtx.providers.ResourceTypeSchema(ctx, meta.Provider, addrs.DataResourceMode, meta.ResourceType)
	if diags.HasErrors() {
		return true, diags
	}
	ty := schema.Block.ImpliedType()
	planned, handled, moreDiags := port.Plan(inst.Addr, configVal, ty, forkPlanHost{p: p, ctx: ctx})
	diags = diags.Append(moreDiags)
	if !handled || diags.HasErrors() {
		return handled, diags
	}
	for _, dep := range planned.Dependencies {
		ret.ConfigDependencies.Add(dep.CurrentObject())
	}
	if !planned.Deferred {
		ret.PlaceholderValue = planned.Value
		p.forkKeepDataRead(ctx, meta, providerInstAddr, ret)
		return true, diags
	}
	reason := plans.ResourceInstanceReadBecauseDependencyPending
	if planned.ConfigUnknown {
		reason = plans.ResourceInstanceReadBecauseConfigUnknown
	}
	ret.PlannedChange = &plans.ResourceInstanceChange{
		Addr:        inst.Addr,
		PrevRunAddr: inst.Addr,
		ProviderAddr: addrs.AbsProviderConfig{
			// The same lossy shim plan_managed.go uses.
			Module:   providerInstAddr.Config.Module.Module(),
			Provider: providerInstAddr.Config.Config.Provider,
			Alias:    providerInstAddr.Config.Config.Alias,
		},
		Action:       plans.Read,
		ActionReason: reason,
		Before:       cty.NullVal(ty),
		After:        planned.Value,
	}
	return true, diags
}

// forkKeepDataRead keeps a terraform_estate_outputs result read during the
// plan in the refreshed state, as the default runtime does, so the apply
// can read it back for anything that depends on it.
func (p *planGlue) forkKeepDataRead(ctx context.Context, meta *exec.ResourceInstanceObjectMeta, providerInstAddr addrs.AbsProviderInstanceCorrect, ret *resourceInstanceObject) {
	if p.planCtx.inRunEstates == nil || !inrunestates.IsEstateOutputs(meta.Addr.InstanceAddr) {
		return
	}
	val := ret.PlaceholderValue
	if val == cty.NilVal || !val.IsWhollyKnown() {
		return
	}
	schema, diags := p.planCtx.providers.ResourceTypeSchema(ctx, meta.Provider, addrs.DataResourceMode, meta.ResourceType)
	if diags.HasErrors() {
		return
	}
	src, err := states.EncodeResourceInstanceObjectFull(&states.ResourceInstanceObjectFull{
		Value:                val,
		Status:               states.ObjectReady,
		ProviderInstanceAddr: providerInstAddr,
		ResourceType:         meta.ResourceType,
		SchemaVersion:        uint64(schema.Version),
	}, schema.Block.ImpliedType())
	if err != nil {
		return
	}
	ret.RefreshedState = src
	ret.RefreshedValue = val
}

// forkPlanHost is the planning engine as package inrunestates sees it.
type forkPlanHost struct {
	p   *planGlue
	ctx context.Context
}

func (h forkPlanHost) InRunModule(target addrs.ModuleInstance) inrunestates.Module {
	outputs, resources := h.p.oracle.ForkInRunModule(h.ctx, target)
	mod := inrunestates.Module{Outputs: outputs, Resources: resources}
	desired := addrs.MakeSet(resources...)
	for _, addr := range resources {
		obj, ok := h.p.planCtx.resourceInstObjs.Get(addr.CurrentObject())
		if ok && obj.PlannedChange != nil && obj.PlannedChange.Action != plans.NoOp {
			mod.Pending = true
		}
	}
	// An object the module no longer declares, or a deposed one, is
	// planned for deletion only once evaluation ends, so it is found here
	// in the prior state.
	for objAddr := range resourceInstancesObjects(h.p.planCtx.prevRoundState) {
		inst := objAddr.InstanceAddr
		if inst.Resource.Resource.Mode != addrs.ManagedResourceMode {
			continue
		}
		if !target.Equal(inst.Module) && !target.IsAncestor(inst.Module) {
			continue
		}
		if objAddr.IsDeposed() || !desired.Has(inst) {
			mod.Pending = true
		}
	}
	return mod
}

// forkDataReadSubgraph adds a deferred terraform_estate_outputs read to the
// execution graph. Its dependencies are wired by
// AddResourceInstanceObjectSubgraphs from the object's ConfigDependencies,
// like a managed create's.
func (b *execGraphBuilder) forkDataReadSubgraph(change *plans.ResourceInstanceChange) resourceInstanceObjectSubgraph {
	unmarkedAfter, _ := change.After.UnmarkDeep()
	addrRef := b.lower.ConstantResourceInstAddr(change.Addr)
	metadata := b.lower.ResourceInstanceCurrentMeta(addrRef, execgraph.NilResultRef[*exec.ResourceInstanceObject]())
	desired := b.lower.ResourceInstanceDesired(metadata)
	waitFor, addDesiredDep := b.lower.MutableWaiter()
	valueRef := b.lower.DataRead(metadata, desired, b.lower.ConstantValue(unmarkedAfter), waitFor)
	return resourceInstanceObjectSubgraph{
		valueRef:      valueRef,
		addDesiredDep: addDesiredDep,
	}
}
