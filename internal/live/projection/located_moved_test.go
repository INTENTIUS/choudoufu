// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is the distilled shape behind the day2_rename wall
// corpus-vpc-complete's and corpus-autoscaling-complete's own scripts
// named: a `moved` block renaming a MODULE whose only content is a
// record-located (untaggable, GitHub issue #270) resource. Before
// [builder.locatedIdentityWithAliases], a located instance's identity was
// looked up strictly by its own declared address, so a moved block that
// relocated the module left the record behind at the address migrate wrote
// it under, and the next plan proposed a CREATE for an object that had only
// been renamed - byte for byte the shape
// live/gauntlet/logs/corpus-vpc-complete.log recorded for
// module.vpc_endpoints -> module.vpc_endpoints_renamed's
// aws_security_group_rule.this["ingress_https"].

// TestLocatedIdentityFollowsAMovedModule is the positive case: a record
// written under the OLD module path is still found, and bound to the NEW
// declared address, once a `moved` block says the module relocated.
func TestLocatedIdentityFollowsAMovedModule(t *testing.T) {
	cfg := loadConfigWithModules(t, "testdata/located-moved-module")
	oldAddr := mustAddr(t, `module.thing.`+locatedTestType+`.bastion`)
	newAddr := mustAddr(t, `module.thing_renamed.`+locatedTestType+`.bastion`)
	const wantID = "eipassoc-0f1e2d3c4b5a69780"

	store := localHintStore(t)
	located := newTestLocatedStore(store, "test-estate")
	if _, err := located.Put(context.Background(), oldAddr, LocatedRecord{ImportID: wantID}, ""); err != nil {
		t.Fatalf("seeding the located record under the OLD address: %s", err)
	}

	var imported []string
	provs := SingleProvider(locatedTestProvider, locatedTypeProvider(&imported))

	res, diags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{Addr: newAddr, Class: identity.ClassRecordLocated}},
		provs, Options{RecordStore: located.rs})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{`module.thing_renamed.` + locatedTestType + `.bastion`})
	if len(imported) != 1 || imported[0] != wantID {
		t.Fatalf("the provider was asked to import %v, want exactly [%q].\n"+
			"The record written under the module's OLD address must still supply the identity once a moved block says the module relocated - the same promise moved.Aliases already keeps for a live marker.",
			imported, wantID)
	}

	inst := res.State.ResourceInstance(newAddr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object for the located instance at its NEW address")
	}
	if res.Has(oldAddr) {
		t.Errorf("the OLD address also materialized; a moved block's origin must never itself become a second prior-state entry")
	}

	// The version read belongs to the OLD key, not the NEW one - see
	// [builder.locatedIdentityWithAliases]'s own doc comment. Asserting it
	// as newAddr's CAS expectation would tell write-back that newAddr's key
	// already held a version it has never had, so it must be absent here;
	// write-back's stale-key fallback (RecordStore.currentVersion) is what
	// actually protects the write from a race, exercised elsewhere.
	if len(res.EnvelopeVersions) != 0 {
		t.Errorf("EnvelopeVersions = %v, want empty for a record found via a moved alias", res.EnvelopeVersions)
	}
}

// TestLocatedIdentityIgnoresAnUnrelatedStaleRecord is the mutation check in
// the direction HANDOFF.md's safety rule cares most about: an address a
// moved block does NOT declare must never bind to a record sitting under
// some other, unrelated address, no matter how plausible a coincidence
// makes it look. Fuzzy matching here is exactly the "adopts or displaces a
// real object" failure the safety rule exists to stop.
func TestLocatedIdentityIgnoresAnUnrelatedStaleRecord(t *testing.T) {
	cfg := loadConfig(t, writeLocatedFixture(t))
	addr := mustAddr(t, locatedTestType+`.bastion`)
	// A record under an address NO moved block in this configuration (there
	// is none at all) ever names. If the lookup fell back to "any record in
	// the store", this is the one it would wrongly find.
	unrelated := mustAddr(t, locatedTestType+`.some_other_resource_entirely`)

	store := localHintStore(t)
	located := newTestLocatedStore(store, "test-estate")
	if _, err := located.Put(context.Background(), unrelated, LocatedRecord{ImportID: "eipassoc-belongs-to-someone-else"}, ""); err != nil {
		t.Fatalf("seeding the unrelated located record: %s", err)
	}

	provs := SingleProvider(locatedTestProvider, locatedTypeProvider(nil))
	res, diags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordLocated}},
		provs, Options{RecordStore: located.rs})
	assertNoErrors(t, diags)

	if res.Has(addr) {
		t.Fatalf("%s materialized from an unrelated address's record with no moved block joining the two:\n%s", addr, res)
	}
	om := omissionFor(t, res, addr.String())
	if om.Reason != ReasonAbsent {
		t.Errorf("omission reason = %v, want ReasonAbsent", om.Reason)
	}
}

// TestLocatedIdentityRefusesTwoMovedRecords is the other mutation check:
// when more than one address a moved block honours for the same
// destination carries a located record, picking either one would be a
// guess - the record-store analogue of "one marker value for two declared
// addresses" - and the run must refuse loudly rather than silently bind to
// whichever alias happened to be checked last.
func TestLocatedIdentityRefusesTwoMovedRecords(t *testing.T) {
	cfg := loadConfigWithModules(t, "testdata/located-moved-module-ambiguous")
	oldAddrA := mustAddr(t, `module.thing_a.`+locatedTestType+`.bastion`)
	oldAddrB := mustAddr(t, `module.thing_b.`+locatedTestType+`.bastion`)
	newAddr := mustAddr(t, `module.thing_renamed.`+locatedTestType+`.bastion`)

	store := localHintStore(t)
	located := newTestLocatedStore(store, "test-estate")
	if _, err := located.Put(context.Background(), oldAddrA, LocatedRecord{ImportID: "eipassoc-from-a"}, ""); err != nil {
		t.Fatalf("seeding record A: %s", err)
	}
	if _, err := located.Put(context.Background(), oldAddrB, LocatedRecord{ImportID: "eipassoc-from-b"}, ""); err != nil {
		t.Fatalf("seeding record B: %s", err)
	}

	provs := SingleProvider(locatedTestProvider, locatedTypeProvider(nil))
	res, diags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{Addr: newAddr, Class: identity.ClassRecordLocated}},
		provs, Options{RecordStore: located.rs})

	if !diags.HasErrors() {
		t.Fatalf("two moved-alias addresses both carrying a located record produced no error; picking one silently is exactly the wrong-marker shape HANDOFF's safety rule forbids:\n%s", res)
	}
	if !hasDiag(diags, "Cannot read a located record", "will not guess which prior record") {
		t.Errorf("no diagnostic naming the ambiguity; got:\n%s", renderDiags(diags))
	}
	if res.Has(newAddr) {
		t.Errorf("%s materialized despite the ambiguous prior records", newAddr)
	}
}
