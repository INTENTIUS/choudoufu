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
)

// GitHub issue #1131, and the repair for #881.
//
// unservednoroute_test.go's
// TestSweepSaysSoWhenCloudControlListsATypeItCanNeverReadTheMarkerOff pins
// what this leg REPLACES: the emulator's verbatim answer for
// AWS::IAM::InstanceProfile - an identifier and three properties, no Tags
// key, on the list and on the refining GetResource - producing
// [SweepGapMarkerUnreadable] and no destroy. Every fixture here is that one,
// unchanged, plus a service tag reader, so the difference under test is the
// leg and nothing else.

// fakeServiceTags is a [servicetags.Reader] whose every answer the test
// states. It counts calls, because "the leg did not run" is as much of an
// assertion here as "the leg recovered the marker".
type fakeServiceTags struct {
	routes map[string]bool
	tags   map[string]map[string]string
	err    error

	calls    int
	askedFor []string
}

func (f *fakeServiceTags) Route(typeName string) bool { return f.routes[typeName] }

func (f *fakeServiceTags) ReadTags(_ context.Context, typeName, importID string) (map[string]string, error) {
	f.calls++
	f.askedFor = append(f.askedFor, typeName+" "+importID)
	if f.err != nil {
		return nil, f.err
	}
	return f.tags[importID], nil
}

// ccInstanceProfileFixture is the emulator's own wire answer for one
// instance profile: enumerable, and carrying no Tags key on either call.
func ccInstanceProfileFixture(t *testing.T, cfnType, liveName string) *ccServer {
	t.Helper()
	bare := map[string]any{
		"InstanceProfileName": liveName,
		"Arn":                 "arn:aws:iam::000000000000:instance-profile/" + liveName,
		"Path":                "/",
	}
	cc := newCCServer(t)
	cc.listResources[cfnType] = []ccResource{{identifier: liveName, properties: bare}}
	cc.getResource[cfnType+" "+liveName] = ccResource{identifier: liveName, properties: bare}
	return cc
}

// serviceTagReadRequest builds the request the three tests below share:
// sweeping, Cloud Control enumerating the untaggable-in-CFN type, and a tag
// index the test supplies.
func serviceTagReadRequest(t *testing.T, tagServerURL, ccServerURL string, reader *fakeServiceTags) Request {
	t.Helper()
	return Request{
		Sweep:        true,
		TaggingSweep: true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagServerURL}),
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: ccServerURL}),
		ServiceTags:  reader,
		Roster: ccRoster(t,
			map[string]string{"aws_iam_instance_profile": "AWS::IAM::InstanceProfile"},
			map[string]bool{"AWS::IAM::InstanceProfile": true},
			map[string]bool{"AWS::IAM::InstanceProfile": false},
		),
	}
}

func gapReasonFor(res *Result, typeName string) SweepGapReason {
	for _, g := range res.SweepGaps {
		if g.TypeName == typeName {
			return g.Reason
		}
	}
	return ""
}

func sweepCovers(res *Result, typeName string) bool {
	for _, c := range res.SweepCovered {
		if c == typeName {
			return true
		}
	}
	return false
}

// TestServiceTagReadRecoversTheDestroyCloudControlCannotSee is #881's own
// verdict line, offline: a live instance profile carrying this estate's
// marker, whose block is gone, is proposed for destruction rather than
// refused - because the service's own tag API answered where Cloud Control
// and the tag index both could not.
func TestServiceTagReadRecoversTheDestroyCloudControlCannotSee(t *testing.T) {
	const (
		typeName = "aws_iam_instance_profile"
		cfnType  = "AWS::IAM::InstanceProfile"
		liveName = "estate-team-0002-profile"
	)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(typeName)
	cloud.unlistable(typeName)

	// The emulator's tag index, verbatim: empty for IAM
	// (lex00/floci#205 / #1152).
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	cc := ccInstanceProfileFixture(t, cfnType, liveName)
	ccServer := cc.start()
	defer ccServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		tags: map[string]map[string]string{
			liveName: {TagEstate: estateName, TagAddress: typeName + ".team_0002_profile"},
		},
	}

	res, diags := discoverFixture(t, cloud, serviceTagReadRequest(t, tagServer.URL, ccServer.URL, reader))
	assertNoErrors(t, diags)

	if reader.calls != 1 {
		t.Fatalf("the service tag reader was called %d time(s), want exactly 1 (asked for %v). The leg is what this test is about; zero calls means it never ran and any pass below is an accident.", reader.calls, reader.askedFor)
	}

	rm := removalsByAddr(res)
	o, ok := rm[typeName+".team_0002_profile"]
	if !ok {
		t.Fatalf("the deleted block's live instance profile is not proposed for removal - this is #881's verdict line, verbatim: choudoufu does not propose destroying it when its block is deleted.\ngap=%q covered=%v\n%s",
			gapReasonFor(res, typeName), sweepCovers(res, typeName), res)
	}
	if o.ImportID != liveName {
		t.Errorf("the removal's ImportID is %q, want %q", o.ImportID, liveName)
	}

	// And the refusal is gone, not merely outvoted. A gap filed on a type
	// the run successfully resolved is a false refusal, which is what
	// constraint 3 of this unit forbids.
	if reason := gapReasonFor(res, typeName); reason != "" {
		t.Errorf("a sweep gap (%q) was still filed for %s on a run that read its marker and proposed its destroy. A gap that fires on a type the run resolved tells an operator to go to the console over nothing.", reason, typeName)
	}
	if !sweepCovers(res, typeName) {
		t.Errorf("%s is missing from SweepCovered (%v), although the sweep did search it and did find the orphan", typeName, res.SweepCovered)
	}

	scan, ok := res.ScanFor(typeName)
	if !ok || scan.ServiceTagReads != 1 {
		t.Errorf("scan.ServiceTagReads = %d (found=%v), want 1. The leg's cost is not flat in estate size, so it has to be countable in the scan row rather than argued about in a comment.", scan.ServiceTagReads, ok)
	}
}

// TestServiceTagReadFailureKeepsTheMarkerUnreadableRefusal is constraint 3
// from the other side, and it is the guard that must stay: a read that did
// not happen establishes nothing, so #1129's refusal has to survive
// intact. Same fixture, reader errors.
func TestServiceTagReadFailureKeepsTheMarkerUnreadableRefusal(t *testing.T) {
	const (
		typeName = "aws_iam_instance_profile"
		cfnType  = "AWS::IAM::InstanceProfile"
		liveName = "estate-team-0002-profile"
	)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(typeName)
	cloud.unlistable(typeName)

	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	cc := ccInstanceProfileFixture(t, cfnType, liveName)
	ccServer := cc.start()
	defer ccServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		err:    errors.New("AccessDenied: User is not authorized to perform iam:ListInstanceProfileTags"),
	}

	res, diags := discoverFixture(t, cloud, serviceTagReadRequest(t, tagServer.URL, ccServer.URL, reader))
	assertNoErrors(t, diags)

	if reader.calls != 1 {
		t.Fatalf("the service tag reader was called %d time(s), want 1", reader.calls)
	}
	if reason := gapReasonFor(res, typeName); reason != SweepGapMarkerUnreadable {
		t.Fatalf("the sweep gap for %s is %q, want %q. A failed tag read establishes nothing about the object, so #1129's refusal is still the honest answer and must not be traded for silence.\n%s",
			typeName, reason, SweepGapMarkerUnreadable, res)
	}
	if sweepCovers(res, typeName) {
		t.Errorf("%s is in SweepCovered although no marker was read off any object of it: the result asserts coverage it does not have", typeName)
	}
	var spoken bool
	for _, d := range diags {
		if d.Description().Summary == SummaryIncompleteSweep {
			spoken = true
		}
	}
	if !spoken {
		t.Errorf("the gap produced no diagnostic, so nothing reaches the operator; diagnostics: %s", renderDiags(diags))
	}
}

// TestServiceTagReadSkippedWhenTheTagIndexAlreadyServesTheType is the cost
// gate, and it is the clause that makes this leg scoped by evidence rather
// than by service name.
//
// #1134 measured a real account serving iam:instance-profile through
// GetResources in us-east-1 while the pinned emulator serves no IAM at all.
// A leg keyed on the service would have to be wrong about one of those two
// targets. Here the index serves the type, so its silence about a profile
// it does NOT hold is a real answer about that profile - joinNone - and the
// leg must not spend a call per unowned object re-deriving it.
//
// The account therefore holds two profiles, which is what makes the
// assertion load-bearing: the owned one binds from the index and would have
// short-circuited the leg on its own, so a one-object fixture cannot tell
// the gate working from the gate missing. The second profile is not in the
// index, reaches joinNone, and is the object the gate has to decline.
func TestServiceTagReadSkippedWhenTheTagIndexAlreadyServesTheType(t *testing.T) {
	const (
		typeName  = "aws_iam_instance_profile"
		cfnType   = "AWS::IAM::InstanceProfile"
		ownedName = "estate-team-0002-profile"
		otherName = "unowned-profile"
	)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(typeName)
	cloud.unlistable(typeName)

	// A real account's answer: GetResources DOES serve the type, and the
	// estate's own profile is in it, marker and all. Someone else's
	// profile is not, because the index is filtered to this estate's tag.
	arn := "arn:aws:iam::000000000000:instance-profile/" + ownedName
	tagSrv := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{
			arn: {TagEstate: estateName, TagAddress: typeName + ".team_0002_profile"},
		},
	}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	cc := newCCServer(t)
	for _, name := range []string{ownedName, otherName} {
		bare := map[string]any{
			"InstanceProfileName": name,
			"Arn":                 "arn:aws:iam::000000000000:instance-profile/" + name,
			"Path":                "/",
		}
		cc.listResources[cfnType] = append(cc.listResources[cfnType], ccResource{identifier: name, properties: bare})
		cc.getResource[cfnType+" "+name] = ccResource{identifier: name, properties: bare}
	}
	ccServer := cc.start()
	defer ccServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		tags: map[string]map[string]string{
			ownedName: {TagEstate: estateName, TagAddress: typeName + ".team_0002_profile"},
			otherName: {TagEstate: "some-other-estate", TagAddress: typeName + ".theirs"},
		},
	}

	res, diags := discoverFixture(t, cloud, serviceTagReadRequest(t, tagServer.URL, ccServer.URL, reader))
	assertNoErrors(t, diags)

	if reader.calls != 0 {
		t.Fatalf("the service tag reader was called %d time(s) (%v), want 0: the estate's tag index already holds this type, so #266's join answered for the owned profile and joinNone is a real answer for the other one. Paying a call per object here is what would bend #1037/#1039's flat-sweep claim on a target that never needed the leg.", reader.calls, reader.askedFor)
	}
	if _, ok := removalsByAddr(res)[typeName+".team_0002_profile"]; !ok {
		t.Fatalf("the orphan was not found by the tag index alone, so this test is not measuring what it claims:\n%s", res)
	}
}

// TestServiceTagReadDoesNotAdoptAnObjectOfAnotherEstate is the safety half.
// A tag read that succeeds and shows no tofu-estate of ours is an ordinary
// unowned object - exactly what a list call carrying tags would have said
// - and nothing may be claimed from it.
func TestServiceTagReadDoesNotAdoptAnObjectOfAnotherEstate(t *testing.T) {
	const (
		typeName = "aws_iam_instance_profile"
		cfnType  = "AWS::IAM::InstanceProfile"
		liveName = "someone-elses-profile"
	)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(typeName)
	cloud.unlistable(typeName)

	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	cc := ccInstanceProfileFixture(t, cfnType, liveName)
	ccServer := cc.start()
	defer ccServer.Close()

	reader := &fakeServiceTags{
		routes: map[string]bool{typeName: true},
		tags: map[string]map[string]string{
			liveName: {TagEstate: "some-other-estate", TagAddress: typeName + ".theirs"},
		},
	}

	res, diags := discoverFixture(t, cloud, serviceTagReadRequest(t, tagServer.URL, ccServer.URL, reader))
	assertNoErrors(t, diags)

	if len(removalsByAddr(res)) != 0 {
		t.Fatalf("a profile marked for another estate produced a removal - this leg must read ownership, never infer it:\n%s", res)
	}
	// The type WAS searched, and the search found nothing of ours, so no
	// gap and no lost coverage: that is the difference between "nobody
	// could look" and "we looked and it is not ours".
	if reason := gapReasonFor(res, typeName); reason != "" {
		t.Errorf("a gap (%q) was filed although every object of the type was read successfully", reason)
	}
	if !sweepCovers(res, typeName) {
		t.Errorf("%s is missing from SweepCovered (%v) although its one object was read", typeName, res.SweepCovered)
	}
}

// TestServiceTagReadIsOffWithNoReader pins the zero value: every caller
// that predates this field gets byte-identical behaviour, which is #1129's
// refusal.
func TestServiceTagReadIsOffWithNoReader(t *testing.T) {
	const (
		typeName = "aws_iam_instance_profile"
		cfnType  = "AWS::IAM::InstanceProfile"
		liveName = "estate-team-0002-profile"
	)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(typeName)
	cloud.unlistable(typeName)

	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	cc := ccInstanceProfileFixture(t, cfnType, liveName)
	ccServer := cc.start()
	defer ccServer.Close()

	req := serviceTagReadRequest(t, tagServer.URL, ccServer.URL, nil)
	req.ServiceTags = nil

	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	if reason := gapReasonFor(res, typeName); reason != SweepGapMarkerUnreadable {
		t.Fatalf("with no reader the sweep gap for %s is %q, want %q - the leg's absence must leave #1129 exactly as it was", typeName, reason, SweepGapMarkerUnreadable)
	}
}

// TestServiceTagReadIsOffForATypeWithNoRoute is the other free gate: a
// reader that has no operation for the type costs nothing and changes
// nothing.
func TestServiceTagReadIsOffForATypeWithNoRoute(t *testing.T) {
	const (
		typeName = "aws_iam_instance_profile"
		cfnType  = "AWS::IAM::InstanceProfile"
		liveName = "estate-team-0002-profile"
	)

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(typeName)
	cloud.unlistable(typeName)

	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	cc := ccInstanceProfileFixture(t, cfnType, liveName)
	ccServer := cc.start()
	defer ccServer.Close()

	reader := &fakeServiceTags{routes: map[string]bool{"aws_iam_virtual_mfa_device": true}}

	res, diags := discoverFixture(t, cloud, serviceTagReadRequest(t, tagServer.URL, ccServer.URL, reader))
	assertNoErrors(t, diags)

	if reader.calls != 0 {
		t.Errorf("the reader was called %d time(s) for a type it routes nothing for (%v)", reader.calls, reader.askedFor)
	}
	if reason := gapReasonFor(res, typeName); reason != SweepGapMarkerUnreadable {
		t.Fatalf("the sweep gap for %s is %q, want %q", typeName, reason, SweepGapMarkerUnreadable)
	}
}
