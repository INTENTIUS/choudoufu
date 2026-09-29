// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"errors"
	"sort"
	"strings"
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

func gapDetailFor(res *Result, typeName string) string {
	for _, g := range res.SweepGaps {
		if g.TypeName == typeName {
			return g.Detail
		}
	}
	return ""
}

// TestFailedTagReadGapNamesTheErrorAndTheAction is the third MARKER_UNREADABLE
// wording, ruled by the maintainer on 2026-09-21 for the one run the two
// older sentences are wrong about: a per-object service tag read was made
// and refused. The older two say the tag index cannot answer for the type
// and that no marker was read off any listed object, and here a sibling's
// marker WAS read off the index. Same reason code, so internal/live/foreign
// and every reader keyed on MARKER_UNREADABLE are untouched.
//
// The two older sentences are asserted unchanged on their own runs beside
// it: no route wired (the index is blind to the service, permanent) and no
// tag index at all (transient).
func TestFailedTagReadGapNamesTheErrorAndTheAction(t *testing.T) {
	const (
		typeName      = "aws_iam_role"
		indexedAddr   = typeName + ".removed_indexed"
		unindexedAddr = typeName + ".removed_lagging"
	)

	t.Run("one of two roles refused", func(t *testing.T) {
		cloud, tagSrv := perObjectFixture(t, typeName, roleARN, indexedAddr, unindexedAddr)
		tagServer := tagSrv.start(t)
		defer tagServer.Close()

		reader := &fakeServiceTags{
			routes:  map[string]bool{typeName: true},
			actions: map[string]string{typeName: "iam:ListRoleTags"},
			err:     errors.New("AccessDenied: User is not authorized to perform iam:ListRoleTags"),
		}
		res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, reader))
		assertNoErrors(t, diags)

		if reason := gapReasonFor(res, typeName); reason != SweepGapMarkerUnreadable {
			t.Fatalf("the sweep gap for %s is %q, want %q", typeName, reason, SweepGapMarkerUnreadable)
		}
		want := "The sweep could not read an ownership marker off 1 of 2 aws_iam_role: the list call returned no tags, the tag index did not hold it, and the service's own tag read failed (AccessDenied: iam:ListRoleTags). A live aws_iam_role this estate owns and no longer declares WILL NOT be proposed for destruction by this run. Grant iam:ListRoleTags, or retry if it was throttled, and re-run."
		if got := gapDetailFor(res, typeName); got != want {
			t.Errorf("gap detail:\n got %q\nwant %q", got, want)
		}
	})

	t.Run("two roles refused with different errors", func(t *testing.T) {
		cloud, tagSrv := perObjectFixture(t, typeName, roleARN, indexedAddr, unindexedAddr)
		tagSrv.arns, tagSrv.tags = nil, nil // the index holds neither
		cloud.own(typeName, "estate-third", typeName+".removed_third")
		stripTags(t, cloud, typeName, "estate-third")
		tagServer := tagSrv.start(t)
		defer tagServer.Close()

		reader := &fakeServiceTags{
			routes:  map[string]bool{typeName: true},
			actions: map[string]string{typeName: "iam:ListRoleTags"},
			errFor: map[string]error{
				perObjectIndexedName:   errors.New("AccessDenied: User is not authorized to perform iam:ListRoleTags"),
				perObjectUnindexedName: errors.New("Throttling: Rate exceeded"),
				"estate-third":         errors.New("Throttling: Rate exceeded"),
			},
		}
		res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, reader))
		assertNoErrors(t, diags)

		if reader.calls != 3 {
			t.Fatalf("the reader was called %d time(s), want 3", reader.calls)
		}
		got := gapDetailFor(res, typeName)
		wantHead := "The sweep could not read an ownership marker off 3 of 3 aws_iam_role: the list call returned no tags, the tag index did not hold them, and the service's own tag read failed (AccessDenied: iam:ListRoleTags, and 1 more)."
		if !strings.HasPrefix(got, wantHead) {
			t.Errorf("gap detail:\n got %q\nwant prefix %q", got, wantHead)
		}
	})

	t.Run("no route keeps the permanent sentence", func(t *testing.T) {
		cloud := nativeRoleFixture(t, typeName, perObjectIndexedName, indexedAddr)
		tagSrv := &taggingServer{}
		tagServer := tagSrv.start(t)
		defer tagServer.Close()

		res, diags := discoverFixture(t, cloud, nativeServiceTagReadRequest(t, typeName, tagServer.URL, &fakeServiceTags{}))
		assertNoErrors(t, diags)
		want := "The estate-wide sweep listed 1 aws_iam_role through the provider's own list resource and could read an ownership marker off none of them: the list call returned no tags for any object of the type, and the Resource Groups Tagging API - the fallback that exists for exactly that - does not index this service at all, so the estate's tag index cannot answer for it either. The provider does give aws_iam_role a tags argument and this estate stamps its markers there, so a live aws_iam_role this estate owns and no longer declares WILL NOT be proposed for destruction by this run. Destroy such a resource before removing its block, or delete it out of band."
		if got := gapDetailFor(res, typeName); got != want {
			t.Errorf("the no-route sentence moved:\n got %q\nwant %q", got, want)
		}
	})

	t.Run("no index keeps the transient sentence", func(t *testing.T) {
		cloud := nativeRoleFixture(t, typeName, perObjectIndexedName, indexedAddr)
		req := nativeServiceTagReadRequest(t, typeName, "", &fakeServiceTags{})
		req.Tagging = nil
		res, diags := discoverFixture(t, cloud, req)
		assertNoErrors(t, diags)
		want := "The estate-wide sweep listed 1 aws_iam_role through the provider's own list resource and could read an ownership marker off none of them: the list call returned no tags for any object of the type, and the estate's tag index - the fallback that exists for exactly that - could not be consulted, because this run has no Resource Groups Tagging API endpoint configured or its one GetResources call failed. Nothing here says this estate owns no aws_iam_role; it says nothing was established either way, so a live aws_iam_role this estate owns and no longer declares is not proposed for destruction by this run. Re-run with the Tagging API reachable before concluding anything about this type."
		if reason := gapReasonFor(res, typeName); reason != SweepGapTagIndexUnavailable {
			t.Fatalf("the gap is %q, want %q", reason, SweepGapTagIndexUnavailable)
		}
		if got := gapDetailFor(res, typeName); got != want {
			t.Errorf("the no-index sentence moved:\n got %q\nwant %q", got, want)
		}
	})
}
