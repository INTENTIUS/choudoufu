// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs"
	"github.com/opentofu/opentofu/internal/configs/configschema"
	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/stateless/flocitest"
	"github.com/opentofu/opentofu/internal/stateless/identity"
	"github.com/opentofu/opentofu/internal/tfdiags"
)

// estateDir is the P0.1 fixture, and every test in this file discovers
// against its real declared addresses: two for_each subnets whose addresses
// only survive the tag round trip because of the escaping rule, and three
// count instances of aws_eip.
// It is resolved from the repository root, not from this package's
// working directory.
func estateDir(t *testing.T) string {
	return flocitest.EstateDir(t)
}

const estateName = "stateless-e2e"

// The nine needs-discovery instances the fixture declares.
var allDiscovered = []string{
	`aws_eip.pool[0]`,
	`aws_eip.pool[1]`,
	`aws_eip.pool[2]`,
	`aws_internet_gateway.main`,
	`aws_route_table.main`,
	`aws_security_group.main`,
	`aws_subnet.this["a"]`,
	`aws_subnet.this["b"]`,
	`aws_vpc.main`,
}

// ---------------------------------------------------------------------------
// The escaping rule
// ---------------------------------------------------------------------------

func TestEscapeAddress(t *testing.T) {
	// The table in stateless/MARKERS.md, verbatim, plus idempotence.
	cases := []struct{ in, want string }{
		{`aws_vpc.this`, `aws_vpc.this`},
		{`aws_subnet.this["a"]`, `aws_subnet.this:a`},
		{`aws_eip.this[2]`, `aws_eip.this:2`},
		{`module.subnets["a"].aws_subnet.this`, `module.subnets:a.aws_subnet.this`},
	}
	for _, c := range cases {
		if got := EscapeAddress(c.in); got != c.want {
			t.Errorf("EscapeAddress(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := EscapeAddress(c.want); got != c.want {
			t.Errorf("EscapeAddress is not idempotent: EscapeAddress(%q) = %q", c.want, got)
		}
	}
}

func TestValidMarkerAddress(t *testing.T) {
	valid := []string{
		`aws_vpc.this`,
		`aws_subnet.this:a`,
		`aws_eip.this:2`,
		`module.subnets:a.aws_subnet.this`,
		`aws_subnet.this:us-east-1a`,
	}
	for _, s := range valid {
		if !ValidMarkerAddress(s) {
			t.Errorf("ValidMarkerAddress(%q) = false, want true", s)
		}
	}
	invalid := []string{
		``,
		`aws_vpc`,
		`not an address`,
		`:2`,
		`aws_subnet.this:a:b`,
		strings.Repeat("a", 130) + "." + strings.Repeat("b", 130),
	}
	for _, s := range invalid {
		if ValidMarkerAddress(s) {
			t.Errorf("ValidMarkerAddress(%q) = true, want false", s)
		}
	}
}

func TestValidEstateName(t *testing.T) {
	for _, s := range []string{"stateless-e2e", "a", "e2e-1"} {
		if !ValidEstateName(s) {
			t.Errorf("ValidEstateName(%q) = false", s)
		}
	}
	for _, s := range []string{"", "Stateless", "1abc", "has_underscore", "has.dot"} {
		if ValidEstateName(s) {
			t.Errorf("ValidEstateName(%q) = true", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Binding
// ---------------------------------------------------------------------------

// TestDiscoverBindsWholeEstate is the happy path over the real fixture: nine
// server-assigned instances, nine live resources carrying spec-conformant
// markers, nine bindings and nothing else.
func TestDiscoverBindsWholeEstate(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.own("aws_subnet", "subnet-a", `aws_subnet.this:a`)
	cloud.own("aws_subnet", "subnet-b", `aws_subnet.this:b`)
	cloud.own("aws_security_group", "sg-1", `aws_security_group.main`)
	cloud.own("aws_route_table", "rtb-1", `aws_route_table.main`)
	cloud.own("aws_internet_gateway", "igw-1", `aws_internet_gateway.main`)
	cloud.own("aws_eip", "eipalloc-0", `aws_eip.pool:0`)
	cloud.own("aws_eip", "eipalloc-1", `aws_eip.pool:1`)
	cloud.own("aws_eip", "eipalloc-2", `aws_eip.pool:2`)

	res, diags := discoverFixture(t, cloud, Request{})
	assertNoErrors(t, diags)

	assertBound(t, res, map[string]string{
		`aws_vpc.main`:              "vpc-1",
		`aws_subnet.this["a"]`:      "subnet-a",
		`aws_subnet.this["b"]`:      "subnet-b",
		`aws_security_group.main`:   "sg-1",
		`aws_route_table.main`:      "rtb-1",
		`aws_internet_gateway.main`: "igw-1",
		`aws_eip.pool[0]`:           "eipalloc-0",
		`aws_eip.pool[1]`:           "eipalloc-1",
		`aws_eip.pool[2]`:           "eipalloc-2",
	})
	if len(res.Unbound) != 0 {
		t.Errorf("unbound instances: %v", res.Unbound)
	}
	if len(res.Problems) != 0 {
		t.Errorf("problems:\n%s", res)
	}

	// The output resolution list is the whole estate: the nine discovered
	// instances are now concrete, and everything static rides through.
	byAddr := map[string]identity.Resolution{}
	for _, r := range res.Resolutions {
		byAddr[r.Addr.String()] = r
	}
	if got := len(res.Resolutions); got != 22 {
		t.Errorf("Resolutions holds %d entries, want the fixture's 22", got)
	}
	for _, addr := range allDiscovered {
		r, ok := byAddr[addr]
		if !ok {
			t.Errorf("%s is missing from the merged resolutions", addr)
			continue
		}
		if r.Class != identity.ClassConcrete || r.ImportID == "" {
			t.Errorf("%s is %s in the merged resolutions, want CONCRETE with an import ID", addr, r)
		}
	}
	if r := byAddr[`aws_s3_bucket.data`]; r.Class != identity.ClassConcrete || r.ImportID != "tofu-stateless-e2e-data" {
		t.Errorf("a statically resolved instance was rewritten: %s", r)
	}
	if r := byAddr[`aws_route.internet_gateway`]; r.Class != identity.ClassParentDerived {
		t.Errorf("the parent-derived route was rewritten: %s", r)
	}
}

// TestDiscoverEscapedComparison is the keyed-address claim on its own: a
// for_each key only survives as a tag because of the escaping rule, and
// binding compares escaped-to-escaped rather than decoding anything.
func TestDiscoverEscapedComparison(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_subnet", "subnet-a", `aws_subnet.this:a`)
	// A tag value that was never escaped, the way the P0.1 fixture writes
	// count instances. It binds, and says it had to be normalized.
	cloud.own("aws_subnet", "subnet-b", `aws_subnet.this["b"]`)

	res, diags := discoverFixture(t, cloud, Request{})
	assertNoErrors(t, diags)

	a, ok := res.BindingFor(mustAddr(t, `aws_subnet.this["a"]`))
	if !ok {
		t.Fatalf("the escaped marker did not bind:\n%s", res)
	}
	if a.Normalized {
		t.Errorf("a spec-conformant marker was reported as normalized: %s", a)
	}
	b, ok := res.BindingFor(mustAddr(t, `aws_subnet.this["b"]`))
	if !ok {
		t.Fatalf("the unescaped marker did not bind:\n%s", res)
	}
	if !b.Normalized {
		t.Errorf("an unescaped marker bound without being reported as normalized: %s", b)
	}
	if b.Marker != `aws_subnet.this["b"]` {
		t.Errorf("the binding does not carry the marker as written: %q", b.Marker)
	}
}

// TestDiscoverUnboundIsNotAnError: a declared address nothing claims means
// the resource does not exist. The plan proposing a create is the right
// answer, so this is reported, not diagnosed.
func TestDiscoverUnboundIsNotAnError(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)

	res, diags := discoverFixture(t, cloud, Request{})
	assertNoErrors(t, diags)

	if len(res.Bindings) != 1 {
		t.Fatalf("want exactly the VPC bound:\n%s", res)
	}
	var got []string
	for _, a := range res.Unbound {
		got = append(got, a.String())
	}
	want := append([]string(nil), allDiscovered...)
	want = removeString(want, `aws_vpc.main`)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("unbound\n %v\nwant\n %v", got, want)
	}
	// An unbound instance must stay needs-discovery, so the projection
	// still records it as absent for the right reason.
	for _, r := range res.Resolutions {
		if r.Addr.String() == `aws_subnet.this["a"]` && r.Class != identity.ClassNeedsDiscovery {
			t.Errorf("an unbound instance was rewritten: %s", r)
		}
	}
}

// TestDiscoverWrongEstateIgnored: another estate's resources are not
// unclaimed, not foreign, not reported - they are none of this estate's
// business.
func TestDiscoverWrongEstateIgnored(t *testing.T) {
	cloud := newFakeCloud()
	cloud.obj("aws_vpc", "vpc-other", map[string]string{
		TagEstate:  "some-other-estate",
		TagAddress: `aws_vpc.main`,
	})

	res, diags := discoverFixture(t, cloud, Request{CollectUnclaimed: true})
	assertNoErrors(t, diags)

	if len(res.Bindings) != 0 {
		t.Errorf("another estate's resource was bound:\n%s", res)
	}
	if len(res.Unclaimed) != 0 {
		t.Errorf("another estate's resource was reported as unclaimed:\n%s", res)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("another estate's resource was reported as an orphan:\n%s", res)
	}
	scan, _ := res.ScanFor("aws_vpc")
	if scan.OtherEstate != 1 {
		t.Errorf("the scan does not record the ignored resource: %s", scan)
	}
	if !containsAddr(res.Unbound, `aws_vpc.main`) {
		t.Errorf("aws_vpc.main should be unbound:\n%s", res)
	}
}

// TestDiscoverCollision: two live resources claiming one address is a named
// error naming both, never a guess.
func TestDiscoverCollision(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-2", `aws_vpc.main`)

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("a collision produced no error diagnostic:\n%s", res)
	}
	problems := res.ProblemsOfKind(ProblemCollision)
	if len(problems) != 1 {
		t.Fatalf("want one collision problem, got:\n%s", res)
	}
	p := problems[0]
	if strings.Join(p.LiveIDs, ",") != "vpc-1,vpc-2" {
		t.Errorf("the collision does not name both live resources: %v", p.LiveIDs)
	}
	if p.Addr.String() != `aws_vpc.main` {
		t.Errorf("the collision does not name the address: %s", p.Addr)
	}
	if _, bound := res.BindingFor(mustAddr(t, `aws_vpc.main`)); bound {
		t.Error("a collided address was bound anyway")
	}
	if !hasDiag(diags, "Two live resources claiming one address", "vpc-2") {
		t.Errorf("the diagnostic does not name the colliding resources:\n%s", renderDiags(diags))
	}
}

// TestDiscoverMalformedMarker: tofu-estate present, tofu-address missing or
// unparseable. Per the spec that is neither owned nor foreign, and it is a
// named error rather than a resource quietly treated as either.
func TestDiscoverMalformedMarker(t *testing.T) {
	for name, tags := range map[string]map[string]string{
		"missing":     {TagEstate: estateName},
		"empty":       {TagEstate: estateName, TagAddress: ""},
		"unparseable": {TagEstate: estateName, TagAddress: "not an address"},
		"bare":        {TagEstate: estateName, TagAddress: "aws_vpc"},
	} {
		t.Run(name, func(t *testing.T) {
			cloud := newFakeCloud()
			cloud.obj("aws_vpc", "vpc-bad", tags)

			res, diags := discoverFixture(t, cloud, Request{})
			if !diags.HasErrors() {
				t.Fatalf("a malformed marker produced no error:\n%s", res)
			}
			problems := res.ProblemsOfKind(ProblemMalformedMarker)
			if len(problems) != 1 {
				t.Fatalf("want one malformed-marker problem, got:\n%s", res)
			}
			if strings.Join(problems[0].LiveIDs, ",") != "vpc-bad" {
				t.Errorf("the problem does not name the live resource: %v", problems[0].LiveIDs)
			}
			if len(res.Unclaimed) != 0 || len(res.Orphans) != 0 {
				t.Errorf("a malformed resource was classified as something else:\n%s", res)
			}
		})
	}
}

// TestDiscoverUnclaimedCollected: resources with no estate tag are collected
// verbatim and left alone. P2.4 is what classifies them.
func TestDiscoverUnclaimedCollected(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.obj("aws_vpc", "vpc-default", nil)
	cloud.obj("aws_security_group", "sg-default", map[string]string{"Name": "default"})

	res, diags := discoverFixture(t, cloud, Request{CollectUnclaimed: true})
	assertNoErrors(t, diags)

	if len(res.Unclaimed) != 2 {
		t.Fatalf("want two unclaimed resources, got:\n%s", res)
	}
	got := map[string]UnclaimedResource{}
	for _, u := range res.Unclaimed {
		got[u.ImportID] = u
	}
	if u, ok := got["sg-default"]; !ok {
		t.Errorf("the untagged security group was not collected:\n%s", res)
	} else {
		if u.TypeName != "aws_security_group" {
			t.Errorf("unclaimed resource carries the wrong type: %s", u.TypeName)
		}
		if u.Tags["Name"] != "default" {
			t.Errorf("unclaimed resource lost its tags: %v", u.Tags)
		}
		if u.Resource == cty.NilVal {
			t.Error("unclaimed resource carries no object, so P2.4 would have to list again")
		}
		if u.IdentityAttr != "id" {
			t.Errorf("unclaimed resource does not say which attribute its ID came from: %q", u.IdentityAttr)
		}
	}
	if _, ok := got["vpc-default"]; !ok {
		t.Errorf("the untagged VPC was not collected:\n%s", res)
	}

	// Every scan widened, and says so.
	for _, s := range res.Scans {
		if s.Scope != ScopeAll {
			t.Errorf("%s scanned with scope %s, want ALL when unclaimed resources are wanted", s.TypeName, s.Scope)
		}
		if s.Filtering != FilterClientSide || s.FilterReason == "" {
			t.Errorf("%s does not explain its client-side filtering: %s", s.TypeName, s)
		}
	}
}

// TestDiscoverUnclaimedNotScannedByDefault: with the default server-side
// filter, unclaimed resources are invisible. The result has to say that
// nothing looked, rather than implying there are none.
func TestDiscoverUnclaimedNotScannedByDefault(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.obj("aws_vpc", "vpc-default", nil)

	res, diags := discoverFixture(t, cloud, Request{})
	assertNoErrors(t, diags)

	if len(res.Unclaimed) != 0 {
		t.Errorf("a server-side filtered scan reported unclaimed resources:\n%s", res)
	}
	scan, ok := res.ScanFor("aws_vpc")
	if !ok {
		t.Fatal("no scan recorded for aws_vpc")
	}
	if scan.Scope != ScopeEstate {
		t.Errorf("aws_vpc scan scope is %s, want ESTATE", scan.Scope)
	}
	if scan.Filtering != FilterServerSide {
		t.Errorf("aws_vpc was not filtered server-side: %s", scan)
	}
	if scan.Listed != 1 {
		t.Errorf("the server-side filter did not narrow the list: %s", scan)
	}
}

// TestDiscoverPushesTagFilter checks the request that actually went to the
// provider, because "we filtered server-side" is a claim about the wire.
func TestDiscoverPushesTagFilter(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)

	res, diags := discoverFixture(t, cloud, Request{Region: "us-east-1"})
	assertNoErrors(t, diags)
	_ = res

	req, ok := cloud.requestFor("aws_vpc")
	if !ok {
		t.Fatal("no list request was made for aws_vpc")
	}
	filters := req.Config.GetAttr("filter")
	if filters.IsNull() || filters.LengthInt() != 1 {
		t.Fatalf("aws_vpc was listed without a filter: %s", req.Config.GoString())
	}
	f := filters.Index(cty.NumberIntVal(0))
	if got := f.GetAttr("name").AsString(); got != "tag:tofu-estate" {
		t.Errorf("filter name is %q, want tag:tofu-estate", got)
	}
	if got := f.GetAttr("values").Index(cty.NumberIntVal(0)).AsString(); got != estateName {
		t.Errorf("filter value is %q, want %q", got, estateName)
	}
	if got := req.Config.GetAttr("region"); got.IsNull() || got.AsString() != "us-east-1" {
		t.Errorf("the region was not passed to the list call: %#v", got)
	}
	if !req.IncludeResourceObject {
		t.Error("the list did not ask for resource objects, so no tags would come back")
	}
}

// TestDiscoverClientSideFallback: a type whose list schema offers no filter
// argument is listed whole and filtered here, with the reason recorded.
func TestDiscoverClientSideFallback(t *testing.T) {
	cloud := newFakeCloud()
	cloud.noFilter("aws_eip") // as the real AWS provider's aws_eip list schema
	cloud.own("aws_eip", "eipalloc-0", `aws_eip.pool:0`)
	cloud.own("aws_eip", "eipalloc-1", `aws_eip.pool:1`)
	cloud.own("aws_eip", "eipalloc-2", `aws_eip.pool:2`)
	cloud.obj("aws_eip", "eipalloc-other", map[string]string{
		TagEstate:  "some-other-estate",
		TagAddress: `aws_eip.pool:0`,
	})
	cloud.obj("aws_eip", "eipalloc-wild", nil)

	res, diags := discoverFixture(t, cloud, Request{})
	assertNoErrors(t, diags)

	assertBound(t, res, map[string]string{
		`aws_eip.pool[0]`: "eipalloc-0",
		`aws_eip.pool[1]`: "eipalloc-1",
		`aws_eip.pool[2]`: "eipalloc-2",
	})

	scan, _ := res.ScanFor("aws_eip")
	if scan.Filtering != FilterClientSide {
		t.Errorf("aws_eip was not filtered client-side: %s", scan)
	}
	if !strings.Contains(scan.FilterReason, "filter") {
		t.Errorf("the fallback reason does not say what the schema lacks: %q", scan.FilterReason)
	}
	if scan.Listed != 5 || scan.OtherEstate != 1 || scan.Unclaimed != 1 {
		t.Errorf("the client-side scan miscounted: %s", scan)
	}
	// A client-side scan sees everything, so unclaimed resources are real
	// findings even without asking for them.
	if len(res.Unclaimed) != 1 || res.Unclaimed[0].ImportID != "eipalloc-wild" {
		t.Errorf("the untagged EIP was not collected:\n%s", res)
	}

	req, _ := cloud.requestFor("aws_eip")
	if req.Config.Type().HasAttribute("filter") {
		t.Error("a filter was sent to a type whose schema has none")
	}
}

// TestDiscoverCountInstancesShareBlockAddress is the P0.1-shaped hazard: the
// three EIPs carry the address of the block rather than of an instance.
// Nothing distinguishes them, so nothing binds and the condition is named.
func TestDiscoverCountInstancesShareBlockAddress(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_eip", "eipalloc-0", `aws_eip.pool`)
	cloud.own("aws_eip", "eipalloc-1", `aws_eip.pool`)
	cloud.own("aws_eip", "eipalloc-2", `aws_eip.pool`)

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("indistinguishable count instances produced no error:\n%s", res)
	}
	problems := res.ProblemsOfKind(ProblemNeedsSlotMarkers)
	if len(problems) != 1 {
		t.Fatalf("want one needs-slot-markers problem:\n%s", res)
	}
	if got := len(problems[0].LiveIDs); got != 3 {
		t.Errorf("the problem names %d live resources, want 3", got)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("a fungible instance was bound by guess:\n%s", res)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("block-addressed markers were reported as orphans instead:\n%s", res)
	}
	// The way out is now something the operator can act on rather than a
	// roadmap phase to wait for: stamp the slots, or write per-instance
	// addresses. Either makes the next run bind.
	if !strings.Contains(problems[0].Detail, TagSlot) {
		t.Errorf("the problem does not name the marker that fixes it: %s", problems[0].Detail)
	}
}

// TestDiscoverCountCollision: two live resources claiming one count
// instance is reported as the slot-marker condition rather than as a plain
// collision, because for a fungible set "which one is slot 1" is not a
// question their shared address can answer.
func TestDiscoverCountCollision(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_eip", "eipalloc-0", `aws_eip.pool:0`)
	cloud.own("aws_eip", "eipalloc-x", `aws_eip.pool:0`)

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("a count collision produced no error:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemNeedsSlotMarkers)) != 1 {
		t.Fatalf("want the slot-marker condition:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemCollision)) != 0 {
		t.Errorf("a count collision was reported as a plain collision:\n%s", res)
	}
	if _, ok := res.BindingFor(mustAddr(t, `aws_eip.pool[0]`)); ok {
		t.Errorf("a contested count instance was bound:\n%s", res)
	}
}

// TestDiscoverOrphan: this estate's marker on an address the configuration
// no longer declares. Removing a resource block is legal, so this is
// reported for removal planning, not diagnosed.
func TestDiscoverOrphan(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-gone", `aws_vpc.retired`)

	res, diags := discoverFixture(t, cloud, Request{})
	assertNoErrors(t, diags)

	if len(res.Orphans) != 1 {
		t.Fatalf("want one orphan:\n%s", res)
	}
	o := res.Orphans[0]
	if o.ImportID != "vpc-gone" || o.Normalized != `aws_vpc.retired` {
		t.Errorf("orphan recorded wrong: %s", o)
	}
	if o.Resource == cty.NilVal {
		t.Error("the orphan does not carry the listed object, which is what a rename pairing compares content on")
	}
	if len(res.Unclaimed) != 0 {
		t.Errorf("an owned resource was reported as unclaimed:\n%s", res)
	}
}

// TestDiscoverTypeNotListable: a needs-discovery type the provider cannot
// list is an error, not an absence. Reporting its instances as absent would
// propose creating resources that may already exist.
func TestDiscoverTypeNotListable(t *testing.T) {
	cloud := newFakeCloud()
	cloud.unlistable("aws_route_table")
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("an unlistable type produced no error:\n%s", res)
	}
	problems := res.ProblemsOfKind(ProblemTypeNotListable)
	if len(problems) != 1 || problems[0].TypeName != "aws_route_table" {
		t.Fatalf("want one type-not-listable problem for aws_route_table:\n%s", res)
	}
	if containsAddr(res.Unbound, `aws_route_table.main`) {
		t.Error("an undiscoverable instance was reported as merely absent")
	}
	if _, ok := res.BindingFor(mustAddr(t, `aws_vpc.main`)); !ok {
		t.Error("one unlistable type stopped the rest of the scan")
	}
}

// TestDiscoverUnresolvedAccount is the roadmap's owner-id warning, detected
// from the only evidence a client has: every listed identity carrying an
// empty account ID.
func TestDiscoverUnresolvedAccount(t *testing.T) {
	cloud := newFakeCloud()
	cloud.accountID = ""
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)

	res, diags := discoverFixture(t, cloud, Request{})
	assertNoErrors(t, diags) // a warning, not an error: this run is fine

	problems := res.ProblemsOfKind(ProblemUnresolvedAccount)
	if len(problems) != 1 {
		t.Fatalf("want the unresolved-account warning:\n%s", res)
	}
	if !strings.Contains(problems[0].Detail, "skip_requesting_account_id") {
		t.Errorf("the warning does not name the flag to remove: %s", problems[0].Detail)
	}
	if ProblemUnresolvedAccount.Severity() != SeverityWarning {
		t.Error("the unresolved-account problem is not a warning")
	}
	// The binding still works, because an emulator ignores the empty
	// filter; the warning is about what real AWS would do.
	if _, ok := res.BindingFor(mustAddr(t, `aws_vpc.main`)); !ok {
		t.Errorf("the VPC did not bind:\n%s", res)
	}

	// With an account ID present, no warning.
	cloud2 := newFakeCloud()
	cloud2.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	res2, _ := discoverFixture(t, cloud2, Request{})
	if len(res2.ProblemsOfKind(ProblemUnresolvedAccount)) != 0 {
		t.Errorf("the warning fired with an account ID present:\n%s", res2)
	}
	if scan, _ := res2.ScanFor("aws_vpc"); scan.AccountID != "000000000000" {
		t.Errorf("the scan did not record the account ID: %s", scan)
	}
}

// TestDiscoverNoIdentity: a marker that binds but a provider that sent no
// identity leaves nothing to import with, which is named rather than
// silently dropped.
func TestDiscoverNoIdentity(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.objects["aws_vpc"][0].noIdentity = true

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("a result with no identity produced no error:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemNoIdentity)) != 1 {
		t.Fatalf("want one no-identity problem:\n%s", res)
	}
	if _, ok := res.BindingFor(mustAddr(t, `aws_vpc.main`)); ok {
		t.Error("an instance with no import ID was bound")
	}
}

// TestDiscoverNoTags: a list result with no object at all cannot have its
// markers read, and must not be mistaken for an untagged - and therefore
// unclaimed - resource.
func TestDiscoverNoTags(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.objects["aws_vpc"][0].noObject = true

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("an objectless result produced no error:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemNoTags)) != 1 {
		t.Fatalf("want one no-tags problem:\n%s", res)
	}
	if len(res.Unclaimed) != 0 {
		t.Errorf("an unreadable resource was classified as unclaimed:\n%s", res)
	}
}

// ---------------------------------------------------------------------------
// Request validation
// ---------------------------------------------------------------------------

func TestDiscoverRejectsBadRequests(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))
	resolutions := resolveOrFail(t, cfg).All()
	cloud := newFakeCloud()

	cases := map[string]struct {
		req  Request
		want string
	}{
		"no estate": {
			req:  Request{Config: cfg, Resolutions: resolutions, Provider: cloud},
			want: "Invalid estate name",
		},
		"bad estate name": {
			req:  Request{Estate: "Not An Estate", Config: cfg, Resolutions: resolutions, Provider: cloud},
			want: "Invalid estate name",
		},
		"no config": {
			req:  Request{Estate: estateName, Resolutions: resolutions, Provider: cloud},
			want: "No configuration to discover against",
		},
		"no provider": {
			req:  Request{Estate: estateName, Config: cfg, Resolutions: resolutions},
			want: "No provider access",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, diags := Discover(context.Background(), tc.req)
			if !hasDiag(diags, tc.want, "") {
				t.Errorf("want a %q diagnostic, got:\n%s", tc.want, renderDiags(diags))
			}
		})
	}
}

// TestDiscoverResolutionNotInConfig: resolutions and configuration that
// disagree are a bug in the caller, not something to discover around.
func TestDiscoverResolutionNotInConfig(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))
	resolutions := []identity.Resolution{{
		Addr:   mustAddr(t, `aws_vpc.ghost`),
		Class:  identity.ClassNeedsDiscovery,
		Reason: "server-assigned",
	}}

	_, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    newFakeCloud(),
	})
	if !hasDiag(diags, "Resolved resource missing from the configuration", "aws_vpc.ghost") {
		t.Errorf("want the mismatch diagnostic, got:\n%s", renderDiags(diags))
	}
}

// TestDiscoverNothingToDo: a configuration whose instances are all statically
// named needs no provider traffic at all.
func TestDiscoverNothingToDo(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))
	var static []identity.Resolution
	for _, r := range resolveOrFail(t, cfg).All() {
		if r.Class != identity.ClassNeedsDiscovery {
			static = append(static, r)
		}
	}
	cloud := newFakeCloud()

	res, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: static,
		Provider:    cloud,
	})
	assertNoErrors(t, diags)
	if len(res.Scans) != 0 {
		t.Errorf("a configuration with nothing to discover still listed: %v", res.Scans)
	}
	if len(cloud.requests) != 0 {
		t.Errorf("the provider was called %d times for nothing", len(cloud.requests))
	}
	if len(res.Resolutions) != len(static) {
		t.Errorf("the resolution list changed length: %d, want %d", len(res.Resolutions), len(static))
	}
}

// ---------------------------------------------------------------------------
// The fake cloud
// ---------------------------------------------------------------------------

// fakeObject is one live resource in the fake cloud.
type fakeObject struct {
	id          string
	displayName string
	tags        map[string]string
	noIdentity  bool
	noObject    bool
}

// fakeCloud is a listclient.Lister backed by canned objects. It serves the
// six needs-discovery types of the v0 subset with the shape the real AWS
// provider serves - a filter block, a region argument, an identity carrying
// id/region/account_id, and a resource object carrying a tags map - and it
// honours a tag filter, so a test that expects server-side filtering fails
// if the filter never leaves this package.
type fakeCloud struct {
	objects   map[string][]*fakeObject
	types     []string
	unfilter  map[string]bool
	missing   map[string]bool
	untagged  map[string]bool
	accountID string

	requests []providers.ListResourceRequest
}

func newFakeCloud() *fakeCloud {
	return &fakeCloud{
		objects:   make(map[string][]*fakeObject),
		types:     []string{"aws_vpc", "aws_subnet", "aws_security_group", "aws_route_table", "aws_internet_gateway", "aws_eip"},
		unfilter:  make(map[string]bool),
		missing:   make(map[string]bool),
		untagged:  make(map[string]bool),
		accountID: "000000000000",
	}
}

// own adds a resource carrying this estate's markers.
func (c *fakeCloud) own(typeName, id, address string) {
	c.obj(typeName, id, map[string]string{
		TagEstate:  estateName,
		TagAddress: address,
	})
}

// obj adds a resource with arbitrary tags.
func (c *fakeCloud) obj(typeName, id string, tags map[string]string) {
	c.objects[typeName] = append(c.objects[typeName], &fakeObject{
		id:          id,
		displayName: id,
		tags:        tags,
	})
}

// noFilter makes a type's list schema offer no filter argument, the way the
// real provider's aws_eip list schema does.
func (c *fakeCloud) noFilter(typeName string) { c.unfilter[typeName] = true }

// unlistable removes a type from the provider's list schemas entirely.
func (c *fakeCloud) unlistable(typeName string) { c.missing[typeName] = true }

// listable adds a type to the provider's schemas, for the types outside the
// six the fake cloud serves by default. The sweep is the only caller that
// needs them: it lists types nothing in the configuration is waiting on.
func (c *fakeCloud) listable(typeName string) {
	for _, t := range c.types {
		if t == typeName {
			return
		}
	}
	c.types = append(c.types, typeName)
}

// listableUntagged adds a type whose objects carry no tags attribute at all,
// which is what the untaggable half of the admission table looks like.
func (c *fakeCloud) listableUntagged(typeName string) {
	c.listable(typeName)
	c.untagged[typeName] = true
}

// ownNoIdentity adds a marked resource the provider serves no identity for.
func (c *fakeCloud) ownNoIdentity(typeName, id, address string) {
	c.own(typeName, id, address)
	objs := c.objects[typeName]
	objs[len(objs)-1].noIdentity = true
}

// drop removes one object from the fake cloud, which is how a test says "this
// live resource is gone" without rebuilding the whole estate.
func (c *fakeCloud) drop(typeName, id string) {
	objs := c.objects[typeName]
	for i, o := range objs {
		if o.id == id {
			c.objects[typeName] = append(objs[:i:i], objs[i+1:]...)
			return
		}
	}
}

func (c *fakeCloud) requestFor(typeName string) (providers.ListResourceRequest, bool) {
	for _, r := range c.requests {
		if r.TypeName == typeName {
			return r, true
		}
	}
	return providers.ListResourceRequest{}, false
}

func (c *fakeCloud) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	resp := providers.GetProviderSchemaResponse{
		ResourceTypes:     map[string]providers.Schema{},
		ListResourceTypes: map[string]providers.Schema{},
	}
	for _, name := range c.types {
		attrs := map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"tags": {Type: cty.Map(cty.String), Optional: true},
		}
		if c.untagged[name] {
			delete(attrs, "tags")
		}
		resp.ResourceTypes[name] = providers.Schema{
			Version: 1,
			Block:   &configschema.Block{Attributes: attrs},
			IdentitySchema: &configschema.Object{
				Nesting: configschema.NestingSingle,
				Attributes: map[string]*configschema.Attribute{
					"id":         {Type: cty.String, Required: true},
					"region":     {Type: cty.String, Optional: true},
					"account_id": {Type: cty.String, Optional: true},
				},
			},
		}
		if c.missing[name] {
			continue
		}
		block := &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"region": {Type: cty.String, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{},
		}
		if !c.unfilter[name] {
			block.BlockTypes["filter"] = &configschema.NestedBlock{
				Nesting: configschema.NestingList,
				Block: configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"name":   {Type: cty.String, Required: true},
						"values": {Type: cty.List(cty.String), Required: true},
					},
				},
			}
		}
		resp.ListResourceTypes[name] = providers.Schema{Block: block}
	}
	return resp
}

func (c *fakeCloud) ListResourceStream(_ context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	c.requests = append(c.requests, req)

	for _, o := range c.objects[req.TypeName] {
		if !c.matchesFilter(req.Config, o) {
			continue
		}
		ev := providers.ListResourceEvent{DisplayName: o.displayName}
		if !o.noIdentity {
			ev.Identity = cty.ObjectVal(map[string]cty.Value{
				"id":         cty.StringVal(o.id),
				"region":     cty.StringVal("us-east-1"),
				"account_id": cty.StringVal(c.accountID),
			})
		}
		if req.IncludeResourceObject && !o.noObject {
			tags := cty.NullVal(cty.Map(cty.String))
			if len(o.tags) > 0 {
				vals := map[string]cty.Value{}
				for k, v := range o.tags {
					vals[k] = cty.StringVal(v)
				}
				tags = cty.MapVal(vals)
			}
			ev.ResourceObject = cty.ObjectVal(map[string]cty.Value{
				"id":   cty.StringVal(o.id),
				"tags": tags,
			})
		}
		if !emit(ev) {
			break
		}
	}
	return diags
}

// matchesFilter applies the tag filters in a list configuration the way a
// server would.
func (c *fakeCloud) matchesFilter(config cty.Value, o *fakeObject) bool {
	if config == cty.NilVal || config.IsNull() || !config.Type().HasAttribute("filter") {
		return true
	}
	filters := config.GetAttr("filter")
	if filters.IsNull() || filters.LengthInt() == 0 {
		return true
	}
	for it := filters.ElementIterator(); it.Next(); {
		_, f := it.Element()
		name := f.GetAttr("name").AsString()
		key, ok := strings.CutPrefix(name, "tag:")
		if !ok {
			continue
		}
		var matched bool
		for vit := f.GetAttr("values").ElementIterator(); vit.Next(); {
			_, v := vit.Element()
			if o.tags[key] == v.AsString() {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// discoverFixture runs a discovery pass over the P0.1 estate with the given
// fake cloud, filling in the parts of the request every test shares.
func discoverFixture(t *testing.T, cloud *fakeCloud, req Request) (*Result, tfdiags.Diagnostics) {
	t.Helper()

	cfg := loadConfig(t, estateDir(t))
	req.Estate = estateName
	req.Config = cfg
	req.Resolutions = resolveOrFail(t, cfg).All()
	req.Provider = cloud

	return Discover(context.Background(), req)
}

func assertBound(t *testing.T, res *Result, want map[string]string) {
	t.Helper()

	for addr, wantID := range want {
		b, ok := res.BindingFor(mustAddr(t, addr))
		if !ok {
			t.Errorf("%s is not bound:\n%s", addr, res)
			continue
		}
		if b.ImportID != wantID {
			t.Errorf("%s bound to %q, want %q", addr, b.ImportID, wantID)
		}
		if b.IdentityAttr != "id" {
			t.Errorf("%s bound via identity attribute %q, want id", addr, b.IdentityAttr)
		}
	}
	if len(res.Bindings) != len(want) {
		var got []string
		for _, b := range res.Bindings {
			got = append(got, b.Addr.String())
		}
		t.Errorf("bound %v, want exactly %d bindings", got, len(want))
	}
	for _, r := range res.Resolutions {
		wantID, ok := want[r.Addr.String()]
		if !ok {
			continue
		}
		if r.Class != identity.ClassConcrete || r.ImportID != wantID {
			t.Errorf("merged resolution for %s is %s, want CONCRETE %s", r.Addr, r, wantID)
		}
	}
}

func containsAddr(addrs []addrs.AbsResourceInstance, want string) bool {
	for _, a := range addrs {
		if a.String() == want {
			return true
		}
	}
	return false
}

func removeString(ss []string, drop string) []string {
	out := ss[:0]
	for _, s := range ss {
		if s != drop {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func loadConfig(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)

	mod, diags := parser.LoadConfigDir(dir, call)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(context.Background(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			t.Fatalf("fixture %s unexpectedly calls module %q", dir, req.Name)
			return nil, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}

func resolveOrFail(t *testing.T, cfg *configs.Config) *identity.Result {
	t.Helper()
	res, diags := identity.Resolve(context.Background(), cfg)
	if diags.HasErrors() {
		t.Fatalf("identity resolution failed:\n%s", renderDiags(diags))
	}
	return res
}

func mustAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("bad test address %q: %s", s, diags.Err())
	}
	return addr
}

func assertNoErrors(t *testing.T, diags tfdiags.Diagnostics) {
	t.Helper()
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics:\n%s", renderDiags(diags))
	}
}

func hasDiag(diags tfdiags.Diagnostics, summary, detailFragment string) bool {
	for _, d := range diags {
		desc := d.Description()
		if desc.Summary == summary && strings.Contains(desc.Detail, detailFragment) {
			return true
		}
	}
	return false
}

func renderDiags(diags tfdiags.Diagnostics) string {
	var b strings.Builder
	for _, d := range diags {
		desc := d.Description()
		fmt.Fprintf(&b, "- [%s] %s: %s\n", d.Severity(), desc.Summary, desc.Detail)
	}
	return b.String()
}

// TestProblemSummariesCoverKinds keeps problemSummaries complete. The
// fallback in problemDiag exists so that a kind added without a summary
// still produces something traceable rather than a bare "Discovery problem",
// but it is a safety net and not a place to land: a problem an operator reads
// should be headed by what went wrong, not by the name of an enum.
func TestProblemSummariesCoverKinds(t *testing.T) {
	kinds := []ProblemKind{
		ProblemCollision,
		ProblemMalformedMarker,
		ProblemNeedsSlotMarkers,
		ProblemMixedSlots,
		ProblemMalformedSlot,
		ProblemDuplicateSlot,
		ProblemSlotExhausted,
		ProblemNoTags,
		ProblemNoIdentity,
		ProblemTypeNotListable,
		ProblemUnresolvedAccount,
		ProblemListFailed,
	}
	for _, kind := range kinds {
		if problemSummaries[kind] == "" {
			t.Errorf("ProblemKind %q has no entry in problemSummaries", kind)
		}
	}
	if got, want := len(problemSummaries), len(kinds); got != want {
		t.Errorf("problemSummaries has %d entries, want exactly the %d kinds listed here", got, want)
	}
}

// TestProblemDiagLiveIDsAreTheirOwnSentence pins the join in problemDiag: the
// live IDs are appended to the detail, and a detail that did not end in
// punctuation used to run straight into them.
func TestProblemDiagLiveIDsAreTheirOwnSentence(t *testing.T) {
	res := &Result{}
	diag := problemDiag(res, Problem{
		Kind:     ProblemCollision,
		TypeName: "aws_s3_bucket",
		Detail:   "Two live buckets carry one address",
		LiveIDs:  []string{"bucket-a", "bucket-b"},
	})
	if got, want := diag.Description().Detail, "Two live buckets carry one address. Live resources: bucket-a, bucket-b."; got != want {
		t.Errorf("detail = %q, want %q", got, want)
	}
}
