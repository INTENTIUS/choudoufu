// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1944's mid-apply write, for an azapi_resource whose ARM type
// takes no tags (corpus-azapi-role-assignment's day2_crash, #1120). Such an
// object carries no marker, so its identity record is the only thing a later
// run can find it by: with no record, the next plan finds it at its declared
// id and refuses it as an overwrite of an unrecorded object
// ([SummaryOverwriteAtKey]). That is exactly the population #1944 writes as
// each apply step returns, so that a run killed before [WriteBack] does not
// leave the object behind with nothing naming it.
//
// [recordCarried] decided that population by resource TYPE alone
// ([identity.LocatedType], the `markers "record"` selection, and the
// plan's own record-fallback and unmarked-apply lists). azapi_resource is one
// type over every ARM type, and as a type it takes tags, so no type-level
// question reaches a role assignment: the mid-apply write skipped it and only
// the final pass recorded it. A graceful interrupt (the e2e build's
// TOFU_E2E_APPLY_RESOURCE_INTERRUPT) still reaches the final pass, which is
// why no gauntlet stage could see this; a run that dies hard could not.
func TestWriteInstanceRecordsAnUntaggableAzapiObject(t *testing.T) {
	ctx := context.Background()
	const estate = k8sOwnershipEstate
	addr := mustAddr(t, `azapi_resource.crash_first`)

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := NewRecordEnvelopeStore(raw, RecordKeyPrefix(estate))
	schema := azapiRecordSchema()
	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		azapiTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{"azapi_resource": schema}},
	}}

	write := func(t *testing.T, obj cty.Value) {
		t.Helper()
		src, err := (&states.ResourceInstanceObject{Value: obj, Status: states.ObjectReady}).Encode(schema.Block.ImpliedType(), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		st := states.NewState()
		st.EnsureModule(addrs.RootModuleInstance).SetResourceInstanceCurrent(addr.Resource, src, azapiTestProvider, addrs.NoKey)
		assertNoErrors(t, WriteInstance(ctx, WriteBackRequest{
			Store:      store,
			FinalState: st,
			Schemas:    schemas,
			Only:       &addr,
		}))
	}

	// The role assignment: no tags at any API version.
	write(t, azapiRoleObject())
	rec, _, _, found, err := store.GetIdentity(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if !found || rec.ImportID != armRoleID {
		t.Fatalf("the mid-apply write left the untaggable role assignment with record %+v (found %v), want its ARM id %q: a run killed after this step would leave an object nothing names", rec, found, armRoleID)
	}

	// The control: an azapi object whose ARM type takes tags carries its own
	// marker, and its record stays the final pass's bookkeeping, as before.
	tagged := mustAddr(t, `azapi_resource.crash_second`)
	addr = tagged
	write(t, cty.ObjectVal(map[string]cty.Value{
		"id":        cty.StringVal(movedRoleParent + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/crash-second"),
		"type":      cty.StringVal("Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31"),
		"name":      cty.StringVal("crash-second"),
		"parent_id": cty.StringVal(movedRoleParent),
		"body":      cty.EmptyObjectVal,
		"tags":      cty.MapValEmpty(cty.String),
	}))
	if _, _, _, found, err := store.GetIdentity(ctx, tagged); err != nil || found {
		t.Errorf("the mid-apply write recorded a taggable azapi object (found %v, %v); its marker carries it and its record is the final pass's", found, err)
	}
}
