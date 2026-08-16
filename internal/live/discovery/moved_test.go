// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"
)

// movedRenameRequest is the issue #198 fixture: two declared subnets and one
// moved block saying aws_subnet.old is now aws_subnet.renamed.
func movedRenameRequest(t *testing.T, cloud *fakeCloud) Request {
	t.Helper()
	cfg := loadConfig(t, "testdata/moved-rename")
	return Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
	}
}

// TestDiscoverMovedBlockBinds is the whole point of GitHub issue #198: a live
// resource still carrying the address a moved block moves *from* is the object
// the destination declares, and it binds there. Without the alias it is an
// orphan - which is to say a proposed destroy - while the destination is
// unbound, which is a proposed create. One cloud object, two wrong beliefs.
func TestDiscoverMovedBlockBinds(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_subnet", "subnet-moved", `aws_subnet.old`)

	res, diags := Discover(context.Background(), movedRenameRequest(t, cloud))
	assertNoErrors(t, diags)

	b, ok := res.BindingFor(mustAddr(t, `aws_subnet.renamed`))
	if !ok {
		t.Fatalf("a resource carrying the moved block's source address did not bind to its destination:\n%s", res)
	}
	if b.ImportID != "subnet-moved" {
		t.Errorf("bound to import ID %q, want subnet-moved", b.ImportID)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("the moved resource was also filed as an orphan, which plans a destroy: %v", res.Orphans)
	}
	for _, addr := range res.Unbound {
		if addr.String() == `aws_subnet.renamed` {
			t.Errorf("the destination was reported unbound even though a live resource claims it, which plans a create: %v", res.Unbound)
		}
	}
}

// TestDiscoverMovedBlockIsIdempotent is the case a moved block shipped inside
// a published module turns on. terraform-aws-modules writes them under a
// "Migrations: vX -> vY" header and a consumer of a pinned module cannot
// delete upstream source, so the block is still there on every later run,
// long after the marker was rewritten. It has to be a no-op then: not an
// error, and not a second rewrite.
func TestDiscoverMovedBlockIsIdempotent(t *testing.T) {
	cloud := newFakeCloud()
	// The marker the first run already rewrote to.
	cloud.own("aws_subnet", "subnet-moved", `aws_subnet.renamed`)

	res, diags := Discover(context.Background(), movedRenameRequest(t, cloud))
	assertNoErrors(t, diags)

	b, ok := res.BindingFor(mustAddr(t, `aws_subnet.renamed`))
	if !ok {
		t.Fatalf("an already-moved resource did not bind to its own address:\n%s", res)
	}
	if b.ImportID != "subnet-moved" {
		t.Errorf("bound to import ID %q, want subnet-moved", b.ImportID)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("an already-moved resource produced orphans: %v", res.Orphans)
	}
	if len(res.Problems) != 0 {
		t.Errorf("an already-moved resource produced problems: %v", res.Problems)
	}
}

// TestDiscoverMovedBlockSourceAbsent: a moved block naming a source no live
// object carries says nothing about the world. The destination is simply
// absent and the plan proposes creating it, which is what would happen with no
// moved block at all - and matches stock, where moving a state entry that is
// not there is a silent no-op.
func TestDiscoverMovedBlockSourceAbsent(t *testing.T) {
	cloud := newFakeCloud()

	res, diags := Discover(context.Background(), movedRenameRequest(t, cloud))
	assertNoErrors(t, diags)

	if len(res.Bindings) != 0 {
		t.Errorf("nothing is live, but something bound: %v", res.Bindings)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("nothing is live, but something is an orphan: %v", res.Orphans)
	}
	var sawDestination bool
	for _, addr := range res.Unbound {
		if addr.String() == `aws_subnet.renamed` {
			sawDestination = true
		}
	}
	if !sawDestination {
		t.Errorf("the destination was not reported unbound, so nothing plans to create it: %v", res.Unbound)
	}
}

// TestDiscoverMovedBlockBothAddressesLive is the ambiguity the alias must not
// resolve by guessing: one live resource carrying the old address and another
// carrying the new one are two claims on one declared instance. Because
// claimants accumulate on the declared entry rather than on the marker string,
// this comes out as the collision it already was for any other pair of
// duplicate claims, and nothing binds.
func TestDiscoverMovedBlockBothAddressesLive(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_subnet", "subnet-old", `aws_subnet.old`)
	cloud.own("aws_subnet", "subnet-new", `aws_subnet.renamed`)

	res, diags := Discover(context.Background(), movedRenameRequest(t, cloud))
	if !diags.HasErrors() {
		t.Fatalf("two live resources claim aws_subnet.renamed and nothing was refused:\n%s", res)
	}

	var collision *Problem
	for i := range res.Problems {
		if res.Problems[i].Kind == ProblemCollision {
			collision = &res.Problems[i]
		}
	}
	if collision == nil {
		t.Fatalf("want a collision problem, got %v", res.Problems)
	}
	if len(collision.LiveIDs) != 2 {
		t.Errorf("collision names %v, want both live IDs", collision.LiveIDs)
	}
	if _, ok := res.BindingFor(mustAddr(t, `aws_subnet.renamed`)); ok {
		t.Errorf("something bound despite the collision:\n%s", res)
	}
}

// TestDiscoverMovedBlockDoesNotWidenToOtherAddresses: the alias is one edge,
// not a wildcard. A resource carrying an address no moved block mentions is
// still an orphan, which is what keeps deleting a resource block working.
func TestDiscoverMovedBlockDoesNotWidenToOtherAddresses(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_subnet", "subnet-retired", `aws_subnet.retired`)

	res, diags := Discover(context.Background(), movedRenameRequest(t, cloud))
	assertNoErrors(t, diags)

	if len(res.Orphans) != 1 {
		t.Fatalf("want the unmentioned address to stay an orphan:\n%s", res)
	}
	if res.Orphans[0].ImportID != "subnet-retired" {
		t.Errorf("orphan is %s, want subnet-retired", res.Orphans[0])
	}
	if len(res.Bindings) != 0 {
		t.Errorf("an address no moved block mentions bound to something: %v", res.Bindings)
	}
}

// TestDiscoverMovedBlockResumesAPartialRewrite answers the interrupted-apply
// question directly. The rewrite is not a separate step this fork drives: it
// is one tag change per instance through the ordinary provider apply, so a run
// that dies partway leaves some instances carrying the new address and some
// still carrying the old one. Both spellings index to the same declared entry,
// so the next run binds every instance and finishes the tag change on the ones
// that missed it. Nothing is half-owned and nothing needs a repair command.
func TestDiscoverMovedBlockResumesAPartialRewrite(t *testing.T) {
	cloud := newFakeCloud()
	// "a" was rewritten before the interruption; "b" was not.
	cloud.own("aws_subnet", "subnet-a", EscapeAddress(`aws_subnet.pair["a"]`))
	cloud.own("aws_subnet", "subnet-b", EscapeAddress(`aws_subnet.pair_old["b"]`))

	res, diags := Discover(context.Background(), movedRenameRequest(t, cloud))
	assertNoErrors(t, diags)

	for addr, wantID := range map[string]string{
		`aws_subnet.pair["a"]`: "subnet-a",
		`aws_subnet.pair["b"]`: "subnet-b",
	} {
		b, ok := res.BindingFor(mustAddr(t, addr))
		if !ok {
			t.Errorf("%s did not bind after a partial rewrite:\n%s", addr, res)
			continue
		}
		if b.ImportID != wantID {
			t.Errorf("%s bound to %q, want %q", addr, b.ImportID, wantID)
		}
	}
	if len(res.Orphans) != 0 {
		t.Errorf("a half-rewritten set produced orphans, which plans destroys: %v", res.Orphans)
	}
}

// TestDiscoverMovedBlockOntoASetWithSlots is the case the instance-level alias
// cannot reach: the block gained `count` in the same change that renamed it,
// so its live members carry the bare pre-count marker. Filing the old block
// address on the count set is what lets the slot matcher place them, exactly
// as it places a member carrying the block's own address.
func TestDiscoverMovedBlockOntoASetWithSlots(t *testing.T) {
	cloud := newFakeCloud()
	cloud.obj("aws_eip", "eip-0", map[string]string{TagEstate: estateName, TagAddress: `aws_eip.single`, TagSlot: "0"})
	cloud.obj("aws_eip", "eip-1", map[string]string{TagEstate: estateName, TagAddress: `aws_eip.single`, TagSlot: "1"})

	cfg := loadConfig(t, "testdata/moved-gained-count")
	res, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
	})
	assertNoErrors(t, diags)

	for addr, wantID := range map[string]string{
		"aws_eip.pool[0]": "eip-0",
		"aws_eip.pool[1]": "eip-1",
	} {
		b, ok := res.BindingFor(mustAddr(t, addr))
		if !ok {
			t.Errorf("%s did not bind through the moved block's block-level alias:\n%s", addr, res)
			continue
		}
		if b.ImportID != wantID {
			t.Errorf("%s bound to %q, want %q", addr, b.ImportID, wantID)
		}
	}
	if len(res.Orphans) != 0 {
		t.Errorf("the moved set's members were filed as orphans, which plans destroys: %v", res.Orphans)
	}
}

// TestDiscoverMovedBlockOntoASetWithoutSlots is the same shape on an estate
// that carries no slot markers, where nothing distinguishes which live member
// is which declared index. That is the question tofu-slot exists to answer, so
// it has to come out as the named refusal a marker on the block's own address
// already produces - and specifically not as an orphan, which would plan a
// destroy of a resource the move was about.
func TestDiscoverMovedBlockOntoASetWithoutSlots(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_eip", "eip-0", `aws_eip.single`)
	cloud.own("aws_eip", "eip-1", `aws_eip.single`)

	cfg := loadConfig(t, "testdata/moved-gained-count")
	res, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
	})
	if !diags.HasErrors() {
		t.Fatalf("an unplaceable set was not refused:\n%s", res)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("an unplaceable set member became an orphan, which plans a destroy: %v", res.Orphans)
	}
	var kinds []ProblemKind
	for _, p := range res.Problems {
		kinds = append(kinds, p.Kind)
	}
	if len(kinds) != 1 || kinds[0] != ProblemNeedsSlotMarkers {
		t.Errorf("problems = %v, want exactly one %s", kinds, ProblemNeedsSlotMarkers)
	}
}
