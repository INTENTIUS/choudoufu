// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// GitHub issue #266. See bindtags.go.
//
// Every test here strips the tags off a live object the fake cloud otherwise
// serves fully marked, which is what a tag-losing list operation
// (iam:ListRoles, ssm:DescribeParameters) does to a real one. The estate's
// tag index still holds the marker, because the resource really does carry
// it - the list call is the only thing that lost it.

// stripTags empties one live object's tag map, leaving the object listable
// and its tags attribute present-but-empty. That is precisely what the AWS
// provider hands back for aws_iam_role: not an untaggable type, not a
// missing object, an object whose marker the list call did not return.
//
// It also turns the type's server-side estate filter off. That is not
// scenery either: a provider that can filter a list by tag is a provider
// whose list has the tags, so the population #266 is about - a type whose
// list drops them - is exactly the population that filters client-side.
// aws_iam_role has no filter block; aws_vpc, the e2e fixture's control, has
// one, which is half of why the control bound and the subject did not.
func stripTags(t *testing.T, cloud *fakeCloud, typeName, id string) {
	t.Helper()
	cloud.noFilter(typeName)
	for _, o := range cloud.objects[typeName] {
		if o.id == id {
			o.tags = map[string]string{}
			return
		}
	}
	t.Fatalf("no %s %q in the fake cloud to strip", typeName, id)
}

// taggedARN is one entry for [taggingServer], written the way the Tagging
// API returns it.
func taggedARN(srv *taggingServer, arn string, tags map[string]string) {
	srv.arns = append(srv.arns, arn)
	if srv.tags == nil {
		srv.tags = map[string]map[string]string{}
	}
	srv.tags[arn] = tags
}

func markedARN(srv *taggingServer, arn, address string) {
	taggedARN(srv, arn, map[string]string{TagEstate: estateName, TagAddress: address})
}

// taggingRequest is a Request with only the Tagging client set: no
// TaggingSweep, no Roster, no Sweep. The join is not the sweep and does not
// wait for it, and a test that turned the sweep on could not tell which of
// the two bound the resource.
func taggingRequest(t *testing.T, srv *taggingServer) Request {
	t.Helper()
	server := srv.start(t)
	t.Cleanup(server.Close)
	return Request{Tagging: cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL})}
}

func problemsOfKind(res *Result, kind ProblemKind) []Problem {
	var out []Problem
	for _, p := range res.Problems {
		if p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

// TestTagJoinBindsAnObjectWhoseListCallDroppedItsTags is #266's whole point,
// at unit scale: the object is on the table, its marker is not on the
// object, and the instance binds anyway.
func TestTagJoinBindsAnObjectWhoseListCallDroppedItsTags(t *testing.T) {
	cloud := newFakeCloud()
	want := ownAllDiscovered(cloud)
	stripTags(t, cloud, "aws_vpc", "vpc-1")

	srv := &taggingServer{}
	markedARN(srv, "arn:aws:ec2:us-east-1:000000000000:vpc/vpc-1", `aws_vpc.main`)

	res, diags := discoverFixture(t, cloud, taggingRequest(t, srv))
	assertNoErrors(t, diags)
	assertBound(t, res, want)

	if srv.calls != 1 {
		t.Errorf("GetResources was called %d times, want exactly 1", srv.calls)
	}
	scan, ok := res.ScanFor("aws_vpc")
	if !ok || scan.Joined != 1 {
		t.Errorf("aws_vpc scan = %+v, want Joined=1", scan)
	}
	if len(res.Unbound) != 0 {
		t.Errorf("unbound: %v, want none", res.Unbound)
	}
	if len(res.Unclaimed) != 0 {
		t.Errorf("the stripped object is still reported as foreign: %v", res.Unclaimed)
	}
}

// TestTagJoinNormalizesTheSSMLeadingSlash is the edge the scouting slot
// named. An SSM parameter's TF import ID keeps the leading slash of its
// hierarchical name; the ARN's "parameter/" prefix has eaten it. Without the
// normalization the two spellings never meet and the join silently does
// nothing, which looks exactly like the tag index not holding the resource.
func TestTagJoinNormalizesTheSSMLeadingSlash(t *testing.T) {
	const arn = "arn:aws:ssm:us-east-1:000000000000:parameter//db/password"

	a, ok := cloudcontrol.ParseARN(arn)
	if !ok {
		t.Fatalf("ParseARN(%q) failed", arn)
	}
	// The premise, stated rather than assumed: the ARN's resource-id
	// segment really does differ from the import ID by exactly one leading
	// slash. If ParseARN ever splits this differently, the normalization
	// below is answering a question nobody asked.
	if a.ResourceID != "/db/password" {
		t.Fatalf("ARN resource id = %q, want /db/password - re-read markerJoinKey before changing it", a.ResourceID)
	}

	objs, byKey := indexTagged([]cloudcontrol.TaggedResource{{
		ResourceARN: arn,
		Tags:        map[string]string{TagEstate: estateName, TagAddress: `aws_ssm_parameter.db`},
	}})
	idx := &markerIndex{estate: estateName, objs: objs, byKey: byKey}
	idx.once.Do(func() {}) // already fetched, as far as resources() is concerned

	for _, importID := range []string{"/db/password", "db/password"} {
		tags, outcome := idx.join(context.Background(), "aws_ssm_parameter", importID)
		if outcome != joinBound {
			t.Errorf("join(aws_ssm_parameter, %q) = %v, want joinBound", importID, outcome)
			continue
		}
		if tags[TagAddress] != `aws_ssm_parameter.db` {
			t.Errorf("join(%q) returned tags %v", importID, tags)
		}
	}

	// And it does not reach across a name that merely looks similar.
	if _, outcome := idx.join(context.Background(), "aws_ssm_parameter", "db/password/extra"); outcome != joinNone {
		t.Errorf("join of an unrelated name = %v, want joinNone", outcome)
	}
}

// TestTagJoinRefusesACrossTypeMatch is the gate that keeps the join from
// adopting somebody else's object. Two resources of different types may
// share an identifier; a marker names the resource it is written on, so a
// marker naming another type is proof this is not the same object.
//
// The refusal has to be silent about markers, too: filing a malformed-marker
// problem here would turn a correct refusal into an error on a run where
// nothing is wrong.
func TestTagJoinRefusesACrossTypeMatch(t *testing.T) {
	cloud := newFakeCloud()
	ownAllDiscovered(cloud)
	stripTags(t, cloud, "aws_vpc", "vpc-1")

	srv := &taggingServer{}
	// Same identifier, a marker naming a different type. Binding this would
	// attach a subnet's ownership record to a VPC.
	markedARN(srv, "arn:aws:ec2:us-east-1:000000000000:subnet/vpc-1", `aws_subnet.this:a`)

	res, diags := discoverFixture(t, cloud, taggingRequest(t, srv))

	if diags.HasErrors() {
		t.Fatalf("a refused join produced errors:\n%s", renderDiags(diags))
	}
	if len(problemsOfKind(res, ProblemMalformedMarker)) != 0 {
		t.Errorf("a refused cross-type join filed a malformed-marker problem:\n%s", res)
	}
	if _, bound := res.BindingFor(mustAddr(t, `aws_vpc.main`)); bound {
		t.Fatalf("aws_vpc.main bound to an object carrying another type's marker:\n%s", res)
	}
	scan, _ := res.ScanFor("aws_vpc")
	if scan.Joined != 0 {
		t.Errorf("aws_vpc scan Joined=%d, want 0", scan.Joined)
	}

	// And the run says so rather than proposing a create in silence.
	found := problemsOfKind(res, ProblemUnreadableMarker)
	if len(found) != 1 {
		t.Fatalf("want exactly one UNREADABLE_MARKER finding, got %d:\n%s", len(found), res)
	}
	if got := found[0].Addr.String(); got != `aws_vpc.main` {
		t.Errorf("the finding names %s, want aws_vpc.main", got)
	}
	if found[0].Kind.Severity() != SeverityWarning {
		t.Error("the finding is an error; it must not refuse a run that may be a perfectly ordinary create")
	}
}

// TestTagJoinRefusesAnAmbiguousMatch: two tagged resources of the right type
// share the identifier. Nothing says which was listed, so nothing binds and
// both are named.
func TestTagJoinRefusesAnAmbiguousMatch(t *testing.T) {
	cloud := newFakeCloud()
	ownAllDiscovered(cloud)
	stripTags(t, cloud, "aws_vpc", "vpc-1")

	srv := &taggingServer{}
	markedARN(srv, "arn:aws:ec2:us-east-1:000000000000:vpc/vpc-1", `aws_vpc.main`)
	markedARN(srv, "arn:aws:ec2:us-west-2:000000000000:vpc/vpc-1", `aws_vpc.main`)

	res, diags := discoverFixture(t, cloud, taggingRequest(t, srv))

	if _, bound := res.BindingFor(mustAddr(t, `aws_vpc.main`)); bound {
		t.Fatalf("aws_vpc.main bound despite two candidates:\n%s", res)
	}
	amb := problemsOfKind(res, ProblemAmbiguousTagJoin)
	if len(amb) != 1 {
		t.Fatalf("want exactly one AMBIGUOUS_TAG_JOIN, got %d:\n%s", len(amb), res)
	}
	for _, want := range []string{"us-east-1", "us-west-2"} {
		if !strings.Contains(amb[0].Detail, want) {
			t.Errorf("the problem does not name the %s candidate: %s", want, amb[0].Detail)
		}
	}
	if !diags.HasErrors() {
		t.Error("an ambiguous join did not produce an error; it must not be planned over")
	}
}

// TestTagJoinBindsAMemberOfACountBlock. The join runs at scan time, before
// anything knows a resource is part of a set, so a count block's members go
// through it exactly like a plain instance. Worth pinning because
// unreadableMarkerProblem's own reach stops short of count blocks - the
// finding does, the join does not, and those are easy to conflate.
func TestTagJoinBindsAMemberOfACountBlock(t *testing.T) {
	cloud := newFakeCloud()
	want := ownAllDiscovered(cloud)
	stripTags(t, cloud, "aws_eip", "eipalloc-1")

	srv := &taggingServer{}
	markedARN(srv, "arn:aws:ec2:us-east-1:000000000000:eip-allocation/eipalloc-1", `aws_eip.pool:1`)

	res, diags := discoverFixture(t, cloud, taggingRequest(t, srv))
	assertNoErrors(t, diags)
	assertBound(t, res, want)

	scan, _ := res.ScanFor("aws_eip")
	if scan.Joined != 1 {
		t.Errorf("aws_eip scan Joined=%d, want 1", scan.Joined)
	}
}

// TestTagJoinTreatsARepeatedARNAsOneObject. GetResources paginates and a
// paginated list can repeat an entry, which must not read as two resources
// claiming one identifier. The repeat is deliberately not adjacent: a dedup
// that only looked at the previous match would pass the adjacent case and
// fail this one.
func TestTagJoinTreatsARepeatedARNAsOneObject(t *testing.T) {
	const arn = "arn:aws:ec2:us-east-1:000000000000:vpc/vpc-1"
	tags := map[string]string{TagEstate: estateName, TagAddress: `aws_vpc.main`}

	objs, byKey := indexTagged([]cloudcontrol.TaggedResource{
		{ResourceARN: arn, Tags: tags},
		{ResourceARN: "arn:aws:ec2:us-east-1:000000000000:vpc/vpc-9", Tags: map[string]string{
			TagEstate: estateName, TagAddress: `aws_vpc.other`,
		}},
		{ResourceARN: arn, Tags: tags},
	})
	idx := &markerIndex{estate: estateName, objs: objs, byKey: byKey}
	idx.once.Do(func() {})

	got, outcome := idx.join(context.Background(), "aws_vpc", "vpc-1")
	if outcome != joinBound {
		t.Fatalf("join = %v, want joinBound - a repeated ARN is one object", outcome)
	}
	if got[TagAddress] != `aws_vpc.main` {
		t.Errorf("join returned tags %v", got)
	}
}

// TestTagJoinIgnoresAnotherEstatesMarker: the GetResources call is filtered
// server-side, but a filter is a request and the index is the authority on
// what it holds.
func TestTagJoinIgnoresAnotherEstatesMarker(t *testing.T) {
	cloud := newFakeCloud()
	ownAllDiscovered(cloud)
	stripTags(t, cloud, "aws_vpc", "vpc-1")

	srv := &taggingServer{}
	taggedARN(srv, "arn:aws:ec2:us-east-1:000000000000:vpc/vpc-1", map[string]string{
		TagEstate:  "somebody-else",
		TagAddress: `aws_vpc.main`,
	})

	res, _ := discoverFixture(t, cloud, taggingRequest(t, srv))
	if _, bound := res.BindingFor(mustAddr(t, `aws_vpc.main`)); bound {
		t.Fatalf("aws_vpc.main bound to another estate's resource:\n%s", res)
	}
}

// TestTagJoinNeverOverridesAnObjectsOwnTags is the direction a stale index
// could do damage in: the listed object told the truth about itself, and a
// tag index minutes behind reality must not be allowed to contradict it.
func TestTagJoinNeverOverridesAnObjectsOwnTags(t *testing.T) {
	cloud := newFakeCloud()
	want := ownAllDiscovered(cloud)
	// vpc-1's own tags are intact and say aws_vpc.main.

	srv := &taggingServer{}
	// The index disagrees, naming an address the fixture also declares.
	markedARN(srv, "arn:aws:ec2:us-east-1:000000000000:vpc/vpc-1", `aws_vpc.other`)

	res, diags := discoverFixture(t, cloud, taggingRequest(t, srv))
	assertNoErrors(t, diags)
	assertBound(t, res, want)
	if srv.calls != 0 {
		t.Errorf("GetResources was called %d times for a run where every object carried its own marker; the fetch is meant to be lazy", srv.calls)
	}
}

// TestTagJoinFailsClosedWhenTheIndexIsUnavailable pins the degradation.
// TOFU_LIVE_CLOUDCONTROL=off leaves Request.Tagging nil, and a real
// account's tag index can lag a write by minutes; both must leave the run
// exactly as it was before #266, plus a finding.
func TestTagJoinFailsClosedWhenTheIndexIsUnavailable(t *testing.T) {
	cloud := newFakeCloud()
	ownAllDiscovered(cloud)
	stripTags(t, cloud, "aws_vpc", "vpc-1")

	res, diags := discoverFixture(t, cloud, Request{}) // no Tagging client at all
	if diags.HasErrors() {
		t.Fatalf("a run with no tag index errored:\n%s", renderDiags(diags))
	}
	if _, bound := res.BindingFor(mustAddr(t, `aws_vpc.main`)); bound {
		t.Fatalf("aws_vpc.main bound with no tag index to bind it from:\n%s", res)
	}

	found := problemsOfKind(res, ProblemUnreadableMarker)
	if len(found) != 1 {
		t.Fatalf("want exactly one UNREADABLE_MARKER finding, got %d:\n%s", len(found), res)
	}
	if !strings.Contains(found[0].Detail, "could not be consulted") {
		t.Errorf("the finding does not say the index was unavailable: %s", found[0].Detail)
	}
}

// TestUnreadableMarkerIsSilentForAGenuineGreenfieldInstance is the other
// half, and the one that decides whether the finding is usable: an estate
// standing up for the first time has unbound instances everywhere and no
// unreadable objects, and must say nothing.
func TestUnreadableMarkerIsSilentForAGenuineGreenfieldInstance(t *testing.T) {
	cloud := newFakeCloud() // nothing live at all
	res, diags := discoverFixture(t, cloud, Request{})
	if diags.HasErrors() {
		t.Fatalf("a greenfield run errored:\n%s", renderDiags(diags))
	}
	if len(res.Unbound) != len(allDiscovered) {
		t.Fatalf("unbound %d instances, want all %d", len(res.Unbound), len(allDiscovered))
	}
	if found := problemsOfKind(res, ProblemUnreadableMarker); len(found) != 0 {
		t.Errorf("a greenfield run filed %d UNREADABLE_MARKER findings:\n%s", len(found), renderProblems(found))
	}
}

// TestUnreadableMarkerNamesTheIndexedResourceWhenItHasOne is the strong form
// of the finding: the tag index holds a resource marked for this exact
// address, so "the plan will create a duplicate" stops being a possibility
// and becomes a statement.
func TestUnreadableMarkerNamesTheIndexedResourceWhenItHasOne(t *testing.T) {
	cloud := newFakeCloud()
	ownAllDiscovered(cloud)
	stripTags(t, cloud, "aws_vpc", "vpc-1")

	srv := &taggingServer{}
	// Marked for the right address, but under an identifier no listed
	// object has - which is what an identifier normalization this join does
	// not know about would look like.
	markedARN(srv, "arn:aws:ec2:us-east-1:000000000000:vpc/vpc-renamed", `aws_vpc.main`)

	res, _ := discoverFixture(t, cloud, taggingRequest(t, srv))

	found := problemsOfKind(res, ProblemUnreadableMarker)
	if len(found) != 1 {
		t.Fatalf("want exactly one UNREADABLE_MARKER finding, got %d:\n%s", len(found), res)
	}
	if len(found[0].LiveIDs) != 1 || !strings.Contains(found[0].LiveIDs[0], "vpc-renamed") {
		t.Errorf("the finding does not name the indexed resource: %v", found[0].LiveIDs)
	}
	if !strings.Contains(found[0].Detail, "already carries the ownership marker") {
		t.Errorf("the finding hedges where the index settled it: %s", found[0].Detail)
	}
}

// TestJoinMarkerFromTagging_BindsAnObjectFoundByLiveMv is [JoinMarkerFromTagging]'s
// own proof, at unit scale: choudoufu live-mv's sweep (internal/live/mv)
// lists a type directly rather than running a full [Discover] pass, so it
// never gets #266's join for free the way an ordinary plan does - this
// exported wrapper is what closes that gap, found empirically renaming
// aws_iam_policy against a real emulator (iam:ListPolicies drops tags the
// same way iam:ListRoles does). Proven load-bearing three ways: a real
// match binds, a nil client (Cloud Control fallback off) finds nothing
// rather than panicking, and an ambiguous match - two tagged resources of
// this type answering to the same identifier - is reported as not found
// rather than picked at random.
func TestJoinMarkerFromTagging_BindsAnObjectFoundByLiveMv(t *testing.T) {
	srv := &taggingServer{}
	markedARN(srv, "arn:aws:iam::000000000000:policy/example_from_data_source", "module.iam_policy_from_data_source.aws_iam_policy.policy:0")
	server := srv.start(t)
	t.Cleanup(server.Close)
	tagging := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL})

	tags, ok := JoinMarkerFromTagging(context.Background(), tagging, estateName, "aws_iam_policy", "arn:aws:iam::000000000000:policy/example_from_data_source")
	if !ok {
		t.Fatalf("JoinMarkerFromTagging() did not bind a resource the tag index carries")
	}
	if got := tags[TagAddress]; got != "module.iam_policy_from_data_source.aws_iam_policy.policy:0" {
		t.Errorf("joined tofu-address = %q, want the marker the index carries", got)
	}
	if srv.calls != 1 {
		t.Errorf("GetResources was called %d times, want exactly 1", srv.calls)
	}

	if _, ok := JoinMarkerFromTagging(context.Background(), nil, estateName, "aws_iam_policy", "arn:aws:iam::000000000000:policy/example_from_data_source"); ok {
		t.Errorf("a nil Tagging client bound a resource; it must degrade to not-found the way an ordinary discovery pass does with no client")
	}

	// A second tagged resource of the same type answering to the same
	// identifier - not a shape this test's ARN scheme can hit legitimately,
	// but markerJoinKeys indexes by resource-id segment too, so two ARNs
	// that share one are enough to force it.
	ambSrv := &taggingServer{}
	markedARN(ambSrv, "arn:aws:iam::000000000000:policy/dup", "aws_iam_policy.a")
	markedARN(ambSrv, "arn:aws:iam::111111111111:policy/dup", "aws_iam_policy.b")
	ambServer := ambSrv.start(t)
	t.Cleanup(ambServer.Close)
	ambTagging := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: ambServer.URL})
	if _, ok := JoinMarkerFromTagging(context.Background(), ambTagging, estateName, "aws_iam_policy", "dup"); ok {
		t.Errorf("an ambiguous match (two resources answering to the same identifier) bound one at random; it must report not-found")
	}
}

// TestMarkerJoinKeysCoverBothSpellings pins what the index is keyed on. An
// ARN whose type IS its identity has to be reachable by the whole ARN, and
// every other type by its resource-id segment.
func TestMarkerJoinKeysCoverBothSpellings(t *testing.T) {
	const arn = "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/main/50dc6c495c0c9188"
	keys := markerJoinKeys(arn)
	if len(keys) != 2 {
		t.Fatalf("markerJoinKeys(%q) = %v, want the ARN and its resource id", arn, keys)
	}
	if keys[0] != arn {
		t.Errorf("first key = %q, want the ARN itself", keys[0])
	}
	if keys[1] != "app/main/50dc6c495c0c9188" {
		t.Errorf("second key = %q, want the resource-id segment", keys[1])
	}

	// An ARN with no resource-type segment at all - an S3 bucket - is keyed
	// by both, because "my-bucket" IS aws_s3_bucket's import ID.
	if got := markerJoinKeys("arn:aws:s3:::my-bucket"); len(got) != 2 || got[1] != "my-bucket" {
		t.Errorf("markerJoinKeys for a bare-resource ARN = %v, want the ARN and my-bucket", got)
	}
	// A string that is not an ARN is still keyed by itself rather than
	// dropped, so a malformed entry in the index cannot make the whole
	// lookup silently miss.
	if got := markerJoinKeys("not-an-arn"); len(got) != 1 || got[0] != "not-an-arn" {
		t.Errorf("markerJoinKeys(not-an-arn) = %v", got)
	}
}

// TestMarkerJoinKeysAddsIAMBareNameForAPathedEntity is the
// corpus-autoscaling-complete regression: an IAM role or instance profile
// built under a non-default Path (module.complete's aws_iam_role and
// aws_iam_instance_profile, both name_prefix'd under "/ec2/") imports by
// its bare NAME, never by "PATH/NAME" - but that is exactly the string
// [cloudcontrol.ParseARN] puts in ResourceID for a "type/PATH/NAME" ARN.
// Without a third key keyed on the trailing segment, a stateless replan's
// tag-index join for such an object always misses (joinNone, no log line,
// no diagnostic) and the plan proposes creating an object that already
// exists.
func TestMarkerJoinKeysAddsIAMBareNameForAPathedEntity(t *testing.T) {
	const arn = "arn:aws:iam::000000000000:role/ec2/complete-6020beeaa727eb5cf8845f0942"
	keys := markerJoinKeys(arn)
	want := []string{arn, "ec2/complete-6020beeaa727eb5cf8845f0942", "complete-6020beeaa727eb5cf8845f0942"}
	if len(keys) != len(want) {
		t.Fatalf("markerJoinKeys(%q) = %v, want %v", arn, keys, want)
	}
	for i, w := range want {
		if keys[i] != w {
			t.Errorf("key %d = %q, want %q", i, keys[i], w)
		}
	}

	// A root-path IAM entity has no path segment to strip, so the
	// resource-id key and the bare-name key already coincide - no third,
	// redundant key.
	const rootARN = "arn:aws:iam::000000000000:role/complete"
	if got := markerJoinKeys(rootARN); len(got) != 2 {
		t.Errorf("markerJoinKeys(%q) = %v, want exactly 2 keys (no duplicate)", rootARN, got)
	}

	// A non-IAM ARN with a "/"-bearing resource-id (ELBv2's own shape,
	// covered above) must not gain this third key: the trailing-segment
	// guess is scoped to IAM's own documented Path convention and nothing
	// else.
	const elb = "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/main/50dc6c495c0c9188"
	if got := markerJoinKeys(elb); len(got) != 2 {
		t.Errorf("markerJoinKeys(%q) = %v, want exactly 2 keys - the IAM-only rule must not fire here", elb, got)
	}
}

// TestScanTypeMarkerFallbackBindsAnUnlistableTaggableType is issue #293 at
// unit scale: aws_route_table has no list route at all in this fake cloud
// (the shape aws_wafv2_web_acl and aws_iam_service_linked_role are in for
// real, neither native nor Cloud Control), and the estate's tag index -
// #266's same [markerIndex], one GetResources call - still finds the
// declared instance by its own marker. The ordinary refusal
// (ProblemTypeNotListable) must not fire.
func TestScanTypeMarkerFallbackBindsAnUnlistableTaggableType(t *testing.T) {
	cloud := newFakeCloud()
	want := ownAllDiscovered(cloud)
	cloud.unlistable("aws_route_table")

	srv := &taggingServer{}
	markedARN(srv, "arn:aws:ec2:us-east-1:000000000000:route-table/rtb-1", `aws_route_table.main`)

	res, diags := discoverFixture(t, cloud, taggingRequest(t, srv))
	assertNoErrors(t, diags)
	assertBound(t, res, want)

	if problems := problemsOfKind(res, ProblemTypeNotListable); len(problems) != 0 {
		t.Errorf("aws_route_table still refused as not-listable:\n%s", renderProblems(problems))
	}
	scan, ok := res.ScanFor("aws_route_table")
	if !ok || scan.Source != SourceTagging || scan.Listed != 1 {
		t.Errorf("aws_route_table scan = %+v, want Source=%s Listed=1", scan, SourceTagging)
	}
}

// TestScanTypeMarkerFallbackLeavesAnUntaggableTypeRefused is the other half
// of #293's gate: an unlistable type with no tags attribute at all could
// never have carried a marker, so the tag index being available changes
// nothing and the caller's original refusal must stand, unweakened, exactly
// as it did before this fallback existed.
func TestScanTypeMarkerFallbackLeavesAnUntaggableTypeRefused(t *testing.T) {
	cloud := newFakeCloud()
	ownAllDiscovered(cloud)
	cloud.unlistable("aws_route_table")
	cloud.untagged["aws_route_table"] = true

	srv := &taggingServer{}
	markedARN(srv, "arn:aws:ec2:us-east-1:000000000000:route-table/rtb-1", `aws_route_table.main`)

	res, diags := discoverFixture(t, cloud, taggingRequest(t, srv))
	if !diags.HasErrors() {
		t.Fatalf("an untaggable unlistable type produced no error, even with a marker sitting in the tag index:\n%s", res)
	}
	problems := problemsOfKind(res, ProblemTypeNotListable)
	if len(problems) != 1 || problems[0].TypeName != "aws_route_table" {
		t.Fatalf("want one type-not-listable problem for aws_route_table:\n%s", res)
	}
	if _, ok := res.BindingFor(mustAddr(t, `aws_route_table.main`)); ok {
		t.Error("an untaggable type must never be bound through the tag index")
	}
}

func renderProblems(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.String() + "\n")
	}
	return b.String()
}
