// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planning

// choudoufu identity resolution on the new runtime (#2256 U6, #2342). A
// fork-only file: planDesiredManagedResourceInstance calls the two functions
// here at the two places a resolver-supplied import differs from an import
// statement written by the operator. See package identityresolve.

import (
	"context"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/identityresolve"
	"github.com/intentius/choudoufu/internal/lang/eval"
	"github.com/intentius/choudoufu/internal/live/noimporter"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/resources"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// resolveImportStatement asks the plan's identity resolver about a desired
// instance that has no prior object and no import statement, and returns the
// import statement its answer becomes, or nil. The old runtime asks at the
// same point (NodePlannableResourceInstance.managedResourceExecute in
// internal/tofu), with the instance's evaluated configuration before any
// adjuster has run. A resolver error (NodeResolver's "No source for this
// instance's identity" refusal, say) fails the instance.
//
// The statement has no DeclRange, so FromConfig reports false, the same as a
// CLI import target. That only matters when a prior object exists, and the
// resolver is never asked then.
func (p *planGlue) resolveImportStatement(ctx context.Context, inst *eval.DesiredResourceInstance, resourceType *resources.ManagedResourceType) (*eval.ImportStatement, tfdiags.Diagnostics) {
	target, found, diags := identityresolve.Resolve(ctx, p.planCtx.identityResolver, inst.Addr, inst.ConfigVal, resourceType.LoadSchema)
	if diags.HasErrors() || !found {
		return nil, diags
	}
	return &eval.ImportStatement{
		Addr:     inst.Addr,
		ID:       target.ID,
		Identity: target.Identity,
	}, diags
}

// processResolvedImport is processImport for a resolver-supplied statement.
// A resolver's target is a guess about an object, not the operator's promise
// that it exists, so two failures processImport treats as fatal are not:
//
//   - the provider answers that the object is absent ([identityresolve.ImportAbsent]):
//     absent is true, with no diagnostics, and the caller plans a create;
//   - the provider has no classic Importer for the type: the stub
//     ImportResourceState would have returned is built from the target's
//     identity object, and refreshed like any import. A target carrying only
//     an opaque ID gets [identityresolve.NoImporterRefusal] instead.
//
// Any other error is returned as it is.
func processResolvedImport(
	ctx context.Context,
	addr addrs.AbsResourceInstanceObject,
	providerInst addrs.AbsProviderInstanceCorrect,
	provider addrs.Provider,
	resourceType *resources.ManagedResourceType,
	stmt *eval.ImportStatement,
) (state *resources.ManagedResourceStateWithIdentity, full *states.ResourceInstanceObjectFullSrc, absent bool, diags tfdiags.Diagnostics) {
	state, full, diags = processImport(ctx, addr, providerInst, resourceType, stmt)
	if !diags.HasErrors() {
		return state, full, false, diags
	}
	target := providers.ImportTarget{ID: stmt.ID, Identity: stmt.Identity}

	if ok, detail := noimporter.Diagnostics(diags); ok {
		schema, schemaDiags := resourceType.LoadSchema(ctx)
		if schemaDiags.HasErrors() {
			return nil, nil, false, diags.Append(schemaDiags)
		}
		stub, stubOK := noimporter.SynthesizeStub(schema, identityresolve.IdentityValues(target))
		if !stubOK {
			var refusal tfdiags.Diagnostics
			return nil, nil, false, refusal.Append(identityresolve.NoImporterRefusal(addr.InstanceAddr, target, detail))
		}
		encoded, encDiags := resourceType.EncodeValue(ctx, stub, addr)
		if encDiags.HasErrors() {
			return nil, nil, false, encDiags
		}
		// The same shape processImport builds from a provider's answer.
		state = &resources.ManagedResourceStateWithIdentity{
			ValueWithPrivate: resources.ValueWithPrivate{Value: stub},
		}
		full = &states.ResourceInstanceObjectFullSrc{
			Value:        states.ValueJSONWithMetadata{ValueJSON: encoded.ValueJSON},
			Status:       states.ObjectReady,
			ResourceType: addr.InstanceAddr.Resource.Resource.Type,
		}
		return state, full, false, nil
	}

	if identityresolve.ImportAbsent(diags, target, provider.Type) {
		return nil, nil, true, nil
	}
	return nil, nil, false, diags
}

// refreshResolvedImport refreshes a resolved import when the plan skips
// refresh. The old runtime's importState refreshes every import it makes,
// whatever -refresh says, and judges a resolved object that reads back null
// absent ("Cannot import non-existent remote object", one of
// [identityresolve.ImportAbsent]'s shapes). Live mode always plans with
// refresh skipped, because its prior state was read from the cloud a moment
// before (internal/backend/local), so without this an import the provider
// answers with a stub for an object that does not exist is planned as an
// update of that stub, and the apply re-plans it as a create.
//
// absent is true, with nil state, when the object reads back null; the
// caller plans a create. Otherwise the refreshed state and its encoding are
// returned, to stand as the plan's refreshed state for the instance.
func refreshResolvedImport(
	ctx context.Context,
	inst *eval.DesiredResourceInstance,
	providerInst addrs.AbsProviderInstanceCorrect,
	resourceType *resources.ManagedResourceType,
	imported *resources.ManagedResourceStateWithIdentity,
	importedFull *states.ResourceInstanceObjectFullSrc,
) (state *resources.ManagedResourceStateWithIdentity, full *states.ResourceInstanceObjectFullSrc, absent bool, diags tfdiags.Diagnostics) {
	state, full, diags = processRefresh(ctx, inst.Addr.CurrentObject(), providerInst, resourceType, inst.RequiredResourceInstances, imported, importedFull)
	if diags.HasErrors() {
		return nil, nil, false, diags
	}
	if state == nil || state.Value.IsNull() {
		return nil, nil, true, diags
	}
	return state, full, false, diags
}
