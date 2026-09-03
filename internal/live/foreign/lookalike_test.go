// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
)

// TestLookalikesMatchTableCandidate is the marker-stripped scenario for a
// matchTable type: an unmarked security group whose name exactly matches
// the declared one, offered for adoption by Classify, is what the guard
// warns about when that same address is actually about to be created.
func TestLookalikesMatchTableCandidate(t *testing.T) {
	res := classifyFixture(t, discovery.Result{Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_security_group", 1)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")}, Unclaimed: []discovery.UnclaimedResource{
		live("aws_security_group", "sg-0abc", "stateless-e2e-main",
			map[string]string{"Name": "stateless-e2e-main"},
			map[string]string{"name": "stateless-e2e-main", "description": "estate fixture security group"}),
	}}})

	warnings := Lookalikes(Request{Estate: estateName}, res, []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")})
	if len(warnings) != 1 {
		t.Fatalf("want exactly one lookalike warning, got %d: %v", len(warnings), warnings)
	}
	w := warnings[0]
	if w.Addr.String() != "aws_security_group.main" {
		t.Errorf("warning is for %s, want aws_security_group.main", w.Addr)
	}
	if w.LiveID != "sg-0abc" {
		t.Errorf("warning names live ID %q, want sg-0abc", w.LiveID)
	}
	if len(w.Matched) != 1 || w.Matched[0].Attr != "name" || w.Matched[0].Value != "stateless-e2e-main" {
		t.Errorf("warning carries matched arguments %v, want the name match", w.Matched)
	}
	if w.MarkerEstate != estateName || w.MarkerAddress != "aws_security_group.main" {
		t.Errorf("warning carries marker pair %q/%q", w.MarkerEstate, w.MarkerAddress)
	}
	if !strings.Contains(w.Hint, "sg-0abc") {
		t.Errorf("warning's adoption hint %q does not name the live resource", w.Hint)
	}
}

// TestLookalikesMatchTableNoConfirmedMatch: a matchTable type with a
// near-miss live resource (or none at all) gets no warning. A guess here
// would point an operator at the wrong resource.
func TestLookalikesMatchTableNoConfirmedMatch(t *testing.T) {
	res := classifyFixture(t, discovery.Result{Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_security_group", 1)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")}, Unclaimed: []discovery.UnclaimedResource{
		live("aws_security_group", "sg-old", "", nil, map[string]string{"name": "stateless-e2e-main-old"}),
	}}})
	if len(res.Candidates) != 0 {
		t.Fatalf("test fixture unexpectedly produced a candidate: %v", res.Candidates)
	}

	warnings := Lookalikes(Request{Estate: estateName}, res, []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")})
	if len(warnings) != 0 {
		t.Errorf("a near-miss matchTable type produced a warning: %v", warnings)
	}
}

// TestLookalikesMatchTableAmbiguousStaysSilent: two equally-good matches is
// the one-to-one rule's territory, and the guard defers to it rather than
// picking one.
func TestLookalikesMatchTableAmbiguousStaysSilent(t *testing.T) {
	res := classifyFixture(t, discovery.Result{Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_security_group", 2)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")}, Unclaimed: []discovery.UnclaimedResource{
		live("aws_security_group", "sg-one", "", nil, map[string]string{"name": "stateless-e2e-main"}),
		live("aws_security_group", "sg-two", "", nil, map[string]string{"name": "stateless-e2e-main"}),
	}}})

	warnings := Lookalikes(Request{Estate: estateName}, res, []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")})
	if len(warnings) != 0 {
		t.Errorf("an ambiguous match produced a warning: %v", warnings)
	}
}

// TestLookalikesGenericSingleUnowned covers a type with no matchTable entry
// at all (aws_route_table has nothing in configuration that distinguishes
// one from another): exactly one unclaimed live resource of that type is
// still worth naming generically when the create is about to happen beside
// it.
func TestLookalikesGenericSingleUnowned(t *testing.T) {
	res := classifyFixture(t, discovery.Result{Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_route_table", 1)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, "aws_route_table.main")}, Unclaimed: []discovery.UnclaimedResource{live("aws_route_table", "rtb-stripped", "", nil, nil)}}})
	if len(res.Foreign) != 1 {
		t.Fatalf("test fixture did not produce exactly one foreign resource: %v", res.Foreign)
	}

	warnings := Lookalikes(Request{Estate: estateName}, res, []addrs.AbsResourceInstance{mustAddr(t, "aws_route_table.main")})
	if len(warnings) != 1 {
		t.Fatalf("want exactly one generic lookalike warning, got %d: %v", len(warnings), warnings)
	}
	w := warnings[0]
	if w.LiveID != "rtb-stripped" {
		t.Errorf("warning names live ID %q, want rtb-stripped", w.LiveID)
	}
	if len(w.Matched) != 0 {
		t.Errorf("a generic warning carries matched arguments %v, which overstates the evidence", w.Matched)
	}
	if w.MarkerEstate != estateName || w.MarkerAddress != "aws_route_table.main" {
		t.Errorf("warning carries marker pair %q/%q", w.MarkerEstate, w.MarkerAddress)
	}
	if !strings.Contains(w.Hint, "rtb-stripped") {
		t.Errorf("warning's adoption hint %q does not name the live resource", w.Hint)
	}
}

// TestLookalikesGenericAmbiguousStaysSilent: two unowned route tables is not
// a hint, it is a guess, and the guard says nothing rather than pick one.
func TestLookalikesGenericAmbiguousStaysSilent(t *testing.T) {
	res := classifyFixture(t, discovery.Result{Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_route_table", 2)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, "aws_route_table.main")}, Unclaimed: []discovery.UnclaimedResource{
		live("aws_route_table", "rtb-one", "", nil, nil),
		live("aws_route_table", "rtb-two", "", nil, nil),
	}}})
	if len(res.Foreign) != 2 {
		t.Fatalf("test fixture did not produce two foreign resources: %v", res.Foreign)
	}

	warnings := Lookalikes(Request{Estate: estateName}, res, []addrs.AbsResourceInstance{mustAddr(t, "aws_route_table.main")})
	if len(warnings) != 0 {
		t.Errorf("two unowned resources of the same type produced a warning: %v", warnings)
	}
}

// TestLookalikesGenericNoneStaysSilent: nothing unowned of the type exists,
// so there is nothing to warn about - an ordinary create.
func TestLookalikesGenericNoneStaysSilent(t *testing.T) {
	res := classifyFixture(t, discovery.Result{Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_route_table", 0)}}})

	warnings := Lookalikes(Request{Estate: estateName}, res, []addrs.AbsResourceInstance{mustAddr(t, "aws_route_table.main")})
	if len(warnings) != 0 {
		t.Errorf("no unowned resource of the type exists, but a warning fired: %v", warnings)
	}
}

// TestLookalikesSkipsKeyedInstances: a count/for_each member is skipped
// outright in the generic path, because cardinality alone cannot tell a
// stripped marker apart from legitimate scale-out.
func TestLookalikesSkipsKeyedInstances(t *testing.T) {
	res := classifyFixture(t, discovery.Result{Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_route_table", 1)}, Unclaimed: []discovery.UnclaimedResource{live("aws_route_table", "rtb-stripped", "", nil, nil)}}})
	if len(res.Foreign) != 1 {
		t.Fatalf("test fixture did not produce exactly one foreign resource: %v", res.Foreign)
	}

	keyed := mustAddr(t, `aws_route_table.main["a"]`)
	warnings := Lookalikes(Request{Estate: estateName}, res, []addrs.AbsResourceInstance{keyed})
	if len(warnings) != 0 {
		t.Errorf("a keyed create produced a generic warning: %v", warnings)
	}
}

// TestLookalikesNilResult: no classification means no warnings, not a panic.
func TestLookalikesNilResult(t *testing.T) {
	if got := Lookalikes(Request{Estate: estateName}, nil, []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")}); got != nil {
		t.Errorf("a nil classification produced warnings: %v", got)
	}
}

// TestLookalikesNoCreates: nothing being created means nothing to warn
// about, even with unowned resources sitting in the classification.
func TestLookalikesNoCreates(t *testing.T) {
	res := classifyFixture(t, discovery.Result{Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_route_table", 1)}, Unclaimed: []discovery.UnclaimedResource{live("aws_route_table", "rtb-stripped", "", nil, nil)}}})
	if got := Lookalikes(Request{Estate: estateName}, res, nil); len(got) != 0 {
		t.Errorf("no creates were given, but warnings were produced: %v", got)
	}
}
