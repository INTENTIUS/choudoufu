// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"errors"
	"sort"
	"testing"
)

// GitHub issue #1162: the service tag-read leg's gate is per OBJECT.
//
// Until this issue the leg's third gate clause was [markerIndex.servesType]:
// the estate's tag index holding ANY marked object of the type switched the
// leg off for EVERY object of the type. That is all-or-nothing, and the
// index is not. #1046 measured the Resource Groups Tagging API lagging the
// marker writes at 3,705 resources, which is "some objects of a type
// indexed, some not", and on that shape two things went wrong at once:
//
//   - the unindexed object got joinNone and no tag read, so its marker was
//     never seen and its destroy never proposed;
//   - its indexed sibling's joinBound set markerReadWorked, which is
//     [sweepMarkerReadGap]'s refutation, so no MARKER_UNREADABLE gap was
//     filed either.
//
// A missed destroy with nothing said about it. Every fixture below is that
// shape: two live, marked, undeclared objects of one routed type, listed
// natively with their tags stripped, and a tag index that holds exactly one
// of them.

const (
	perObjectIndexedName   = "estate-indexed"
	perObjectUnindexedName = "estate-lagging"
)

// perObjectFixture builds the partial-coverage shape for typeName and
// returns the fake cloud plus the tagging fake, which holds only the indexed
// object. arnOf renders the ARN the index would carry for a live name.
func perObjectFixture(t *testing.T, typeName string, arnOf func(string) string, indexedAddr, unindexedAddr string) (*fakeCloud, *taggingServer) {
	t.Helper()
	cloud := nativeRoleFixture(t, typeName, perObjectIndexedName, indexedAddr)
	cloud.own(typeName, perObjectUnindexedName, unindexedAddr)
	stripTags(t, cloud, typeName, perObjectUnindexedName)

	tagSrv := &taggingServer{}
	markedARN(tagSrv, arnOf(perObjectIndexedName), indexedAddr)
	return cloud, tagSrv
}

func roleARN(name string) string { return "arn:aws:iam::123456789012:role/" + name }

func sortedRemovalAddrs(res *Result) []string {
	var out []string
	for addr := range removalsByAddr(res) {
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}

// TestPerObjectGateReadsTheObjectTheIndexDidNotAnswerFor is the silent miss
// and its repair. How the per-object decision is made: the leg runs for an
// object exactly when that object's own listing and that object's own index
// join both produced no tofu-estate. The indexed sibling's join is bound, so
// it never reaches the leg and costs no call; the lagging one's join is
// none, so it is read.
func TestPerObjectGateReadsTheObjectTheIndexDidNotAnswerFor(t *testing.T) {
	const (
		typeName      = "aws_iam_role"
		indexedAddr   = typeName + ".removed_indexed"
		unindexedAddr = typeName + ".removed_lagging"
	)

	cloud, tagSrv := perObjectFixture(t, typeName, roleARN, indexedAddr, unindexedAddr)
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		tags: map[string]map[string]string{
			perObjectIndexedName:   {TagEstate: estateName, TagAddress: indexedAddr},
			perObjectUnindexedName: {TagEstate: estateName, TagAddress: unindexedAddr},
		},
	}
	res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, reader))
	assertNoErrors(t, diags)

	scan, ok := res.ScanFor(typeName)
	if !ok || scan.Source != SourceProvider || scan.Listed != 2 {
		t.Fatalf("the %s scan is %+v (found=%v), want Source=%s Listed=2 - this fixture is not the native partial-coverage shape:\n%s",
			typeName, scan, ok, SourceProvider, res)
	}

	got := sortedRemovalAddrs(res)
	want := []string{indexedAddr, unindexedAddr}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("destroys proposed for %v, want %v - the role the tag index lags on carries this estate's marker and is declared nowhere", got, want)
	}
	if removalsByAddr(res)[unindexedAddr].ImportID != perObjectUnindexedName {
		t.Errorf("the lagging role's removal names live object %q, want %q", removalsByAddr(res)[unindexedAddr].ImportID, perObjectUnindexedName)
	}

	// The call count is the gate, asserted: one read, for the object the
	// index did not answer for, and none for the one it did.
	wantAsked := typeName + " " + perObjectUnindexedName
	if reader.calls != 1 || len(reader.askedFor) != 1 || reader.askedFor[0] != wantAsked {
		t.Errorf("the service tag reader was asked %v (%d call(s)), want exactly [%q] - the index answered for %s, so reading it again is a call that buys nothing",
			reader.askedFor, reader.calls, wantAsked, perObjectIndexedName)
	}
	if scan.ServiceTagReads != 1 {
		t.Errorf("TypeScan.ServiceTagReads is %d, want 1 - the scan row is where the leg's cost is published", scan.ServiceTagReads)
	}
	if reason := gapReasonFor(res, typeName); reason != "" {
		t.Errorf("a sweep gap %q is filed for %s although every listed object's marker was read, one off the index and one off the service\n%s", reason, typeName, res)
	}
}

// TestPerObjectGateCoversPolicies is the same shape for aws_iam_policy, whose
// tag-read identifier is an ARN rather than a name. It is the type #1134
// measured real AWS indexing in us-east-1, so it is the one where "the index
// serves the type and lags on an object" is an ordinary day.
func TestPerObjectGateCoversPolicies(t *testing.T) {
	const (
		typeName      = "aws_iam_policy"
		indexedAddr   = typeName + ".removed_indexed"
		unindexedAddr = typeName + ".removed_lagging"
	)

	// The fake cloud's live id is what importIdentity hands both the join
	// and the leg, so for a policy the fixture's ids are the ARNs.
	policyARN := func(name string) string { return "arn:aws:iam::123456789012:policy/" + name }
	indexedID, unindexedID := policyARN(perObjectIndexedName), policyARN(perObjectUnindexedName)

	cloud := nativeRoleFixture(t, typeName, indexedID, indexedAddr)
	cloud.own(typeName, unindexedID, unindexedAddr)
	stripTags(t, cloud, typeName, unindexedID)
	tagSrv := &taggingServer{}
	markedARN(tagSrv, indexedID, indexedAddr)
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		tags: map[string]map[string]string{
			unindexedID: {TagEstate: estateName, TagAddress: unindexedAddr},
		},
	}
	res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, reader))
	assertNoErrors(t, diags)

	got := sortedRemovalAddrs(res)
	if len(got) != 2 || got[0] != indexedAddr || got[1] != unindexedAddr {
		t.Errorf("destroys proposed for %v, want [%s %s]", got, indexedAddr, unindexedAddr)
	}
	if removalsByAddr(res)[unindexedAddr].ImportID != unindexedID {
		t.Errorf("the lagging policy's removal names live object %q, want %q", removalsByAddr(res)[unindexedAddr].ImportID, unindexedID)
	}
	wantAsked := typeName + " " + unindexedID
	if reader.calls != 1 || len(reader.askedFor) != 1 || reader.askedFor[0] != wantAsked {
		t.Errorf("the service tag reader was asked %v (%d call(s)), want exactly [%q]", reader.askedFor, reader.calls, wantAsked)
	}
	if reason := gapReasonFor(res, typeName); reason != "" {
		t.Errorf("a sweep gap %q is filed for %s although every listed object's marker was read\n%s", reason, typeName, res)
	}
}

// TestPerObjectGateLeavesAnUnmarkedSiblingAlone: the object the index did
// not answer for is read, the read succeeds, and it carries no tofu-estate.
// That is an ordinary unowned role - no destroy, and no gap, because nothing
// about it went unread.
func TestPerObjectGateLeavesAnUnmarkedSiblingAlone(t *testing.T) {
	const (
		typeName      = "aws_iam_role"
		indexedAddr   = typeName + ".removed_indexed"
		unindexedAddr = typeName + ".never_ours"
	)

	cloud, tagSrv := perObjectFixture(t, typeName, roleARN, indexedAddr, unindexedAddr)
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		tags:   map[string]map[string]string{perObjectUnindexedName: {"Name": "unrelated"}},
	}
	res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, reader))
	assertNoErrors(t, diags)

	if reader.calls != 1 {
		t.Fatalf("the service tag reader was called %d time(s) (%v), want exactly one - the fixture is not reaching the leg", reader.calls, reader.askedFor)
	}
	got := sortedRemovalAddrs(res)
	if len(got) != 1 || got[0] != indexedAddr {
		t.Errorf("destroys proposed for %v, want [%s] only - %s's own tag read says it carries no tofu-estate", got, indexedAddr, perObjectUnindexedName)
	}
	if reason := gapReasonFor(res, typeName); reason != "" {
		t.Errorf("a sweep gap %q is filed for %s although the one object the index was silent about was tag-read successfully and is simply not ours\n%s", reason, typeName, res)
	}
}

// TestPerObjectGateKeepsTheGapForASiblingWhoseReadFailed is the loud half. A
// bound join on one role proves the index serves the type; it proves nothing
// about a sibling the index is silent on whose own tag read was then refused.
// That sibling's marker is as unread as it would be on a target with no
// index at all, so the existing MARKER_UNREADABLE gap stands, with its
// existing wording - and the indexed sibling's destroy is still proposed,
// because a failure on one object is not a reason to forget another.
func TestPerObjectGateKeepsTheGapForASiblingWhoseReadFailed(t *testing.T) {
	const (
		typeName      = "aws_iam_role"
		indexedAddr   = typeName + ".removed_indexed"
		unindexedAddr = typeName + ".removed_lagging"
	)

	cloud, tagSrv := perObjectFixture(t, typeName, roleARN, indexedAddr, unindexedAddr)
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		err:    errors.New("AccessDenied: User is not authorized to perform iam:ListRoleTags"),
	}
	res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, reader))
	assertNoErrors(t, diags)

	wantAsked := typeName + " " + perObjectUnindexedName
	if reader.calls != 1 || len(reader.askedFor) != 1 || reader.askedFor[0] != wantAsked {
		t.Fatalf("the service tag reader was asked %v (%d call(s)), want exactly [%q]", reader.askedFor, reader.calls, wantAsked)
	}
	got := sortedRemovalAddrs(res)
	if len(got) != 1 || got[0] != indexedAddr {
		t.Errorf("destroys proposed for %v, want [%s] - the index read that marker, and nothing read the other", got, indexedAddr)
	}
	if reason := gapReasonFor(res, typeName); reason != SweepGapMarkerUnreadable {
		t.Errorf("the sweep gap for %s is %q, want %q - one listed role's tag read was refused and no other route read its marker, so this run cannot say the estate owns no undeclared role\n%s",
			typeName, reason, SweepGapMarkerUnreadable, res)
	}
}

// TestPerObjectGateKeepsTheGapWhenOneOfTwoServiceReadsFailed is the same
// rule with no index in the picture at all, which is the pinned emulator's
// shape and real AWS's for iam:role (#1134): the index holds nothing, both
// roles are read through the service, one read answers and one is throttled.
// Before #1162 the answered read set markerReadWorked and the throttled
// role's gap was suppressed with it.
func TestPerObjectGateKeepsTheGapWhenOneOfTwoServiceReadsFailed(t *testing.T) {
	const (
		typeName   = "aws_iam_role"
		readAddr   = typeName + ".removed_read"
		failedAddr = typeName + ".removed_throttled"
		readName   = "estate-read"
		failedName = "estate-throttled"
	)

	cloud := nativeRoleFixture(t, typeName, readName, readAddr)
	cloud.own(typeName, failedName, failedAddr)
	stripTags(t, cloud, typeName, failedName)
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		tags: map[string]map[string]string{
			readName: {TagEstate: estateName, TagAddress: readAddr},
		},
		errFor: map[string]error{failedName: errors.New("Throttling: Rate exceeded")},
	}
	res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, reader))
	assertNoErrors(t, diags)

	if reader.calls != 2 {
		t.Fatalf("the service tag reader was called %d time(s) (%v), want 2 - the index answered for neither role", reader.calls, reader.askedFor)
	}
	if got := sortedRemovalAddrs(res); len(got) != 1 || got[0] != readAddr {
		t.Errorf("destroys proposed for %v, want [%s]", got, readAddr)
	}
	if reason := gapReasonFor(res, typeName); reason != SweepGapMarkerUnreadable {
		t.Errorf("the sweep gap for %s is %q, want %q - %s's tag read was throttled and its marker is unread\n%s",
			typeName, reason, SweepGapMarkerUnreadable, failedName, res)
	}
}
