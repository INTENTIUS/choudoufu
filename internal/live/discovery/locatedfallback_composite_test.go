// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// This file is GitHub issue #746's unit: [scanTypeLocatedFallback] used to
// skip every record whose ImportID was empty, which is the shape
// [projection.LocatedRecordFrom] writes for a type whose identity is the
// provider's own wire-identity COMPOSITE - one string per identity-schema
// attribute and no joined string at all. The instance was left unbound, and
// an unbound declared instance is a proposed create of a second copy of a
// live object the estate had already recorded.
//
// The assertions are on rendered values - the components the binding and the
// merged resolution carry, by value - never on a predicate boolean, and each
// was proven able to fail (see TestScanTypeLocatedFallback_theGuardsCanFail).

// locatedFallbackComponents is the record an apply's own write-back leaves
// for a composite-identity instance: attributes, no import ID. It is the
// shape [projection.LocatedRecordFrom]'s Composite branch produces.
var locatedFallbackComponents = map[string]string{
	"domain_identifier": "dzd_1234567890",
	"id":                "5x0hj0aab3d1cq",
}

// seedCompositeRecord writes a Components-only record for addr through the
// ordinary seeding path, so the test exercises the record shape the writer
// really produces rather than a hand-built envelope.
func seedCompositeRecord(t *testing.T, store *projection.RecordStore, addr string, components map[string]string) {
	t.Helper()
	seedCurrentIdentity(t, store, addr, projection.LocatedRecord{Components: components})
}

// untaggableUnlistableWithRecord sets up the one situation this function is
// reached in: a declared type with no tags argument in its schema and no
// list route of any kind, plus an estate record store. aws_route_table is
// the fixture's stand-in for that shape, the same repurposing
// TestScanTypeMarkerFallbackLeavesAnUntaggableTypeRefused already makes.
func untaggableUnlistableWithRecord(t *testing.T) (*fakeCloud, *projection.RecordStore, Request) {
	t.Helper()
	cloud := newFakeCloud()
	cloud.unlistable("aws_route_table")
	cloud.untagged["aws_route_table"] = true
	// The composite the record's components belong to, stated on the
	// provider side too, so the fixture is the whole shape and not just its
	// record half.
	cloud.withIdentitySchema("aws_route_table", "domain_identifier", "id")

	raw, seed := supersededHintStore(t, estateName)
	return cloud, seed, Request{HintStore: raw}
}

// TestScanTypeLocatedFallback_bindsACompositeRecord is the headline. The
// estate's record store holds a composite identity for a declared instance
// of a markerless, unlistable type; that instance must bind to exactly those
// components, and must not be left unbound for the plan to propose creating.
func TestScanTypeLocatedFallback_bindsACompositeRecord(t *testing.T) {
	cloud, seed, req := untaggableUnlistableWithRecord(t)
	seedCompositeRecord(t, seed, `aws_route_table.main`, locatedFallbackComponents)

	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	addr := mustAddr(t, `aws_route_table.main`)
	b, bound := res.BindingFor(addr)
	if !bound {
		t.Fatalf("%s did not bind, so the plan would propose creating a second copy of the recorded object:\n%s", addr, res)
	}
	if got, want := len(b.IdentityValues), len(locatedFallbackComponents); got != want {
		t.Fatalf("%s bound with %d identity component(s), want %d: %v", addr, got, want, b.IdentityValues)
	}
	for name, want := range locatedFallbackComponents {
		if got := b.IdentityValues[name]; got != want {
			t.Errorf("%s bound with identity component %s=%q, want %q", addr, name, got, want)
		}
	}
	if b.ImportID != "" {
		t.Errorf("%s bound with import ID %q; a wire-identity composite has no separator to join its components with, so the string must stay empty", addr, b.ImportID)
	}

	// The merged resolution is what the projection actually consumes, and a
	// binding the resolution list does not carry changes no plan.
	var seen bool
	for _, r := range res.Resolutions {
		if r.Addr.String() != addr.String() {
			continue
		}
		seen = true
		if r.Class != identity.ClassConcrete {
			t.Errorf("merged resolution for %s is %s, want CONCRETE", addr, r.Class)
		}
		for name, want := range locatedFallbackComponents {
			if got := r.IdentityValues[name]; got != want {
				t.Errorf("merged resolution for %s carries identity component %s=%q, want %q", addr, name, got, want)
			}
		}
	}
	if !seen {
		t.Fatalf("no merged resolution for %s at all:\n%s", addr, res)
	}

	if problems := problemsOfKind(res, ProblemTypeNotListable); len(problems) != 0 {
		t.Errorf("the type was still refused as not-listable:\n%s", renderProblems(problems))
	}
	scan, ok := res.ScanFor("aws_route_table")
	if !ok || scan.Source != SourceRecordStore || scan.Listed != 1 {
		t.Errorf("aws_route_table scan = %+v, want Source=%s Listed=1", scan, SourceRecordStore)
	}
}

// TestScanTypeLocatedFallback_noRecordStillLeavesTheInstanceUnbound is the
// other half, and the part of the old skip that was right: with nothing
// written down there is no answer to which live object the instance owns,
// and inventing one would be exactly the wrong marker HANDOFF.md's safety
// rule forbids. Unbound - so the plan proposes a create - is the honest
// answer, and reading composite records must not have widened what binds.
func TestScanTypeLocatedFallback_noRecordStillLeavesTheInstanceUnbound(t *testing.T) {
	cloud, _, req := untaggableUnlistableWithRecord(t)
	// A store, opened and asked, with no record for this address.

	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	addr := mustAddr(t, `aws_route_table.main`)
	if b, bound := res.BindingFor(addr); bound {
		t.Fatalf("%s bound to %q with nothing recorded for it", addr, b.identityDisplay())
	}
	if !containsAddr(res.Unbound, addr.String()) {
		t.Errorf("%s is neither bound nor reported unbound:\n%s", addr, res)
	}
}

// TestScanTypeLocatedFallback_theGuardsCanFail records the mutations each
// assertion above was proven red under, so a later reader does not have to
// take "it passes" as evidence it can ever fail.
//
//   - Restoring locatedfallback.go's `if rec.ImportID == "" { continue }`
//     skip makes TestScanTypeLocatedFallback_bindsACompositeRecord fail at
//     "did not bind, so the plan would propose creating a second copy".
//   - Dropping `IdentityValues: c.identityValues` from bindClaimant makes it
//     fail at "bound with 0 identity component(s), want 2".
//   - Dropping `IdentityValues: b.IdentityValues` from the merged-resolution
//     rewrite makes it fail at "merged resolution for ... carries identity
//     component domain_identifier=\"\"".
//   - Seeding no record at all makes
//     TestScanTypeLocatedFallback_noRecordStillLeavesTheInstanceUnbound's
//     sibling assertion the live one: seeding the composite record instead
//     makes that test fail at "bound to ... with nothing recorded for it",
//     which is how it was checked to be about the record and not about the
//     type being unlistable.
//
// Not proven red, and deliberately not asserted anywhere:
// locatedfallback.go's len(rec.Components) == 0 guard. It is unreachable -
// [projection.RecordStore.GetIdentity] refuses an empty identity, and an
// empty component, with an error before ever returning identityFound - so
// removing it changes no test outcome. It is written down here rather than
// claimed as covered, because a guard nothing can turn red is not a check.
//
// The test body itself asserts nothing; the comment is the artifact.
func TestScanTypeLocatedFallback_theGuardsCanFail(t *testing.T) {}
