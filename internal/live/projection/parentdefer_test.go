// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is the projection-side half of GitHub issue #346, and the half
// that decides whether the identity-side widening is safe to have.
//
// #346 lets an identity argument defer to one NON-IDENTITY attribute of a
// parent whose own identity is not in the configuration - a
// [identity.ClassNeedsDiscovery] parent - on the argument that marker
// discovery makes such a parent concrete before any formula renders, so its
// whole live object is in the projection by the time the promise is read.
//
// Two things have to hold for that to be a safe claim rather than a hopeful
// one, and neither is provable from the identity package:
//
//  1. When discovery DID find the parent, the attribute rendered is the one
//     the LIVE OBJECT carries, not anything derived from the identity the
//     parent was looked up with. TestDeferredAttributeRendersFromTheLiveObject.
//  2. When discovery did NOT find it, the child is omitted with a reason a
//     reader can act on, and nothing is imported. It must not render half a
//     formula, must not fall back to the parent's import ID, and must not
//     vanish from the report. TestDeferredAttributeOverUndiscoveredParentIsOmitted.
//
// The second is the negative control for the data-integrity hazard #346 was
// filed with, and it is worth stating exactly which hazard it is and is not.
// The named hazard - [ReadInstances]'s own doc comment, and
// internal/live/discovery's classifyOrphans - is that a live, marker-carrying
// resource is withheld from Removal only while its block still has an unbound
// declared instance, so a design that makes a FIRST resolution pass non-fatal
// in order to read live values for a SECOND one can let a block that failed to
// resolve contribute no declared instances and have its live objects swept as
// orphans. #346's fix is not that design: it decides inside one ordinary
// resolution pass, before discovery has run at all, and resolution stays fatal
// exactly as it was. What this file pins is the weaker property that has to
// hold anyway - a promise that cannot be kept costs a MISSING marker, and the
// missing one is visible in Omitted rather than silent.

// TestDeferredAttributeRendersFromTheLiveObject is case (1). The route table
// is handed to the builder as CONCRETE with the import ID discovery would have
// found for it, and the route's formula reads the table's vpc_id - a plain
// attribute of the table's object that is not part of its identity at all, and
// which is exactly the shape #346 admits.
//
// Two strings could stand in for the right answer and both are wrong: the
// import ID the table was looked up with ("rtb-imported") and the table's own
// live id ("rtb-live"). The fixture makes vpc_id differ from both, so a render
// that reached for either fails here.
func TestDeferredAttributeRendersFromTheLiveObject(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")

	rtb := mustAddr(t, `aws_route_table.main`)
	route := mustAddr(t, `aws_route.internet_gateway`)

	cloud := newFakeCloud()
	cloud.put("aws_route_table", "rtb-imported", map[string]string{
		"id": "rtb-live", "vpc_id": "vpc-live",
	})
	cloud.put("aws_route", "vpc-live_0.0.0.0/0", map[string]string{
		"id": "r-1", "route_table_id": "rtb-live", "destination_cidr_block": "0.0.0.0/0",
	})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		derivedOn(route, rtb, "vpc_id", "_0.0.0.0/0"),
		// What discovery hands back for a parent it found: the class is
		// rewritten CONCRETE and the import ID is the live one. See
		// internal/live/discovery/result.go.
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-imported"},
	}, cloud.providers(t))
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{
		`aws_route.internet_gateway`,
		`aws_route_table.main`,
	})
	for _, wrong := range []string{"aws_route/rtb-imported_0.0.0.0/0", "aws_route/rtb-live_0.0.0.0/0"} {
		for _, got := range cloud.imports {
			if got == wrong {
				t.Errorf("the route was imported as %q; the formula read something other than the parent's live vpc_id", got)
			}
		}
	}
	var sawRoute bool
	for _, got := range cloud.imports {
		if got == "aws_route/vpc-live_0.0.0.0/0" {
			sawRoute = true
		}
	}
	if !sawRoute {
		t.Errorf("the route was never imported by its parent's live vpc_id; imports were %v", cloud.imports)
	}
}

// TestDeferredAttributeOverUndiscoveredParentIsOmitted is case (2), and the
// negative control the whole change rests on: a parent that stayed
// [identity.ClassNeedsDiscovery] - discovery ran and did not find it - must
// take its child down with it, visibly.
//
// "Visibly" is the load-bearing word and is asserted three ways, because the
// dangerous failure here is not an error but a silence. Nothing is imported;
// the parent is recorded omitted as ReasonNeedsDiscovery and the child as
// ReasonParentUnavailable; and both omissions carry a Detail (assertOmitted
// fails an empty one), so the report names the resource and the reason rather
// than dropping a row.
func TestDeferredAttributeOverUndiscoveredParentIsOmitted(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")

	rtb := mustAddr(t, `aws_route_table.main`)
	route := mustAddr(t, `aws_route.internet_gateway`)

	// The object IS in the cloud. It is not found because nothing named it -
	// which is what a needs-discovery resolution means - and this fixture
	// makes that the only reason, so an accidental render would have real
	// values to render from and would be caught here rather than failing for
	// want of data.
	cloud := newFakeCloud()
	cloud.put("aws_route_table", "rtb-imported", map[string]string{
		"id": "rtb-live", "vpc_id": "vpc-live",
	})
	cloud.put("aws_route", "vpc-live_0.0.0.0/0", map[string]string{
		"id": "r-1", "route_table_id": "rtb-live", "destination_cidr_block": "0.0.0.0/0",
	})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		derivedOn(route, rtb, "vpc_id", "_0.0.0.0/0"),
		{
			Addr:   rtb,
			Class:  identity.ClassNeedsDiscovery,
			Reason: "aws_route_table's id is assigned by AWS at create time.",
		},
	}, cloud.providers(t))
	assertNoErrors(t, diags)

	if len(cloud.imports) != 0 {
		t.Errorf("something was imported although the formula's parent was never discovered: %v", cloud.imports)
	}
	if len(res.Materialized) != 0 {
		t.Errorf("something was materialized although the formula's parent was never discovered:\n%s", res)
	}
	assertOmitted(t, res, map[string]Reason{
		`aws_route_table.main`:       ReasonNeedsDiscovery,
		`aws_route.internet_gateway`: ReasonParentUnavailable,
	})
}

// TestDeferredAttributeAbsentFromTheLiveObjectIsRefused is the third leg, and
// the one that keeps the identity package's schema check from being the only
// thing standing between a typo and a marker. [identity.resolver.parentPart]
// admits a deferred attribute only when the provider's schema declares it -
// but a schema is a claim about the type, and this is a claim about one
// object. An attribute the object does not carry has to refuse HERE too,
// loudly, rather than render as an empty segment.
func TestDeferredAttributeAbsentFromTheLiveObjectIsRefused(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")

	rtb := mustAddr(t, `aws_route_table.main`)
	route := mustAddr(t, `aws_route.internet_gateway`)

	cloud := newFakeCloud()
	// No vpc_id on the object at all.
	cloud.put("aws_route_table", "rtb-imported", map[string]string{"id": "rtb-live"})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		derivedOn(route, rtb, "vpc_id", "_0.0.0.0/0"),
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-imported"},
	}, cloud.providers(t))

	if !hasDiag(diags, "Cannot read a parent's identity from the projection", "vpc_id") {
		t.Errorf("no diagnostic names the attribute the parent's object does not carry:\n%s", renderDiags(diags))
	}
	for _, m := range res.Materialized {
		if m.Equal(route) {
			t.Errorf("%s was materialized from a formula that could not be rendered", route)
		}
	}
	assertOmitted(t, res, map[string]Reason{
		`aws_route.internet_gateway`: ReasonFailed,
	})
}
