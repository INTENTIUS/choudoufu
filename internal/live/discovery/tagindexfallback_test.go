// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"errors"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// GitHub issue #1321: in us-east-1 a served-but-empty tag index for a
// region-restricted type is a gap, and it should be an answer.
//
// The routing is what makes this region-only. [taggingAPITypeCoverage] says
// GetResources holds aws_iam_instance_profile in us-east-1 and nowhere else
// (#1134: 500 returned there at scale 50, 0 in us-east-2), so
// [taggingAPIUnservedTypeInRegion] is false there and [arnJoinReaches] sends
// the type to the tagging leg. That leg's one estate-wide GetResources call
// is then the ONLY enumeration the type gets. When the index answers holding
// none of this estate's - #1046 measured it holding 104 of 1,655 stamped
// objects about twenty-one minutes after migrate had verified every one -
// #1318 says so out loud ([SweepGapTagIndexHeldNothing]) and stops.
//
// Every other region already does the right thing with the identical
// fixture: there the coverage row says the index does not serve the type,
// [arnJoinReaches] routes it to the native per-type leg, Cloud Control
// enumerates it, and #1131's per-service tag read supplies the marker Cloud
// Control cannot carry - servicetagread_test.go's own fixtures, which set no
// Region and so take exactly that path. So the repair is a routing fallback
// and not a new leg: take the native route as WELL, when the index this type
// was routed to answered with nothing.
//
// What the fallback must not do is make the gap disappear. #1318 made it
// loud deliberately and #881 requires it: a fallback that finds the object
// turns the gap into an answer, and a fallback that fails leaves the loud
// gap exactly where it was.

const (
	fbType     = "aws_iam_instance_profile"
	fbCFNType  = "AWS::IAM::InstanceProfile"
	fbLiveName = "estate-team-0002-profile"
	fbAddr     = fbType + ".gone"
)

// fbRequest is the shared request: sweeping in us-east-1, a tag index that
// answers with nothing, and Cloud Control able to enumerate the type.
// ccListable false is the "no native route" arm, where the fallback must not
// even be attempted.
func fbRequest(t *testing.T, tagServerURL, ccServerURL string, reader *fakeServiceTags, ccListable bool) Request {
	t.Helper()
	return Request{
		Sweep:        true,
		TaggingSweep: true,
		Region:       laggedRegion,
		SweepTypes:   []string{fbType},
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagServerURL}),
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: ccServerURL}),
		ServiceTags:  reader,
		Roster: ccRoster(t,
			map[string]string{fbType: fbCFNType},
			map[string]bool{fbCFNType: ccListable},
			map[string]bool{fbCFNType: false},
		),
	}
}

// fbPremises states what has to hold for any of these fixtures to be
// exercising #1321 rather than something else. Every one of them is a fact
// about the shipped tables, not about the fixture.
func fbPremises(t *testing.T) {
	t.Helper()
	if taggingAPIUnservedTypeInRegion(laggedRegion, fbType) {
		t.Fatalf("%s is recorded as unserved from %s, so arnJoinReaches routes it to the NATIVE leg and the "+
			"tagging leg #1321 is about is never reached", fbType, laggedRegion)
	}
	if !taggingAPIRestrictedType(fbType) {
		t.Fatalf("%s has no taggingAPITypeCoverage row any more, so tagIndexHeldNothingGap's region term "+
			"declines and this fixture exercises a different arm", fbType)
	}
	if !arnJoinCovers(fbCFNType) {
		t.Fatalf("%s is no longer joined by arnJoinTable, so the type never reaches the tagging universe", fbCFNType)
	}
}

// TestServedButEmptyTagIndexFallsBackAndRecoversTheDestroy is #1321's
// decisive arm, and it is #881's own shape: delete a declared, marked
// aws_iam_instance_profile's block in us-east-1 with an index that does not
// hold it, and get the destroy rather than a gap.
func TestServedButEmptyTagIndexFallsBackAndRecoversTheDestroy(t *testing.T) {
	fbPremises(t)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(fbType)
	cloud.unlistable(fbType)

	// The index answers, successfully, holding none of this estate's
	// profiles. That is the lag's shape on the wire; srv.calls is asserted
	// so a pass cannot come from a call that never happened.
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	cc := ccInstanceProfileFixture(t, fbCFNType, fbLiveName)
	ccServer := cc.start()
	defer ccServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{fbType: true},
		tags: map[string]map[string]string{
			fbLiveName: {TagEstate: estateName, TagAddress: fbAddr},
		},
	}

	res, diags := discoverFixture(t, cloud, fbRequest(t, tagServer.URL, ccServer.URL, reader, true))
	assertNoErrors(t, diags)

	if tagSrv.calls != 1 {
		t.Fatalf("GetResources was called %d time(s), want exactly 1 - the index has to have ANSWERED for its "+
			"empty answer to be the thing the fallback fires on", tagSrv.calls)
	}
	if len(cc.calls) == 0 {
		t.Fatalf("Cloud Control was never called, so no fallback enumeration happened at all and the type is "+
			"still reaching nothing but the index:\n%s", res)
	}
	if reader.calls != 1 {
		t.Fatalf("the service tag reader was called %d time(s) (asked for %v), want exactly 1 - one object of "+
			"one type was enumerated by the fallback", reader.calls, reader.askedFor)
	}

	rm := removalsByAddr(res)
	o, ok := rm[fbAddr]
	if !ok {
		t.Fatalf("no destroy is proposed for %s. In %s the tag index is this type's only enumeration and it "+
			"answered with nothing, so the live, marked object its deleted block owned goes unproposed - which "+
			"is #881's verdict line and what #1321 is for.\ngap=%q covered=%v\n%s",
			fbAddr, laggedRegion, gapReasonFor(res, fbType), sweepCovers(res, fbType), res)
	}
	if o.ImportID != fbLiveName {
		t.Errorf("the removal's ImportID is %q, want %q", o.ImportID, fbLiveName)
	}

	// The gap has to be GONE, not outvoted. A run that enumerated the type
	// and read its marker has no honest way to also say it could not.
	if reason := gapReasonFor(res, fbType); reason != "" {
		t.Errorf("a sweep gap %q is still filed for %s on a run that enumerated it and read the marker off its "+
			"one object\n%s", reason, fbType, res)
	}
	if spokenAbout(diags, fbType) {
		t.Errorf("an incomplete-sweep diagnostic still names %s: %s\nThese are the two warnings per plan #1320 "+
			"adds in %s, and retiring them is #1321's own acceptance.", fbType, renderDiags(diags), laggedRegion)
	}
	if !sweepCovers(res, fbType) {
		t.Errorf("%s is missing from Result.SweepCovered (%v) although the fallback listed it and read its "+
			"marker\n%s", fbType, res.SweepCovered, res)
	}
}

// TestServedButEmptyTagIndexFallbackFailureKeepsTheLoudGap is the direction
// that would make this change dangerous if it were wrong. The fallback runs,
// enumerates the object, and cannot read its marker - so it has established
// nothing, and #1318's loud gap must be standing afterwards exactly as it
// was.
func TestServedButEmptyTagIndexFallbackFailureKeepsTheLoudGap(t *testing.T) {
	fbPremises(t)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(fbType)
	cloud.unlistable(fbType)

	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	cc := ccInstanceProfileFixture(t, fbCFNType, fbLiveName)
	ccServer := cc.start()
	defer ccServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{fbType: true},
		err:    errors.New("AccessDenied: User is not authorized to perform iam:ListInstanceProfileTags"),
	}

	res, diags := discoverFixture(t, cloud, fbRequest(t, tagServer.URL, ccServer.URL, reader, true))
	assertNoErrors(t, diags)

	if reader.calls != 1 {
		t.Fatalf("the service tag reader was called %d time(s), want exactly 1 - the fallback has to have "+
			"reached the leg and had it fail, or this test is not the failure arm", reader.calls)
	}
	if _, ok := removalsByAddr(res)[fbAddr]; ok {
		t.Fatalf("a destroy was proposed for %s on a run that could read no marker off any object of the type. "+
			"Ownership is read, never inferred (live/MARKERS.md).\n%s", fbAddr, res)
	}

	var reasons []SweepGapReason
	for _, g := range res.SweepGaps {
		if g.TypeName == fbType {
			reasons = append(reasons, g.Reason)
		}
	}
	if !hasGapReason(reasons, SweepGapTagIndexHeldNothing) {
		t.Errorf("%s has gaps %v and none of them is %q. #1318 made the served-but-empty index loud on purpose "+
			"and #881 requires it; a fallback that fails must leave that gap exactly where it was.",
			fbType, reasons, SweepGapTagIndexHeldNothing)
	}
	if !hasGapReason(reasons, SweepGapMarkerUnreadable) {
		t.Errorf("%s has gaps %v and none of them is %q - the fallback enumerated the object and no leg could "+
			"read its marker, which is #1129's own refusal and has to be filed by the leg that hit it.",
			fbType, reasons, SweepGapMarkerUnreadable)
	}
	if !spokenAbout(diags, fbType) {
		t.Errorf("nothing reached the operator about %s: %s\nA run that looked and established nothing saying "+
			"nothing is the whole of #881.", fbType, renderDiags(diags))
	}
	if sweepCovers(res, fbType) {
		t.Errorf("%s is in Result.SweepCovered (%v) although no marker could be read off the one object the "+
			"fallback listed - that claims the sweep established this estate owns no undeclared %s",
			fbType, res.SweepCovered, fbType)
	}
}

// TestServedButEmptyTagIndexWithNoNativeRouteCostsNothing is the cost bound,
// and it is the half a fallback is easiest to get wrong. When no native leg
// can enumerate the type, there is nothing to fall back TO: the run must
// make no extra call at all and file exactly the gap #1318 shipped.
func TestServedButEmptyTagIndexWithNoNativeRouteCostsNothing(t *testing.T) {
	fbPremises(t)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(fbType)
	cloud.unlistable(fbType)

	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	// Registry handlers.list false, so [cloudControlSource] finds no
	// enumeration source and [nativeSweepReaches] is false.
	cc := ccInstanceProfileFixture(t, fbCFNType, fbLiveName)
	ccServer := cc.start()
	defer ccServer.Close()

	reader := &fakeServiceTags{routes: map[string]bool{fbType: true}}

	res, diags := discoverFixture(t, cloud, fbRequest(t, tagServer.URL, ccServer.URL, reader, false))
	assertNoErrors(t, diags)

	if len(cc.calls) != 0 {
		t.Errorf("Cloud Control was called %v for a type the registry gives no list handler. The fallback pays a "+
			"listing plus one tag read per object; it must not be attempted where it cannot work.", cc.calls)
	}
	if reader.calls != 0 {
		t.Errorf("the service tag reader was called %d time(s) on a run that enumerated nothing", reader.calls)
	}
	if got := gapReasonFor(res, fbType); got != SweepGapTagIndexHeldNothing {
		t.Errorf("the sweep gap for %s is %q, want %q - with no route to fall back to, #1318's verdict is still "+
			"the whole answer\n%s", fbType, got, SweepGapTagIndexHeldNothing, res)
	}
	if !spokenAbout(diags, fbType) {
		t.Errorf("nothing reached the operator about %s: %s", fbType, renderDiags(diags))
	}
	if sweepCovers(res, fbType) {
		t.Errorf("%s is in Result.SweepCovered (%v) although nothing enumerated it", fbType, res.SweepCovered)
	}
}

// TestServedButEmptyTagIndexDoesNotRelistADeclaredType is the other cost
// bound, and the one that is easy to miss because the type reaches this arm
// by a route that looks the same from inside it.
//
// [sweepTypes] adds a [taggingAPIRestrictedType] back into the sweep universe
// even when the configuration declares needs-discovery instances of it, so a
// DECLARED aws_iam_policy reaches the tagging leg too. But the config-driven
// loop already listed that type in full, at ScopeAll, before the sweep
// started - res.Orphans is appended there with no sweep gate - so the
// fallback would pay the whole listing again, and for aws_iam_policy that is
// #1039's GetPolicyVersion once per policy in the account, to find nothing
// the first pass did not.
func TestServedButEmptyTagIndexDoesNotRelistADeclaredType(t *testing.T) {
	const (
		declType    = "aws_iam_policy"
		declCFNType = "AWS::IAM::Policy"
		liveName    = "arn:aws:iam::000000000000:policy/declared"
	)
	if taggingAPIUnservedTypeInRegion(laggedRegion, declType) {
		t.Fatalf("%s is recorded as unserved from %s, so it never reaches the tagging leg from there", declType, laggedRegion)
	}

	// A resource schema with a tags argument and no list resource, so
	// [typeTaggable] is true (which is what [tagIndexHeldNothingGap] turns
	// on) and both passes enumerate through Cloud Control. Without the
	// resource schema the type files the quiet SweepGapNotTaggable instead
	// and never reaches the arm at all - which is how the first draft of
	// this test passed with the guard under test removed.
	cloud := newFakeCloud()
	cloud.listable(declType)
	cloud.unlistable(declType)

	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	cc := newCCServer(t)
	cc.listResources[declCFNType] = []ccResource{{
		identifier: liveName,
		properties: tagsProps(estateName, declType+".x"),
	}}
	ccSrv := cc.start()
	defer ccSrv.Close()

	res, diags := Discover(context.Background(), Request{
		Estate:       estateName,
		Config:       ccConfig(declType),
		Resolutions:  []identity.Resolution{{Addr: mustAddr(t, declType+".x"), Class: identity.ClassNeedsDiscovery}},
		Provider:     cloud,
		Sweep:        true,
		TaggingSweep: true,
		Region:       laggedRegion,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagServer.URL}),
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: ccSrv.URL}),
		Roster: ccRoster(t,
			map[string]string{declType: declCFNType},
			map[string]bool{declCFNType: true},
			map[string]bool{declCFNType: false},
		),
	})
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	var lists int
	for _, c := range cc.calls {
		if c == "ListResources:"+declCFNType {
			lists++
		}
	}
	if lists != 1 {
		t.Errorf("Cloud Control listed %s %d times (%v), want exactly 1 - the config-driven pass already listed "+
			"the whole account for this type before the sweep began, so the fallback must not list it again\n%s",
			declCFNType, lists, cc.calls, res)
	}
	if _, ok := res.BindingFor(mustAddr(t, declType+".x")); !ok {
		t.Errorf("%s.x did not bind, so the config-driven pass this test says already covered the type did not "+
			"actually cover it and the premise is wrong\n%s", declType, res)
	}
	// The premise, stated: the type really did reach the arm the fallback
	// hangs off. A fixture whose provider schema carries no tags files the
	// quiet SweepGapNotTaggable instead, never calls the fallback, and would
	// pass this test with the guard it is about deleted.
	if got := gapReasonFor(res, declType); got != SweepGapTagIndexHeldNothing {
		t.Fatalf("the sweep gap for %s is %q, want %q - this fixture is not reaching the arm #1321's fallback "+
			"hangs off, so the guard under test is never consulted", declType, got, SweepGapTagIndexHeldNothing)
	}
}

func hasGapReason(reasons []SweepGapReason, want SweepGapReason) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}
