// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// This file is [gauntlet:corpus-ecs-fargate/day2_replace]'s own unit, and
// GitHub issue #879: the terminated-shadow prune supersededclaimant.go
// describes, for an address whose live objects are found by their MARKER
// alone.
//
// #849 gave the prune its licence - a claimant is dropped only when the
// record says this estate's own apply destroyed it - and measured it on
// records identified by one string. A type can instead be identified by a
// composite identity OBJECT (aws_ecs_task_definition's family + revision),
// and that is what an apply records for it. But no live object of such a
// type carries an identity object when the only thing that found it was its
// own tag: [scanTypeMarkerFallback] (a declared type with no list route of
// any kind) and [sweepViaTagging] both compose a single import-identity
// string out of the object's ARN and file a claimant with
// identity == cty.NilVal.
//
// So the record named the object one way, the sighting named it the other,
// and [recordIdentityMatches] could only answer "no". Measured end to end on
// corpus-ecs-fargate: a plain ForceNew replace of the standalone task
// definition applied cleanly (8 add / 8 destroy, the marker confirmed moved
// onto the new object via the AWS CLI), and the very next plan refused
//
//	Error: Indistinguishable instances without per-instance markers
//
// naming both the new task definition and the deregistered one - forever,
// since ECS deregisters rather than deletes and the dead object keeps its
// tags. The record for that address held BOTH halves of the evidence at
// once (identity family=…-v2, tombstone family=…), and neither could be
// compared to what discovery had in hand.
//
// [projection.LocatedRecord.SecondaryID] is the missing half: the apply
// records the object's other name too, the one-string import identity a
// marker-driven pass composes, read with the same rule that pass uses
// ([identity.SecondaryImportID]).

// markerFoundCountRequest is a Request for the count fixture whose live
// members can only be found by their markers: the provider serves no list
// resource for the type at all, so [scanTypeMarkerFallback] is the only
// route to them, which is what makes their claimants carry an import ID and
// no identity object - the #879 shape, produced by the real code path rather
// than by hand-building a claimant.
func markerFoundCountRequest(t *testing.T, srv *taggingServer, store staterecord.Store) Request {
	t.Helper()
	server := srv.start(t)
	t.Cleanup(server.Close)
	return Request{
		HintStore: store,
		Tagging:   cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
	}
}

// countEIPARN is one elastic IP's ARN, whose resource-id segment is the
// import identity discovery composes for it - "eipalloc-…", the same string
// the record's second name holds.
func countEIPARN(id string) string {
	return "arn:aws:ec2:us-east-1:000000000000:elastic-ip/" + id
}

// TestDiscover_supersededCountShadowFoundByMarkerAlone is #879 end to end
// through Discover: the count instance's record names the surviving object
// and tombstones the destroyed one, both live objects are found by their
// markers alone (no identity object anywhere), and the plan must bind the
// survivor and report the shadow rather than refuse.
func TestDiscover_supersededCountShadowFoundByMarkerAlone(t *testing.T) {
	cloud := newFakeCloud()
	// No list route: the marker index is the only way to these objects,
	// which is what strips the identity object off their claimants.
	cloud.unlistable("aws_eip")

	srv := &taggingServer{}
	markedCountARN(srv, countEIPARN("eipalloc-new"))
	markedCountARN(srv, countEIPARN("eipalloc-old"))

	rawStore, seedStore := supersededHintStore(t, countEstate)
	// The record an apply writes for a composite-identity type: the
	// identity object, AND the object's other name.
	seedCurrentIdentity(t, seedStore, `aws_eip.pool[0]`, projection.LocatedRecord{
		Components:  map[string]string{"allocation_id": "eipalloc-new", "region": "us-east-1"},
		SecondaryID: "eipalloc-new",
	})
	seedTombstone(t, seedStore, `aws_eip.pool[0]`, projection.TombstoneRecord{
		Components:  map[string]string{"allocation_id": "eipalloc-old", "region": "us-east-1"},
		SecondaryID: "eipalloc-old",
	})

	addr := mustAddr(t, `aws_eip.pool[0]`)
	res, diags := discoverCountWith(t, cloud, 1, markerFoundCountRequest(t, srv, rawStore))
	assertNoErrors(t, diags)

	// The refusal #879 measured, by the summary an operator reads.
	for _, d := range diags {
		if d.Description().Summary == "Indistinguishable instances without per-instance markers" {
			t.Fatalf("the replace's own shadow still refuses the count instance:\n%s", res)
		}
	}

	b, bound := res.BindingFor(addr)
	if !bound {
		t.Fatalf("%s did not bind at all:\n%s", addr, res)
	}
	// BY VALUE: binding the destroyed object here is the wrong marker, not
	// a missing one - the plan would read and update an object the previous
	// apply already destroyed.
	if b.ImportID != "eipalloc-new" {
		t.Errorf("%s bound to %q, want the object the record names, eipalloc-new", addr, b.ImportID)
	}
	if got := displacedIDs(res); len(got) != 1 || got[0] != "eipalloc-old" {
		t.Fatalf("the tombstoned shadow was not reported as displaced (got %v), so a live marked object is acted on by nothing and mentioned by nothing:\n%s", got, res)
	}
	problems := res.ProblemsOfKind(ProblemDisplacedMarker)
	if !strings.Contains(problems[0].Detail, "destroyed by an earlier apply of this estate") {
		t.Errorf("the displaced report does not say the object was recorded destroyed:\n%s", problems[0].Detail)
	}
}

// TestDiscover_supersededCountLiveDuplicateFoundByMarkerAloneRefuses is the
// control, and #849's own rule carried onto this route: the SAME two
// marker-found claimants, the same record naming one of them, and NO
// tombstone. Nothing says the second object is dead, so it is a second live
// object wearing one address's marker and the run must still refuse.
//
// Without it, "match the record's second name" would read as a licence to
// prune whatever the record does not name, which is exactly the regression
// #670 measured and #849 closed.
func TestDiscover_supersededCountLiveDuplicateFoundByMarkerAloneRefuses(t *testing.T) {
	cloud := newFakeCloud()
	cloud.unlistable("aws_eip")

	srv := &taggingServer{}
	markedCountARN(srv, countEIPARN("eipalloc-new"))
	markedCountARN(srv, countEIPARN("eipalloc-duplicate"))

	rawStore, seedStore := supersededHintStore(t, countEstate)
	seedCurrentIdentity(t, seedStore, `aws_eip.pool[0]`, projection.LocatedRecord{
		Components:  map[string]string{"allocation_id": "eipalloc-new", "region": "us-east-1"},
		SecondaryID: "eipalloc-new",
	})

	res, diags := discoverCountWith(t, cloud, 1, markerFoundCountRequest(t, srv, rawStore))
	if !diags.HasErrors() {
		t.Fatalf("a second LIVE count member wearing the recorded member's marker was resolved away with nothing recording it as destroyed:\n%s", res)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("a count member bound despite a live duplicate:\n%s", res)
	}
	if got := displacedIDs(res); len(got) != 0 {
		t.Errorf("a live duplicate was reported as a displaced marker (%v), which says nothing is proposed for it while it is live and marked:\n%s", got, res)
	}
}

// markedCountARN puts one ARN on the Tagging API's table wearing the count
// fixture's first instance marker, which is the tag both a replace's new
// object and its shadow carry.
func markedCountARN(srv *taggingServer, arn string) {
	taggedARN(srv, arn, map[string]string{TagEstate: countEstate, TagAddress: "aws_eip.pool:0"})
}
