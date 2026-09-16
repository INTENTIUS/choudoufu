// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// TestNativeSweepSaysSoWhenNoLegCanReadAListedObjectsMarker is issue #1136:
// the native/provider leg's half of #881, which PR #1129 fixed only on the
// Cloud Control leg.
//
// aws_iam_role is enumerated natively (scan.Source PROVIDER - #394's
// unconditional routing, re-verified by #1050/#1130 which found BOTH of
// [partitionSweepTypes]'s arms independently true for it). Enumeration is
// not the problem. The marker READ is: iam:ListRoles returns no tags at
// all, so [scanType] falls through to issue #266's tag-index join, which is
// fed by the one GetResources call the tagging leg makes - and
// GetResources never indexes IAM ([taggingAPIUnservedServices], probed
// against real AWS on #692, and floci has matched that since lex00/floci#202,
// pinned by #1045).
//
// So the join answers joinNone for a reason that has nothing to do with the
// object: the index was never going to hold it. joinNone's own doc comment
// says "the object is genuinely not this estate's, AS FAR AS THE TAG INDEX
// CAN SAY" - and for an unserved service the tag index can say nothing at
// all. The marker then reads unset and the `case estate == ""` arm below
// `continue`s on an ordinary sweep (CollectUnclaimed unset), which is where
// the object disappeared: no Orphan, no Unclaimed, no Problem, and the type
// still recorded in [Result.SweepCovered] as searched.
func TestNativeSweepSaysSoWhenNoLegCanReadAListedObjectsMarker(t *testing.T) {
	const (
		unservedType = "aws_iam_role"
		liveName     = "estate-team-role"
		deletedAddr  = unservedType + ".removed"
	)

	// The premises, stated rather than assumed.
	if !taggingAPIUnservedType(unservedType) {
		t.Fatalf("%s is no longer in a service taggingAPIUnservedServices names, so this fixture no longer exercises the join that cannot answer", unservedType)
	}

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(unservedType)
	// A live role this estate owns, carrying this estate's marker for an
	// address the configuration no longer declares - the removal the sweep
	// exists to propose.
	cloud.own(unservedType, liveName, deletedAddr)
	// ...whose marker the list call does not return. [stripTags]'s own doc
	// comment is exactly this case: "precisely what the AWS provider hands
	// back for aws_iam_role: not an untaggable type, not a missing object,
	// an object whose marker the list call did not return."
	stripTags(t, cloud, unservedType, liveName)

	// GetResources answers, successfully, with nothing - which is what real
	// AWS and the pinned floci both do for IAM. The index is AVAILABLE and
	// still cannot speak about this object, which is the whole distinction
	// this test turns on: joinUnavailable would be a different (transient)
	// fact and carries a different reason.
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	req := Request{
		Sweep:        true,
		TaggingSweep: true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagServer.URL}),
		SweepTypes:   []string{unservedType},
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	// Premise: the native leg really did enumerate the object. Without this
	// every assertion below could pass because nothing ran.
	scan, ok := res.ScanFor(unservedType)
	if !ok || scan.Source != SourceProvider || scan.Listed != 1 {
		t.Fatalf("the %s scan is %+v (found=%v), want Source=%s Listed=1 - this fixture is not the native-enumeration shape at all:\n%s",
			unservedType, scan, ok, SourceProvider, res)
	}
	if tagSrv.calls == 0 {
		t.Fatalf("the Resource Groups Tagging API fake was never called, so no tag-index join was attempted and this fixture does not reach the defect")
	}

	// The silent drop itself, stated so a regression that merely moves the
	// object somewhere else is not read as a fix.
	if len(removalsByAddr(res)) != 0 {
		t.Fatalf("this fixture is meant to be UNREPAIRABLE at this pin - no leg can read the marker - yet a removal was proposed; the test no longer isolates the reporting question:\n%s", res)
	}

	var reason SweepGapReason
	for _, g := range res.SweepGaps {
		if g.TypeName == unservedType {
			reason = g.Reason
		}
	}
	if reason != SweepGapMarkerUnreadable {
		var covered bool
		for _, c := range res.SweepCovered {
			if c == unservedType {
				covered = true
			}
		}
		t.Fatalf("the sweep gap for %s is %q, want %q (reported as covered=%v, orphans=%d, unclaimed=%d, problems=%d). The provider listed the object, its own tags carry no marker, and the tag index cannot speak for this service - so the sweep cannot tell \"this estate owns none\" from \"nobody could look\", and said neither.\n%s",
			unservedType, reason, SweepGapMarkerUnreadable, covered, len(res.Orphans), len(res.Unclaimed), len(res.Problems), res)
	}

	for _, c := range res.SweepCovered {
		if c == unservedType {
			t.Errorf("%s is still in Result.SweepCovered - \"searched for resources this estate owns but no longer declares\" - on the same run that files the gap saying the search established nothing. #1129 dropped the type on the Cloud Control leg for exactly this reason.\n%s", unservedType, res)
		}
	}

	var spoken bool
	for _, d := range diags {
		if d.Description().Summary == SummaryIncompleteSweep {
			spoken = true
			t.Logf("GREEN, quoted verbatim: %s", d.Description().Detail)
		}
	}
	if !spoken {
		t.Fatalf("the gap for %s produced no diagnostic at all, so nothing reaches the operator; diagnostics: %s", unservedType, renderDiags(diags))
	}
}

// TestNativeSweepDoesNotCryMarkerUnreadableWhenTheListCallCarriesTags is the
// mutation control, and it is the assertion that keeps the fix above from
// being a gap on every sweep of every unserved type.
//
// A sweep's ordinary population is objects that are NOT this estate's, and
// every one of them reaches the same join with the same empty answer. If
// "the join said nothing" alone filed the gap, an account full of other
// people's IAM roles would report a coverage hole that does not exist. The
// discriminating fact is empirical and the same shape [sweepViaTagging]
// already uses for its own unserved case (`len(byType[typeName]) == 0`):
// did ANY object of this type come back from the list call carrying tags of
// its own? One that did proves the list route delivers tags for this type,
// which makes an untagged sibling genuinely untagged rather than unreadable.
func TestNativeSweepDoesNotCryMarkerUnreadableWhenTheListCallCarriesTags(t *testing.T) {
	const unservedType = "aws_iam_role"

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(unservedType)
	// The real aws_iam_role list schema carries no filter block, so the
	// scan runs CLIENT_SIDE/ALL and sees every role in the account - which
	// is the shape this control has to reproduce. [stripTags] sets this for
	// the test above; here it is set on its own because nothing is being
	// stripped.
	cloud.noFilter(unservedType)
	// Somebody else's role, tagged - the list call demonstrably carries
	// tags for this type on this run.
	cloud.obj(unservedType, "someone-elses-role", map[string]string{"Owner": "platform"})
	// ...and an untagged one beside it. Genuinely untagged, not unreadable:
	// the run has direct evidence the route would have shown a marker.
	cloud.obj(unservedType, "bare-role", map[string]string{})

	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	req := Request{
		Sweep:        true,
		TaggingSweep: true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagServer.URL}),
		SweepTypes:   []string{unservedType},
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	if scan, ok := res.ScanFor(unservedType); !ok || scan.Listed != 2 {
		t.Fatalf("the %s scan is %+v (found=%v), want Listed=2 - this control is not exercising the list path:\n%s", unservedType, scan, ok, res)
	}
	for _, g := range res.SweepGaps {
		if g.TypeName == unservedType && g.Reason == SweepGapMarkerUnreadable {
			t.Fatalf("a %s gap was filed although a listed object of the type came back carrying tags: the list route delivers tags for this type on this run, so an untagged sibling is genuinely untagged. This gap fires on every sweep of every unserved type and is noise.\ngap: %s\n%s", unservedType, g, res)
		}
	}
	for _, c := range res.SweepCovered {
		if c == unservedType {
			return
		}
	}
	t.Errorf("%s is not in Result.SweepCovered, but the sweep really did search it - the list call answered and its tags were readable:\n%s", unservedType, res)
}

// TestNativeSweepStaysQuietOverAnOrdinaryUntaggedType is the second control,
// and it is the one that keeps the fix off the default real-AWS path.
//
// req.Tagging is nil for every run that names no endpoint override and does
// not opt into Cloud Control (internal/command/live_plan.go), which is most
// runs against a real account. On such a run EVERY marker read that falls
// to the tag-index join answers joinUnavailable - for every type, served or
// not. If that alone filed a gap, an account with an untagged corner would
// warn per type about types whose markers read perfectly well, because "no
// object of this type came back tagged" has a second, utterly ordinary
// cause: nothing of that type in the account is tagged.
//
// The gate is [taggingAPIUnservedType], read for what it implies about the
// LIST route (see [sweepMarkerReadGap]). aws_s3_bucket is outside it: the
// provider's own list call returns bucket tags, so an untagged bucket is
// evidence about the bucket and not about the route.
func TestNativeSweepStaysQuietOverAnOrdinaryUntaggedType(t *testing.T) {
	const ordinaryType = "aws_s3_bucket"

	if taggingAPIUnservedType(ordinaryType) {
		t.Fatalf("%s has joined taggingAPIUnservedServices, so it is no longer the off-the-list control this test needs", ordinaryType)
	}

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(ordinaryType)
	cloud.noFilter(ordinaryType)
	// Two untagged buckets and nothing else - the untagged corner of a real
	// account, where the marker read falls to a join that is not there.
	cloud.obj(ordinaryType, "logs-bucket", map[string]string{})
	cloud.obj(ordinaryType, "backups-bucket", map[string]string{})

	// No Tagging client: every join answers joinUnavailable.
	res, diags := discoverFixture(t, cloud, Request{
		Sweep:      true,
		SweepTypes: []string{ordinaryType},
	})
	assertNoErrors(t, diags)

	if scan, ok := res.ScanFor(ordinaryType); !ok || scan.Listed != 2 {
		t.Fatalf("the %s scan is %+v (found=%v), want Listed=2 - this control is not exercising the list path:\n%s", ordinaryType, scan, ok, res)
	}
	for _, g := range res.SweepGaps {
		if g.TypeName == ordinaryType && (g.Reason == SweepGapTagIndexUnavailable || g.Reason == SweepGapMarkerUnreadable) {
			t.Fatalf("a %s gap was filed on a run with no tag index, although nothing says this type's list route drops tags. This warning lands on the DEFAULT real-AWS path, once per untagged type, and none of it is true.\ngap: %s\n%s", ordinaryType, g, res)
		}
	}
	for _, c := range res.SweepCovered {
		if c == ordinaryType {
			return
		}
	}
	t.Errorf("%s is not in Result.SweepCovered, but the sweep really did search it - the list route delivers this type's tags and the objects are simply untagged:\n%s", ordinaryType, res)
}

// TestNativeSweepSeparatesAnIndexThatFailedFromOneThatCannotAnswer is the
// design question issue #1136 asks, settled in a test.
//
// Two different facts sit behind "the marker could not be read":
//
//   - PERMANENT. The tag index does not index this service at all, so the
//     join could never have answered, on any account, in any run. That is
//     the same operator fact [SweepGapMarkerUnreadable] already carries on
//     the Cloud Control leg - where the CFN schema has no Tags property -
//     and the same remedy: nothing in this run will ever propose the
//     destroy; do it out of band.
//   - TRANSIENT. The tag index was asked and could not answer: no Tagging
//     client configured this run, or its one GetResources call failed
//     (throttled, denied, a network blip). Retrying may well work, and
//     telling an operator "this can never be read" when the truth is "the
//     call failed once" sends them to destroy a resource by hand that the
//     next plan would have proposed itself.
//
// So the split is permanent vs transient, NOT Cloud-Control vs native: the
// leg is an implementation detail and the remedy is not. The permanent half
// reuses the existing reason, which is what makes the two legs agree; the
// transient half gets [SweepGapTagIndexUnavailable] of its own.
//
// Note what is deliberately NOT given its own reason: issue #1046's lag. A
// lagging index answers its call successfully and simply does not hold the
// resource yet, which is joinNone - indistinguishable, for a SERVED type,
// from "this object is genuinely not ours", which is the overwhelming
// majority of every sweep. There is nothing in the data to report it from,
// so it is not reported.
func TestNativeSweepSeparatesAnIndexThatFailedFromOneThatCannotAnswer(t *testing.T) {
	const (
		unservedType = "aws_iam_role"
		liveName     = "estate-team-role"
		deletedAddr  = unservedType + ".removed"
	)

	build := func() *fakeCloud {
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		cloud.listable(unservedType)
		cloud.own(unservedType, liveName, deletedAddr)
		stripTags(t, cloud, unservedType, liveName)
		return cloud
	}

	gapFor := func(t *testing.T, res *Result) SweepGap {
		t.Helper()
		for _, g := range res.SweepGaps {
			if g.TypeName == unservedType {
				return g
			}
		}
		t.Fatalf("no sweep gap for %s at all:\n%s", unservedType, res)
		return SweepGap{}
	}

	t.Run("no Tagging client at all is transient, not permanent", func(t *testing.T) {
		// req.Tagging nil - [newMarkerIndex] returns a nil index and every
		// join answers joinUnavailable. Nothing was learned about the
		// service, so nothing may be claimed about it forever.
		res, diags := discoverFixture(t, build(), Request{
			Sweep:      true,
			SweepTypes: []string{unservedType},
		})
		assertNoErrors(t, diags)

		g := gapFor(t, res)
		if g.Reason != SweepGapTagIndexUnavailable {
			t.Fatalf("the gap is %q, want %q. With no tag index this run, \"the marker can never be read\" is a claim the run has no evidence for - and it sends the operator to destroy by hand something a configured run would propose itself.\ngap: %s", g.Reason, SweepGapTagIndexUnavailable, g)
		}
		t.Logf("transient, quoted verbatim: %s", g.Detail)
	})

	t.Run("an index that answers and cannot hold this service is permanent", func(t *testing.T) {
		tagSrv := &taggingServer{}
		tagServer := tagSrv.start(t)
		defer tagServer.Close()

		res, diags := discoverFixture(t, build(), Request{
			Sweep:        true,
			TaggingSweep: true,
			Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagServer.URL}),
			SweepTypes:   []string{unservedType},
		})
		assertNoErrors(t, diags)

		g := gapFor(t, res)
		if g.Reason != SweepGapMarkerUnreadable {
			t.Fatalf("the gap is %q, want %q - the index answered and this service is one it structurally never indexes, so a retry changes nothing.\ngap: %s", g.Reason, SweepGapMarkerUnreadable, g)
		}
		t.Logf("permanent, quoted verbatim: %s", g.Detail)
	})
}

// TestNativeSweepStaysQuietWhenTheIndexAnswersForOneObjectOfTheType is the
// third control, and it is a defect found by auditing this fix's own diff
// rather than by any test that existed before it.
//
// The first draft refuted the gap only on tags read off a listed object.
// But the tag-index join is the OTHER marker route, and an index that
// answers for one object of a type demonstrably serves the type - which
// makes joinNone for a sibling a real answer about that sibling, not the
// absence of one. Without this refutation, a listing where the index bound
// one role and had nothing for a second filed a gap saying no marker could
// be read off any of them, on the same run that read one.
//
// Reachable, not hypothetical: floci served IAM through GetResources before
// lex00/floci#202, and any endpoint that does index the service puts a
// [taggingAPIUnservedType] straight into this shape.
func TestNativeSweepStaysQuietWhenTheIndexAnswersForOneObjectOfTheType(t *testing.T) {
	const (
		unservedType = "aws_iam_role"
		bound        = "estate-bound-role"
		unbound      = "estate-unbound-role"
	)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(unservedType)
	cloud.own(unservedType, bound, unservedType+".removed")
	cloud.own(unservedType, unbound, unservedType+".also_removed")
	// iam:ListRoles returns no tags for either.
	stripTags(t, cloud, unservedType, bound)
	stripTags(t, cloud, unservedType, unbound)

	// An index that DOES serve this service, and holds exactly one of the
	// two roles - the lag shape (#1046), not the unserved shape.
	tagSrv := &taggingServer{}
	markedARN(tagSrv, "arn:aws:iam::000000000000:role/"+bound, unservedType+".removed")
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	res, diags := discoverFixture(t, cloud, Request{
		Sweep:        true,
		TaggingSweep: true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagServer.URL}),
		SweepTypes:   []string{unservedType},
	})
	assertNoErrors(t, diags)

	scan, ok := res.ScanFor(unservedType)
	if !ok || scan.Joined != 1 {
		t.Fatalf("the %s scan is %+v (found=%v), want Joined=1 - the index must have answered for exactly one of the two roles, or this control is not the mixed shape at all:\n%s", unservedType, scan, ok, res)
	}
	for _, g := range res.SweepGaps {
		if g.TypeName == unservedType {
			t.Fatalf("a %s gap was filed saying no marker could be read off any object of the type, on a run that joined one from the index. The index serves this type; joinNone for the sibling is an answer about the sibling.\ngap: %s\n%s", unservedType, g, res)
		}
	}
	if _, found := removalsByAddr(res)[unservedType+".removed"]; !found {
		t.Errorf("the joined role was not proposed for removal, so this control is not proving the index answered:\n%s", res)
	}
	for _, c := range res.SweepCovered {
		if c == unservedType {
			return
		}
	}
	t.Errorf("%s is not in Result.SweepCovered although a marker really was read off one of its objects:\n%s", unservedType, res)
}
