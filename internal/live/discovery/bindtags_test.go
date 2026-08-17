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

func renderProblems(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.String() + "\n")
	}
	return b.String()
}
