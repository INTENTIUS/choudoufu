// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planning

// choudoufu fork port (#2414): remove when upstream #4058 and #4740 land. The
// whole file is the planning engine's side of package datasources: upstream
// pull request #4740's plan_data.go (commit 4b689c442b), adapted to sit
// beside the in-run-estates port.

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport/checkrules"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/lang/eval"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/resources"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// forkPlanDataRead plans a data resource instance: it reads it now when its
// configuration is wholly known and derives from no pending change, and
// otherwise plans a Read change that defers the read to the apply. A
// refresh-only plan never defers: it leaves such an instance unread, as the
// default runtime does. ret is planDesiredDataResourceInstance's partly
// built result.
func (p *planGlue) forkPlanDataRead(ctx context.Context, inst *eval.DesiredResourceInstance, meta *exec.ResourceInstanceObjectMeta, ret *resourceInstanceObject) (*resourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	ret.PrevRoundAddr = ret.Addr

	providerInstUnmarked, _ := meta.ProviderInstance.Unmark()
	providerInstAddr, ok := providerInstUnmarked.ValueOk()
	if !ok {
		// TODO: Record that this was deferred because we don't yet know which
		// provider instance it belongs to.
		return ret, diags
	}
	ret.ProviderInst = providerInstAddr

	unmarkedConfigVal, _ := inst.ConfigVal.UnmarkDeep()
	// choudoufu fork port (#2370): remove when upstream #4058 lands.
	if done, moreDiags := p.forkPlanEstateOutputs(ctx, inst, meta, providerInstAddr, unmarkedConfigVal, ret); done {
		return ret, diags.Append(moreDiags)
	}

	providerClient, moreDiags := p.providerClient(ctx, providerInstAddr)
	if providerClient == nil {
		moreDiags = moreDiags.Append(tfdiags.AttributeValue(
			tfdiags.Error,
			"Provider instance not available",
			fmt.Sprintf("Cannot plan %s because its associated provider instance %s cannot initialize.", inst.Addr, providerInstAddr),
			nil,
		))
	}
	diags = diags.Append(moreDiags)
	if moreDiags.HasErrors() {
		return ret, diags
	}

	resourceType := resources.NewDataResourceType(meta.Provider, meta.ResourceType, providerClient)

	requiredChanges := addrs.CollectSet(objchange.PrereqChangesForValue(inst.ConfigVal))
	depsPending := len(requiredChanges) != 0 || p.forkConditionDepsPending(inst)
	configKnown := inst.ConfigVal.IsWhollyKnown()
	if depsPending || !configKnown {
		reason := plans.ResourceInstanceReadBecauseDependencyPending
		if !configKnown {
			// configKnown takes precedence because it's more specific.
			reason = plans.ResourceInstanceReadBecauseConfigUnknown
		}
		if p.planCtx.refreshOnly {
			// A refresh-only plan, and a destroy plan's refresh, cannot
			// defer the read: as in the default runtime, the instance keeps
			// the value the prior state has for it, if any.
			p.forkKeepPriorDataValue(ctx, resourceType, ret)
			return ret, diags
		}
		ret, moreDiags = p.forkPlanDelayedDataRead(ctx, inst, providerInstAddr, resourceType, reason, ret)
		diags = diags.Append(moreDiags)
		return ret, diags
	}

	readDiags := p.forkReadDataNow(ctx, inst, meta, providerInstAddr, resourceType, ret)
	if !inst.ForkCheckNested {
		return ret, diags.Append(readDiags)
	}
	// choudoufu fork port (#2418): remove when upstream #4390 lands. The
	// check block reports the read's failure as its result.
	checkrules.RecordCheckDataRead(ctx, inst.Addr, readDiags)
	// A data block inside a check block never fails the plan: as in the
	// default runtime, its errors are warnings, an unreadable one plans with
	// an unknown value, and it is read again during the apply so the check
	// sees the latest data.
	diags = diags.Append(tfdiags.OverrideAll(readDiags, tfdiags.Warning, nil))
	if readDiags.HasErrors() {
		unmarkedConfigVal, configMarkPaths := inst.ConfigVal.UnmarkDeepWithPaths()
		if schema, schemaDiags := resourceType.LoadSchema(ctx); !schemaDiags.HasErrors() {
			unknown := objchange.PlannedUnknownObject(schema.Block, unmarkedConfigVal)
			ret.PlaceholderValue = unknown.MarkWithPaths(configMarkPaths)
			// The default runtime keeps what the configuration says of it.
			if src, err := states.EncodeResourceInstanceObjectFull(&states.ResourceInstanceObjectFull{
				Value:                cty.UnknownAsNull(unknown),
				Status:               states.ObjectReady,
				ProviderInstanceAddr: providerInstAddr,
				ResourceType:         meta.ResourceType,
				SchemaVersion:        uint64(schema.Version),
			}, schema.Block.ImpliedType()); err == nil {
				ret.RefreshedState = src
			}
		}
	}
	if !p.planCtx.refreshOnly {
		ret.PlannedChange = &plans.ResourceInstanceChange{
			Addr:         inst.Addr,
			PrevRunAddr:  inst.Addr,
			ProviderAddr: forkProviderConfigShim(providerInstAddr),
			Action:       plans.Read,
			ActionReason: plans.ResourceInstanceReadBecauseCheckNested,
			Before:       cty.NullVal(ret.PlaceholderValue.Type()),
			After:        ret.PlaceholderValue,
		}
	}
	return ret, diags
}

// forkReadDataNow reads a data resource instance during the plan and keeps
// the result in ret: its value, and its refreshed state.
func (p *planGlue) forkReadDataNow(ctx context.Context, inst *eval.DesiredResourceInstance, meta *exec.ResourceInstanceObjectMeta, providerInstAddr addrs.AbsProviderInstanceCorrect, resourceType *resources.DataResourceType, ret *resourceInstanceObject) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	tracer := contextTracer(ctx)

	validateDiags := resourceType.ValidateConfig(ctx, inst.ConfigVal)
	diags = diags.Append(validateDiags)
	if diags.HasErrors() {
		return diags
	}

	unmarkedConfigVal, _ := inst.ConfigVal.UnmarkDeepWithPaths()
	proposedNewVal, unkDiags := resourceType.UnknownValue(ctx, unmarkedConfigVal)
	diags = diags.Append(unkDiags)
	if diags.HasErrors() {
		return diags
	}

	readCtx := ctx
	if cb := tracer.StartDataResourceInstanceRead; cb != nil {
		readCtx = cb(ctx, inst.Addr, proposedNewVal)
	}

	resp, readDiags := resourceType.Read(readCtx, &resources.DataResourceReadRequest{
		ResourceAddress: inst.Addr,
		ConfigValue:     inst.ConfigVal,
	}, inst.Addr.CurrentObject())
	diags = diags.Append(readDiags)

	if cb := tracer.EndDataResourceInstanceRead; cb != nil {
		resultVal := cty.DynamicVal
		if resp != nil && resp.Result != cty.NilVal {
			resultVal = resp.Result
		}
		cb(readCtx, inst.Addr, resultVal, diags.Err())
	}
	if diags.HasErrors() {
		return diags
	}

	encodedVal, encodeDiags := resourceType.EncodeValue(ctx, resp.ResultUnmarked, inst.Addr.CurrentObject())
	diags = diags.Append(encodeDiags)
	if diags.HasErrors() {
		return diags
	}

	dataResourceState := &states.ResourceInstanceObjectFullSrc{
		Value: states.ValueJSONWithMetadata{
			ValueJSON:      encodedVal.ValueJSON,
			SensitivePaths: resp.SensitivePaths,
		},
		Status:               resp.Status,
		ProviderInstanceAddr: providerInstAddr,
		ResourceType:         meta.ResourceType,
		SchemaVersion:        encodedVal.SchemaVersion,
	}
	ret.RefreshedState = dataResourceState
	ret.UpgradedState = dataResourceState

	// Read during the plan, so no PlannedChange: the result is the value.
	ret.PlaceholderValue = resp.Result
	return diags
}

// forkPlanDelayedDataRead plans a Read change for a data resource instance
// whose configuration will be final only during the apply, and uses a
// placeholder for evaluation until then.
func (p *planGlue) forkPlanDelayedDataRead(ctx context.Context, inst *eval.DesiredResourceInstance, providerAddr addrs.AbsProviderInstanceCorrect, resourceType *resources.DataResourceType, reason plans.ResourceInstanceChangeActionReason, ret *resourceInstanceObject) (*resourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	schema, schemaDiags := resourceType.LoadSchema(ctx)
	if schemaDiags.HasErrors() {
		diags = diags.Append(tfdiags.AttributeValue(
			tfdiags.Error,
			"Resource type schema unavailable",
			fmt.Sprintf(
				"Cannot plan %s because provider %s failed to return the schema for its resource type %q.",
				inst.Addr, providerAddr.Config.Config.Provider, inst.Addr.Resource.Resource.Type,
			),
			nil, // this error belongs to the whole resource config
		))
		return ret, diags
	}

	unmarkedConfigVal, configMarkPaths := inst.ConfigVal.UnmarkDeepWithPaths()
	proposedNewVal := objchange.PlannedUnknownObject(schema.Block, unmarkedConfigVal)
	proposedNewVal = proposedNewVal.MarkWithPaths(configMarkPaths)

	ret.PlannedChange = &plans.ResourceInstanceChange{
		Addr:         inst.Addr,
		PrevRunAddr:  inst.Addr,
		ProviderAddr: forkProviderConfigShim(providerAddr),
		Action:       plans.Read,
		ActionReason: reason,
		// The prior value of a data resource instance is always null: it is
		// read anew each round.
		Before: cty.NullVal(schema.Block.ImpliedType()),
		After:  proposedNewVal,
	}
	return ret, diags
}

// forkKeepPriorDataValue makes the prior state's object for ret, if there is
// one, its value and its refreshed state.
func (p *planGlue) forkKeepPriorDataValue(ctx context.Context, resourceType *resources.DataResourceType, ret *resourceInstanceObject) {
	prior := p.planCtx.prevRoundState.SyncWrapper().ResourceInstanceObjectFull(ret.Addr)
	if prior == nil {
		return
	}
	schema, diags := resourceType.LoadSchema(ctx)
	if diags.HasErrors() {
		return
	}
	obj, err := states.DecodeResourceInstanceObjectFull(prior, schema.Block.ImpliedType())
	if err != nil {
		return
	}
	ret.PlaceholderValue = obj.Value
	ret.RefreshedState = prior
}

// forkKeepDestroyData remembers what a destroy plan's refresh read, or kept
// from the prior state, for a data resource instance.
func (p *planGlue) forkKeepDestroyData(obj *resourceInstanceObject) {
	if p.planCtx.forkDestroyData == nil || obj == nil || obj.RefreshedState == nil {
		return
	}
	p.planCtx.forkDestroyData.Store(obj.Addr.String(), obj.RefreshedState)
}

// forkDestroyDataObject plans a data resource instance in a destroy plan,
// where every object in the prior state is an orphan: as in the default
// runtime, the plan's prior state keeps what the destroy plan's refresh
// read for it, so the apply evaluates references to it (a provider
// configuration, a count or for_each) from that, and the apply leaves it
// out of the final state. It returns nil outside a destroy plan.
func (p *planGlue) forkDestroyDataObject(addr addrs.AbsResourceInstance, state *states.ResourceInstanceObjectFullSrc) *resourceInstanceObject {
	if p.planCtx.forkDestroyData == nil {
		return nil
	}
	refreshed := state
	if v, ok := p.planCtx.forkDestroyData.Load(addr.CurrentObject().String()); ok {
		refreshed = v.(*states.ResourceInstanceObjectFullSrc)
	}
	return &resourceInstanceObject{
		Addr:               addr.CurrentObject(),
		PrevRoundAddr:      addr.CurrentObject(),
		ConfigDependencies: addrs.MakeSet[addrs.AbsResourceInstanceObject](),
		Provider:           state.ProviderInstanceAddr.Config.Config.Provider,
		ProviderInst:       state.ProviderInstanceAddr,
		PlaceholderValue:   cty.NullVal(cty.DynamicPseudoType),
		RefreshedState:     refreshed,
	}
}

// forkProviderConfigShim is the same lossy shim plan_managed.go uses for a
// change's provider configuration address.
func forkProviderConfigShim(addr addrs.AbsProviderInstanceCorrect) addrs.AbsProviderConfig {
	return addrs.AbsProviderConfig{
		Module:   addr.Config.Module.Module(),
		Provider: addr.Config.Config.Provider,
		Alias:    addr.Config.Config.Alias,
	}
}

// forkConditionDepsPending reports whether a data resource instance with a
// precondition or postcondition depends on a managed resource instance that
// has a change planned. The default runtime then defers the read even when
// the configuration does not use a changing value, because the change may be
// what makes the condition pass.
func (p *planGlue) forkConditionDepsPending(inst *eval.DesiredResourceInstance) bool {
	if !inst.ForkHasConditions {
		return false
	}
	for dep := range inst.RequiredResourceInstances.All() {
		if dep.Resource.Resource.Mode != addrs.ManagedResourceMode {
			continue
		}
		obj, ok := p.planCtx.resourceInstObjs.Get(dep.CurrentObject())
		if ok && obj.PlannedChange != nil && obj.PlannedChange.Action != plans.NoOp {
			return true
		}
	}
	return false
}
