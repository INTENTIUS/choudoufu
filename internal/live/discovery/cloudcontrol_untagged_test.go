// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// Issue #355's coverage. Cloud Control omits a CFN property that has no
// value, so an object carrying zero tags arrives with no Tags key at all -
// byte-identical to what a type with no Tags property in its schema would
// send. [scanTypeCloudControl] used to read that absence as "this object's
// ownership markers cannot be read" and raise [ProblemNoTags], an ERROR that
// aborts the whole plan, over an ordinary untagged object sitting in the same
// account.
//
// Measured against floci at cdd50ec0, listing AWS::EC2::DHCPOptions:
//
//	dopt-default            Properties: {DhcpOptionsId, OwnerId, Region, DhcpConfigurationSet}
//	dopt-0c049a60e0156307e  Properties: {..., Tags: [{Key: tofu-estate, ...}]}
//	dopt-c04f43fb6cd93bd0f  Properties: {DhcpOptionsId, OwnerId, Region, DhcpConfigurationSet}
//
// - i.e. the Tags key appears iff the object carries at least one tag, on the
// same CFN type, in the same listing. The account's default DHCP options set
// is only ever an instance of that general shape, which is why the rule under
// test is derived from the registry's taggability answer and not from any
// notion of "account default": AWS publishes no such flag for this type, and
// an ordinary untagged object of any admitted type reproduces the wall.
//
// The native-list twin has always been ordinary: a provider that lists an
// untagged object returns tags = {}, which lands in Unclaimed.

// ccUntaggedProps is a listing with no Tags key - the wire shape floci and
// real Cloud Control both send for an object that carries no tags.
func ccUntaggedProps(id string) map[string]any {
	return map[string]any{"DhcpOptionsId": id, "Region": "eu-west-1"}
}

// runCCTaggability is [runCCDiscovery] with the roster's registry answer for
// cfnType under the test's control, which is the whole input the rule reads.
// registryRow=false builds a mapping row with no registry row behind it at
// all - [registry.Roster.TaggableKnown]'s known=false case.
func runCCTaggability(t *testing.T, srv *ccServer, server *httptest.Server, typeName, cfnType string, taggable, registryRow bool) (*Result, bool) {
	t.Helper()

	var roster *registry.Roster
	if registryRow {
		roster = ccRoster(t,
			map[string]string{typeName: cfnType},
			map[string]bool{cfnType: true},
			map[string]bool{cfnType: taggable},
		)
	} else {
		roster = ccRosterWithoutRegistryRow(t, typeName, cfnType)
	}

	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1})
	req := Request{
		Estate:       ccEstate,
		Config:       ccConfig(typeName),
		Resolutions:  []identity.Resolution{ccResolutionFor(t, typeName)},
		Provider:     newFakeCloud(),
		CloudControl: cc,
		Roster:       roster,
	}
	res, diags := Discover(context.Background(), req)
	return res, diags.HasErrors()
}

// ccRosterWithoutRegistryRow maps typeName to cfnType and marks it listable,
// but gives live/registry.json no row for cfnType at all. ccRoster cannot
// express this: it synthesizes one registry entry per mapped CFN type.
func ccRosterWithoutRegistryRow(t *testing.T, typeName, cfnType string) *registry.Roster {
	t.Helper()

	mappingJSON, err := json.Marshal(map[string]any{
		"rows": []map[string]any{{"tf_type": typeName, "cfn_type": cfnType, "via": "name"}},
	})
	if err != nil {
		t.Fatalf("marshaling mapping fixture: %v", err)
	}
	// One unrelated row, so the registry parses as a real artifact rather than
	// an empty one, and so "no row for THIS type" is what is under test rather
	// than "no registry at all".
	registryJSON, err := json.Marshal(map[string]any{
		"types": []map[string]any{{
			"type_name": "AWS::Unrelated::Thing",
			"tagging":   map[string]any{"taggable": true},
			"handlers":  map[string]any{"list": true},
		}},
	})
	if err != nil {
		t.Fatalf("marshaling registry fixture: %v", err)
	}
	r, err := registry.Parse(mappingJSON, registryJSON)
	if err != nil {
		t.Fatalf("registry.Parse: %v", err)
	}
	if _, known := r.TaggableKnown(cfnType); known {
		t.Fatalf("fixture is not what it claims: the roster has a registry row for %s", cfnType)
	}
	return r
}

// ccUntaggedFixture lists two objects of one type: one carrying this estate's
// marker (so the declared instance still binds and the run has something to
// succeed at), and one with no Tags key at all.
func ccUntaggedFixture(t *testing.T, cfnType, typeName string) *ccServer {
	t.Helper()
	srv := newCCServer(t)
	srv.listResources[cfnType] = []ccResource{
		{identifier: "dopt-owned", properties: mergeProps(tagsProps(ccEstate, typeName+".x"), map[string]any{"DhcpOptionsId": "dopt-owned"})},
		{identifier: "dopt-default", properties: ccUntaggedProps("dopt-default")},
	}
	// Refining the untagged one succeeds and still carries no Tags - floci's
	// exact answer for dopt-default, verified by hand against the emulator.
	srv.getResource[cfnType+" dopt-default"] = ccResource{
		identifier: "dopt-default",
		properties: ccUntaggedProps("dopt-default"),
	}
	return srv
}

// TestCloudControlUntaggedObjectOfATaggableTypeIsUnclaimedNotFatal is #355
// itself: registry says the type carries Tags, the object was read cleanly and
// carries none, so it is untagged - the same place the native leg's tags = {}
// lands - and the estate's own marked object still binds.
func TestCloudControlUntaggedObjectOfATaggableTypeIsUnclaimedNotFatal(t *testing.T) {
	const typeName, cfnType = "aws_efs_file_system", "AWS::EFS::FileSystem"
	srv := ccUntaggedFixture(t, cfnType, typeName)
	server := srv.start()
	defer server.Close()

	res, hadErrors := runCCTaggability(t, srv, server, typeName, cfnType, true, true)
	if got := res.ProblemsOfKind(ProblemNoTags); len(got) != 0 {
		t.Errorf("an untagged object of a registry-taggable type still raised the hard ProblemNoTags error:\n%s", renderProblems(got))
	}
	if hadErrors {
		t.Errorf("discovery reported errors for an ordinary untagged object:\n%s", res)
	}
	if _, ok := res.BindingFor(mustAddr(t, typeName+".x")); !ok {
		t.Errorf("the estate's own marked object no longer binds:\n%s", res)
	}
	scan, ok := res.ScanFor(typeName)
	if !ok {
		t.Fatalf("no scan recorded for %s", typeName)
	}
	if scan.Unclaimed != 1 {
		t.Errorf("scan.Unclaimed = %d, want 1 (the untagged object is a foreign unclaimed resource, not a refusal)", scan.Unclaimed)
	}
	if scan.Refined != 1 {
		t.Errorf("scan.Refined = %d, want 1 (the untagged object's listing carried no Tags, so it was refined once)", scan.Refined)
	}
}

// TestCloudControlEmptyTagsListStillReadsAsUntagged is the same fact in its
// other spelling, pinned so the two never diverge again: a Tags key holding an
// empty list has always been "tagged with nothing" ([ccPropertiesTags]), and
// #355's change makes the no-Tags-key spelling reach the identical outcome.
func TestCloudControlEmptyTagsListStillReadsAsUntagged(t *testing.T) {
	const typeName, cfnType = "aws_efs_file_system", "AWS::EFS::FileSystem"
	srv := newCCServer(t)
	srv.listResources[cfnType] = []ccResource{
		{identifier: "dopt-owned", properties: mergeProps(tagsProps(ccEstate, typeName+".x"), map[string]any{"DhcpOptionsId": "dopt-owned"})},
		{identifier: "dopt-default", properties: map[string]any{"DhcpOptionsId": "dopt-default", "Tags": []map[string]string{}}},
	}
	server := srv.start()
	defer server.Close()

	res, hadErrors := runCCTaggability(t, srv, server, typeName, cfnType, true, true)
	if hadErrors {
		t.Errorf("discovery reported errors for an object tagged with nothing:\n%s", res)
	}
	scan, _ := res.ScanFor(typeName)
	if scan.Unclaimed != 1 {
		t.Errorf("scan.Unclaimed = %d, want 1", scan.Unclaimed)
	}
	if scan.Refined != 0 {
		t.Errorf("scan.Refined = %d, want 0 (the Tags key was present, so nothing needed refining)", scan.Refined)
	}
}

// TestCloudControlUntaggedObjectOfAnUntaggableTypeIsSkipped is issue #322's
// ruling reached through the registry rather than the provider schema: the
// registry says no object of this CFN type can carry a tag, so none could ever
// carry a marker, and an ERROR that aborts every other resource in the estate
// is not the right report for one address the run already warns about at bind
// time.
func TestCloudControlUntaggedObjectOfAnUntaggableTypeIsSkipped(t *testing.T) {
	const typeName, cfnType = "aws_efs_file_system", "AWS::EFS::FileSystem"
	srv := ccUntaggedFixture(t, cfnType, typeName)
	server := srv.start()
	defer server.Close()

	res, hadErrors := runCCTaggability(t, srv, server, typeName, cfnType, false, true)
	if got := res.ProblemsOfKind(ProblemNoTags); len(got) != 0 {
		t.Errorf("an untagged object of a registry-untaggable type still raised ProblemNoTags:\n%s", renderProblems(got))
	}
	if hadErrors {
		t.Errorf("discovery reported errors for a type the registry says cannot carry a tag:\n%s", res)
	}
	scan, _ := res.ScanFor(typeName)
	if scan.Unclaimed != 0 {
		t.Errorf("scan.Unclaimed = %d, want 0 - a type that cannot carry a tag cannot be reported as unclaimed-and-adoptable either", scan.Unclaimed)
	}
}

// TestCloudControlUnreadableObjectStillRefuses is the mutation check on the
// rule: remove the one stated obstacle - the object being readable - and the
// refusal must come back. A GetResource that errors is the genuine "ownership
// markers cannot be read" ProblemNoTags exists for, and #355 must not have
// deleted it.
func TestCloudControlUnreadableObjectStillRefuses(t *testing.T) {
	const typeName, cfnType = "aws_efs_file_system", "AWS::EFS::FileSystem"
	srv := ccUntaggedFixture(t, cfnType, typeName)
	// The refinement now fails outright instead of coming back clean. Not
	// UnsupportedOperation, which has its own list-and-match fallback:
	// AccessDenied, which nothing rescues.
	srv.getResource[cfnType+" dopt-default"] = "AccessDeniedException"
	// ...and the fallback's own re-list must not hand the tags back either.
	srv.listResourcesAfterFirst[cfnType] = []ccResource{
		{identifier: "dopt-default", properties: ccUntaggedProps("dopt-default")},
	}
	server := srv.start()
	defer server.Close()

	res, _ := runCCTaggability(t, srv, server, typeName, cfnType, true, true)
	if got := res.ProblemsOfKind(ProblemNoTags); len(got) != 1 {
		t.Errorf("an object that could not be read at all did NOT raise ProblemNoTags (got %d):\n%s", len(got), res)
	}
}

// TestCloudControlUntaggedObjectDuringASweepIsNotFatal covers the leg the
// original refusal was widest on. The native list path has always had a sweep
// branch for this (SweepGapObjectUntagged: "a hole in removal coverage, not a
// reason to fail every plan this estate ever runs"); the Cloud Control path
// had none, so an untagged object of an admitted type the configuration does
// not even DECLARE aborted the whole plan. Every account has those.
func TestCloudControlUntaggedObjectDuringASweepIsNotFatal(t *testing.T) {
	const swept, cfnType = "aws_efs_file_system", "AWS::EFS::FileSystem"
	srv := newCCServer(t)
	srv.listResources[cfnType] = []ccResource{
		{identifier: "fs-somebody-elses", properties: ccUntaggedProps("fs-somebody-elses")},
	}
	srv.getResource[cfnType+" fs-somebody-elses"] = ccResource{
		identifier: "fs-somebody-elses",
		properties: ccUntaggedProps("fs-somebody-elses"),
	}
	server := srv.start()
	defer server.Close()

	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", "aws_vpc.x")
	req := Request{
		Estate:       estateName,
		Config:       ccConfig("aws_vpc"),
		Resolutions:  []identity.Resolution{{Addr: mustAddr(t, "aws_vpc.x"), Class: identity.ClassNeedsDiscovery}},
		Provider:     cloud,
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1}),
		Roster: ccRoster(t,
			map[string]string{swept: cfnType},
			map[string]bool{cfnType: true},
			map[string]bool{cfnType: true},
		),
		Sweep: true,
		// The sweep universe, narrowed to the one undeclared type under test
		// so this does not list every admitted type through the fake server.
		SweepTypes: []string{swept},
	}
	res, diags := Discover(context.Background(), req)
	if got := res.ProblemsOfKind(ProblemNoTags); len(got) != 0 {
		t.Errorf("an untagged object of an UNDECLARED type aborted the plan during a sweep:\n%s", renderProblems(got))
	}
	if diags.HasErrors() {
		t.Errorf("the sweep reported errors over somebody else's untagged resource:\n%s", diags.Err())
	}
	if _, ok := res.BindingFor(mustAddr(t, "aws_vpc.x")); !ok {
		t.Errorf("the declared resource did not bind:\n%s", res)
	}
}

// TestCloudControlNoRegistryRowNeverReachesThisLeg pins why the rule's
// known=false residual is unreachable rather than leaving a reader to wonder
// whether it was forgotten. [registry.Roster.EnumerationSource] requires
// live/registry.json's own row for the CFN type (Listable is false for a type
// the registry never saw), so a type with no row is refused a whole leg
// earlier, as TYPE_NOT_LISTABLE, and never gets as far as reading an object's
// Tags at all.
//
// If that ever changes, this test goes red and the residual in
// [scanTypeCloudControl] starts carrying real traffic - which is the order
// those two want to happen in.
func TestCloudControlNoRegistryRowNeverReachesThisLeg(t *testing.T) {
	const typeName, cfnType = "aws_efs_file_system", "AWS::EFS::FileSystem"
	srv := ccUntaggedFixture(t, cfnType, typeName)
	server := srv.start()
	defer server.Close()

	res, _ := runCCTaggability(t, srv, server, typeName, cfnType, true, false)
	if got := res.ProblemsOfKind(ProblemNoTags); len(got) != 0 {
		t.Errorf("ProblemNoTags fired for a type that should not have reached the Cloud Control leg at all:\n%s", renderProblems(got))
	}
	if got := res.ProblemsOfKind(ProblemTypeNotListable); len(got) != 1 {
		t.Errorf("a type with no live/registry.json row did not refuse as TYPE_NOT_LISTABLE (got %d):\n%s", len(got), res)
	}
	for _, c := range srv.calls {
		t.Errorf("the Cloud Control fake server was called (%v) for a type with no registry row", c)
		break
	}
}
