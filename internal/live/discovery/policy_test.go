// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/policy"
)

// TestPolicyOmittedIsByteIdenticalToDefaultVerb pins issue #67's hard bar:
// a nil Policy and a Policy built entirely from [policy.DefaultVerb] must
// produce the identical [Result] over the same input - "omitted policy =
// byte-identical current behavior" is a claim this test can fail.
func TestPolicyOmittedIsByteIdenticalToDefaultVerb(t *testing.T) {
	build := func(pol *policy.Policy) *Result {
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		cloud.listable("aws_cloudwatch_log_group")
		cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)

		res, diags := discoverFixture(t, cloud, Request{Sweep: true, Policy: pol})
		assertNoErrors(t, diags)
		return res
	}

	withoutPolicy := build(nil)
	withDefaults := build(policy.Build(&policy.Raw{
		DeclaredTagged: "converge", DeclaredTaggedSet: true,
		DeclaredUntagged: "refuse", DeclaredUntaggedSet: true,
		UndeclaredTagged: "delete", UndeclaredTaggedSet: true,
		UndeclaredUntagged: "keep", UndeclaredUntaggedSet: true,
	}, estateName))

	rmWithout := removalsByAddr(withoutPolicy)
	rmWith := removalsByAddr(withDefaults)
	if len(rmWithout) != len(rmWith) {
		t.Fatalf("removal count differs: nil policy %d, explicit-default policy %d", len(rmWithout), len(rmWith))
	}
	for addr, o := range rmWithout {
		o2, ok := rmWith[addr]
		if !ok {
			t.Errorf("%s is a removal with no policy but not with an explicit-default policy", addr)
			continue
		}
		if o.ImportID != o2.ImportID || o.Withheld != o2.Withheld || o.PolicyVerb != o2.PolicyVerb {
			t.Errorf("%s differs: %+v vs %+v", addr, o, o2)
		}
	}
	if len(withoutPolicy.Resolutions) != len(withDefaults.Resolutions) {
		t.Errorf("resolution count differs: %d vs %d", len(withoutPolicy.Resolutions), len(withDefaults.Resolutions))
	}
}

// TestOrphanPolicyKeep: undeclared_tagged = "keep" withholds a removal the
// sweep would otherwise destroy, and does so silently enough to not be
// mistaken for a rename withholding - the resource is not destroyed, and
// [OwnedResource.PolicyVerb] names why.
func TestOrphanPolicyKeep(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")
	cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)

	pol := policy.Build(&policy.Raw{
		UndeclaredTagged: "keep", UndeclaredTaggedSet: true,
	}, estateName)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true, Policy: pol})
	assertNoErrors(t, diags)

	if len(res.Removals()) != 0 {
		t.Fatalf("undeclared_tagged = \"keep\" must withhold every removal, got %d:\n%s", len(res.Removals()), res)
	}
	var kept *OwnedResource
	for i := range res.Orphans {
		if res.Orphans[i].Normalized == `aws_cloudwatch_log_group.deleted` {
			kept = &res.Orphans[i]
		}
	}
	if kept == nil {
		t.Fatal("the orphan is gone entirely, not merely withheld")
	}
	if kept.PolicyVerb != policy.Keep {
		t.Errorf("PolicyVerb = %q, want %q", kept.PolicyVerb, policy.Keep)
	}
	if kept.Withheld == "" {
		t.Error("a policy-withheld orphan must say why")
	}
	for _, r := range res.Resolutions {
		if r.Addr.String() == `aws_cloudwatch_log_group.deleted` {
			t.Errorf("a kept orphan must not enter the resolution list: %+v", r)
		}
	}
}

// TestOrphanPolicyReport is the same withholding as keep, with a distinct
// verb recorded so a caller can render it as an explicit roster entry
// rather than silently.
func TestOrphanPolicyReport(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")
	cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)

	pol := policy.Build(&policy.Raw{
		UndeclaredTagged: "report", UndeclaredTaggedSet: true,
	}, estateName)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true, Policy: pol})
	assertNoErrors(t, diags)

	if len(res.Removals()) != 0 {
		t.Fatalf("undeclared_tagged = \"report\" must never destroy, got %d removals", len(res.Removals()))
	}
	found := false
	for _, o := range res.Orphans {
		if o.Normalized == `aws_cloudwatch_log_group.deleted` {
			found = true
			if o.PolicyVerb != policy.Report {
				t.Errorf("PolicyVerb = %q, want %q", o.PolicyVerb, policy.Report)
			}
		}
	}
	if !found {
		t.Fatal("the reported orphan is missing")
	}
}

// TestOrphanPolicyUntagStatesManagementConsequenceForTheEstateMarker is the
// maintainer's ruling on issue #67's tag_key question, the undeclared_tagged
// side: tag_key defaults to the estate marker, and when it is the estate
// marker, the withheld message states the management consequence in plain
// words, because releasing it is what a later run losing track of the
// resource would look like.
func TestOrphanPolicyUntagStatesManagementConsequenceForTheEstateMarker(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")
	cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)

	pol := policy.Build(&policy.Raw{
		UndeclaredTagged: "untag", UndeclaredTaggedSet: true,
	}, estateName) // TagKey defaults to the estate marker.

	res, diags := discoverFixture(t, cloud, Request{Sweep: true, Policy: pol})
	assertNoErrors(t, diags)

	var got *OwnedResource
	for i := range res.Orphans {
		if res.Orphans[i].Normalized == `aws_cloudwatch_log_group.deleted` {
			got = &res.Orphans[i]
		}
	}
	if got == nil {
		t.Fatal("the orphan is missing")
	}
	if got.Removal {
		t.Error("untag must never destroy")
	}
	if !strings.Contains(got.Withheld, "unmanaged") && !strings.Contains(got.Withheld, "marker discovery could no longer find it") {
		t.Errorf("Withheld does not state the management consequence for the estate-marker case: %q", got.Withheld)
	}
}

// TestOrphanPolicyUntagCustomTagStatesNoManagementConsequence is the same
// verb with tag_key naming a preservation tag distinct from the estate
// marker: the ruling's other half - the estate marker stays intact, so
// there is no management consequence to state.
func TestOrphanPolicyUntagCustomTagStatesNoManagementConsequence(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")
	// This orphan carries the estate's ownership marker (own(), otherwise it
	// would not be an orphan at all) plus a distinct preservation tag, which
	// is the tag this policy's tag_key names.
	cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)
	objs := cloud.objects["aws_cloudwatch_log_group"]
	objs[len(objs)-1].tags["team-owns"] = "platform"

	pol := policy.Build(&policy.Raw{
		UndeclaredTagged: "untag", UndeclaredTaggedSet: true,
		TagKey: "team-owns", TagKeySet: true,
		TagValue: "platform", TagValueSet: true,
	}, estateName)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true, Policy: pol})
	assertNoErrors(t, diags)

	var got *OwnedResource
	for i := range res.Orphans {
		if res.Orphans[i].Normalized == `aws_cloudwatch_log_group.deleted` {
			got = &res.Orphans[i]
		}
	}
	if got == nil {
		t.Fatal("the orphan is missing")
	}
	if strings.Contains(got.Withheld, "unmanaged") || strings.Contains(got.Withheld, "marker discovery could no longer find it") {
		t.Errorf("a custom preservation tag's release must not state a management consequence: %q", got.Withheld)
	}
}

// TestOrphanPolicyReadsTheQuadrantItReports is GitHub issue #116's first
// finding, and the fixture the issue said would settle it.
//
// An orphan is undeclared by construction, so its quadrant turns on whether
// it carries the policy's tag. With a custom tag_key, an orphan can carry
// the estate's ownership marker - which is why discovery found it at all -
// and not carry the policy's chosen tag, which puts it in
// undeclared_untagged.
//
// applyOrphanPolicy computed the verb from that answer and then named
// "undeclared_tagged" in the message unconditionally, so the operator was
// told the wrong quadrant had withheld their resource. The issue notes the
// finding was read rather than run; this runs it.
func TestOrphanPolicyReadsTheQuadrantItReports(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")
	// Carries the estate marker (own) and nothing else. The policy below
	// keys on "team-owns", which this object does not have.
	cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)

	pol := policy.Build(&policy.Raw{
		UndeclaredTagged: "delete", UndeclaredTaggedSet: true,
		UndeclaredUntagged: "report", UndeclaredUntaggedSet: true,
		TagKey: "team-owns", TagKeySet: true,
		TagValue: "platform", TagValueSet: true,
	}, estateName)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true, Policy: pol})
	assertNoErrors(t, diags)

	var got *OwnedResource
	for i := range res.Orphans {
		if res.Orphans[i].Normalized == `aws_cloudwatch_log_group.deleted` {
			got = &res.Orphans[i]
		}
	}
	if got == nil {
		t.Fatal("the orphan is missing")
	}
	if got.PolicyVerb != policy.Report {
		t.Fatalf("PolicyVerb = %q, want %q: the untagged quadrant's verb is what applies here", got.PolicyVerb, policy.Report)
	}
	if !strings.Contains(got.Withheld, "policy.undeclared_untagged") {
		t.Errorf("Withheld names the wrong quadrant: %q\nThe orphan does not carry tag_key=tag_value, so undeclared_untagged is the quadrant whose verb was applied.", got.Withheld)
	}
	if strings.Contains(got.Withheld, "policy.undeclared_tagged") {
		t.Errorf("Withheld names undeclared_tagged, whose verb (delete) was not the one applied: %q", got.Withheld)
	}
}

// TestOrphanPolicyDeleteIsUnaffected: the default verb, explicit or
// omitted, changes nothing about which orphans are removals.
func TestOrphanPolicyDeleteIsUnaffected(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")
	cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)

	pol := policy.Build(&policy.Raw{
		UndeclaredTagged: "delete", UndeclaredTaggedSet: true,
	}, estateName)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true, Policy: pol})
	assertNoErrors(t, diags)

	rm := removalsByAddr(res)
	if len(rm) != 1 {
		t.Fatalf("want exactly one removal, got %d:\n%s", len(rm), res)
	}
	o, ok := rm[`aws_cloudwatch_log_group.deleted`]
	if !ok || o.Withheld != "" {
		t.Errorf("delete must not withhold: %+v (present=%v)", o, ok)
	}
}
