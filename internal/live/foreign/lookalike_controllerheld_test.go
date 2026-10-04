// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestControllerHeldLookalikeWarnsWithNoAdoptionHint is GitHub issue #1628,
// ruled 2026-09-27: a create whose identity-bearing arguments match a
// controller-held resource gets a lookalike-style warning naming the
// controller and its object, and no adoption hint - #1604 already ruled a
// controller-held resource is never offered for adoption.
//
// aws_security_group.web is declared with the same name as a security group
// carrying ACK's default tags. Before #1606 that group would have been an
// adoption Candidate and the lookalike guard would have warned about it (the
// matchTable path [TestLookalikesMatchTableCandidate] covers). After #1606
// the group leaves [discovery.Report.Unclaimed] entirely for
// [discovery.Report.ControllerHeld], which neither [Result.Candidates] nor
// [Result.Foreign] ever sees - so on the unfixed code this test's create
// gets no warning at all, and nothing here tells an operator their create is
// about to collide with a resource an in-cluster controller made.
func TestControllerHeldLookalikeWarnsWithNoAdoptionHint(t *testing.T) {
	held := discovery.ControllerHeldResource{
		TypeName:    "aws_security_group",
		ImportID:    "sg-controller",
		DisplayName: "live-e2e-main",
		Controller:  string(markers.ControllerACK),
		HeldBy: markers.ControllerHold{
			Controller: markers.ControllerACK,
			Tags: map[string]string{
				"services.k8s.aws/controller-version": "ec2-v1.2.3",
				"services.k8s.aws/namespace":          "team-a",
			},
		}.Describe(),
		Resource: cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("live-e2e-main")}),
	}

	res := classifyFixture(t, discovery.Result{Report: discovery.Report{
		Scans:          []discovery.TypeScan{scan("aws_security_group", 1)},
		Unbound:        []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")},
		ControllerHeld: []discovery.ControllerHeldResource{held},
	}})

	// The controller-held resource must stay out of both lists: it is
	// neither an adoption candidate (#1604's ruling) nor foreign (it is
	// not unowned, a controller owns it).
	if len(res.Candidates) != 0 {
		t.Fatalf("a controller-held resource was offered for adoption: %v", res.Candidates)
	}
	if len(res.Foreign) != 0 {
		t.Fatalf("a controller-held resource was reported foreign: %v", res.Foreign)
	}

	warnings := Lookalikes(Request{Estate: estateName}, res, []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")})
	if len(warnings) != 1 {
		t.Fatalf("want exactly one lookalike warning naming the controller-held resource, got %d: %v", len(warnings), warnings)
	}
	w := warnings[0]
	if w.Addr.String() != "aws_security_group.main" {
		t.Errorf("warning is for %s, want aws_security_group.main", w.Addr)
	}
	if w.LiveID != "sg-controller" {
		t.Errorf("warning names live ID %q, want sg-controller", w.LiveID)
	}
	if len(w.Matched) != 1 || w.Matched[0].Attr != "name" || w.Matched[0].Value != "live-e2e-main" {
		t.Errorf("warning carries matched arguments %v, want the name match", w.Matched)
	}
	if w.Hint != "" {
		t.Errorf("a controller-held lookalike carries an adoption hint %q; #1604 ruled none is offered", w.Hint)
	}
	if w.MarkerEstate != "" || w.MarkerAddress != "" {
		t.Errorf("a controller-held lookalike carries a marker pair (%q/%q) to stamp, which is not offered", w.MarkerEstate, w.MarkerAddress)
	}
	if !strings.Contains(w.HeldBy, "ACK ec2 controller") || !strings.Contains(w.HeldBy, "team-a") {
		t.Errorf("warning does not name the controller and its object: %q", w.HeldBy)
	}
}

// TestControllerHeldLookalikeAmbiguousStaysSilent: two controller-held
// resources matching the same content is the one-to-one rule's territory,
// same as an ordinary adoption pair - a guess here would point an operator
// at the wrong one.
func TestControllerHeldLookalikeAmbiguousStaysSilent(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("live-e2e-main")})
	held := discovery.ControllerHeldResource{
		TypeName: "aws_security_group", ImportID: "sg-one",
		Controller: string(markers.ControllerACK),
		HeldBy:     markers.ControllerHold{Controller: markers.ControllerACK, Tags: map[string]string{"services.k8s.aws/controller-version": "ec2-v1.2.3"}}.Describe(),
		Resource:   obj,
	}
	held2 := held
	held2.ImportID = "sg-two"

	res := classifyFixture(t, discovery.Result{Report: discovery.Report{
		Scans:          []discovery.TypeScan{scan("aws_security_group", 2)},
		Unbound:        []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")},
		ControllerHeld: []discovery.ControllerHeldResource{held, held2},
	}})

	warnings := Lookalikes(Request{Estate: estateName}, res, []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")})
	if len(warnings) != 0 {
		t.Errorf("two equally-matching controller-held resources produced a warning: %v", warnings)
	}
}
