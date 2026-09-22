// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// lookalikeRelistFixture is the whole of GitHub issue #1480's scenario in
// the fake cloud: one declared, needs-discovery aws_security_group, and one
// live security group of the same type and name whose ownership markers
// were stripped out of band. stripped=false gives the same estate with its
// markers intact, which is the steady-state "No changes" plan.
func lookalikeRelistFixture(t *testing.T, stripped bool) (Request, *fakeCloud) {
	t.Helper()

	cloud := newFakeCloud()
	cloud.withAttr("aws_security_group", "name")
	if stripped {
		// Console cleanup, a tag policy misfire: the live group is still
		// there, still named what configuration names, and carries no
		// tofu-estate tag at all.
		cloud.obj("aws_security_group", "sg-stripped", map[string]string{"Name": "estate-main"})
	} else {
		cloud.own("aws_security_group", "sg-stripped", "aws_security_group.main")
	}
	objs := cloud.objects["aws_security_group"]
	objs[len(objs)-1].extra = map[string]string{"name": "estate-main"}

	return Request{
		Estate:      estateName,
		Config:      loadConfig(t, filepath.Join("testdata", "lookalike-relist")),
		Resolutions: []identity.Resolution{{Addr: mustAddr(t, "aws_security_group.main"), Class: identity.ClassNeedsDiscovery}},
		Provider:    cloud,
	}, cloud
}

// TestLookalikeRelistOnPlainPlan is GitHub issue #1480's red test. A plain
// plan - CollectUnclaimed unset, no -adoption-only, no
// TOFU_LIVE_COLLECT_UNCLAIMED - must still be able to see a live resource
// whose markers were stripped off it, because that is the single scenario
// the lookalike guard (b32bb7d5dd, live/MARKERS.md "The residual risk, and
// the last line of defense") exists for.
//
// Since 09d180f921 it could not: aws_security_group's list schema carries a
// filter block, so the config-driven scan listed it with
// scan.Scope = ScopeEstate and tag:tofu-estate = this estate, and a group
// with no tofu-estate tag never crossed the wire. Result.Unclaimed came
// back empty, internal/live/foreign had no candidate, and the plan proposed
// a silent duplicate.
//
// The assertions are exactly what internal/live/foreign needs downstream:
// the unmarked group in Result.Unclaimed with the resource object its
// content match reads, and a scan row saying ScopeAll so the coverage
// report does not turn round and say unclaimed resources were never
// visible.
func TestLookalikeRelistOnPlainPlan(t *testing.T) {
	req, _ := lookalikeRelistFixture(t, true)

	res, diags := Discover(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	if len(res.Unbound) != 1 || res.Unbound[0].String() != "aws_security_group.main" {
		t.Fatalf("fixture premise broken: want aws_security_group.main unbound (a create pending), got %v:\n%s", res.Unbound, res)
	}

	var found *UnclaimedResource
	for i := range res.Unclaimed {
		if res.Unclaimed[i].ImportID == "sg-stripped" {
			found = &res.Unclaimed[i]
		}
	}
	if found == nil {
		t.Fatalf("the stripped security group is not in Result.Unclaimed, so the lookalike guard has nothing to warn about:\n%s", res)
	}
	if found.TypeName != "aws_security_group" {
		t.Errorf("unclaimed resource type is %q, want aws_security_group", found.TypeName)
	}
	if found.Resource == cty.NilVal || found.Resource.IsNull() {
		t.Errorf("the unclaimed resource carries no resource object, so internal/live/foreign cannot content-match it")
	} else if v := found.Resource.GetAttr("name"); v.IsNull() || v.AsString() != "estate-main" {
		t.Errorf("the unclaimed resource's name attribute is %#v, want estate-main", v)
	}

	scan, ok := res.ScanFor("aws_security_group")
	if !ok {
		t.Fatalf("no scan row for aws_security_group:\n%s", res)
	}
	if scan.Scope != ScopeAll {
		t.Errorf("scan row reads Scope=%s, want ALL - internal/live/foreign reads this and reports the type unswept otherwise", scan.Scope)
	}
}

// TestLookalikeRelistCosts pins the price of the test above, because the
// alternative #1480 rejected - passing true for the config-driven loop's
// collectUnclaimed - pages every declared type's whole regional population
// on every plan, which is the cost side the CollectUnclaimed ruling (#604)
// settled at 157 calls for a migrated 79-instance terralith.
//
// The rule the extra list has to obey: nothing at all on a steady-state
// plan, and exactly one extra list per type that actually has a create
// pending.
func TestLookalikeRelistCosts(t *testing.T) {
	t.Run("steady state makes no extra call", func(t *testing.T) {
		req, cloud := lookalikeRelistFixture(t, false)

		res, diags := Discover(context.Background(), req)
		if diags.HasErrors() {
			t.Fatalf("unexpected errors: %s", diags.Err())
		}
		if len(res.Unbound) != 0 {
			t.Fatalf("fixture premise broken: the marked group should bind, leaving nothing unbound, got %v:\n%s", res.Unbound, res)
		}
		if n := listCallsFor(cloud, "aws_security_group"); n != 1 {
			t.Errorf("a plan with no pending create made %d list calls for aws_security_group, want 1", n)
		}
		if n := len(cloud.requests); n != 1 {
			t.Errorf("a plan with no pending create made %d list calls in total, want 1", n)
		}
	})

	t.Run("one pending create costs exactly one extra list", func(t *testing.T) {
		req, cloud := lookalikeRelistFixture(t, true)

		res, diags := Discover(context.Background(), req)
		if diags.HasErrors() {
			t.Fatalf("unexpected errors: %s", diags.Err())
		}
		if len(res.Unbound) != 1 {
			t.Fatalf("fixture premise broken: want one unbound instance, got %v:\n%s", res.Unbound, res)
		}
		if n := listCallsFor(cloud, "aws_security_group"); n != 2 {
			t.Errorf("a plan with one pending create made %d list calls for aws_security_group, want 2 (the estate-scoped one plus one targeted widening)", n)
		}
		if n := len(cloud.requests); n != 2 {
			t.Errorf("a plan with one pending create made %d list calls in total, want 2", n)
		}
	})
}
