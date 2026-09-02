// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/genconfig"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/intentius/choudoufu/internal/live/noimporter"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// NodePlannableResourceInstance represents a _single_ resource
// instance that is plannable. This means this represents a single
// count index, for example.
type NodePlannableResourceInstance struct {
	*NodeAbstractResourceInstance
	ForceCreateBeforeDestroy bool

	// skipRefresh indicates that we should skip refreshing individual instances
	skipRefresh bool

	// skipPlanChanges indicates we should skip trying to plan change actions
	// for any instances.
	skipPlanChanges bool

	// forceReplace are resource instance addresses where the user wants to
	// force generating a replace action. This set isn't pre-filtered, so
	// it might contain addresses that have nothing to do with the resource
	// that this node represents, which the node itself must therefore ignore.
	forceReplace []addrs.AbsResourceInstance

	// replaceTriggeredBy stores references from replace_triggered_by which
	// triggered this instance to be replaced.
	replaceTriggeredBy []*addrs.Reference

	// importTarget, if populated, contains the information necessary to plan
	// an import of this resource.
	importTarget EvaluatedConfigImportTarget
}

// EvaluatedConfigImportTarget is a target that we need to import. It's created when an import target originated from
// an import block, after everything regarding the configuration has been evaluated.
// At this point, the import target is of a single resource instance
type EvaluatedConfigImportTarget struct {
	// Config is the original import block for this import. This might be nil if the import did not originate in config.
	// Example of this is explicitly setting config as nil in ImportResolver.addCLIImportTarget.
	// It is done during nodeExpandPlannableResource.DynamicExpand and there should be no possible paths to dereference this with a nil value.
	Config *configs.Import

	// Addr is the actual address of the resource instance that we should import into. At this point, the address
	// should be fully evaluated
	Addr addrs.AbsResourceInstance

	// ID is the string ID of the resource to import. This is resource-instance specific.
	// This is used for ID-based imports (when the import block uses the "id" argument).
	ID string

	// Identity is the identity value for identity-based imports (when the import block uses the "identity" argument).
	// This will be a cty.Value containing an object with the identity attributes.
	// Either ID or Identity will be set, but not both.
	Identity cty.Value
}

var (
	_ GraphNodeModuleInstance       = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeReferenceable        = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeReferencer           = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeConfigResource       = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeResourceInstance     = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeAttachResourceConfig = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeAttachResourceState  = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeExecutable           = (*NodePlannableResourceInstance)(nil)
)

// GraphNodeEvalable
func (n *NodePlannableResourceInstance) Execute(ctx context.Context, evalCtx EvalContext, op walkOperation) tfdiags.Diagnostics {
	addr := n.ResourceInstanceAddr()

	ctx, span := tracing.Tracer().Start(
		ctx, traceNamePlanResourceInstance,
		tracing.SpanAttributes(
			traceattrs.String(traceAttrResourceInstanceAddr, addr.String()),
			traceattrs.String(traceAttrResourceType, addr.Resource.Resource.Type),
			traceattrs.Bool(traceAttrPlanRefresh, !n.skipRefresh),
			traceattrs.Bool(traceAttrPlanPlanChanges, !n.skipPlanChanges),
		),
	)
	defer span.End()

	diags := n.resolveProvider(ctx, evalCtx, true, states.NotDeposed)
	if diags.HasErrors() {
		tracing.SetSpanError(span, diags)
		return diags
	}
	span.SetAttributes(
		traceattrs.String(traceAttrProviderInstanceAddr, traceProviderInstanceAddr(n.ResolvedProvider.ProviderConfig, n.ResolvedProviderKey)),
	)

	// Eval info is different depending on what kind of resource this is
	switch addr.Resource.Resource.Mode {
	case addrs.ManagedResourceMode:
		diags = diags.Append(
			n.managedResourceExecute(ctx, evalCtx),
		)
	case addrs.DataResourceMode:
		diags = diags.Append(
			n.dataResourceExecute(ctx, evalCtx),
		)
	case addrs.EphemeralResourceMode:
		diags = diags.Append(
			n.ephemeralResourceExecute(ctx, evalCtx),
		)
	default:
		panic(fmt.Errorf("unsupported resource mode %s", n.Config.Mode))
	}
	tracing.SetSpanError(span, diags)
	return diags
}

func (n *NodePlannableResourceInstance) dataResourceExecute(ctx context.Context, evalCtx EvalContext) (diags tfdiags.Diagnostics) {
	config := n.Config
	addr := n.ResourceInstanceAddr()

	var change *plans.ResourceInstanceChange

	_, providerSchema, err := n.getProvider(ctx, evalCtx)
	diags = diags.Append(err)
	if diags.HasErrors() {
		return diags
	}

	diags = diags.Append(validateSelfRef(addr.Resource, config.Config, providerSchema))
	if diags.HasErrors() {
		return diags
	}

	checkRuleSeverity := tfdiags.Error
	if n.skipPlanChanges || n.preDestroyRefresh {
		checkRuleSeverity = tfdiags.Warning
	}

	change, state, repeatData, planDiags := n.planDataSource(ctx, evalCtx, checkRuleSeverity, n.skipPlanChanges)
	diags = diags.Append(planDiags)
	if diags.HasErrors() {
		return diags
	}

	// write the data source into both the refresh state and the
	// working state
	diags = diags.Append(n.writeResourceInstanceState(ctx, evalCtx, state, refreshState))
	if diags.HasErrors() {
		return diags
	}
	diags = diags.Append(n.writeResourceInstanceState(ctx, evalCtx, state, workingState))
	if diags.HasErrors() {
		return diags
	}

	diags = diags.Append(n.writeChange(ctx, evalCtx, change, ""))

	// Post-conditions might block further progress. We intentionally do this
	// _after_ writing the state/diff because we want to check against
	// the result of the operation, and to fail on future operations
	// until the user makes the condition succeed.
	checkDiags := evalCheckRules(
		ctx,
		addrs.ResourcePostcondition,
		n.Config.Postconditions,
		evalCtx, addr, repeatData,
		checkRuleSeverity,
	)
	diags = diags.Append(checkDiags)

	return diags
}

func (n *NodePlannableResourceInstance) ephemeralResourceExecute(ctx context.Context, evalCtx EvalContext) (diags tfdiags.Diagnostics) {
	config := n.Config
	addr := n.ResourceInstanceAddr()

	_, providerSchema, err := n.getProvider(ctx, evalCtx)
	diags = diags.Append(err)
	if diags.HasErrors() {
		return diags
	}

	diags = diags.Append(validateSelfRef(addr.Resource, config.Config, providerSchema))
	if diags.HasErrors() {
		return diags
	}

	checkRuleSeverity := tfdiags.Error
	if n.skipPlanChanges || n.preDestroyRefresh {
		checkRuleSeverity = tfdiags.Warning
	}

	state, repeatData, planDiags := n.planEphemeralResource(ctx, evalCtx, checkRuleSeverity, n.skipPlanChanges)
	diags = diags.Append(planDiags)
	if diags.HasErrors() {
		return diags
	}

	// write ephemeral resource only in the working state to make it accessible to the evaluator.
	// This is later filtered out when it comes to the state or plan writing.
	diags = diags.Append(n.writeResourceInstanceState(ctx, evalCtx, state, workingState))
	if diags.HasErrors() {
		return diags
	}

	// Post-conditions might block further progress. We intentionally do this
	// _after_ writing the state/diff because we want to check against
	// the result of the operation, and to fail on future operations
	// until the user makes the condition succeed.
	checkDiags := evalCheckRules(
		ctx,
		addrs.ResourcePostcondition,
		n.Config.Postconditions,
		evalCtx, addr, repeatData,
		checkRuleSeverity,
	)
	diags = diags.Append(checkDiags)

	return diags
}

func (n *NodePlannableResourceInstance) managedResourceExecute(ctx context.Context, evalCtx EvalContext) (diags tfdiags.Diagnostics) {
	config := n.Config
	addr := n.ResourceInstanceAddr()

	var instanceRefreshState *states.ResourceInstanceObject

	checkRuleSeverity := tfdiags.Error
	if n.skipPlanChanges || n.preDestroyRefresh {
		checkRuleSeverity = tfdiags.Warning
	}

	provider, providerSchema, err := n.getProvider(ctx, evalCtx)
	diags = diags.Append(err)
	if diags.HasErrors() {
		return diags
	}

	if config != nil {
		diags = diags.Append(validateSelfRef(addr.Resource, config.Config, providerSchema))
		if diags.HasErrors() {
			return diags
		}
	}

	importing := n.shouldImport(evalCtx)

	if importing && n.Config == nil && len(n.generateConfigPath) == 0 {
		// Then the user wrote an import target to a target that didn't exist.
		if n.Addr.Module.IsRoot() {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Import block target does not exist",
				Detail:   "The target for the given import block does not exist. If you wish to automatically generate config for this resource, use the -generate-config-out option within choudoufu plan. Otherwise, make sure the target resource exists within your configuration. For example:\n\n  choudoufu plan -generate-config-out=generated.tf",
				Subject:  n.importTarget.Config.DeclRange.Ptr(),
			})
		} else {
			// You can't generate config for a resource that is inside a
			// module, so we will present a different error message for
			// this case.
			diags = diags.Append(importResourceWithoutConfigDiags(n.Addr.String(), n.importTarget.Config))
		}
		return diags
	}

	// The plan-node seam (the foundation-order ruling (#388), ruling
	// 3): when nothing already targets this instance for import, and it
	// has no prior state, ask a configured resolver whether it knows this
	// instance's identity. resolver is nil by default, which makes this
	// whole block inert and leaves importing/instanceRefreshState exactly
	// as the pre-existing code above computed them.
	var resolvedImport *providers.ImportTarget
	if !importing && n.Config != nil {
		if resolver := evalCtx.ResourceIdentityResolver(); resolver != nil {
			// A RESOLVER-matched target, unlike an import block, is answered
			// by n.importState making a REAL live call through n.provider -
			// the same provider n.getProvider fetched above, ALREADY
			// configured (or not) by the time this node runs. An ordinary
			// plan/apply walk tolerates that provider being built from an
			// unknown value (verifyConfigIsKnown is only true for
			// walkImport, in NodeApplyableProvider.Execute), because
			// vanilla core never attempts a LIVE call through it for a
			// resource with no prior state and no import block - only this
			// resolver hook does. corpus-eks-basic's own greenfield stage
			// is what found this: provider.kubernetes's config depends on
			// data.aws_eks_cluster.cluster, correctly deferred to apply
			// (planDataSource's own "configuration not fully known yet, so
			// deferring to apply phase"), which leaves provider.kubernetes
			// itself built from an unknown value - and without this check,
			// n.importState went ahead and read against it anyway,
			// producing a live network call to whatever an unknown host
			// decodes to (localhost, for the kubernetes SDK) instead of the
			// plain "propose a create" a resolver that found nothing at all
			// already falls through to below. Skipping the resolver here
			// costs nothing a later run cannot recover: the object is
			// re-verified, correctly, the moment its provider's real
			// dependency becomes known (a later plan/apply, once the
			// managed resource it needs exists).
			configKnown := n.ResolvedProvider.ConfigKnown == nil || n.ResolvedProvider.ConfigKnown(n.ResolvedProviderKey)
			if evalCtx.State().ResourceInstance(addr) == nil && configKnown {
				configVal, resourceSchema, evalDiags := n.evaluateConfigForIdentity(ctx, evalCtx, providerSchema)
				diags = diags.Append(evalDiags)
				if diags.HasErrors() {
					return diags
				}
				if configVal != cty.NilVal {
					target, found, resolveDiags := resolver.ResolveResourceIdentity(ctx, addr, configVal, resourceSchema)
					diags = diags.Append(resolveDiags)
					if diags.HasErrors() {
						return diags
					}
					if found {
						resolvedImport = &target
					}
				}
			}
		}
	}

	// If the resource is to be imported, we now ask the provider for an Import
	// and a Refresh, and save the resulting state to instanceRefreshState.
	if importing {
		instanceRefreshState, diags = n.importState(ctx, evalCtx, addr, providers.ImportTarget{ID: n.importTarget.ID, Identity: n.importTarget.Identity}, provider, providerSchema)
	} else if resolvedImport != nil {
		// Edge 2 of the plan-node seam (the foundation-order ruling (#388),
		// ruling 3; issue #388): unlike an import block, which is the
		// operator's own promise that the object exists, a RESOLVER-supplied
		// target is this run's best guess at what a not-yet-applied
		// instance's identity would be. n.importState's hard-fail-on-absence
		// behavior is correct for the promise; it is wrong for the guess.
		// When the provider reports absence, that just means there is
		// nothing to import yet, so this falls through to the ordinary
		// no-prior-state path - the same one a resolver that found nothing
		// at all would take - instead of aborting the plan. A genuine
		// provider error (credentials, a malformed request, an actual
		// failure to answer) is not absence-shaped and stays fatal.
		var importDiags tfdiags.Diagnostics
		instanceRefreshState, importDiags = n.importState(ctx, evalCtx, addr, *resolvedImport, provider, providerSchema)
		if importDiags.HasErrors() && resolverImportAbsentDiagnostics(importDiags) {
			// Absent: this is no longer an import. Clear resolvedImport too
			// (not just instanceRefreshState), since the later "if importing
			// / else if resolvedImport != nil" block below - which sets
			// change.Importing on the plan - reads resolvedImport again and
			// would otherwise still record an import for an object that was
			// just found not to exist.
			resolvedImport = nil
			var readDiags tfdiags.Diagnostics
			instanceRefreshState, readDiags = n.readResourceInstanceState(ctx, evalCtx, addr)
			diags = diags.Append(readDiags)
			if diags.HasErrors() {
				return diags
			}
		} else {
			diags = importDiags
		}
	} else {
		var readDiags tfdiags.Diagnostics
		instanceRefreshState, readDiags = n.readResourceInstanceState(ctx, evalCtx, addr)
		diags = diags.Append(readDiags)
		if diags.HasErrors() {
			return diags
		}
	}

	// We'll save a snapshot of what we just read from the state into the
	// prevRunState before we do anything else, since this will capture the
	// result of any schema upgrading that readResourceInstanceState just did,
	// but not include any out-of-band changes we might detect in in the
	// refresh step below.
	diags = diags.Append(n.writeResourceInstanceState(ctx, evalCtx, instanceRefreshState, prevRunState))
	if diags.HasErrors() {
		return diags
	}
	// Also the refreshState, because that should still reflect schema upgrades
	// even if it doesn't reflect upstream changes.
	diags = diags.Append(n.writeResourceInstanceState(ctx, evalCtx, instanceRefreshState, refreshState))
	if diags.HasErrors() {
		return diags
	}

	// In 0.13 we could be refreshing a resource with no config.
	// We should be operating on managed resource, but check here to be certain
	if n.Config == nil || n.Config.Managed == nil {
		log.Printf("[WARN] managedResourceExecute: no Managed config value found in instance state for %q", n.Addr)
	} else {
		if instanceRefreshState != nil {
			prevCreateBeforeDestroy := instanceRefreshState.CreateBeforeDestroy
			prevSkipDestroy := instanceRefreshState.SkipDestroy

			// This change is usually written to the refreshState and then
			// updated value used for further graph execution. However, with
			// "refresh=false", refreshState is not being written, and then
			// some resources with updated configuration could be detached
			// due to misaligned create_before_destroy and skip_destroy in different graph nodes.
			instanceRefreshState.CreateBeforeDestroy = n.Config.Managed.CreateBeforeDestroy || n.ForceCreateBeforeDestroy
			// Destroy coming from the config is an hcl.Expression, so we need to evaluate it here, currently this only supports constant booleans
			skipDestroy, skipDiags := n.shouldSkipDestroy()
			diags = diags.Append(skipDiags)
			if diags.HasErrors() {
				return diags
			}
			instanceRefreshState.SkipDestroy = skipDestroy

			if n.skipRefresh {
				if prevCreateBeforeDestroy != instanceRefreshState.CreateBeforeDestroy || prevSkipDestroy != instanceRefreshState.SkipDestroy {
					diags = diags.Append(n.writeResourceInstanceState(ctx, evalCtx, instanceRefreshState, refreshState))
					if diags.HasErrors() {
						return diags
					}
				}
			}
		}
	}

	// Refresh, maybe
	// The import process handles its own refresh
	if !n.skipRefresh && !importing {
		s, refreshDiags := n.refresh(ctx, evalCtx, states.NotDeposed, instanceRefreshState)
		diags = diags.Append(refreshDiags)
		if diags.HasErrors() {
			return diags
		}

		instanceRefreshState = s

		if instanceRefreshState != nil {
			// When refreshing we start by merging the stored dependencies and
			// the configured dependencies. The configured dependencies will be
			// stored to state once the changes are applied. If the plan
			// results in no changes, we will re-write these dependencies
			// below.
			instanceRefreshState.Dependencies = mergeDeps(n.Dependencies, instanceRefreshState.Dependencies)
		}

		diags = diags.Append(n.writeResourceInstanceState(ctx, evalCtx, instanceRefreshState, refreshState))
		if diags.HasErrors() {
			return diags
		}
	}

	// Plan the instance, unless we're in the refresh-only mode
	expander := evalCtx.InstanceExpander()
	if !n.skipPlanChanges {

		// add this instance to n.forceReplace if replacement is triggered by
		// another change
		repData := expander.GetResourceInstanceRepetitionData(n.Addr)

		diags = diags.Append(n.replaceTriggered(ctx, evalCtx, repData))
		if diags.HasErrors() {
			return diags
		}

		change, instancePlanState, repeatData, planDiags := n.plan(
			ctx, evalCtx, nil, instanceRefreshState, n.ForceCreateBeforeDestroy, n.forceReplace,
		)
		diags = diags.Append(planDiags)
		if diags.HasErrors() {
			// If we are importing and generating a configuration, we need to
			// ensure the change is written out so the configuration can be
			// captured.
			if len(n.generateConfigPath) > 0 {
				// Update our return plan
				change := &plans.ResourceInstanceChange{
					Addr:         n.Addr,
					PrevRunAddr:  n.prevRunAddr(evalCtx),
					ProviderAddr: n.ResolvedProvider.ProviderConfig,
					Change: plans.Change{
						// we only need a placeholder, so this will be a NoOp
						Action:          plans.NoOp,
						Before:          instanceRefreshState.Value,
						After:           instanceRefreshState.Value,
						GeneratedConfig: n.generatedConfigHCL,
					},
				}
				diags = diags.Append(n.writeChange(ctx, evalCtx, change, ""))
			}

			return diags
		}

		if importing {
			change.Importing = &plans.Importing{
				ID:       n.importTarget.ID,
				Identity: n.importTarget.Identity,
			}
		} else if resolvedImport != nil {
			change.Importing = &plans.Importing{
				ID:       resolvedImport.ID,
				Identity: resolvedImport.Identity,
			}
		}

		// FIXME: here we update the change to reflect the reason for
		// replacement, but we still overload forceReplace to get the correct
		// change planned.
		if len(n.replaceTriggeredBy) > 0 {
			change.ActionReason = plans.ResourceInstanceReplaceByTriggers
		}

		// FIXME: it is currently important that we write resource changes to
		// the plan (n.writeChange) before we write the corresponding state
		// (n.writeResourceInstanceState).
		//
		// This is because the planned resource state will normally have the
		// status of states.ObjectPlanned, which causes later logic to refer to
		// the contents of the plan to retrieve the resource data. Because
		// there is no shared lock between these two data structures, reversing
		// the order of these writes will cause a brief window of inconsistency
		// which can lead to a failed safety check.
		//
		// Future work should adjust these APIs such that it is impossible to
		// update these two data structures incorrectly through any objects
		// reachable via the tofu.EvalContext API.
		diags = diags.Append(n.writeChange(ctx, evalCtx, change, ""))
		if diags.HasErrors() {
			return diags
		}
		diags = diags.Append(n.checkPreventDestroy(ctx, evalCtx, change))
		if diags.HasErrors() {
			return diags
		}

		diags = diags.Append(n.writeResourceInstanceState(ctx, evalCtx, instancePlanState, workingState))
		if diags.HasErrors() {
			return diags
		}

		// If this plan resulted in a NoOp, then apply won't have a chance to make
		// any changes to the stored dependencies. Since this is a NoOp we know
		// that the stored dependencies will have no effect during apply, and we can
		// write them out now.
		if change.Action == plans.NoOp && !depsEqual(instanceRefreshState.Dependencies, n.Dependencies) {
			// the refresh state will be the final state for this resource, so
			// finalize the dependencies here if they need to be updated.
			instanceRefreshState.Dependencies = n.Dependencies
			diags = diags.Append(n.writeResourceInstanceState(ctx, evalCtx, instanceRefreshState, refreshState))
			if diags.HasErrors() {
				return diags
			}
		}

		// Post-conditions might block completion. We intentionally do this
		// _after_ writing the state/diff because we want to check against
		// the result of the operation, and to fail on future operations
		// until the user makes the condition succeed.
		// (Note that some preconditions will end up being skipped during
		// planning, because their conditions depend on values not yet known.)
		checkDiags := evalCheckRules(
			ctx,
			addrs.ResourcePostcondition,
			n.Config.Postconditions,
			evalCtx, n.ResourceInstanceAddr(), repeatData,
			checkRuleSeverity,
		)
		diags = diags.Append(checkDiags)
	} else {
		// In refresh-only mode we need to evaluate the for-each expression in
		// order to supply the value to the pre- and post-condition check
		// blocks. This has the unfortunate edge case of a refresh-only plan
		// executing with a for-each map which has the same keys but different
		// values, which could result in a post-condition check relying on that
		// value being inaccurate. Unless we decide to store the value of the
		// for-each expression in state, this is unavoidable.
		forEach, _ := evaluateForEachExpression(ctx, n.Config.ForEach, evalCtx, n.ResourceAddr())
		repeatData := EvalDataForInstanceKey(n.ResourceInstanceAddr().Resource.Key, forEach)

		checkDiags := evalCheckRules(
			ctx,
			addrs.ResourcePrecondition,
			n.Config.Preconditions,
			evalCtx, addr, repeatData,
			checkRuleSeverity,
		)
		diags = diags.Append(checkDiags)

		// Even if we don't plan changes, we do still need to at least update
		// the working state to reflect the refresh result. If not, then e.g.
		// any output values referring to this will not react to the drift.
		// (Even if we didn't actually refresh above, this will still save
		// the result of any schema upgrading we did in readResourceInstanceState.)
		diags = diags.Append(n.writeResourceInstanceState(ctx, evalCtx, instanceRefreshState, workingState))
		if diags.HasErrors() {
			return diags
		}

		// Here we also evaluate post-conditions after updating the working
		// state, because we want to check against the result of the refresh.
		// Unlike in normal planning mode, these checks are still evaluated
		// even if pre-conditions generated diagnostics, because we have no
		// planned changes to block.
		checkDiags = evalCheckRules(
			ctx,
			addrs.ResourcePostcondition,
			n.Config.Postconditions,
			evalCtx, addr, repeatData,
			checkRuleSeverity,
		)
		diags = diags.Append(checkDiags)
	}

	return diags
}

// evaluateConfigForIdentity evaluates this instance's configuration for the
// plan-node seam's ResourceIdentityResolver (see resource_identity.go), at a
// point in managedResourceExecute that runs before n.plan.
//
// It repeats, ahead of time, exactly the evaluation
// NodeAbstractResourceInstance.plan performs a few lines later into a local
// it calls origConfigVal (node_resource_abstract_instance.go): call
// evaluateForEachExpression over n.Config.ForEach to build keyData, then
// evalCtx.EvaluateBlock over n.Config.Config and the resource's schema
// block. managedResourceExecute has neither keyData nor origConfigVal in
// scope yet at the point it needs to ask the resolver, so this duplicates
// that call rather than restructuring plan() to return early; both calls
// are the same deterministic HCL evaluation over the same inputs, so
// n.plan's own evaluation and result are unaffected by this one running
// first.
//
// Returns cty.NilVal (no error) if this resource type has no schema, which
// callers must treat as "nothing to resolve against" rather than an error;
// that case is already unreachable in practice because managedResourceExecute
// has a provider and schema by the time it calls this, but a resolver must
// never be handed a NilVal by mistake.
func (n *NodePlannableResourceInstance) evaluateConfigForIdentity(ctx context.Context, evalCtx EvalContext, providerSchema providers.ProviderSchema) (cty.Value, providers.Schema, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	resourceSchema, _ := providerSchema.SchemaForResourceAddr(n.Addr.Resource.Resource)
	if resourceSchema == nil {
		return cty.NilVal, providers.Schema{}, diags
	}

	forEach, _ := evaluateForEachExpression(ctx, n.Config.ForEach, evalCtx, n.Addr)
	keyData := EvalDataForInstanceKey(n.ResourceInstanceAddr().Resource.Key, forEach)

	configVal, _, configDiags := evalCtx.EvaluateBlock(ctx, n.Config.Config, resourceSchema.Block, nil, keyData)
	diags = diags.Append(configDiags.InConfigBody(n.Config.Config, n.Addr.String()))
	if configDiags.HasErrors() {
		return cty.NilVal, providers.Schema{}, diags
	}

	return configVal, *resourceSchema, diags
}

// replaceTriggered checks if this instance needs to be replace due to a change
// in a replace_triggered_by reference. If replacement is required, the
// instance address is added to forceReplace
func (n *NodePlannableResourceInstance) replaceTriggered(ctx context.Context, evalCtx EvalContext, repData instances.RepetitionData) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if n.Config == nil {
		return diags
	}

	for _, expr := range n.Config.TriggersReplacement {
		ref, replace, evalDiags := evalCtx.EvaluateReplaceTriggeredBy(ctx, expr, repData)
		diags = diags.Append(evalDiags)
		if diags.HasErrors() {
			continue
		}

		if replace {
			// FIXME: forceReplace accomplishes the same goal, however we may
			// want to communicate more information about which resource
			// triggered the replacement in the plan.
			// Rather than further complicating the plan method with more
			// options, we can refactor both of these features later.
			n.forceReplace = append(n.forceReplace, n.Addr)
			log.Printf("[DEBUG] ReplaceTriggeredBy forcing replacement of %s due to change in %s", n.Addr, ref.DisplayString())

			n.replaceTriggeredBy = append(n.replaceTriggeredBy, ref)
			break
		}
	}

	return diags
}

// resolverImportAbsentSignals are substrings of a provider's own
// ImportResourceState diagnostic that mean "no such object" rather than
// "the provider failed to answer" - the same pair
// internal/live/projection/build.go's notFoundDiagnostics checks for the
// pre-walk projection path (aws_lambda_permission, issue #297, is the
// confirmed instance: GetPolicy on the *function* returns
// ResourceNotFoundException when the function itself does not exist
// either). Duplicated here rather than imported: this package must never
// import the fork's live-mode package (see ResourceIdentityResolver's doc
// comment in resource_identity.go - the dependency runs the other way),
// so the two lists are kept in sync by hand.
var resolverImportAbsentSignals = []string{
	"couldn't find resource",
	"ResourceNotFoundException",
}

// resolverImportSyntheticAbsentSummaries are importState's OWN
// diagnostics - not the provider's - that already mean "there is nothing
// here": an empty ImportedResources list, an imported object with a null
// value, or a post-import refresh that comes back null (the object
// existed a moment ago as far as ImportResourceState was concerned, but is
// gone by the time refresh asks again). Matched on Summary, since
// importState constructs these itself with fixed text rather than
// forwarding a provider's free-form diagnostic.
var resolverImportSyntheticAbsentSummaries = map[string]bool{
	"Import returned no resources":             true,
	"Import returned null resource":            true,
	"Cannot import non-existent remote object": true,
}

// resolverImportAbsentDiagnostics reports whether every error-severity
// diagnostic in diags describes an ordinary absence - either one of
// importState's own synthetic "there is nothing here" diagnostics or a
// provider's not-found-shaped error - rather than a genuine failure to
// answer. It is edge 2 of the plan-node seam
// (the foundation-order ruling (#388), ruling 3; issue #388): a
// resolver-supplied target is a guess, not a promise the way an import
// block's is, so an absent object here means the guess was wrong about
// there being anything to import, not a reason to abort the plan.
//
// A single diagnostic that does not match either shape - a credentials
// problem, a malformed request, a genuine provider failure - makes this
// report false, and the caller's existing hard stop applies untouched.
// diags with no errors at all (only warnings, or empty) also reports
// false, since the caller only consults this after confirming
// HasErrors().
func resolverImportAbsentDiagnostics(diags tfdiags.Diagnostics) bool {
	sawError := false
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		sawError = true
		desc := d.Description()
		if resolverImportSyntheticAbsentSummaries[desc.Summary] {
			continue
		}
		matched := false
		for _, signal := range resolverImportAbsentSignals {
			if strings.Contains(desc.Summary, signal) || strings.Contains(desc.Detail, signal) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return sawError
}

// resolvedIdentityValues extracts a resolved providers.ImportTarget's own
// identity object as one string per attribute, keyed by the provider's own
// name for it - the shape [noimporter.SynthesizeStub] needs to place a
// no-classic-Importer stub, and the same shape
// internal/live/projection/build.go's own identityValues parameter to
// importAndRead carries for the pre-walk projection path.
//
// Only IsIdentityBased reports anything: an ID-based-only target is an
// opaque string with no attribute breakdown to recover one from, and
// inventing an attribute name for it (an "id" attribute, say) would be a
// guess this run has no basis for - HANDOFF's safety rule names exactly
// this shape of fabrication as the one thing worse than a refusal.
// [providers.ImportTarget.IsIdentityBased] is only ever true for an
// object this run itself resolved - an import block's own literal
// identity, or a resolver's answer built from a real record, marker or
// evaluated configuration value (see internal/live/projection/
// noderesolver.go's ResolveResourceIdentity) - never a default, so every
// value this returns is one the resolution path already stands behind.
//
// A null, unknown or unconvertible attribute is silently skipped rather
// than forced to a string: [noimporter.SynthesizeStub] already leaves an
// unnamed attribute null on the stub, the same place ImportResourceState's
// own stub would have left it, so skipping here is not a loss of
// information, only a refusal to fabricate one.
func resolvedIdentityValues(target providers.ImportTarget) map[string]string {
	if !target.IsIdentityBased() {
		return nil
	}
	ty := target.Identity.Type()
	if !ty.IsObjectType() {
		return nil
	}
	values := make(map[string]string, len(ty.AttributeTypes()))
	for name := range ty.AttributeTypes() {
		v := target.Identity.GetAttr(name)
		if v.IsNull() || !v.IsKnown() {
			continue
		}
		s, err := convert.Convert(v, cty.String)
		if err != nil {
			continue
		}
		values[name] = s.AsString()
	}
	return values
}

func (n *NodePlannableResourceInstance) importState(ctx context.Context, evalCtx EvalContext, addr addrs.AbsResourceInstance, importTarget providers.ImportTarget, provider providers.Interface, providerSchema providers.ProviderSchema) (*states.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	absAddr := addr.Resource.Absolute(evalCtx.Path())

	diags = diags.Append(evalCtx.Hook(func(h Hook) (HookAction, error) {
		return h.PrePlanImport(absAddr, importTarget)
	}))
	if diags.HasErrors() {
		return nil, diags
	}

	resp := provider.ImportResourceState(ctx, providers.ImportResourceStateRequest{
		TypeName: addr.Resource.Resource.Type,
		Target:   importTarget,
	})
	if resp.Diagnostics.HasErrors() {
		if ok, detail := noimporter.Diagnostics(resp.Diagnostics); ok {
			// Not the provider erroring - the opposite. It is correctly
			// answering that ImportResourceState is not implemented for
			// this type at all, a fact fixed in the provider's own Go code
			// that no identity or retry changes - see noimporter.Diagnostics'
			// own doc comment. internal/live/projection/build.go's
			// importAndRead reaches the identical fact through the
			// pre-walk projection path and, rather than stop there,
			// builds the stub ImportResourceState itself would have
			// returned from an identity this run already resolved. This
			// mirrors that: a target with a resolved identity OBJECT
			// (providers.ImportTarget.Identity, set only when this run
			// resolved a real value, never a default) has real,
			// attribute-named values to place; noimporter.SynthesizeStub
			// places what it can and leaves the rest null, exactly as
			// ImportResourceState's own stub would. A target carrying
			// only an opaque ID string has no such breakdown - nothing
			// here invents one - so the refusal below stands for it.
			var stubbed bool
			if resourceSchema, _ := providerSchema.SchemaForResourceAddr(addr.Resource.Resource); resourceSchema != nil {
				if stub, stubOK := noimporter.SynthesizeStub(*resourceSchema, resolvedIdentityValues(importTarget)); stubOK {
					log.Printf("[TRACE] importState: %s has no classic Importer; synthesizing an import stub from its own resolved identity instead of refusing", addr.Resource.Resource.Type)
					resp = providers.ImportResourceStateResponse{
						ImportedResources: []providers.ImportedResource{{
							TypeName: addr.Resource.Resource.Type,
							State:    stub,
						}},
					}
					stubbed = true
				}
			}
			if !stubbed {
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Resource type has no classic Importer",
					fmt.Sprintf(
						"The %s with %s cannot be imported: %s. This is not the provider erroring - it is answering that ImportResourceState is not implemented for this type at all, a fixed property of the provider's own code that no identity or retry changes.",
						addr, importTarget.String(), detail,
					),
				))
				return nil, diags
			}
		}
	}
	diags = diags.Append(resp.Diagnostics)
	if diags.HasErrors() {
		return nil, diags
	}

	imported := resp.ImportedResources

	if len(imported) == 0 {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Import returned no resources",
			fmt.Sprintf("While attempting to import with ID %s, the provider"+
				"returned no instance states.",
				importTarget.String(),
			),
		))
		return nil, diags
	}
	for _, obj := range imported {
		log.Printf("[TRACE] graphNodeImportState: import %s %q produced instance object of type %s", absAddr.String(), importTarget.String(), obj.TypeName)
	}
	if len(imported) > 1 {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Multiple import states not supported",
			fmt.Sprintf("While attempting to import with ID %s, the provider "+
				"returned multiple resource instance states. This "+
				"is not currently supported.",
				importTarget.String(),
			),
		))
		return nil, diags
	}

	// call post-import hook
	diags = diags.Append(evalCtx.Hook(func(h Hook) (HookAction, error) {
		return h.PostPlanImport(absAddr, imported)
	}))

	if imported[0].TypeName == "" {
		diags = diags.Append(fmt.Errorf("import of %s didn't set type", n.Addr.String()))
		return nil, diags
	}

	importedState := imported[0].AsInstanceObject()

	if importedState.Value.IsNull() {
		importDesc := n.importTarget.ID
		if importDesc == "" {
			importDesc = n.importTarget.Identity.GoString()
		}
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Import returned null resource",
			fmt.Sprintf("While attempting to import with %s, the provider "+
				"returned an instance with no state.",
				importDesc,
			),
		))
	}

	// refresh
	riNode := &NodeAbstractResourceInstance{
		Addr: n.Addr,
		NodeAbstractResource: NodeAbstractResource{
			ResolvedProvider: n.ResolvedProvider,
		},
		ResolvedProviderKey: n.ResolvedProviderKey,
	}
	instanceRefreshState, refreshDiags := riNode.refresh(ctx, evalCtx, states.NotDeposed, importedState)
	diags = diags.Append(refreshDiags)
	if diags.HasErrors() {
		return instanceRefreshState, diags
	}

	// verify the existence of the imported resource
	if instanceRefreshState.Value.IsNull() {
		var diags tfdiags.Diagnostics
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot import non-existent remote object",
			fmt.Sprintf(
				"While attempting to import an existing object to %q, "+
					"the provider detected that no object exists with the given id or identity. "+
					"Only pre-existing objects can be imported; check that the id or identity "+
					"is correct and that it is associated with the provider's "+
					"configured region or endpoint, or use \"choudoufu apply\" to "+
					"create a new remote object for this resource.",
				n.Addr,
			),
		))
		return instanceRefreshState, diags
	}

	// Insert marks from configuration
	if n.Config != nil {
		keyData := EvalDataForNoInstanceKey

		switch {
		case n.Config.Count != nil:
			keyData = InstanceKeyEvalData{
				CountIndex: cty.UnknownVal(cty.Number),
			}
		case n.Config.ForEach != nil:
			keyData = InstanceKeyEvalData{
				EachKey:   cty.UnknownVal(cty.String),
				EachValue: cty.UnknownVal(cty.DynamicPseudoType),
			}
		}

		valueWithConfigurationSchemaMarks, _, configDiags := evalCtx.EvaluateBlock(ctx, n.Config.Config, n.Schema, nil, keyData)
		diags = diags.Append(configDiags)
		if configDiags.HasErrors() {
			return instanceRefreshState, diags
		}

		_, marks := instanceRefreshState.Value.UnmarkDeepWithPaths()
		_, configSchemaMarks := valueWithConfigurationSchemaMarks.UnmarkDeepWithPaths()
		merged := combinePathValueMarks(marks, configSchemaMarks)

		instanceRefreshState.Value = instanceRefreshState.Value.MarkWithPaths(merged)
	}

	// If we're importing and generating config, generate it now.
	if len(n.generateConfigPath) > 0 {
		if n.Config != nil {
			return instanceRefreshState, diags.Append(fmt.Errorf("tried to generate config for %s, but it already exists", n.Addr))
		}

		schema, _ := providerSchema.SchemaForResourceAddr(n.Addr.Resource.Resource)
		if schema == nil {
			// Should be caught during validation, so we don't bother with a pretty error here
			diags = diags.Append(fmt.Errorf("provider does not support resource type for %q", n.Addr))
			return instanceRefreshState, diags
		}

		// Generate the HCL string first, then parse the HCL body from it.
		// First we generate the contents of the resource block for use within
		// the planning node. Then we wrap it in an enclosing resource block to
		// pass into the plan for rendering.
		generatedHCLAttributes, generatedDiags := n.generateHCLStringAttributes(n.Addr, instanceRefreshState, schema.Block)
		diags = diags.Append(generatedDiags)

		n.generatedConfigHCL = genconfig.WrapResourceContents(n.Addr, generatedHCLAttributes)

		// parse the "file" as HCL to get the hcl.Body
		synthHCLFile, hclDiags := hclsyntax.ParseConfig([]byte(generatedHCLAttributes), filepath.Base(n.generateConfigPath), hcl.Pos{Byte: 0, Line: 1, Column: 1})
		diags = diags.Append(hclDiags)
		if hclDiags.HasErrors() {
			return instanceRefreshState, diags
		}

		// We have to do a kind of mini parsing of the content here to correctly
		// mark attributes like 'provider' as hidden. We only care about the
		// resulting content, so it's remain that gets passed into the resource
		// as the config.
		_, remain, resourceDiags := synthHCLFile.Body.PartialContent(configs.ResourceBlockSchema)
		diags = diags.Append(resourceDiags)
		if resourceDiags.HasErrors() {
			return instanceRefreshState, diags
		}

		n.Config = &configs.Resource{
			Mode:     addrs.ManagedResourceMode,
			Type:     n.Addr.Resource.Resource.Type,
			Name:     n.Addr.Resource.Resource.Name,
			Config:   remain,
			Managed:  &configs.ManagedResource{},
			Provider: n.ResolvedProvider.ProviderConfig.Provider,
		}
	}

	diags = diags.Append(riNode.writeResourceInstanceState(ctx, evalCtx, instanceRefreshState, refreshState))
	return instanceRefreshState, diags
}

func (n *NodePlannableResourceInstance) shouldImport(evalCtx EvalContext) bool {
	if n.importTarget.ID == "" && (n.importTarget.Identity == cty.NilVal || n.importTarget.Identity.IsNull()) {
		return false
	}

	// If the import target already has a state - we should not attempt to import it, but instead run a normal plan
	// for it
	state := evalCtx.State()
	return state.ResourceInstance(n.ResourceInstanceAddr()) == nil
}

// generateHCLStringAttributes produces a string in HCL format for the given
// resource state and schema without the surrounding block.
func (n *NodePlannableResourceInstance) generateHCLStringAttributes(addr addrs.AbsResourceInstance, state *states.ResourceInstanceObject, schema *configschema.Block) (string, tfdiags.Diagnostics) {
	filteredSchema := schema.Filter(
		configschema.FilterOr(
			configschema.FilterReadOnlyAttribute,
			configschema.FilterDeprecatedAttribute,

			// The legacy SDK adds an Optional+Computed "id" attribute to the
			// resource schema even if not defined in provider code.
			// During validation, however, the presence of an extraneous "id"
			// attribute in config will cause an error.
			// Remove this attribute so we do not generate an "id" attribute
			// where there is a risk that it is not in the real resource schema.
			//
			// TRADEOFF: Resources in which there actually is an
			// Optional+Computed "id" attribute in the schema will have that
			// attribute missing from generated config.
			configschema.FilterHelperSchemaIdAttribute,
		),
		configschema.FilterDeprecatedBlock,
	)

	providerAddr := addrs.LocalProviderConfig{
		LocalName: n.ResolvedProvider.ProviderConfig.Provider.Type,
		Alias:     n.ResolvedProvider.ProviderConfig.Alias,
	}

	return genconfig.GenerateResourceContents(addr, filteredSchema, providerAddr, state.Value)
}

// mergeDeps returns the union of 2 sets of dependencies
func mergeDeps(a, b []addrs.ConfigResource) []addrs.ConfigResource {
	switch {
	case len(a) == 0:
		return b
	case len(b) == 0:
		return a
	}

	set := make(map[string]addrs.ConfigResource)

	for _, dep := range a {
		set[dep.String()] = dep
	}

	for _, dep := range b {
		set[dep.String()] = dep
	}

	newDeps := make([]addrs.ConfigResource, 0, len(set))
	for _, dep := range set {
		newDeps = append(newDeps, dep)
	}

	return newDeps
}

func depsEqual(a, b []addrs.ConfigResource) bool {
	if len(a) != len(b) {
		return false
	}

	// Because we need to sort the deps to compare equality, make shallow
	// copies to prevent concurrently modifying the array values on
	// dependencies shared between expanded instances.
	copyA, copyB := make([]addrs.ConfigResource, len(a)), make([]addrs.ConfigResource, len(b))
	copy(copyA, a)
	copy(copyB, b)
	a, b = copyA, copyB

	less := func(s []addrs.ConfigResource) func(i, j int) bool {
		return func(i, j int) bool {
			return s[i].String() < s[j].String()
		}
	}

	sort.Slice(a, less(a))
	sort.Slice(b, less(b))

	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}
