// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// These tests pin what [builder.checkOwnership] does TODAY about the
// tofu-address marker, which is nothing: it reads tags[markers.TagEstate]
// and no other tag, so a live object carrying this estate's marker under
// some other resource's address is adopted into prior state without a
// diagnostic, an omission or an Unowned entry.
//
// That is GitHub issue #244, and it is a defect, not a decision. These
// assertions record the defective behavior so the reproduction is not lost
// and so the eventual fix has to come here and invert them deliberately
// rather than discovering this file by surprise. Every want below that
// reads "adopted" or "no refusal" is the bug; when #244 lands, each becomes
// its opposite.
//
// The other half of #244 is in internal/live/discovery, whose scan drops a
// live object whose marker names a ClassConcrete declared address
// (discovery.go:1171's `if decl.declares(...) { continue }`) on the stated
// grounds that "the marker only confirms the estate still owns what it
// thinks it owns" - the confirmation this function does not perform. A
// probe for that half needs that package's own list-client fakes and is
// not written yet.

// TestOwnershipAddress_wrongAddressSameEstateIsAdopted: this estate's
// marker, another resource's address. Stock OpenTofu, holding no state for
// this address, would plan a create (and, for a unique-named type, collide);
// choudoufu adopts.
func TestOwnershipAddress_wrongAddressSameEstateIsAdopted(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate: ownershipEstate,
		// A different resource in this same estate owns this object.
		markers.TagAddress: "aws_cloudwatch_log_group.somebody_else",
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)

	// #244: all four of these are the defect.
	if !res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatal("issue #244 appears to be fixed: another instance's live object is no longer adopted. Invert this file's assertions.")
	}
	if len(res.Unowned) != 0 {
		t.Errorf("issue #244 appears to be fixed: the address disagreement now produces a refusal (%+v). Invert this file's assertions.", res.Unowned)
	}
	if len(res.Omitted) != 0 {
		t.Errorf("issue #244 appears to be fixed: the address disagreement now produces an omission (%+v). Invert this file's assertions.", res.Omitted)
	}
	if len(diags) != 0 {
		t.Errorf("issue #244 appears to be fixed: the address disagreement now warns:\n%s", renderDiags(diags))
	}
}

// TestOwnershipAddress_renumberedIndexIsAdopted is the shape the widened
// count.index domain rule (internal/live/lint/count_index_domain.go) admits:
// an identity-bearing argument indexed by count.index, where deleting a
// middle element renumbers the survivors. app resolves to the identity the
// live object marked app[2] holds, and adopts it.
func TestOwnershipAddress_renumberedIndexIsAdopted(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/c", map[string]string{
		"id": "/ours/c", "name": "/ours/c",
	}, map[string]string{
		markers.TagEstate:  ownershipEstate,
		markers.TagAddress: markers.EscapeAddress(`aws_cloudwatch_log_group.app[2]`),
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/c"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if !res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatal("issue #244 appears to be fixed: a renumbered index no longer adopts the neighbouring instance's live object. Invert this file's assertions.")
	}
	if len(res.Unowned) != 0 {
		t.Errorf("issue #244 appears to be fixed: %+v", res.Unowned)
	}
}

// TestOwnershipAddress_missingAddressMarkerIsAdopted: the estate marker
// alone, no tofu-address at all. Whether this one is a defect or a
// deliberate hand-adoption affordance is the open policy question on #244;
// it is pinned here because the fix has to answer it either way.
func TestOwnershipAddress_missingAddressMarkerIsAdopted(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate: ownershipEstate,
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: ownershipEstate}})

	assertNoErrors(t, diags)
	if !res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatal("issue #244's no-address case changed: an object with the estate marker and no tofu-address is no longer adopted. Settle it on the issue and update this test.")
	}
}
