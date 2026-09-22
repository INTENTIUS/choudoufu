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
	"github.com/intentius/choudoufu/internal/live/servicetags"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1477: the per-service list leg, for a declared type no
// other enumeration route reaches.
//
// Every fixture here is the iam-ecr cohort's shape: an aws_iam_role WITH a
// configured name beside an aws_iam_service_linked_role. The named role
// resolves from configuration, so nothing lists aws_iam_role on discovery's
// behalf and #302's sibling bind has no producer. The service-linked role's
// own type has no provider list resource (the fake never registers one), no
// Cloud Control list handler (no roster is supplied), and a Resource Groups
// Tagging API that answers successfully with nothing, which is what real AWS
// does for iam:role everywhere (#1134) and what the pinned emulator does for
// all of IAM since lex00/floci#202. The only thing added is a service lister
// and a service tag reader, so the difference under test is the leg.

const (
	slType = "aws_iam_service_linked_role"
	slAddr = slType + ".app"
	slARN  = "arn:aws:iam::000000000000:role/aws-service-role/elasticbeanstalk.amazonaws.com/AWSServiceRoleForElasticBeanstalk"
	slName = "AWSServiceRoleForElasticBeanstalk"
)

// fakeServiceList is a [servicetags.Lister] whose every answer the test
// states. It counts calls, because "the leg did not run" is as much of an
// assertion here as "the leg recovered the binding".
type fakeServiceList struct {
	routes  map[string]bool
	objects map[string][]servicetags.Listed
	err     error
	actions map[string]string

	calls    int
	askedFor []string
}

func (f *fakeServiceList) ListRoute(typeName string) bool { return f.routes[typeName] }

func (f *fakeServiceList) ListAction(typeName string) string { return f.actions[typeName] }

func (f *fakeServiceList) List(_ context.Context, typeName string) ([]servicetags.Listed, error) {
	f.calls++
	f.askedFor = append(f.askedFor, typeName)
	if f.err != nil {
		return nil, f.err
	}
	return f.objects[typeName], nil
}

// slFixture is the cohort's shape against the fakes: the configuration
// above, a fake cloud that lists neither IAM type (the named role needs no
// discovery, the service-linked role has no list resource), and the marker
// routes the test supplies.
func slFixture(t *testing.T, lister *fakeServiceList, reader *fakeServiceTags, tagServerURL string, sweep bool) (*Result, tfdiags.Diagnostics) {
	t.Helper()
	cloud := newFakeCloud()
	cloud.listable(slType)
	cloud.unlistable(slType) // a resource schema with a tags argument, and no list schema
	cloud.withAttr(slType, "arn")
	cfg := loadConfig(t, "testdata/iam-service-linked-role-named")
	req := Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
		Sweep:       sweep,
	}
	if sweep {
		// One type, so the fixture is about this leg and not about the
		// whole admission table's per-type sweep. The declared type
		// joins the universe through sweepTypes' unserved-service loop.
		req.SweepTypes = []string{slType}
	}
	// A nil *fake in an interface field is a non-nil interface holding a
	// nil pointer, so "no lister" has to be the field left unset.
	if lister != nil {
		req.ServiceList = lister
	}
	if reader != nil {
		req.ServiceTags = reader
	}
	if tagServerURL != "" {
		req.Tagging = cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: tagServerURL})
	}
	return Discover(context.Background(), req)
}

func slLister() *fakeServiceList {
	return &fakeServiceList{
		routes:  map[string]bool{slType: true},
		actions: map[string]string{slType: "iam:ListRoles"},
		objects: map[string][]servicetags.Listed{slType: {{ImportID: slARN, IdentityAttr: "arn", ReadKey: slName}}},
	}
}

func slReader(tags map[string]string) *fakeServiceTags {
	return &fakeServiceTags{
		routes:  map[string]bool{slType: true},
		actions: map[string]string{slType: "iam:ListRoleTags"},
		tags:    map[string]map[string]string{slName: tags},
	}
}

// TestServiceListBindsTheServiceLinkedRoleWithItsARN is the verdict line:
// the declared service-linked role binds, under its own type, to the ARN
// the listing returned, with arn as the identity attribute - the same
// binding #302's sibling path produced when it still had a producer.
//
// Before the leg existed this printed UNBOUND aws_iam_service_linked_role.app.
func TestServiceListBindsTheServiceLinkedRoleWithItsARN(t *testing.T) {
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	lister := slLister()
	reader := slReader(map[string]string{TagEstate: estateName, TagAddress: slAddr})
	res, diags := slFixture(t, lister, reader, tagServer.URL, false)
	if diags.HasErrors() {
		t.Fatalf("errors:\n%s\n%v", res, diags.Err())
	}

	b, ok := res.BindingFor(mustAddr(t, slAddr))
	if !ok {
		t.Fatalf("%s did not bind:\n%s", slAddr, res)
	}
	if b.TypeName != slType {
		t.Errorf("bound under TypeName %q, want %s", b.TypeName, slType)
	}
	if b.ImportID != slARN {
		t.Errorf("bound to import ID %q, want the role's ARN %q", b.ImportID, slARN)
	}
	if b.IdentityAttr != "arn" {
		t.Errorf("bound via identity attribute %q, want \"arn\"", b.IdentityAttr)
	}

	// Premise: the service leg, not a native listing or the tag index, is
	// what enumerated it.
	scan, ok := res.ScanFor(slType)
	if !ok || scan.Source != SourceService || scan.Listed != 1 {
		t.Fatalf("the %s scan is %+v (found=%v), want Source=%s Listed=1:\n%s", slType, scan, ok, SourceService, res)
	}
	if lister.calls != 1 || len(lister.askedFor) != 1 || lister.askedFor[0] != slType {
		t.Errorf("the lister was asked %v (%d call(s)), want exactly [%s]", lister.askedFor, lister.calls, slType)
	}
	if reader.calls != 1 || len(reader.askedFor) != 1 || reader.askedFor[0] != slType+" "+slName {
		t.Errorf("the tag reader was asked %v (%d call(s)), want exactly [%q] - the read keys on the role NAME, not the ARN", reader.askedFor, reader.calls, slType+" "+slName)
	}
	if scan.ServiceTagReads != 1 {
		t.Errorf("ServiceTagReads = %d, want 1 - the cost has to be in the scan row", scan.ServiceTagReads)
	}
	if tagSrv.calls == 0 {
		t.Errorf("the tag index was never consulted, so the per-object gate's third clause was not exercised")
	}
}

// TestServiceListKeepsALoudGapWhenTheListingFails is the first safety
// direction: a failed ListRoles establishes nothing, so the declared
// instance must not silently fall through to a create.
func TestServiceListKeepsALoudGapWhenTheListingFails(t *testing.T) {
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	lister := slLister()
	lister.err = errors.New("AccessDenied: User is not authorized to perform iam:ListRoles")
	reader := slReader(map[string]string{TagEstate: estateName, TagAddress: slAddr})
	res, diags := slFixture(t, lister, reader, tagServer.URL, false)
	if !diags.HasErrors() {
		t.Fatalf("a failed service listing produced no error:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemListFailed)) != 1 {
		t.Fatalf("want one %s problem, got:\n%s", ProblemListFailed, res)
	}
	if _, ok := res.BindingFor(mustAddr(t, slAddr)); ok {
		t.Errorf("%s bound although nothing was listed:\n%s", slAddr, res)
	}
	if reader.calls != 0 {
		t.Errorf("the tag reader was called %d time(s) after the listing failed, want 0", reader.calls)
	}
	for _, a := range res.Unbound {
		if a.String() == slAddr {
			t.Errorf("%s is reported as unbound, which would drive a create; a type whose listing failed was never scanned:\n%s", slAddr, res)
		}
	}
}

// TestServiceListReadThatFindsNoMarkerFilesNothing is the second: a
// successful read that returns no tofu-estate is an ANSWER - somebody
// else's (or AWS's own) service-linked role - so nothing binds, nothing is
// refused, and the declared instance is an ordinary create.
func TestServiceListReadThatFindsNoMarkerFilesNothing(t *testing.T) {
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	lister := slLister()
	reader := slReader(map[string]string{})
	res, diags := slFixture(t, lister, reader, tagServer.URL, false)
	if diags.HasErrors() {
		t.Fatalf("errors:\n%s\n%v", res, diags.Err())
	}
	if _, ok := res.BindingFor(mustAddr(t, slAddr)); ok {
		t.Fatalf("%s bound to an object whose own tag read says it carries no tofu-estate:\n%s", slAddr, res)
	}
	if n := len(res.ProblemsOfKind(ProblemUnreadableMarker)); n != 0 {
		t.Errorf("%d %s problem(s) filed although the one listed object's marker was read successfully and found empty:\n%s", n, ProblemUnreadableMarker, res)
	}
	if reader.calls != 1 {
		t.Errorf("the tag reader was called %d time(s), want exactly one", reader.calls)
	}
	var unbound bool
	for _, a := range res.Unbound {
		unbound = unbound || a.String() == slAddr
	}
	if !unbound {
		t.Errorf("%s is not reported unbound, so the plan would not propose creating it:\n%s", slAddr, res)
	}
}

// TestServiceListIgnoresAnotherEstatesServiceLinkedRole is the third: a
// marker naming another estate is another estate's, and this run neither
// binds it nor complains about it.
func TestServiceListIgnoresAnotherEstatesServiceLinkedRole(t *testing.T) {
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	lister := slLister()
	reader := slReader(map[string]string{TagEstate: "some-other-estate", TagAddress: slAddr})
	res, diags := slFixture(t, lister, reader, tagServer.URL, false)
	if diags.HasErrors() {
		t.Fatalf("errors:\n%s\n%v", res, diags.Err())
	}
	if _, ok := res.BindingFor(mustAddr(t, slAddr)); ok {
		t.Fatalf("%s bound to another estate's role:\n%s", slAddr, res)
	}
	scan, _ := res.ScanFor(slType)
	if scan.OtherEstate != 1 {
		t.Errorf("scan = %+v, want OtherEstate=1", scan)
	}
	if len(res.Problems) != 0 {
		t.Errorf("problems filed over another estate's role:\n%s", res)
	}
}

// TestServiceListKeepsTheGapWhenTheTagReadFails: the listing worked and the
// tag read was refused. Nothing is established about the object, so the
// declared instance goes unbound with the per-address warning #322 ruled
// for exactly this ("N listed resources came back unreadable"), never a
// silent create over a resource that may be its own.
func TestServiceListKeepsTheGapWhenTheTagReadFails(t *testing.T) {
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	lister := slLister()
	reader := slReader(nil)
	reader.err = errors.New("AccessDenied: User is not authorized to perform iam:ListRoleTags")
	res, diags := slFixture(t, lister, reader, tagServer.URL, false)
	if diags.HasErrors() {
		t.Fatalf("errors:\n%s\n%v", res, diags.Err())
	}
	if _, ok := res.BindingFor(mustAddr(t, slAddr)); ok {
		t.Fatalf("%s bound although its marker could not be read:\n%s", slAddr, res)
	}
	if n := len(res.ProblemsOfKind(ProblemUnreadableMarker)); n != 1 {
		t.Fatalf("want one %s problem for the instance left unbound beside an unreadable object, got %d:\n%s", ProblemUnreadableMarker, n, res)
	}
}

// TestServiceListSweepProposesTheDestroy is #1131's own direction on this
// leg: a live, marked service-linked role whose block is gone is proposed
// for destruction, because the sweep can now enumerate the type.
func TestServiceListSweepProposesTheDestroy(t *testing.T) {
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	const goneAddr = slType + ".gone"
	const goneARN = "arn:aws:iam::000000000000:role/aws-service-role/ecs.amazonaws.com/AWSServiceRoleForECS"
	const goneName = "AWSServiceRoleForECS"

	lister := slLister()
	lister.objects[slType] = append(lister.objects[slType], servicetags.Listed{ImportID: goneARN, IdentityAttr: "arn", ReadKey: goneName})
	reader := slReader(map[string]string{TagEstate: estateName, TagAddress: slAddr})
	reader.tags[goneName] = map[string]string{TagEstate: estateName, TagAddress: goneAddr}
	res, diags := slFixture(t, lister, reader, tagServer.URL, true)
	if diags.HasErrors() {
		t.Fatalf("errors:\n%s\n%v", res, diags.Err())
	}
	if _, ok := res.BindingFor(mustAddr(t, slAddr)); !ok {
		t.Fatalf("%s did not bind:\n%s", slAddr, res)
	}
	o, ok := removalsByAddr(res)[goneAddr]
	if !ok {
		t.Fatalf("no destroy was proposed for %s:\n%s", goneAddr, res)
	}
	if o.ImportID != goneARN || o.IdentityAttr != "arn" || o.TypeName != slType {
		t.Errorf("removal = %+v, want TypeName=%s ImportID=%s IdentityAttr=arn", o, slType, goneARN)
	}
	if reason := gapReasonFor(res, slType); reason != "" {
		t.Errorf("a sweep gap %q is filed for %s on a run that read every listed object's marker\n%s", reason, slType, res)
	}
	if !sweepCovers(res, slType) {
		t.Errorf("%s is missing from Result.SweepCovered although the sweep listed it and read its markers\n%s", slType, res)
	}
}

// TestServiceListSweepKeepsTheGapWhenAReadFails is the sweep's safety
// direction: a refused read leaves a loud MARKER_UNREADABLE gap naming the
// action to grant, and the type is not recorded as covered.
func TestServiceListSweepKeepsTheGapWhenAReadFails(t *testing.T) {
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	lister := slLister()
	reader := slReader(nil)
	reader.err = errors.New("AccessDenied: User is not authorized to perform iam:ListRoleTags")
	res, diags := slFixture(t, lister, reader, tagServer.URL, true)
	if diags.HasErrors() {
		t.Fatalf("errors:\n%s\n%v", res, diags.Err())
	}
	if len(removalsByAddr(res)) != 0 {
		t.Fatalf("a destroy was proposed although no route read the object's marker:\n%s", res)
	}
	if reason := gapReasonFor(res, slType); reason != SweepGapMarkerUnreadable {
		t.Fatalf("the sweep gap for %s is %q, want %q\n%s", slType, reason, SweepGapMarkerUnreadable, res)
	}
	if sweepCovers(res, slType) {
		t.Errorf("%s is reported as covered on the same run that files a gap saying the search established nothing\n%s", slType, res)
	}
}

// TestServiceListIsOffWithoutALister pins the default: a run with no
// lister behaves exactly as before the leg existed. With a tag index that
// answers with nothing, that is #293's fallback finding nothing and the
// instance going unbound.
func TestServiceListIsOffWithoutALister(t *testing.T) {
	tagSrv := &taggingServer{}
	tagServer := tagSrv.start(t)
	defer tagServer.Close()

	reader := slReader(map[string]string{TagEstate: estateName, TagAddress: slAddr})
	res, diags := slFixture(t, nil, reader, tagServer.URL, false)
	if diags.HasErrors() {
		t.Fatalf("errors:\n%s\n%v", res, diags.Err())
	}
	if _, ok := res.BindingFor(mustAddr(t, slAddr)); ok {
		t.Fatalf("%s bound with no lister configured:\n%s", slAddr, res)
	}
	if reader.calls != 0 {
		t.Errorf("the tag reader was called %d time(s) with nothing listed, want 0", reader.calls)
	}
	scan, ok := res.ScanFor(slType)
	if !ok || scan.Source != SourceTagging {
		t.Errorf("scan = %+v (found=%v), want the #293 tag-index fallback row", scan, ok)
	}
}
