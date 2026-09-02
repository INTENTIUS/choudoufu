// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"
)

// This file executes GitHub issue #244's half 2. It was written first as a
// pin on what discovery did - which was nothing - and is now the demand.
//
// # What the hole was
//
// scanType classifies every listed object this estate owns. For an object
// whose tofu-address is a declared client-named address, the classification
// ended at:
//
//	if decl.declares(typeName, escaped) {
//	    // ... the marker only confirms the estate still owns what it
//	    // thinks it owns.
//	    continue
//	}
//
// decl.declares consulted d.all, a set of strings populated from every
// resolution with no class filter, so any declared address matched and the
// confirmation the comment describes was never performed. The projection
// could not perform it either: it reads the DIFFERENT object the
// configuration's own identity resolves to, and never learns this one exists.
//
// # What it is now
//
// d.all holds the resolution rather than a bool, and declared.displacedFrom
// compares the identity the configuration computes for the address against
// the identity the cloud attached to the object carrying its marker. A
// positive mismatch is filed as ProblemDisplacedMarker - a WARNING, and
// nothing more: the object stays out of Orphans, out of Removals and out of
// the resolutions, exactly where it was. See displaced.go for why reporting
// and not acting is the whole safety argument.
//
// The same short-circuit is in cloudcontrol.go and tagging.go and all three
// now go through the one function.

// TestOwnershipAddress_displacedObjectIsReported is #244 half 2's own shape.
//
// Two live buckets, both this estate's. One sits at the identity the
// configuration computes for aws_s3_bucket.data ("tofu-stateless-e2e-data" -
// storage.tf's own bucket argument). The other carries the SAME tofu-address
// marker at a different identity, which is what a renumbered count.index
// leaves behind: the object that used to be this address's is still marked as
// this address's.
//
// Two live objects, one estate, one address, is what live/MARKERS.md calls a
// named error. It is now reported.
func TestOwnershipAddress_displacedObjectIsReported(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_s3_bucket")

	// The object the configuration's identity finds.
	cloud.own("aws_s3_bucket", "tofu-stateless-e2e-data", `aws_s3_bucket.data`)
	// The displaced object: same estate, same address marker, different
	// identity.
	cloud.own("aws_s3_bucket", "tofu-stateless-e2e-data-displaced", `aws_s3_bucket.data`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	const displaced = "tofu-stateless-e2e-data-displaced"

	p := findDisplaced(t, res, displaced)
	if p.TypeName != "aws_s3_bucket" {
		t.Errorf("problem type %q, want aws_s3_bucket", p.TypeName)
	}
	if p.Marker != `aws_s3_bucket.data` {
		t.Errorf("problem marker %q, want aws_s3_bucket.data", p.Marker)
	}
	// The detail has to name BOTH identities, or the operator cannot tell
	// which of the two objects the configuration means.
	if !strings.Contains(p.Detail, displaced) || !strings.Contains(p.Detail, "tofu-stateless-e2e-data\"") {
		t.Errorf("the detail does not name both identities:\n%s", p.Detail)
	}

	// The object at the identity the configuration DOES compute is not
	// reported. Only the displaced one is.
	for _, q := range res.Problems {
		if q.Kind != ProblemDisplacedMarker {
			continue
		}
		for _, id := range q.LiveIDs {
			if id == "tofu-stateless-e2e-data" {
				t.Errorf("the object the configuration's own identity names was reported as displaced:\n%s", q.Detail)
			}
		}
	}

	// And the safety property, which is the reason this is a warning: the
	// finding proposes nothing. Reporting a displaced object must never
	// become destroying one.
	assertNotAnOrphan(t, res, displaced)
	if p.Kind.Severity() != SeverityWarning {
		t.Errorf("ProblemDisplacedMarker severity is %s; an inexact identity comparison must not be able to fail a plan", p.Kind.Severity())
	}
}

// TestOwnershipAddress_displacedObjectNoOtherClaimant is the single-object
// form, and it is the one that proves the check consults the identity rather
// than counting markers.
//
// The import ID here is not merely a different object's; it is a string the
// configuration could never produce for any instance, and no second object
// carries the address. A rule that fired only on two objects sharing a marker
// would be silent here - and would be silent for the renumbering shape #244
// describes too, where before the apply exactly one object carries each
// address and the disagreement is entirely between a marker and an identity.
func TestOwnershipAddress_displacedObjectNoOtherClaimant(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_s3_bucket")
	cloud.own("aws_s3_bucket", "nothing-in-this-configuration-names-this", `aws_s3_bucket.data`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	findDisplaced(t, res, "nothing-in-this-configuration-names-this")
	assertNotAnOrphan(t, res, "nothing-in-this-configuration-names-this")
}

// TestOwnershipAddress_undeclaredAddressIsStillAnOrphan is the control, and
// it bounds the fix. The short-circuit does its intended job for a marker
// naming an address the configuration no longer declares: that one still
// reaches the orphan path.
//
// Without this, a fix that simply deleted the short-circuit would look
// correct in the two tests above and would drown every client-named estate in
// false orphans - the hazard TestSweepLeavesDeclaredClientNamedResourcesAlone
// was written for.
func TestOwnershipAddress_undeclaredAddressIsStillAnOrphan(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_s3_bucket")
	cloud.own("aws_s3_bucket", "tofu-stateless-e2e-deleted", `aws_s3_bucket.deleted`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	if len(res.Orphans) != 1 || res.Orphans[0].ImportID != "tofu-stateless-e2e-deleted" {
		t.Fatalf("a marker for an address the configuration no longer declares is not an orphan:\n%s", res)
	}
}

// TestOwnershipAddress_matchingObjectIsSilent is the other control, and it is
// the one that would catch a comparison too eager to report. The estate
// fixture's own bucket, at its own identity, under its own marker: the case
// every run of every client-named estate takes. Nothing may be said about it.
func TestOwnershipAddress_matchingObjectIsSilent(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_s3_bucket")
	cloud.own("aws_s3_bucket", "tofu-stateless-e2e-data", `aws_s3_bucket.data`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	for _, p := range res.Problems {
		if p.Kind == ProblemDisplacedMarker {
			t.Fatalf("a client-named resource at exactly the identity its own address computes was reported as displaced:\n%s", p.Detail)
		}
	}
	assertNotAnOrphan(t, res, "tofu-stateless-e2e-data")
}

// TestOwnershipAddress_movedBlockIsNotDisplacement is the #198 guard, over the
// one branch #198's own tests cannot reach.
//
// A pending move legitimately leaves the live object carrying the OLD address
// until the marker rewrite lands, so its marker and its identity disagree by
// design. testdata/moved-rename covers this for aws_subnet, a needs-discovery
// type - which binds through declared.entryFor and never reaches the
// declares() short-circuit where #244's check lives. This fixture is
// client-named, so it does reach it, and the check must stay quiet: the
// resolution filed under a moved origin in declared.all is the DESTINATION's
// own resolution, so the identity compared is the destination's.
//
// If this ever fails, a moved block over a client-named resource has started
// producing a warning on every run until the tag is rewritten.
func TestOwnershipAddress_movedBlockIsNotDisplacement(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_s3_bucket")
	cloud.own("aws_s3_bucket", "tofu-stateless-e2e-renamed", `aws_s3_bucket.old`)

	cfg := loadConfig(t, "testdata/moved-clientnamed")
	res, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
		Sweep:       true,
	})
	assertNoErrors(t, diags)

	for _, p := range res.Problems {
		if p.Kind == ProblemDisplacedMarker {
			t.Fatalf("a pending move over a client-named resource was reported as a displacement:\n%s", p.Detail)
		}
	}
	assertNotAnOrphan(t, res, "tofu-stateless-e2e-renamed")
}

// TestOwnershipAddress_movedBlockWithAWrongIdentityIsStillDisplacement is the
// same fixture with the object at an identity the destination does not
// compute. The alias explains a wrong ADDRESS, not a wrong object: a moved
// block in the configuration must not become a blanket exemption for
// everything of that type.
func TestOwnershipAddress_movedBlockWithAWrongIdentityIsStillDisplacement(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_s3_bucket")
	cloud.own("aws_s3_bucket", "some-other-bucket-entirely", `aws_s3_bucket.old`)

	cfg := loadConfig(t, "testdata/moved-clientnamed")
	res, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
		Sweep:       true,
	})
	assertNoErrors(t, diags)

	findDisplaced(t, res, "some-other-bucket-entirely")
	assertNotAnOrphan(t, res, "some-other-bucket-entirely")
}

// findDisplaced is the assertion the tests above share: exactly one
// ProblemDisplacedMarker naming this live ID.
func findDisplaced(t *testing.T, res *Result, importID string) Problem {
	t.Helper()

	var found []Problem
	for _, p := range res.Problems {
		if p.Kind != ProblemDisplacedMarker {
			continue
		}
		for _, id := range p.LiveIDs {
			if id == importID {
				found = append(found, p)
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("no DISPLACED_MARKER problem names %q:\n%s", importID, res)
	}
	t.Fatalf("%d DISPLACED_MARKER problems name %q, want one:\n%s", len(found), importID, res)
	return Problem{}
}

// assertNotAnOrphan is the safety half of every test here, and the reason
// they all carry it: this change reports, and a report that quietly became a
// removal would be the one outcome worse than the bug it fixes.
func assertNotAnOrphan(t *testing.T, res *Result, importID string) {
	t.Helper()

	for _, o := range res.Orphans {
		if o.ImportID == importID {
			t.Fatalf("%q was filed as an orphan, which proposes a destroy:\n%s", importID, res)
		}
	}
	for _, o := range res.Removals() {
		if o.ImportID == importID {
			t.Fatalf("%q was proposed for removal:\n%s", importID, res)
		}
	}
}
