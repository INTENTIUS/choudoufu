// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/intentius/choudoufu/pkg/staterecord"
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

// corpus-azapi-role-assignment's day2_replace, distilled: a
// create_before_destroy replace of an untaggable azapi role assignment (its
// name, a GUID, changes). With the mid-apply write now reaching it, the
// create leg records the new object as the identity and the old one under
// its deposed key, and the destroy leg returns with the deposed key gone.
// What must hold after the final pass is what stock's replace leaves: the
// record names the new object, nothing deposed is left for a later run to
// read as a second claimant, and the old object's identity is nowhere.
func TestCreateBeforeDestroyOfAnUntaggableAzapiObjectLeavesTheNewRecord(t *testing.T) {
	ctx := context.Background()
	addr := mustAddr(t, `azapi_resource.rotating_grant`)
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := NewRecordEnvelopeStore(raw, RecordKeyPrefix(k8sOwnershipEstate))
	schema := azapiRecordSchema()
	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		azapiTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{"azapi_resource": schema}},
	}}
	const (
		oldName = "3b0e6f1c-5a2d-4c8e-9f17-2d6a4b8c0e1a"
		newName = "7c4d2e9a-1b3f-4a6c-8d5e-9f0a1b2c3d4e"
	)
	idOf := func(name string) string {
		return movedRoleParent + "/providers/Microsoft.Authorization/roleAssignments/" + name
	}
	encode := func(name string) *states.ResourceInstanceObjectSrc {
		src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: cty.ObjectVal(map[string]cty.Value{
			"id":        cty.StringVal(idOf(name)),
			"type":      cty.StringVal(movedRoleType),
			"name":      cty.StringVal(name),
			"parent_id": cty.StringVal(movedRoleParent),
			"body":      cty.EmptyObjectVal,
			"tags":      cty.MapValEmpty(cty.String),
		})}).Encode(schema.Block.ImpliedType(), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return src
	}
	state := func(current string, deposed string) *states.State {
		st := states.NewState()
		st.EnsureModule(addrs.RootModuleInstance).SetResourceInstanceCurrent(addr.Resource, encode(current), azapiTestProvider, addrs.NoKey)
		if deposed != "" {
			st.EnsureModule(addrs.RootModuleInstance).SetResourceInstanceDeposed(addr.Resource, states.DeposedKey("00000001"), encode(deposed), azapiTestProvider, addrs.NoKey)
		}
		return st
	}

	// The record an earlier apply wrote: the old object.
	seed, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		rec, _ := LocatedRecordFrom("azapi_resource", schema, mustDecode(t, schema, encode(oldName)))
		env.Identity = identityPayloadFrom(rec)
		env.Provider = providerString(azapiTestProvider)
	})
	if err != nil {
		t.Fatal(err)
	}
	req := func(final *states.State, only bool) WriteBackRequest {
		r := WriteBackRequest{
			Store:            store,
			EnvelopeVersions: []RecordVersion{{Addr: addr, Version: seed}},
			FinalState:       final,
			Schemas:          schemas,
			ReplacedAddrs:    []addrs.AbsResourceInstance{addr},
			DestroyedDeposed: []DeposedDestroy{{Addr: addr, Key: states.DeposedKey("00000001")}},
		}
		if only {
			r.Only = &addr
		}
		return r
	}

	// The create leg returns: new current, old deposed.
	assertNoErrors(t, WriteInstance(ctx, req(state(newName, oldName), true)))
	if rec, _, _, found, err := store.GetIdentity(ctx, addr); err != nil || !found || rec.ImportID != idOf(newName) {
		t.Fatalf("after the create leg the record names %q (found %v, %v), want the new object %q", rec.ImportID, found, err, idOf(newName))
	}
	// The destroy leg returns, then the final pass.
	assertNoErrors(t, WriteInstance(ctx, req(state(newName, ""), true)))
	assertNoErrors(t, WriteBack(ctx, req(state(newName, ""), false)))

	rec, _, _, found, err := store.GetIdentity(ctx, addr)
	if err != nil || !found || rec.ImportID != idOf(newName) {
		t.Errorf("after the replace the record names %q (found %v, %v), want the new object %q", rec.ImportID, found, err, idOf(newName))
	}
	deposed, _, _, err := store.GetDeposed(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if len(deposed) != 0 {
		t.Errorf("the record still holds deposed objects after the replace finished, which a later plan would read as a second claimant: %+v", deposed)
	}
}

func mustDecode(t *testing.T, schema providers.Schema, src *states.ResourceInstanceObjectSrc) cty.Value {
	t.Helper()
	obj, err := src.Decode(schema.Block.ImpliedType())
	if err != nil {
		t.Fatal(err)
	}
	return obj.Value
}
