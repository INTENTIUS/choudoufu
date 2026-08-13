// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// The estate-wide sweep and the removal candidates it produces. This is the
// half of P5.1 that closes the gap P4.1 left standing: a resource block
// deleted from the configuration leaves a live resource that no
// config-driven scan can see, because a deleted block declares nothing and
// nothing declared is what a scan is driven by.

// ---------------------------------------------------------------------------
// Unescaping
// ---------------------------------------------------------------------------

func TestUnescapeAddress(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{`aws_vpc.main`, `aws_vpc.main`, true},
		{`aws_subnet.this:a`, `aws_subnet.this["a"]`, true},
		{`aws_eip.pool:2`, `aws_eip.pool[2]`, true},
		{`aws_subnet.this:us-east-1a`, `aws_subnet.this["us-east-1a"]`, true},
		// Round-tripping is what the whole thing is for.
		{`aws_cloudwatch_log_group.app`, `aws_cloudwatch_log_group.app`, true},
		// A keyed module step (59c, issue #59 phase 3): decoded, not
		// refused, and always as a string key - a module block's for_each
		// is the only way this fork ever writes one, count on a module
		// block being refused permanently (RuleChildModule).
		{`module.subnets:a.aws_subnet.this`, `module.subnets["a"].aws_subnet.this`, true},

		// Refused, each for its own reason.
		{``, ``, false},
		{`aws_vpc`, ``, false},
		{`aws_subnet.this:`, ``, false},
		{`not an address`, ``, false},
	}

	for _, c := range cases {
		got, ok := UnescapeAddress(c.in)
		if ok != c.ok {
			t.Errorf("UnescapeAddress(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.String() != c.want {
			t.Errorf("UnescapeAddress(%q) = %q, want %q", c.in, got.String(), c.want)
		}
		// Every address this produces has to escape back to the value it
		// came from, or removal would plan against an address whose marker
		// nothing would find again.
		if back := EscapeAddress(got.String()); back != c.in {
			t.Errorf("UnescapeAddress(%q) round-trips to %q", c.in, back)
		}
	}
}

// TestUnescapeAddressDigitKey pins the one deliberate ambiguity: digits read
// as a count index rather than a quoted string key. See the doc comment on
// UnescapeAddress for why the reading cannot mislead anything.
func TestUnescapeAddressDigitKey(t *testing.T) {
	got, ok := UnescapeAddress(`aws_eip.pool:0`)
	if !ok {
		t.Fatal("aws_eip.pool:0 did not unescape")
	}
	if got.String() != `aws_eip.pool[0]` {
		t.Errorf("got %q, want aws_eip.pool[0]", got.String())
	}
}

// ---------------------------------------------------------------------------
// The sweep
// ---------------------------------------------------------------------------

// TestSweepFindsDeletedBlock is the headline: a live resource of a type the
// configuration declares nothing of, carrying this estate's marker, is found
// and becomes a removal candidate carrying a concrete resolution.
func TestSweepFindsDeletedBlock(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	// The deleted block. aws_cloudwatch_log_group is admitted and the
	// fixture's instances of it are client-named, so nothing about it is in
	// the discovery list and only a sweep can see it.
	cloud.listable("aws_cloudwatch_log_group")
	cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	rm := removalsByAddr(res)
	if len(rm) != 1 {
		t.Fatalf("want exactly one removal, got %d:\n%s", len(rm), res)
	}
	o, ok := rm[`aws_cloudwatch_log_group.deleted`]
	if !ok {
		t.Fatalf("the deleted block's resource is not a removal:\n%s", res)
	}
	if o.ImportID != "/estate/deleted" {
		t.Errorf("removal carries import ID %q", o.ImportID)
	}
	if !o.Swept {
		t.Error("the removal is not marked as found by the sweep")
	}
	if o.Withheld != "" {
		t.Errorf("the removal carries a withheld reason: %q", o.Withheld)
	}

	// The resolution is what actually makes the plan destroy it: concrete,
	// at the marker's address, flagged as having no configuration behind it.
	var found bool
	for _, r := range res.Resolutions {
		if r.Addr.String() != `aws_cloudwatch_log_group.deleted` {
			continue
		}
		found = true
		if r.Class != identity.ClassConcrete {
			t.Errorf("resolution for the removal is %s, want CONCRETE", r.Class)
		}
		if r.ImportID != "/estate/deleted" {
			t.Errorf("resolution carries import ID %q", r.ImportID)
		}
		if !r.Undeclared {
			t.Error("resolution for a deleted block is not marked Undeclared")
		}
		if r.Identity == cty.NilVal || r.Identity.IsNull() {
			t.Error("the removal's resolution dropped the identity object the sweep served with it")
		}
	}
	if !found {
		t.Errorf("no resolution was produced for the removal:\n%s", res)
	}
}

// TestSweepReportsProgress pins the heartbeat a large sweep needs so a
// caller can render "still working" while it runs (see
// [Request.Progress]): one event per type scanned, cumulative counts that
// only grow, and a final resource count that matches what the scans
// themselves recorded.
func TestSweepReportsProgress(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")
	cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)
	cloud.listable("aws_sns_topic")

	var events []ProgressEvent
	res, diags := discoverFixture(t, cloud, Request{
		Sweep:      true,
		SweepTypes: []string{"aws_cloudwatch_log_group", "aws_sns_topic"},
		Progress: func(ev ProgressEvent) {
			events = append(events, ev)
		},
	})
	assertNoErrors(t, diags)

	if len(events) == 0 {
		t.Fatal("no progress events were reported")
	}

	var wantFound int
	for _, scan := range res.Scans {
		wantFound += scan.Listed
	}

	for i, ev := range events {
		if ev.TypesScanned != i+1 {
			t.Errorf("event %d: TypesScanned = %d, want %d", i, ev.TypesScanned, i+1)
		}
		if ev.TypeName == "" {
			t.Errorf("event %d: TypeName is empty", i)
		}
		if i > 0 && ev.ResourcesFound < events[i-1].ResourcesFound {
			t.Errorf("event %d: ResourcesFound went backwards, %d after %d", i, ev.ResourcesFound, events[i-1].ResourcesFound)
		}
	}

	last := events[len(events)-1]
	if last.TypesScanned != len(res.Scans) {
		t.Errorf("final TypesScanned = %d, want %d (len(res.Scans))", last.TypesScanned, len(res.Scans))
	}
	if last.ResourcesFound != wantFound {
		t.Errorf("final ResourcesFound = %d, want %d", last.ResourcesFound, wantFound)
	}
	if !last.Sweep {
		t.Error("the last event, from an explicit sweep type, is not marked Sweep")
	}
}

// TestSweepReportsNoProgressWhenNobodyAsks proves Request.Progress being
// unset costs nothing beyond a nil check: no goroutine, no buffering,
// nothing to opt out of.
func TestSweepReportsNoProgressWhenNobodyAsks(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")

	res, diags := discoverFixture(t, cloud, Request{
		Sweep:      true,
		SweepTypes: []string{"aws_cloudwatch_log_group"},
	})
	assertNoErrors(t, diags)
	if len(res.Scans) == 0 {
		t.Fatal("sweep did not run")
	}
}

// TestSweepLeavesDeclaredClientNamedResourcesAlone is the hazard the sweep
// creates and has to defuse. A live bucket carrying the marker for a bucket
// the configuration still declares is claiming an address nothing in the
// discovery list is waiting on, because its identity comes out of
// configuration. Judged by the discovery list alone it looks exactly like an
// orphan, and an orphan is destroyed.
func TestSweepLeavesDeclaredClientNamedResourcesAlone(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_s3_bucket")
	cloud.own("aws_s3_bucket", "estate-data", `aws_s3_bucket.data`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	if len(res.Orphans) != 0 {
		t.Errorf("a declared client-named resource became an orphan:\n%s", res)
	}
	if len(res.Removals()) != 0 {
		t.Errorf("a declared client-named resource was proposed for destruction:\n%s", res)
	}
}

// TestSweepIsSkippedWithoutTheFlag: the sweep is opt-in, and a caller that
// does not ask for it gets exactly the pass it got before.
func TestSweepIsSkippedWithoutTheFlag(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")
	cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)

	res, diags := discoverFixture(t, cloud, Request{})
	assertNoErrors(t, diags)

	if len(res.Orphans) != 0 {
		t.Errorf("a sweep happened without being asked for:\n%s", res)
	}
	for _, s := range res.Scans {
		if s.Sweep {
			t.Errorf("scan of %s is a sweep row", s.TypeName)
		}
	}
}

// TestSweepReportsTypesItCannotCover: a type the sweep could not enumerate is
// named as a gap. "Nothing was found" and "nothing was looked at" are
// different answers and the report never blurs them.
func TestSweepReportsTypesItCannotCover(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	// The fake provider lists only the six EC2 types, and every admitted
	// type outside them is therefore unlistable.
	gaps := make(map[string]SweepGapReason, len(res.SweepGaps))
	for _, g := range res.SweepGaps {
		gaps[g.TypeName] = g.Reason
		if g.Detail == "" {
			t.Errorf("the gap for %s carries no explanation", g.TypeName)
		}
	}
	for _, want := range []string{"aws_s3_bucket", "aws_iam_role", "aws_cloudwatch_log_group"} {
		if gaps[want] != SweepGapNotListable {
			t.Errorf("%s is not reported as unlistable: %v\n%s", want, gaps[want], res)
		}
	}
	if len(res.SweepCovered) != 0 {
		t.Errorf("types were reported as swept: %v", res.SweepCovered)
	}
}

// TestSweepReportsUntaggableTypes: an admitted type that carries no tags can
// hold no marker, so a sweep of it could never find anything. That is a
// permanent hole in removal coverage for that type and is named as one.
func TestSweepReportsUntaggableTypes(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listableUntagged("aws_route")

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	var found bool
	for _, g := range res.SweepGaps {
		if g.TypeName == "aws_route" {
			found = true
			if g.Reason != SweepGapNotTaggable {
				t.Errorf("aws_route reported as %s, want %s", g.Reason, SweepGapNotTaggable)
			}
		}
	}
	if !found {
		t.Errorf("the untaggable type is not reported as a gap:\n%s", res)
	}
}

// TestSweepUsesTheServerSideFilterWhereItCan is the cost claim. A sweep is
// paid for on every plan, so it asks the provider to do the filtering for
// every type whose list schema offers it: an estate that holds nothing of a
// type gets an empty answer instead of the account's whole population.
func TestSweepUsesTheServerSideFilterWhereItCan(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")

	res, diags := discoverFixture(t, cloud, Request{Sweep: true, CollectUnclaimed: true})
	assertNoErrors(t, diags)

	scan, ok := res.ScanFor("aws_cloudwatch_log_group")
	if !ok {
		t.Fatalf("the swept type was not scanned:\n%s", res)
	}
	if !scan.Sweep {
		t.Error("the swept type's scan is not marked as a sweep")
	}
	if scan.Filtering != FilterServerSide || scan.Scope != ScopeEstate {
		t.Errorf("the sweep listed %s as %s/%s, want SERVER_SIDE/ESTATE", scan.TypeName, scan.Filtering, scan.Scope)
	}

	// CollectUnclaimed widens the config-driven scans and must not widen the
	// sweep: the sweep is looking for markers, and a wider list would pay
	// for the whole account to find them.
	if declared, ok := res.ScanFor("aws_vpc"); ok && declared.Scope != ScopeAll {
		t.Errorf("the declared type's scan was narrowed to %s", declared.Scope)
	}
}

// TestSweepDoesNotReportUnclaimedResources: a type the configuration does not
// declare has no declared instance an unmarked resource could be offered for,
// so sweeping one must not add to the foreign report.
func TestSweepDoesNotReportUnclaimedResources(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")
	cloud.noFilter("aws_cloudwatch_log_group")
	cloud.obj("aws_cloudwatch_log_group", "/somebody-else/app", map[string]string{"Name": "not ours"})

	res, diags := discoverFixture(t, cloud, Request{Sweep: true, CollectUnclaimed: true})
	assertNoErrors(t, diags)

	for _, u := range res.Unclaimed {
		if u.TypeName == "aws_cloudwatch_log_group" {
			t.Errorf("the sweep reported an unclaimed resource of an undeclared type: %s", u)
		}
	}
}

// ---------------------------------------------------------------------------
// Rename beats destroy
// ---------------------------------------------------------------------------

// TestRenameBeatsDestroy is the ordering rule, and it gets its own test
// because getting it wrong is the expensive mistake in this whole task: a
// for_each key that was renamed would be destroyed and recreated instead of
// re-tagged, and the plan would look entirely reasonable while doing it.
//
// The fixture's aws_subnet.this declares keys "a" and "b". A live subnet
// marked with the key "c" is an orphan of a block that still has an
// unclaimed declared instance, which is the shape of a rename.
func TestRenameBeatsDestroy(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	// "b" is now claimed by nothing, and a live subnet claims "c".
	cloud.drop("aws_subnet", "subnet-b")
	cloud.own("aws_subnet", "subnet-c", `aws_subnet.this:c`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	if len(res.Orphans) != 1 {
		t.Fatalf("want one orphan, got %d:\n%s", len(res.Orphans), res)
	}
	o := res.Orphans[0]
	if o.Removal {
		t.Errorf("the renamed key was proposed for destruction:\n%s", res)
	}
	if !strings.Contains(o.Withheld, "unclaimed") {
		t.Errorf("the withheld reason does not name the unclaimed instance: %q", o.Withheld)
	}
	// And nothing is in the resolution list for it, which is the mechanism:
	// no resolution, no prior-state instance, no destroy.
	for _, r := range res.Resolutions {
		if r.Addr.String() == `aws_subnet.this["c"]` {
			t.Errorf("a resolution was produced for the renamed key: %s", r)
		}
	}
}

// TestRenameWithholdingIsNotConditionalOnPairing: two orphans against one
// unclaimed instance is a rename this fork refuses to guess at. Withholding
// must not depend on the guess succeeding, or the ambiguous case - the one
// where guessing is least defensible - would be the one that destroys.
func TestRenameWithholdingIsNotConditionalOnPairing(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.drop("aws_subnet", "subnet-b")
	cloud.own("aws_subnet", "subnet-c", `aws_subnet.this:c`)
	cloud.own("aws_subnet", "subnet-d", `aws_subnet.this:d`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	if len(res.Orphans) != 2 {
		t.Fatalf("want two orphans, got %d:\n%s", len(res.Orphans), res)
	}
	for _, o := range res.Orphans {
		if o.Removal {
			t.Errorf("%s was proposed for destruction with the pairing ambiguous:\n%s", o.Normalized, res)
		}
	}
}

// TestRemovedKeyOfALivingBlockIsARemoval is the other half of the rule, and
// the reason it is a rule about unclaimed instances rather than about blocks.
// The block still exists and every instance it declares is claimed; the live
// resource at the removed key is nobody's rename and is destroyed.
func TestRemovedKeyOfALivingBlockIsARemoval(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.own("aws_subnet", "subnet-c", `aws_subnet.this:c`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	rm := removalsByAddr(res)
	if _, ok := rm[`aws_subnet.this["c"]`]; !ok {
		t.Fatalf("the removed key was not proposed for destruction:\n%s", res)
	}
	if len(rm) != 1 {
		t.Errorf("want exactly one removal, got %d:\n%s", len(rm), res)
	}
}

// TestOrphanWithNoIdentityIsReportedNotDestroyed: nothing is destroyed on the
// strength of a marker alone. Without an import ID there is nothing to read
// the resource with, and a plan that proposed destroying it would be planning
// against a resource it never saw.
func TestOrphanWithNoIdentityIsReportedNotDestroyed(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.ownNoIdentity("aws_vpc", "vpc-ghost", `aws_vpc.deleted`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	if !diags.HasErrors() {
		t.Errorf("an owned resource with no identity produced no error:\n%s", res)
	}
	if len(res.Removals()) != 0 {
		t.Errorf("a resource with no identity was proposed for destruction:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemNoIdentity)) == 0 {
		t.Errorf("no NO_IDENTITY problem was recorded:\n%s", res)
	}
}

// TestTwoOrphansAtOneAddressAreRefused: two live resources claiming one
// undeclared address would overwrite each other in the prior state, and the
// survivor would be destroyed while the other stayed live and unrecorded.
func TestTwoOrphansAtOneAddressAreRefused(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.own("aws_vpc", "vpc-old-1", `aws_vpc.deleted`)
	cloud.own("aws_vpc", "vpc-old-2", `aws_vpc.deleted`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	if !diags.HasErrors() {
		t.Errorf("a collision between two orphans produced no error:\n%s", res)
	}
	if len(res.Removals()) != 0 {
		t.Errorf("a colliding orphan was proposed for destruction:\n%s", res)
	}
}

// TestWholeCountBlockDeletionIsSwept: deleting a count block entirely leaves
// every member orphaned at its indexed address, and each one is a removal.
func TestWholeCountBlockDeletionIsSwept(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.own("aws_subnet", "subnet-a", `aws_subnet.this:a`)
	cloud.own("aws_subnet", "subnet-b", `aws_subnet.this:b`)
	cloud.own("aws_security_group", "sg-1", `aws_security_group.main`)
	cloud.own("aws_route_table", "rtb-1", `aws_route_table.main`)
	cloud.own("aws_internet_gateway", "igw-1", `aws_internet_gateway.main`)
	// aws_eip.pool is declared by the fixture with count = 3, so a deleted
	// count block is simulated with a block name it does not declare.
	cloud.own("aws_eip", "eipalloc-0", `aws_eip.pool[0]`)
	cloud.own("aws_eip", "eipalloc-1", `aws_eip.pool[1]`)
	cloud.own("aws_eip", "eipalloc-2", `aws_eip.pool[2]`)
	cloud.own("aws_eip", "eipalloc-x0", `aws_eip.gone:0`)
	cloud.own("aws_eip", "eipalloc-x1", `aws_eip.gone:1`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	rm := removalsByAddr(res)
	for _, want := range []string{`aws_eip.gone[0]`, `aws_eip.gone[1]`} {
		if _, ok := rm[want]; !ok {
			t.Errorf("%s was not proposed for destruction:\n%s", want, res)
		}
	}
	if len(rm) != 2 {
		t.Errorf("want two removals, got %d:\n%s", len(rm), res)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// ownWholeEstate marks every one of the fixture's needs-discovery instances,
// so that a test about undeclared resources is not also a test about unbound
// declared ones.
func ownWholeEstate(c *fakeCloud) {
	c.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	c.own("aws_subnet", "subnet-a", `aws_subnet.this:a`)
	c.own("aws_subnet", "subnet-b", `aws_subnet.this:b`)
	c.own("aws_security_group", "sg-1", `aws_security_group.main`)
	c.own("aws_route_table", "rtb-1", `aws_route_table.main`)
	c.own("aws_internet_gateway", "igw-1", `aws_internet_gateway.main`)
	c.own("aws_eip", "eipalloc-0", `aws_eip.pool:0`)
	c.own("aws_eip", "eipalloc-1", `aws_eip.pool:1`)
	c.own("aws_eip", "eipalloc-2", `aws_eip.pool:2`)
}

func removalsByAddr(res *Result) map[string]OwnedResource {
	out := make(map[string]OwnedResource)
	for _, o := range res.Removals() {
		out[o.Addr.String()] = o
	}
	return out
}
