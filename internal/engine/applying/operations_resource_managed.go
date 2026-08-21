// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

import (
	"context"
	"fmt"
	"log"
	"slices"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/lang/eval"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/resources"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// ManagedFinalPlan implements [exec.Operations].
func (ops *execOperations) ManagedFinalPlan(
	ctx context.Context,
	metadata *exec.ResourceInstanceObjectMeta,
	desired *eval.DesiredResourceInstance,
	prior *exec.ResourceInstanceObject,
	initialPlannedVal cty.Value,
) (*exec.ManagedResourceObjectFinalPlan, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	var instAddr addrs.AbsResourceInstance
	var providerConfigAddr addrs.AbsProviderInstanceCorrect
	var resourceTypeName string
	var requiredConfigResources addrs.Set[addrs.AbsResourceInstance]
	var provisionersBefore, provisionersAfter []*eval.ResourceProvisioner
	deposedKey := states.NotDeposed
	if desired != nil {
		// By the time we're in the apply phase the desired and prior addresses
		// should already match because the plan phase is responsible for
		// handling concerns like 'moved" blocks that can cause addresses to
		// change, so we'll arbitrarily choose to prefer the desired address
		// whenever both are set.
		instAddr = desired.Addr
		// (deposed objects are never "desired")
		resourceTypeName = desired.ResourceType
		// TODO possibly nil here
		providerConfigAddr = *desired.ProviderInstance
		requiredConfigResources = desired.RequiredResourceInstances
	} else if prior != nil {
		instAddr = prior.Addr.InstanceAddr
		deposedKey = prior.Addr.DeposedKey
		resourceTypeName = prior.State.ResourceType
		providerConfigAddr = prior.State.ProviderInstanceAddr
	} else {
		// Both should not be nil but if they are then we'll treat it the same
		// way as if we dynamically discover that no change is actually
		// required, by returning a nil final plan to represent "noop".
		log.Printf("[TRACE] apply phase: ManagedFinalPlan without either desired or prior state, so no change is needed")
		return nil, diags
	}
	objAddr := instAddr.Object(deposedKey)
	log.Printf("[TRACE] apply phase: ManagedFinalPlan %s using %s", objAddr, providerConfigAddr)

	if desired != nil && prior == nil { // creating
		provisionersAfter = metadata.PostCreateProvisioners
	} else if prior != nil && desired == nil { // deleting
		if prior.State.Status == states.ObjectTainted {
			// No point in provisioning an object that is already tainted, since
			// it's going to get recreated on the next apply anyway.
			log.Printf("[TRACE] %s is tainted, so skipping provisioning", instAddr)
		} else {
			provisionersBefore = metadata.PreDeleteProvisioners
		}
	}

	tracer := contextTracer(ctx)
	if cb := tracer.StartManagedResourceInstanceObjectFinalPlan; cb != nil {
		priorVal := cty.NullVal(cty.DynamicPseudoType)
		if prior != nil && prior.State != nil {
			priorVal = prior.State.Value
		}
		configVal := cty.NullVal(cty.DynamicPseudoType)
		if desired != nil {
			configVal = desired.ConfigVal
		}
		ctx = cb(ctx, objAddr, priorVal, configVal, initialPlannedVal)
	}
	plannedVal := cty.DynamicVal // reassigned once we have a final value to return
	if cb := tracer.EndManagedResourceInstanceObjectFinalPlan; cb != nil {
		priorVal := cty.NullVal(cty.DynamicPseudoType)
		if prior != nil && prior.State != nil {
			priorVal = prior.State.Value
		}
		defer func() { // Extra closure to delay evaluating plannedVal and diags until we actually return
			cb(ctx, objAddr, priorVal, plannedVal, diags)
		}()
	}

	providerClient, moreDiags := ops.configOracle.ProviderInstance(ctx, providerConfigAddr)
	diags = diags.Append(moreDiags)
	if diags.HasErrors() {
		return nil, diags
	}

	providerAddr := providerConfigAddr.Config.Config.Provider
	resourceType := resources.NewManagedResourceType(providerAddr, resourceTypeName, providerClient)

	var desiredVal, currentVal cty.Value
	var currentPrivate []byte
	if desired != nil {
		desiredVal, moreDiags = ops.resourceDependenciesMissingCheck("resource", instAddr.String(), desired.ConfigVal)
		diags = diags.Append(moreDiags)
		if moreDiags.HasErrors() {
			return nil, diags
		}
	}
	if prior != nil {
		currentVal = prior.State.Value
		currentPrivate = prior.State.Private
	}

	resp, moreDiags := resourceType.PlanChanges(ctx, &resources.ManagedResourcePlanRequest{
		Current: resources.ValueWithPrivate{
			Value:   currentVal,
			Private: currentPrivate,
		},
		DesiredValue: desiredVal,
		// TODO: Do we want to still support ProviderMeta? If so, who is
		// responsible for propagating its value into here?
		ProviderMetaValue: cty.NilVal,
	}, objAddr)
	diags = diags.Append(moreDiags)
	if moreDiags.HasErrors() {
		return nil, diags
	}

	// The final plan must be a valid concretization of the initial plan,
	// which includes the rule that any known values from the initial plan
	// remain unchanged in the final plan.
	moreDiags = resourceType.ValidateFinalPlan(ctx, initialPlannedVal, resp.Planned.Value, objAddr)
	diags = diags.Append(moreDiags)
	if moreDiags.HasErrors() {
		return nil, diags
	}

	plannedVal = resp.Planned.Value // for our deferred call to tracer.EndManagedResourceInstanceObjectFinalPlan
	return &exec.ManagedResourceObjectFinalPlan{
		Addr:                      instAddr.Object(deposedKey),
		ResourceType:              resourceTypeName,
		RequiredResourceInstances: requiredConfigResources,
		PriorStateVal:             resp.Current.Value,
		ConfigVal:                 resp.DesiredValue,
		PlannedVal:                resp.Planned.Value,
		ProvisionersBefore:        provisionersBefore,
		ProvisionersAfter:         provisionersAfter,
		ProviderInstance:          providerConfigAddr,
		ProviderPrivate:           resp.Planned.Private,
	}, diags
}

// ManagedApply implements [exec.Operations].
func (ops *execOperations) ManagedApply(
	ctx context.Context,
	plan *exec.ManagedResourceObjectFinalPlan,
	fallback *exec.ResourceInstanceObject,
) (*exec.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if plan == nil {
		// TODO: if "fallback" is set then we should set it as current here to
		// honor the overall contract. In practice we currently never construct
		// an execution graph where it's possible for there to be a fallback
		// when there's no plan -- the dynamic absense of a plan is only
		// possible for in-place updates when we learn that no change is
		// actually needed, while fallback is only used for "create then
		// destroy" replacement -- so we'll skip this for now and just do nothing.
		log.Printf("[TRACE] apply phase: ManagedApply skipped because no change is needed")
		return nil, diags
	}

	providerConfigAddr := plan.ProviderInstance

	log.Printf("[TRACE] apply phase: ManagedApply %s using %s", plan.Addr, providerConfigAddr)
	if fallback != nil {
		if plan.Addr.IsDeposed() {
			// This should not happen: we can't have a fallback deposed object
			// when the object we're applying is already deposed itself.
			// (This is just a safety check because below we're still using the
			// old states.SyncState API that wants to model the fallback as
			// "maybe restore the deposed object to current" instead of just
			// generically rewriting the fallback object's address to not be deposed.
			diags = diags.Append(fmt.Errorf("can't apply changes to %s with fallback to deposed object %s", plan.Addr, fallback.Addr.DeposedKey))
			return nil, diags
		}
		if !fallback.Addr.IsDeposed() {
			// This should also not happen: the fallback object must always
			// be a deposed object that would become current again if we
			// fail to create the new object.
			diags = diags.Append(fmt.Errorf("can't apply changes to %s with fallback to non-deposed object %s", plan.Addr, fallback.Addr))
			return nil, diags
		}
		if !fallback.Addr.InstanceAddr.Equal(plan.Addr.InstanceAddr) {
			// This should also not happen: we should always be falling back
			// to a deposed object from the same resource instance we're trying
			// to create a new current object for here, since the fallback
			// will become the current instead if creation fails.
			diags = diags.Append(fmt.Errorf("can't apply changes to %s with fallback to %s: resource instance must match", plan.Addr, fallback.Addr))
		}
	}

	// This particular operation has a broader scope than most of them because
	// applying changes required careful coordination between the provider
	// calls and the state updates to make sure we always produce a consistent
	// result even in the face of partial failures. We have all of that behavior
	// grouped together into a single operation so that it's easier to read
	// through as normal, linear code without any special control flow, but
	// that comes at the expense of this function doing considerably more
	// work than most other operation methods do.

	tracer := contextTracer(ctx)
	if cb := tracer.StartManagedResourceInstanceObjectApply; cb != nil {
		ctx = cb(ctx, plan.Addr, plan.PriorStateVal, plan.PlannedVal)
	}
	resultVal := cty.DynamicVal // reassigned once we have a final value to return
	if cb := tracer.EndManagedResourceInstanceObjectApply; cb != nil {
		defer func() { // Extra closure to delay evaluating resultVal and diags until we actually return
			cb(ctx, plan.Addr, resultVal, diags)
		}()
	}

	providerAddr := providerConfigAddr.Config.Config.Provider
	schema, moreDiags := ops.plugins.ResourceTypeSchema(
		ctx,
		providerAddr,
		addrs.ManagedResourceMode,
		plan.ResourceType,
	)
	diags = diags.Append(moreDiags)
	if moreDiags.HasErrors() {
		return nil, diags
	}

	objAddr := plan.Addr

	// TODO: Encapsulate most of the following logic into a method of
	// [resources.ManagedResourceType].

	// TODO: We should preserve the marks from prior and config and reapply
	// them to the result.
	priorValUnmarked, _ := plan.PriorStateVal.UnmarkDeep()
	configValUnmarked, _ := plan.ConfigVal.UnmarkDeep()
	plannedValUnmarked, _ := plan.PlannedVal.UnmarkDeep()

	// Some provider client implementations can't tolerate the values being
	// completely nil, so we'll substitute null values to avoid crashes.
	if priorValUnmarked == cty.NilVal {
		priorValUnmarked = cty.NullVal(schema.Block.ImpliedType())
	}
	if configValUnmarked == cty.NilVal {
		configValUnmarked = cty.NullVal(schema.Block.ImpliedType())
	}
	if plannedValUnmarked == cty.NilVal {
		plannedValUnmarked = cty.NullVal(schema.Block.ImpliedType())
	}

	providerClient, moreDiags := ops.configOracle.ProviderInstance(ctx, providerConfigAddr)
	diags = diags.Append(moreDiags)
	if diags.HasErrors() {
		return nil, diags
	}

	if provs := plan.ProvisionersBefore; len(provs) != 0 {
		log.Printf("[TRACE] apply phase: ManagedApply running %d pre-apply provisioner(s) for %s", len(provs), plan.Addr)
		for _, prov := range provs {
			cont, provDiags := ops.runProvisioner(ctx, objAddr, prov, plan.PriorStateVal)
			diags = diags.Append(provDiags)
			if !cont {
				log.Printf("[TRACE] apply phase: ManagedApply %s pre-apply provisioner failed, so aborting", plan.Addr)
				return nil, diags
			}
		}
		log.Printf("[TRACE] apply phase: ManagedApply %s pre-apply provisioners finished", plan.Addr)
	}

	resp := providerClient.ApplyResourceChange(ctx, providers.ApplyResourceChangeRequest{
		TypeName:       plan.ResourceType,
		PriorState:     priorValUnmarked,
		Config:         configValUnmarked,
		PlannedState:   plannedValUnmarked,
		PlannedPrivate: plan.ProviderPrivate,
		// TODO: Do we want to still support ProviderMeta? If so, who is
		// responsible for propagating its value into here?
		ProviderMeta: cty.NullVal(cty.DynamicPseudoType),
	})
	diags = diags.Append(resp.Diagnostics)
	if resp.NewState == cty.NilVal {
		if !plan.PlannedVal.IsNull() && !diags.HasErrors() {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Provider produced inconsistent result after apply",
				fmt.Sprintf(
					"Provider %s did not return an error when applying changes for %s, but it also didn't return a new object to save.\n\nThis is a bug in the provider, which should be reported in the provider's own issue tracker.",
					providerAddr, plan.Addr,
				),
			))
		}
		// If we were given a "fallback" object then we need to restore it
		// back to being the current object for our resource instance before
		// we return.
		if fallback != nil {
			ok := ops.workingState.MaybeRestoreResourceInstanceDeposed(fallback.Addr.InstanceAddr, fallback.Addr.DeposedKey)
			if !ok {
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Failed to restore deposed object",
					fmt.Sprintf(
						"Failed to restore %s deposed object %s as the current object after failing to create its replacement.\n\nThe next plan will propose to destroy this deposed object. This is a bug in OpenTofu.",
						fallback.Addr.InstanceAddr, fallback.Addr.DeposedKey,
					),
				))
			}
		}
		result, moreDiags := ops.resourceInstanceStateObject(ctx, ops.workingState, plan.Addr.InstanceAddr, states.NotDeposed)
		diags = diags.Append(moreDiags)
		return result, diags
	}

	if provs := plan.ProvisionersAfter; len(provs) != 0 {
		log.Printf("[TRACE] apply phase: ManagedApply running %d post-apply provisioner(s) for %s", len(provs), plan.Addr)
		// The value a create-time provisioner's `self` refers to has to
		// carry its marks, or nothing downstream of it can tell that an
		// argument is sensitive: internal/engine/applying's own
		// runProvisioner unmarks the built provisioner config and hands the
		// marks to the ProvisionOutput hook precisely so a sensitive value
		// is not echoed into the log, and it has nothing to hand over when
		// the value it built from was never marked in the first place.
		//
		// resp.NewState comes back off the plugin wire, where marks cannot
		// travel, so they are re-applied here from the two sources
		// internal/tofu's own apply path composes (see
		// node_resource_abstract_instance.go's newValMarks): the marks the
		// PLANNED value carried, which is where a sensitive input variable
		// or a sensitive upstream attribute puts them, and the schema's own
		// Sensitive flags as they read TODAY.
		selfVal := markedAppliedValue(resp.NewState, plan.PlannedVal, schema.Block)
		for _, prov := range provs {
			cont, provDiags := ops.runProvisioner(ctx, objAddr, prov, selfVal)
			diags = diags.Append(provDiags)
			if !cont {
				log.Printf("[TRACE] apply phase: ManagedApply %s post-apply provisioner failed, so aborting", plan.Addr)
				break
			}
		}
		log.Printf("[TRACE] apply phase: ManagedApply %s post-apply provisioners finished", plan.Addr)
	}

	// TODO: objchange.AssertObjectCompatible to verify that the result is
	// consistent with what was planned. (That'll need the provider schema
	// we fetched above, but currently we're just discarding that schema.)

	var state *states.ResourceInstanceObjectFull
	if !resp.NewState.IsNull() {
		state = &states.ResourceInstanceObjectFull{
			Status:               appliedObjectStatus(plan, diags.HasErrors()),
			Value:                resp.NewState,
			Private:              resp.Private,
			ProviderInstanceAddr: providerConfigAddr,
			ResourceType:         plan.ResourceType,
			SchemaVersion:        uint64(schema.Version),

			// TODO: Propagate the dependencies from the desired object into
			// the final plan and then populate "Dependencies" here.
			// TODO: Propagate whether this resource instance has
			// "create_before_destroy" set into the final plan and then
			// populate CreateBeforeDestroy here.
		}

		// Legacy: This translates abs resource instances to config resources
		configDeps := addrs.MakeSet[addrs.ConfigResource]()
		for inst := range plan.RequiredResourceInstances.All() {
			configDeps.Add(inst.ConfigResource())
		}
		state.ConfigDependencies = slices.Collect(configDeps.All())

		// Modern: Precise dependencies
		state.Dependencies = slices.Collect(plan.RequiredResourceInstances.All())

		stateSrc, err := states.EncodeResourceInstanceObjectFull(state, schema.Block.ImpliedType())
		if err != nil {
			// This is a worst-case scenario where we've successfully changed
			// something but we can't represent what changed in the state for some
			// reason, and so the changes just get lost. It shouldn't be possible
			// to get here in practice though, because resp.NewState would've
			// already been decoded using the same schema if it came from a plugin,
			// and so it should definitely conform to that schema.
			// FIXME: A proper error message for this.
			diags = diags.Append(fmt.Errorf("failed to encode the new state for %s: %w", plan.Addr, err))
			return nil, diags
		}
		ops.workingState.SetResourceInstanceObjectFull(objAddr, stateSrc)
	} else {
		// A null value for "new state" represents that the object has been
		// deleted, so we now just need to remove it from the state.
		// Unfortunately this API is still a little quirkly and wants us to
		// pass the provider instance address so that it can update some
		// resource-level and instance-level metadata as a side-effect.
		ops.workingState.RemoveResourceInstanceObjectFull(objAddr, providerConfigAddr)
	}

	if state != nil {
		resultVal = state.Value // for our deferred call to tracer.EndManagedResourceInstanceObjectApply
	} else {
		resultVal = cty.NullVal(schema.Block.ImpliedType())
	}

	ret := &exec.ResourceInstanceObject{
		Addr:  plan.Addr,
		State: state, // nil if the object was deleted
	}
	return ret, diags
}

// ManagedPerformDepose implements [exec.Operations].
func (ops *execOperations) ManagedPerformDepose(
	ctx context.Context,
	currentObj *exec.ResourceInstanceObject,
	deletePlan *exec.ManagedResourceObjectFinalPlan,
) (*exec.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if currentObj == nil {
		log.Println("[TRACE] apply phase: ManagedPerformDepose with nil object (ignored)")
		return nil, diags
	}
	if deletePlan == nil || deletePlan.Addr.IsCurrent() || !deletePlan.PlannedVal.IsNull() || !deletePlan.Addr.InstanceAddr.Equal(currentObj.Addr.InstanceAddr) {
		// None of these situations should arise for a correct execution graph.
		diags = diags.Append(fmt.Errorf(
			"invalid delete plan for %s; this is a bug in OpenTofu",
			currentObj.Addr.InstanceAddr,
		))
		return nil, diags
	}
	log.Printf("[TRACE] apply phase: ManagedPerformDepose %s as %s", currentObj.Addr, deletePlan.Addr.DeposedKey)
	if currentObj.Addr.IsDeposed() {
		diags = diags.Append(fmt.Errorf(
			"attempting do depose %s when it's already deposed; this is a bug in OpenTofu",
			currentObj.Addr,
		))
		return nil, diags
	}

	deposedKey := deletePlan.Addr.DeposedKey
	ops.workingState.DeposeResourceInstanceObjectForceKey(deletePlan.Addr.InstanceAddr, deposedKey)
	return currentObj.IntoDeposed(deposedKey), diags
}

// ManagedDeposedMeta implements [exec.Operations].
func (ops *execOperations) ManagedDeposedMeta(ctx context.Context, instAddr addrs.AbsResourceInstance, deposedKey states.DeposedKey, prior *exec.ResourceInstanceObject) (*exec.ResourceInstanceObjectMeta, tfdiags.Diagnostics) {
	log.Printf("[TRACE] apply phase: ManagedDeposedMeta %s deposed object %s", instAddr, deposedKey)

	objAddr := instAddr.Object(deposedKey)
	configMeta := ops.configOracle.ResourceInstanceObjectMeta(ctx, objAddr)
	var state *states.ResourceInstanceObjectFull
	if prior != nil {
		state = prior.State
	}

	return exec.BuildResourceInstanceObjectMeta(objAddr, configMeta, state), nil
}

// ManagedAlreadyDeposed implements [exec.Operations].
func (ops *execOperations) ManagedAlreadyDeposed(
	ctx context.Context,
	instAddr addrs.AbsResourceInstance,
	deposedKey states.DeposedKey,
) (*exec.ResourceInstanceObject, tfdiags.Diagnostics) {
	log.Printf("[TRACE] apply phase: ManagedAlreadyDeposed %s deposed object %s", instAddr, deposedKey)
	// This is essentially the same as ResourceInstancePrior, but for deposed
	// objects rather than "current" objects. Therefore we'll share most of the
	// implementation between these two.
	return ops.resourceInstanceStateObject(ctx, ops.priorState, instAddr, deposedKey)
}

// ManagedChangeAddr implements [exec.Operations].
func (ops *execOperations) ManagedChangeAddr(
	ctx context.Context,
	currentObj *exec.ResourceInstanceObject,
	newAddr addrs.AbsResourceInstance,
) (*exec.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if currentObj == nil {
		log.Println("[TRACE] apply phase: ManagedChangeAddr with nil object (ignored)")
		return nil, diags
	}
	log.Printf("[TRACE] apply phase: ManagedChangeAddr from %s to %s", currentObj.Addr, newAddr)

	// Only "current" objects are expected to move between addresses in this
	// way, because the only reasonable thing to do with a deposed object is
	// to destroy it.
	if currentObj.Addr.IsDeposed() {
		diags = diags.Append(fmt.Errorf(
			"can't move %s to %s; this is a bug in OpenTofu",
			currentObj.Addr, newAddr,
		))
		return nil, diags
	}

	if !ops.workingState.MaybeMoveResourceInstance(currentObj.Addr.InstanceAddr, newAddr) {
		// We should not get here with a correctly-constructed execution graph
		// because currentObj being non-nil means that there should definitely
		// be something to move.
		diags = diags.Append(fmt.Errorf(
			"failed to move %s to %s; this is a bug in OpenTofu",
			currentObj.Addr, newAddr,
		))
		return nil, diags
	}
	return currentObj.WithNewAddr(newAddr), diags
}

// appliedObjectStatus decides whether the object an apply just produced is
// ready or tainted, and it answers the same way internal/tofu's maybeTainted
// (node_resource_apply_instance.go) answers for the old runtime: an error
// taints a CREATE and leaves anything else alone.
//
// The reason is stock's own, quoted from that function because it is the
// whole argument: "errors during updates will often not change the remote
// object at all. If there _were_ changes prior to the error, it's the
// provider's responsibility to record the effect of those changes in the
// object value it returned." A create that errors, by contrast, leaves an
// object in an undefined state, which is what tainting is for.
//
// This used to taint on ANY error here, which is a divergence that matters
// more in this fork than it would upstream. A tainted object drives
// internal/live/projection's issue #353 record: a failed UPDATE on a
// resource that happens to declare a create-time provisioner would have
// persisted a taint record, and every later plan would then propose
// destroying and re-creating a live resource whose update merely needed
// retrying. Stock never proposes that, and "match stock and go no further"
// is the bar.
//
// A create is recognized the way [exec.ManagedResourceObjectFinalPlan]
// documents it: a null prior state, which is also how a replacement's
// create leg arrives, since the graph splits a replace into two final plans
// and gives the create leg a null prior. There is no Action field on a
// final plan to consult instead, deliberately - see that type's own comment
// on why it carries no identity of its own.
func appliedObjectStatus(plan *exec.ManagedResourceObjectFinalPlan, failed bool) states.ObjectStatus {
	if !failed {
		return states.ObjectReady
	}
	if prior := plan.PriorStateVal; prior != cty.NilVal && !prior.IsNull() {
		// An update, or a destroy leg that errored with an object still
		// present. Stock leaves the status alone here.
		return states.ObjectReady
	}
	return states.ObjectTainted
}

// markedAppliedValue re-applies to a provider's post-apply object the marks
// that could not cross the plugin wire, so that a create-time provisioner's
// `self` sees a sensitive attribute as sensitive.
//
// It composes exactly the two sources internal/tofu's own apply path
// composes when it rebuilds newVal (node_resource_abstract_instance.go's
// newValMarks): the marks the planned value carried - where a sensitive
// input variable or a sensitive upstream attribute leaves them - and the
// schema's own Sensitive flags as the installed provider declares them
// today. Neither alone is enough: the schema does not know a value came
// from a sensitive variable, and the plan does not know a provider version
// has since started marking an attribute.
//
// A planned mark at a path the applied object does not have is dropped
// rather than carried, which is why the planned marks are filtered through
// the applied value's own structure before they are applied: a create can
// legitimately return an object shaped differently from the plan (an
// unknown that resolved into a null, say), and marking a path that is not
// there would fail the whole apply over a log-redaction detail.
func markedAppliedValue(applied, planned cty.Value, schema *configschema.Block) cty.Value {
	if applied == cty.NilVal || applied.IsNull() || schema == nil {
		return applied
	}

	var pvms []cty.PathValueMarks
	if planned != cty.NilVal {
		_, plannedMarks := planned.UnmarkDeepWithPaths()
		for _, pvm := range plannedMarks {
			if _, err := pvm.Path.Apply(applied); err != nil {
				continue
			}
			pvms = append(pvms, pvm)
		}
	}
	for _, pvm := range schema.ValueMarks(applied, nil, nil) {
		var merged bool
		for i, existing := range pvms {
			if !existing.Path.Equals(pvm.Path) {
				continue
			}
			combined := make(cty.ValueMarks, len(existing.Marks)+len(pvm.Marks))
			for k, v := range existing.Marks {
				combined[k] = v
			}
			for k, v := range pvm.Marks {
				combined[k] = v
			}
			pvms[i] = cty.PathValueMarks{Path: existing.Path, Marks: combined}
			merged = true
			break
		}
		if !merged {
			pvms = append(pvms, pvm)
		}
	}
	if len(pvms) == 0 {
		return applied
	}
	return applied.MarkWithPaths(pvms)
}
