// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"errors"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// GitHub issue #1125: the native leg's half of #1131's per-service tag-read.
//
// Every fixture here is
// [TestNativeSweepSaysSoWhenNoLegCanReadAListedObjectsMarker]'s, unchanged -
// an aws_iam_role the provider lists, whose list call returns no tags, and a
// Resource Groups Tagging API that answers successfully with nothing, which
// is what real AWS does for iam:role in every region (#1134) and what the
// pinned emulator does for all of IAM (#1152). The only thing added is a
// service tag reader, so the difference under test is the leg and nothing
// else.
//
// The type matters. aws_iam_instance_profile has no native list resource and
// reaches [scanTypeCloudControl], which has called [serviceTagRead] since PR
// #1161; aws_iam_role has one and reaches [scanType], which did not. Same
// object shape, same unreadable marker, opposite outcomes - that asymmetry
// is #1125, and corpus-ec2-instance-complete's day2_remove measured it from
// the other end, destroying the profile and leaving the role standing.

// nativeRoleFixture builds the shared shape: one live, marked, undeclared
// role of a type the tag index can never speak for, listed natively with its
// tags stripped.
func nativeRoleFixture(t *testing.T, typeName, liveName, addr string) *fakeCloud {
	t.Helper()
	if !taggingAPIUnservedTypeInRegion("", typeName) {
		t.Fatalf("%s is no longer a type taggingAPITypeCoverage/taggingAPIServiceCoverage put out of the tag index's reach from an unset region, so this fixture no longer exercises the join that cannot answer", typeName)
	}
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(typeName)
	cloud.own(typeName, liveName, addr)
	stripTags(t, cloud, typeName, liveName)
	return cloud
}

// nativeServiceTagReadRequest is the request the subtests share.
func nativeServiceTagReadRequest(t *testing.T, typeName, tagServerURL string, reader *fakeServiceTags) Request {
	t.Helper()
	return Request{
		Sweep:        true,
		TaggingSweep: true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagServerURL}),
		ServiceTags:  reader,
		SweepTypes:   []string{typeName},
	}
}

// TestNativeServiceTagReadRecoversTheRoleDestroy is the verdict line: the
// destroy #1125 reported missing is proposed, because iam:ListRoleTags
// answered where the list call and the tag index both could not.
func TestNativeServiceTagReadRecoversTheRoleDestroy(t *testing.T) {
	const (
		typeName    = "aws_iam_role"
		liveName    = "estate-team-role"
		deletedAddr = typeName + ".removed"
	)

	cloud := nativeRoleFixture(t, typeName, liveName, deletedAddr)
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		tags: map[string]map[string]string{
			liveName: {TagEstate: estateName, TagAddress: deletedAddr},
		},
	}
	res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, reader))
	assertNoErrors(t, diags)

	// Premise: the native leg, not Cloud Control, is what enumerated it.
	// Without this the whole test could pass on the leg #1161 already fixed.
	scan, ok := res.ScanFor(typeName)
	if !ok || scan.Source != SourceProvider || scan.Listed != 1 {
		t.Fatalf("the %s scan is %+v (found=%v), want Source=%s Listed=1 - this fixture is not the native-enumeration shape at all:\n%s",
			typeName, scan, ok, SourceProvider, res)
	}
	if tagSrv.calls == 0 {
		t.Fatalf("the Resource Groups Tagging API fake was never called, so no tag-index join was attempted and the leg under test was never reached")
	}

	if reader.calls != 1 {
		t.Fatalf("the service tag reader was called %d time(s) for %v, want exactly one - one object of one type was listed", reader.calls, reader.askedFor)
	}
	if len(reader.askedFor) != 1 || reader.askedFor[0] != typeName+" "+liveName {
		t.Fatalf("the reader was asked %v, want [%q] - the identifier a role's tag read needs is its name", reader.askedFor, typeName+" "+liveName)
	}

	removals := removalsByAddr(res)
	if _, ok := removals[deletedAddr]; !ok {
		t.Fatalf("no destroy was proposed for %s, which is the orphan #1125 reported. Removals: %v\n%s", deletedAddr, removals, res)
	}

	// The gap must be gone, not merely outvoted by a removal: the run read
	// this type's markers, so asserting it could not is now false.
	if reason := gapReasonFor(res, typeName); reason != "" {
		t.Errorf("a sweep gap %q is still filed for %s on a run that read the marker off its one listed object\n%s", reason, typeName, res)
	}
	if !sweepCovers(res, typeName) {
		t.Errorf("%s is missing from Result.SweepCovered although the sweep listed it and read its marker\n%s", typeName, res)
	}
}

// TestNativeServiceTagReadKeepsTheGapWhenTheReadFails is the safety
// direction, and the one that would make this change dangerous if it were
// wrong. A failed tag read establishes nothing, so the run must keep saying
// so - never turn the failure into "this estate owns no roles".
func TestNativeServiceTagReadKeepsTheGapWhenTheReadFails(t *testing.T) {
	const (
		typeName    = "aws_iam_role"
		liveName    = "estate-team-role"
		deletedAddr = typeName + ".removed"
	)

	cloud := nativeRoleFixture(t, typeName, liveName, deletedAddr)
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		err:    errors.New("AccessDenied: User is not authorized to perform iam:ListRoleTags"),
	}
	res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, reader))
	assertNoErrors(t, diags)

	if reader.calls != 1 {
		t.Fatalf("the service tag reader was called %d time(s), want exactly one - the fixture is not reaching the leg", reader.calls)
	}
	if len(removalsByAddr(res)) != 0 {
		t.Fatalf("a destroy was proposed although no route read the object's marker:\n%s", res)
	}
	if reason := gapReasonFor(res, typeName); reason != SweepGapMarkerUnreadable {
		t.Fatalf("the sweep gap for %s is %q, want %q - a tag read that failed leaves the marker exactly as unread as it was before the leg existed\n%s",
			typeName, reason, SweepGapMarkerUnreadable, res)
	}
	if sweepCovers(res, typeName) {
		t.Errorf("%s is reported as covered on the same run that files a gap saying the search established nothing\n%s", typeName, res)
	}
}

// TestNativeServiceTagReadAnswersEmptyForSomebodyElsesRole is the other half
// of the same safety question, from the opposite side. A successful read
// that returns no tofu-estate is an ANSWER: the object is not ours. So no
// destroy, and - this is the part that is easy to get wrong - no gap either,
// because the run demonstrably can read this type's markers and simply found
// none. Filing a gap here would tell an operator to go and look in the
// console at an account that is behaving perfectly normally.
func TestNativeServiceTagReadAnswersEmptyForSomebodyElsesRole(t *testing.T) {
	const (
		typeName = "aws_iam_role"
		liveName = "someone-elses-role"
	)

	cloud := nativeRoleFixture(t, typeName, liveName, typeName+".removed")
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	// Routed, answering, and holding nothing of ours.
	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		tags:   map[string]map[string]string{liveName: {"Name": "unrelated"}},
	}
	res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, reader))
	assertNoErrors(t, diags)

	if reader.calls != 1 {
		t.Fatalf("the service tag reader was called %d time(s), want exactly one", reader.calls)
	}
	if len(removalsByAddr(res)) != 0 {
		t.Fatalf("a destroy was proposed for an object whose own tag read says it carries no tofu-estate:\n%s", res)
	}
	if reason := gapReasonFor(res, typeName); reason != "" {
		t.Errorf("a sweep gap %q is filed for %s although every listed object of it was tag-read successfully - an account with no roles of ours is an ordinary account, not a blind spot\n%s", reason, typeName, res)
	}
}

// TestNativeServiceTagReadStaysOffWhenTheIndexServesTheType is clause 3 of
// [serviceTagRead]'s gate, exercised on the native leg: an object the index
// answered for costs no ListRoleTags. On a target whose GetResources does
// index the type - real AWS us-east-1 for iam:policy and
// iam:instance-profile (#1134), and every floci pin before lex00/floci#202 -
// the join supplies the marker from the one call the sweep already paid for.
//
// GitHub issue #1162 made the clause per object, so the fixture carries a
// sibling the index does NOT hold, and the assertion is that the reader was
// asked about the sibling and only the sibling. "Stays off" is about the
// indexed role; nativeperobjectgate_test.go holds what happens to the other.
func TestNativeServiceTagReadStaysOffWhenTheIndexServesTheType(t *testing.T) {
	const (
		typeName    = "aws_iam_role"
		liveName    = "estate-team-role"
		deletedAddr = typeName + ".removed"
	)

	cloud := nativeRoleFixture(t, typeName, liveName, deletedAddr)

	// A tag index that DOES hold the role, which is what makes the leg
	// unnecessary here. The join supplies the marker on its own.
	arn := "arn:aws:iam::123456789012:role/" + liveName
	tagSrv := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{
			arn: {TagEstate: estateName, TagAddress: deletedAddr},
		},
	}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	// The sibling: listed, tagless, absent from the index, somebody else's.
	const siblingName = "someone-elses-role"
	cloud.own(typeName, siblingName, typeName+".theirs")
	stripTags(t, cloud, typeName, siblingName)

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		tags: map[string]map[string]string{
			liveName:    {TagEstate: estateName, TagAddress: deletedAddr},
			siblingName: {TagEstate: "some-other-estate", TagAddress: typeName + ".theirs"},
		},
	}
	res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, reader))
	assertNoErrors(t, diags)

	if got := sortedRemovalAddrs(res); len(got) != 1 || got[0] != deletedAddr {
		t.Fatalf("destroys proposed for %v, want [%s] only - the index-served role is this estate's orphan and the sibling's tag read names another estate:\n%s", got, deletedAddr, res)
	}
	for _, asked := range reader.askedFor {
		if asked == typeName+" "+liveName {
			t.Errorf("the service tag reader was asked about %s although the estate's tag index had already answered for it - the join's one GetResources became a ListRoleTags as well", liveName)
		}
	}
	wantAsked := typeName + " " + siblingName
	if reader.calls != 1 || len(reader.askedFor) != 1 || reader.askedFor[0] != wantAsked {
		t.Errorf("the service tag reader was asked %v (%d call(s)), want exactly [%q]", reader.askedFor, reader.calls, wantAsked)
	}
}
