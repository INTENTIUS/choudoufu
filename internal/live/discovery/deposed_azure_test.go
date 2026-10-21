// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/azsweep"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// The azure lane's day2_replace and day2_crash (#1120, reference-azure)
// rename a create_before_destroy object: the configuration's name changes,
// the address does not. Two things on the Resource Graph leg were measured
// against a record as a real apply writes it, and both were wrong.
//
// An azurerm type with a resource identity schema records its identity as
// components (name, resource_group_name, subscription_id) and no import id
// (projection.LocatedRecordFromIn's composite branch;
// internal/live/projection/markers_record_azurerm_test.go writes one). A
// Resource Graph claimant carries an ARM id and no identity object. So the
// record could never name a listed object: TestRenameBind_azureRecordNaming
// TheOldObjectIsAReplace passed only because it seeded an import id no
// apply writes.
//
// And an apply interrupted between the rename's create and its destroy
// leaves the old object standing with the address's marker and the record
// holding it as deposed. The Resource Graph leg filed that object through
// displacedFrom, which never read the deposed record, so it was reported
// displaced and left standing for good, where stock destroys it as the
// deposed object on the next apply.

// renameComponents is the record an apply writes for azurerm_route_table.rt
// at name: components only, keyed by identity attribute.
func renameComponents(name string) map[string]string {
	return map[string]string{"name": name, "resource_group_name": "Kept", "subscription_id": rgSub1}
}

// renameNewHit is the route table the interrupted apply created, as
// Resource Graph lists it, carrying the same marker as the old one.
func renameNewHit() azsweep.Resource {
	hit := renameOldHit()
	hit.ID, hit.Name = strings.ToLower(renameRTID(renameNewRT)), renameNewRT
	return hit
}

func TestAzureRecordComponentsRebuildTheARMID(t *testing.T) {
	// The external source: the removal leg's own rebuild of the same
	// record, which TestAzurermRecordWritesWhatTheRemovalLegRebuilds holds
	// to the id azurerm gives the object. If that ever stops producing an
	// ARM id this test is not exercising the comparison it names.
	id, ok := substrate.AzurermRecordImportID("azurerm_route_table", renameComponents(renameOldRT))
	if !ok || !strings.EqualFold(id, renameRTID(renameOldRT)) {
		t.Fatalf("AzurermRecordImportID = %q, %v; want %q", id, ok, renameRTID(renameOldRT))
	}
	old := claimant{importID: strings.ToLower(renameRTID(renameOldRT))}
	rec := projection.LocatedRecord{Components: renameComponents(renameOldRT)}
	if !claimantMatchesRecord("azurerm_route_table", rec, old) {
		t.Error("a components-only azurerm record does not name the object Resource Graph lists at its ARM id")
	}
	if claimantMatchesRecord("azurerm_route_table", rec, claimant{importID: strings.ToLower(renameRTID(renameNewRT))}) {
		t.Error("a components-only azurerm record names an object at another name")
	}
	// Without the type nothing can be rebuilt, and nothing is guessed.
	if claimantMatchesRecord("", rec, old) {
		t.Error("a record matched with no type to rebuild its ARM id from")
	}
	// A hole in the components is no rebuild, so no match.
	holed := projection.LocatedRecord{Components: map[string]string{"name": renameOldRT, "resource_group_name": "Kept"}}
	if claimantMatchesRecord("azurerm_route_table", holed, old) {
		t.Error("a record missing its subscription matched")
	}
}

// TestRenameBind_azureComponentsRecordIsAReplace is #885 on Azure with the
// record an apply really writes: the old object is the replace's old half,
// not displaced.
func TestRenameBind_azureComponentsRecordIsAReplace(t *testing.T) {
	old := renameOldHit()
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {old}}}
	leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}
	rawStore, seedStore := supersededHintStore(t, rgEstate)
	seedCurrentIdentity(t, seedStore, renameRTAdr, projection.LocatedRecord{Components: renameComponents(renameOldRT)})

	res, diags := Discover(context.Background(), Request{
		Estate:      rgEstate,
		Config:      azConfigWith(t, renameRTConfig),
		Provider:    newFakeCloud(),
		Resolutions: []identity.Resolution{keptResolution(rgSub1), aksResolution("azurerm_route_table", "rt", renameRTID(renameNewRT))},
		Sweepers:    []Sweeper{leg},
		HintStore:   rawStore,
	})
	assertNoErrors(t, diags)
	for _, p := range res.ProblemsOfKind(ProblemDisplacedMarker) {
		t.Fatalf("an ordinary ForceNew rename, recorded as components, was reported as a displacement:\n%s", p.Detail)
	}
	assertNotAnOrphan(t, res, old.ID)
	if len(res.DeposedBindings) != 0 {
		t.Errorf("a plain rename with no deposed record produced deposed bindings: %+v", res.DeposedBindings)
	}
}

// discoverInterruptedRename runs the Resource Graph leg over the position an
// interrupted create_before_destroy rename leaves: rt-new created and
// recorded as current, rt-old still standing with the same marker, and
// deposed as the record's deposed entry.
func discoverInterruptedRename(t *testing.T, deposed projection.DeposedRecord) (*Result, azsweep.Resource) {
	t.Helper()
	old, created := renameOldHit(), renameNewHit()
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {old, created}}}
	leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}
	rawStore, seedStore := supersededHintStore(t, rgEstate)
	seedCurrentIdentity(t, seedStore, renameRTAdr, projection.LocatedRecord{Components: renameComponents(renameNewRT)})

	res, diags := Discover(context.Background(), Request{
		Estate:         rgEstate,
		Config:         azConfigWith(t, renameRTConfig),
		Provider:       newFakeCloud(),
		Resolutions:    []identity.Resolution{keptResolution(rgSub1), aksResolution("azurerm_route_table", "rt", renameRTID(renameNewRT))},
		Sweepers:       []Sweeper{leg},
		HintStore:      rawStore,
		DeposedRecords: map[string]map[string]projection.DeposedRecord{renameRTAdr: {"5a1e0c3d": deposed}},
	})
	assertNoErrors(t, diags)
	return res, old
}

func TestRenameBind_azureInterruptedRenameBindsTheDeposedObject(t *testing.T) {
	res, old := discoverInterruptedRename(t, projection.DeposedRecord{Components: renameComponents(renameOldRT)})
	for _, p := range res.ProblemsOfKind(ProblemDisplacedMarker) {
		t.Errorf("the record's deposed object was reported displaced, which leaves it standing:\n%s", p.Detail)
	}
	assertNotAnOrphan(t, res, old.ID)
	if len(res.DeposedBindings) != 1 {
		t.Fatalf("deposed bindings %+v, want exactly one for %s", res.DeposedBindings, renameRTAdr)
	}
	db := res.DeposedBindings[0]
	if db.Addr.String() != renameRTAdr || string(db.DeposedKey) != "5a1e0c3d" || db.Components["name"] != renameOldRT {
		t.Errorf("deposed binding %+v, want %s's deposed key 5a1e0c3d naming %s", db, renameRTAdr, renameOldRT)
	}
	if res.MarkerVerified()[renameRTAdr] != true {
		// The new object, at the id the configuration computes, still
		// vouches for the address: the deposed half changes nothing there.
		t.Errorf("rt-new no longer vouches for %s", renameRTAdr)
	}
}

// TestRenameBind_azureDeposedRecordNamingAnotherObjectStaysDisplaced is the
// bound: a deposed entry that names some other object binds nothing, and the
// old object is reported as before.
func TestRenameBind_azureDeposedRecordNamingAnotherObjectStaysDisplaced(t *testing.T) {
	res, old := discoverInterruptedRename(t, projection.DeposedRecord{Components: renameComponents("rt-elsewhere")})
	if len(res.DeposedBindings) != 0 {
		t.Errorf("a deposed entry naming another object bound rt-old: %+v", res.DeposedBindings)
	}
	findDisplaced(t, res, old.ID)
	assertNotAnOrphan(t, res, old.ID)
}
