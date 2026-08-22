// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// GitHub issue #372: the tofu-slot a migration can settle without a discovery
// pass, and the four shapes where it must not try.
//
// Every assertion here is on the tag map a write actually handed the provider,
// read back out of [capturingProvider] - HANDOFF.md's "assert the rendered
// identity by value", which for a marker means the string that reached the
// cloud and not the fact that some later comparison converged. A slot is the
// one marker whose whole job is telling two otherwise identical resources
// apart, so a test that only counted writes, or only checked that a slot was
// present, would pass just as happily on a set where every member claimed slot
// 0 - which is exactly the wrong marker HANDOFF's safety rule is about.

// slotFixture is a set of eligible instances of one resource, each with its own
// capturing provider, wired into a Ratification ready for Approve.
type slotFixture struct {
	rat *Ratification
	by  map[string]*capturingProvider
}

// newSlotFixture builds one instance per entry of instances, keyed by the
// instance address, carrying exactly the live tags given.
func newSlotFixture(t *testing.T, estate string, instances map[string]map[string]string) *slotFixture {
	t.Helper()

	f := &slotFixture{
		rat: &Ratification{Estate: estate, eligible: map[string]*eligible{}},
		by:  map[string]*capturingProvider{},
	}

	// Sorted, because Approve walks Entries in order and a map would make the
	// walk order - and so the failure a broken change produces - vary per run.
	addrStrs := make([]string, 0, len(instances))
	for s := range instances {
		addrStrs = append(addrStrs, s)
	}
	sort.Strings(addrStrs)

	for _, s := range addrStrs {
		addr := mustAddr(t, s)
		e, p := vpcEligible(instances[s])
		f.rat.Entries = append(f.rat.Entries, Entry{Addr: addr, TypeName: "aws_vpc", Status: StatusVerified})
		f.rat.eligible[addr.String()] = e
		f.by[addr.String()] = p
	}
	return f
}

// tagsWrittenTo is the tag map the write for one instance handed the provider,
// or nil when that instance was never written to.
func (f *slotFixture) tagsWrittenTo(t *testing.T, addr string) map[string]string {
	t.Helper()
	p, ok := f.by[addr]
	if !ok {
		t.Fatalf("no instance %q in this fixture", addr)
	}
	if p.applyCount == 0 {
		return nil
	}
	return p.appliedTags
}

func (f *slotFixture) outcomes(t *testing.T) map[string]Outcome {
	t.Helper()
	rep, diags := f.rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve: %s", diags.Err())
	}
	out := make(map[string]Outcome, len(rep.Outcomes))
	for _, o := range rep.Outcomes {
		out[o.Addr.String()] = o.Outcome
	}
	return out
}

// ---------------------------------------------------------------------------
// The case the issue is about
// ---------------------------------------------------------------------------

// TestApprove_WritesSequentialSlotsOnASlotlessCountSet is the by-value pin.
// Three live members of one count set, none carrying a slot, are stamped with
// slots 0, 1 and 2 - each one on the instance whose tofu-address names the same
// index, which is the only assignment that changes nothing about which live
// resource is which (see slots.Sequential).
func TestApprove_WritesSequentialSlotsOnASlotlessCountSet(t *testing.T) {
	f := newSlotFixture(t, "acme", map[string]map[string]string{
		"aws_vpc.this[0]": nil,
		"aws_vpc.this[1]": nil,
		"aws_vpc.this[2]": nil,
	})

	got := f.outcomes(t)
	for _, addr := range []string{"aws_vpc.this[0]", "aws_vpc.this[1]", "aws_vpc.this[2]"} {
		if got[addr] != OutcomeStamped {
			t.Fatalf("%s: outcome %s, want STAMPED", addr, got[addr])
		}
	}

	// The whole point, by value: address and slot agree, instance by instance.
	for _, want := range []struct{ addr, address, slot string }{
		{"aws_vpc.this[0]", "aws_vpc.this:0", "0"},
		{"aws_vpc.this[1]", "aws_vpc.this:1", "1"},
		{"aws_vpc.this[2]", "aws_vpc.this:2", "2"},
	} {
		tags := f.tagsWrittenTo(t, want.addr)
		if tags == nil {
			t.Fatalf("%s: nothing was written", want.addr)
		}
		if got := tags[discovery.TagEstate]; got != "acme" {
			t.Errorf("%s: tofu-estate = %q, want acme", want.addr, got)
		}
		if got := tags[discovery.TagAddress]; got != want.address {
			t.Errorf("%s: tofu-address = %q, want %q", want.addr, got, want.address)
		}
		if got := tags[discovery.TagSlot]; got != want.slot {
			t.Errorf("%s: tofu-slot = %q, want %q", want.addr, got, want.slot)
		}
	}

	// And no two members claim one slot, which is the failure a per-instance
	// assertion alone cannot see if the expectations themselves are wrong.
	seen := map[string]string{}
	for addr := range f.by {
		slot := f.tagsWrittenTo(t, addr)[discovery.TagSlot]
		if other, dup := seen[slot]; dup {
			t.Fatalf("tofu-slot %q was written to both %s and %s", slot, other, addr)
		}
		seen[slot] = addr
	}
}

// TestApprove_CompletesAnEarlierMigrationsMissingSlot: an estate migrated
// before this existed carries the other two markers and no slot. Re-running
// live-import finishes the job rather than reporting ALREADY_STAMPED and
// leaving the tag for the plan to propose forever.
func TestApprove_CompletesAnEarlierMigrationsMissingSlot(t *testing.T) {
	f := newSlotFixture(t, "acme", map[string]map[string]string{
		"aws_vpc.this[0]": {discovery.TagEstate: "acme", discovery.TagAddress: "aws_vpc.this:0"},
		"aws_vpc.this[1]": {discovery.TagEstate: "acme", discovery.TagAddress: "aws_vpc.this:1"},
	})

	got := f.outcomes(t)
	for _, addr := range []string{"aws_vpc.this[0]", "aws_vpc.this[1]"} {
		if got[addr] != OutcomeStamped {
			t.Fatalf("%s: outcome %s, want STAMPED", addr, got[addr])
		}
	}
	if got := f.tagsWrittenTo(t, "aws_vpc.this[0]")[discovery.TagSlot]; got != "0" {
		t.Errorf("aws_vpc.this[0]: tofu-slot = %q, want 0", got)
	}
	if got := f.tagsWrittenTo(t, "aws_vpc.this[1]")[discovery.TagSlot]; got != "1" {
		t.Errorf("aws_vpc.this[1]: tofu-slot = %q, want 1", got)
	}
}

// TestApprove_IsIdempotentOnceSlotsAreWritten: the run above, run again. The
// set now classifies ModeAll, so no slot is computed at all and the whole
// write is skipped - the same idempotence a second live-import has always had.
func TestApprove_IsIdempotentOnceSlotsAreWritten(t *testing.T) {
	f := newSlotFixture(t, "acme", map[string]map[string]string{
		"aws_vpc.this[0]": {discovery.TagEstate: "acme", discovery.TagAddress: "aws_vpc.this:0", discovery.TagSlot: "0"},
		"aws_vpc.this[1]": {discovery.TagEstate: "acme", discovery.TagAddress: "aws_vpc.this:1", discovery.TagSlot: "1"},
	})

	got := f.outcomes(t)
	for _, addr := range []string{"aws_vpc.this[0]", "aws_vpc.this[1]"} {
		if got[addr] != OutcomeAlreadyStamped {
			t.Fatalf("%s: outcome %s, want ALREADY_STAMPED", addr, got[addr])
		}
		if tags := f.tagsWrittenTo(t, addr); tags != nil {
			t.Fatalf("%s: a second migration wrote tags %v, want none", addr, tags)
		}
	}
}

// ---------------------------------------------------------------------------
// The four shapes a migration declines to settle
// ---------------------------------------------------------------------------

// TestApprove_NeverRenumbersAnEstablishedSlotScheme is the safety case that
// matters most. Slots 3 and 9 are a perfectly good assignment - a set that has
// been scaled down and back up holds exactly that shape - and renumbering them
// to 0 and 1 would silently move which live resource every external reader
// thinks is which member. ModeAll, so nothing is written.
func TestApprove_NeverRenumbersAnEstablishedSlotScheme(t *testing.T) {
	f := newSlotFixture(t, "acme", map[string]map[string]string{
		"aws_vpc.this[0]": {discovery.TagEstate: "acme", discovery.TagAddress: "aws_vpc.this:0", discovery.TagSlot: "3"},
		"aws_vpc.this[1]": {discovery.TagEstate: "acme", discovery.TagAddress: "aws_vpc.this:1", discovery.TagSlot: "9"},
	})

	got := f.outcomes(t)
	for _, addr := range []string{"aws_vpc.this[0]", "aws_vpc.this[1]"} {
		if got[addr] != OutcomeAlreadyStamped {
			t.Fatalf("%s: outcome %s, want ALREADY_STAMPED", addr, got[addr])
		}
		if tags := f.tagsWrittenTo(t, addr); tags != nil {
			t.Fatalf("%s: the migration rewrote tags %v over an established slot scheme", addr, tags)
		}
	}
}

// TestApprove_WritesNoSlotForAMixedSet: some members carry a slot and some do
// not, which is the case discovery refuses to guess at by name. A migration
// does not get to pick a side either - it writes the other two markers and
// leaves the slot alone.
func TestApprove_WritesNoSlotForAMixedSet(t *testing.T) {
	f := newSlotFixture(t, "acme", map[string]map[string]string{
		"aws_vpc.this[0]": {discovery.TagSlot: "0"},
		"aws_vpc.this[1]": nil,
	})

	got := f.outcomes(t)
	for _, addr := range []string{"aws_vpc.this[0]", "aws_vpc.this[1]"} {
		if got[addr] != OutcomeStamped {
			t.Fatalf("%s: outcome %s, want STAMPED", addr, got[addr])
		}
	}
	// [0] keeps the slot it already carried; [1] gains none.
	if got := f.tagsWrittenTo(t, "aws_vpc.this[0]")[discovery.TagSlot]; got != "0" {
		t.Errorf("aws_vpc.this[0]: tofu-slot = %q, want its own pre-existing 0 carried through untouched", got)
	}
	if got, present := f.tagsWrittenTo(t, "aws_vpc.this[1]")[discovery.TagSlot]; present {
		t.Errorf("aws_vpc.this[1]: tofu-slot = %q was written into a mixed set, want none", got)
	}
}

// TestApprove_WritesNoSlotForAGappedSet: index 1 is not in this migration (its
// live object is gone), so writing slots 0 and 2 would leave a set whose
// ascending-slot-to-ascending-index match binds the [2] object to index 1 -
// a different answer than the addresses this run is writing give. Declined.
func TestApprove_WritesNoSlotForAGappedSet(t *testing.T) {
	f := newSlotFixture(t, "acme", map[string]map[string]string{
		"aws_vpc.this[0]": nil,
		"aws_vpc.this[2]": nil,
	})

	got := f.outcomes(t)
	for _, addr := range []string{"aws_vpc.this[0]", "aws_vpc.this[2]"} {
		if got[addr] != OutcomeStamped {
			t.Fatalf("%s: outcome %s, want STAMPED", addr, got[addr])
		}
		if slot, present := f.tagsWrittenTo(t, addr)[discovery.TagSlot]; present {
			t.Errorf("%s: tofu-slot = %q was written for a set missing index 1, want none", addr, slot)
		}
	}
}

// TestApprove_WritesNoSlotForForEachOrSingleInstances: a slot names a member of
// a fungible set, and only a count block has one. for_each instances are named
// by their keys and an unexpanded resource is named by itself; neither is ever
// stamped with a slot by the ordinary stamping pass, so neither may be here.
func TestApprove_WritesNoSlotForForEachOrSingleInstances(t *testing.T) {
	f := newSlotFixture(t, "acme", map[string]map[string]string{
		`aws_vpc.keyed["a"]`: nil,
		`aws_vpc.keyed["b"]`: nil,
		"aws_vpc.solo":       nil,
	})

	got := f.outcomes(t)
	for _, addr := range []string{`aws_vpc.keyed["a"]`, `aws_vpc.keyed["b"]`, "aws_vpc.solo"} {
		if got[addr] != OutcomeStamped {
			t.Fatalf("%s: outcome %s, want STAMPED", addr, got[addr])
		}
		if slot, present := f.tagsWrittenTo(t, addr)[discovery.TagSlot]; present {
			t.Errorf("%s: tofu-slot = %q was written for a non-count instance, want none", addr, slot)
		}
	}
	// And the addresses themselves, by value, so this fixture is not quietly
	// stamping three copies of one string.
	for addr, want := range map[string]string{
		`aws_vpc.keyed["a"]`: "aws_vpc.keyed:a",
		`aws_vpc.keyed["b"]`: "aws_vpc.keyed:b",
		"aws_vpc.solo":       "aws_vpc.solo",
	} {
		if got := f.tagsWrittenTo(t, addr)[discovery.TagAddress]; got != want {
			t.Errorf("%s: tofu-address = %q, want %q", addr, got, want)
		}
	}
}

// TestApprove_WritesNoSlotForAClientNamedType is gate 4, and the case that was
// found by running this change rather than by reading it. Discovery indexes a
// count block only for instances it classifies ClassNeedsDiscovery, so a count
// instance of a type whose identity comes out of its own configuration gets no
// slot assignment - and a tofu-slot written onto one is a tag the very next
// plan proposes REMOVING, which is a worse outcome than the one this whole
// change exists to fix.
//
// aws_s3_bucket is the live case (corpus-overture-tiles planned
// `- "tofu-slot" = "0" -> null` on aws_s3_bucket.tiles[0] before this gate
// existed); aws_vpc, used by every other test in this file, is the
// server-assigned side of the same table column.
func TestApprove_WritesNoSlotForAClientNamedType(t *testing.T) {
	if ti, ok := identity.LookupType("aws_s3_bucket"); !ok || ti.ServerAssigned {
		t.Fatalf("test premise: aws_s3_bucket must be an admitted, NOT server-assigned type (ok=%v serverAssigned=%v)", ok, ti.ServerAssigned)
	}
	if ti, ok := identity.LookupType("aws_vpc"); !ok || !ti.ServerAssigned {
		t.Fatalf("test premise: aws_vpc must be an admitted, server-assigned type (ok=%v serverAssigned=%v)", ok, ti.ServerAssigned)
	}

	f := newSlotFixture(t, "acme", map[string]map[string]string{
		"aws_s3_bucket.this[0]": nil,
		"aws_s3_bucket.this[1]": nil,
	})
	// newSlotFixture builds every instance as an aws_vpc; retype both so the
	// gate under test is the type and not the address.
	for i := range f.rat.Entries {
		f.rat.Entries[i].TypeName = "aws_s3_bucket"
		f.rat.eligible[f.rat.Entries[i].Addr.String()].typeName = "aws_s3_bucket"
	}

	got := f.outcomes(t)
	for _, addr := range []string{"aws_s3_bucket.this[0]", "aws_s3_bucket.this[1]"} {
		if got[addr] != OutcomeStamped {
			t.Fatalf("%s: outcome %s, want STAMPED", addr, got[addr])
		}
		tags := f.tagsWrittenTo(t, addr)
		if slot, present := tags[discovery.TagSlot]; present {
			t.Errorf("%s: tofu-slot = %q was written for a client-named type, want none", addr, slot)
		}
		// The other two markers still land: this gate is about the third one
		// only, and a client-named resource is stamped exactly as before.
		if tags[discovery.TagEstate] != "acme" {
			t.Errorf("%s: tofu-estate = %q, want acme", addr, tags[discovery.TagEstate])
		}
	}
}

// TestApprove_SlotsAreScopedToOneResourceBlock: two count blocks of the same
// type, each numbered from zero on its own. The set a slot belongs to is the
// block, and folding two blocks into one would number the second block's
// instances from where the first left off.
func TestApprove_SlotsAreScopedToOneResourceBlock(t *testing.T) {
	f := newSlotFixture(t, "acme", map[string]map[string]string{
		"aws_vpc.a[0]": nil,
		"aws_vpc.a[1]": nil,
		"aws_vpc.b[0]": nil,
	})

	f.outcomes(t)
	for addr, want := range map[string]string{
		"aws_vpc.a[0]": "0",
		"aws_vpc.a[1]": "1",
		"aws_vpc.b[0]": "0",
	} {
		if got := f.tagsWrittenTo(t, addr)[discovery.TagSlot]; got != want {
			t.Errorf("%s: tofu-slot = %q, want %q", addr, got, want)
		}
	}
}

// TestApprove_SlotsAreScopedToOneModuleInstance: the same resource block under
// two instances of a keyed module call. Each module instance's block is its own
// count set - its instances carry their own module-qualified addresses - so
// both are numbered from zero.
func TestApprove_SlotsAreScopedToOneModuleInstance(t *testing.T) {
	f := newSlotFixture(t, "acme", map[string]map[string]string{
		`module.m["x"].aws_vpc.this[0]`: nil,
		`module.m["x"].aws_vpc.this[1]`: nil,
		`module.m["y"].aws_vpc.this[0]`: nil,
	})

	f.outcomes(t)
	for addr, want := range map[string]string{
		`module.m["x"].aws_vpc.this[0]`: "0",
		`module.m["x"].aws_vpc.this[1]`: "1",
		`module.m["y"].aws_vpc.this[0]`: "0",
	} {
		if got := f.tagsWrittenTo(t, addr)[discovery.TagSlot]; got != want {
			t.Errorf("%s: tofu-slot = %q, want %q", addr, got, want)
		}
	}
}
