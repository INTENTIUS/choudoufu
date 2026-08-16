// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"
)

// This file executes the half of GitHub issue #244 that was established by
// reading only. It pins what discovery does TODAY, which is nothing, and it
// is written so that fixing it makes these tests fail loudly rather than
// leaving a stale assertion behind.
//
// The other half - the projection adopting another instance's live object -
// is fixed: internal/live/projection's checkOwnership now compares the
// tofu-address marker through moved.Accepts, and
// internal/live/projection/ownership_address_test.go pins it. That fix does
// not reach here. The two halves are separate code paths over separate
// inputs: projection sees only the object the configuration's own identity
// fetched, and never learns that a second object exists carrying the address
// the first one should have had.
//
// # The hole
//
// scanType classifies every listed object this estate owns. For an object
// whose tofu-address is a ClassConcrete declared address, the classification
// ends at:
//
//	if decl.declares(typeName, escaped) {
//	    // ... the marker only confirms the estate still owns what it
//	    // thinks it owns.
//	    continue
//	}
//
// decl.declares consults d.all, which is populated from EVERY resolution
// with no class filter, so any declared concrete address matches. The
// confirmation the comment describes is not performed here and cannot be
// performed by the projection either, because the projection never sees this
// object: it reads the DIFFERENT object that the configuration's identity
// resolves to.
//
// The same short-circuit is in cloudcontrol.go and tagging.go, so this is not
// one branch. TestOwnershipAddress_displacedObjectIsInvisible_CloudControl
// and ..._Tagging are not written; the shape is identical and the fix has to
// cover all three.

// TestOwnershipAddress_displacedObjectIsInvisible is the executable form of
// #244's half 2, using the renumbering shape the issue describes.
//
// Two live buckets, both this estate's. One sits at the identity the
// configuration computes for aws_s3_bucket.data ("tofu-stateless-e2e-data" -
// storage.tf's own bucket argument). The other carries the SAME
// tofu-address marker at a different identity, which is what a renumbered
// count.index leaves behind: the object that used to be this address's is
// still marked as this address's.
//
// Two live objects, one estate, one address, is what live/MARKERS.md calls a
// named error: "Two resources carrying the same tofu-estate and the same
// tofu-address at once is also a named error." Nothing reports it here.
//
// collisionProblem, which reports that error elsewhere, cannot reach this
// case even if the short-circuit were deleted: it fires on an entry with more
// than one claimant, and entries live in d.types, which declaredInstances
// populates only from ClassNeedsDiscovery resolutions. A ClassConcrete
// address has no entry to accumulate claimants on. Whatever half 2's fix
// turns out to be, it is not "let these objects fall through to the existing
// collision path" - there is no such path for a client-named type. That is
// worth knowing before the fix is scoped.
func TestOwnershipAddress_displacedObjectIsInvisible(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_s3_bucket")

	// The object the configuration's identity finds.
	cloud.own("aws_s3_bucket", "tofu-stateless-e2e-data", `aws_s3_bucket.data`)
	// The displaced object: same estate, same address marker, different
	// identity. Nothing in this estate should be able to hold this and stay
	// silent.
	cloud.own("aws_s3_bucket", "tofu-stateless-e2e-data-displaced", `aws_s3_bucket.data`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	const displaced = "tofu-stateless-e2e-data-displaced"

	var seen []string
	for _, o := range res.Orphans {
		if o.ImportID == displaced {
			seen = append(seen, "Orphans")
		}
	}
	for _, p := range res.Problems {
		for _, id := range p.LiveIDs {
			if id == displaced {
				seen = append(seen, "Problems/"+string(p.Kind))
			}
		}
	}

	if len(seen) != 0 {
		t.Fatalf("GitHub issue #244's half 2 appears to be fixed: the displaced object now appears in %v.\n"+
			"That is the intended outcome - rewrite this file's assertions to demand it, and say which of the "+
			"three short-circuits (discovery.go, cloudcontrol.go, tagging.go) you fixed.\n%s", seen, res)
	}

	// Pinned, and this is the defect: a live object this estate owns, carrying
	// an address a second live object also carries, is in no section of the
	// result at all. Nothing proposes to remove it, nothing reports it, and
	// nothing tells the operator it exists.
	if len(res.Removals()) != 0 {
		t.Errorf("unexpected removals, which would change what this test is about:\n%s", res)
	}
}

// TestOwnershipAddress_declaresIgnoresTheIdentityEntirely isolates the cause
// from the symptom. The import ID here is not merely a different object's; it
// is a string the configuration could never produce for any instance. The
// short-circuit still fires, which proves decl.declares consults nothing but
// the marker string and the resource type.
//
// This is the assertion to read when deciding what half 2's fix must do: the
// check that is missing is "does this object's own identity match what the
// address it names resolves to", and discovery is the only layer holding both
// halves at once.
func TestOwnershipAddress_declaresIgnoresTheIdentityEntirely(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_s3_bucket")
	cloud.own("aws_s3_bucket", "nothing-in-this-configuration-names-this", `aws_s3_bucket.data`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	if len(res.Orphans) != 0 || len(res.Problems) != 0 {
		t.Fatalf("issue #244's half 2 appears to be fixed: an object whose identity no declared instance computes is now reported. Rewrite this file's assertions.\n%s", res)
	}
}

// TestOwnershipAddress_undeclaredAddressIsStillAnOrphan is the control, and
// it is what bounds the fix. The short-circuit does its intended job for a
// marker naming an address the configuration no longer declares: that one
// still reaches the orphan path, and a fix for the two tests above must not
// disturb it. Without this, a fix that simply deleted the short-circuit would
// look correct here and would drown every client-named estate in false
// orphans - which is the hazard TestSweepLeavesDeclaredClientNamedResourcesAlone
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
