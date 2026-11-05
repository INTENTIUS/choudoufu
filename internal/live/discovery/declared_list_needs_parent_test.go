// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"strings"
	"testing"
)

// A declared taggable type whose list schema requires a parent argument the
// config-driven scan does not set (aws_lb_listener's load_balancer_arn).
// listclient refuses that call rather than send it, and before the declared
// scan routed around the refusal it failed the plan: corpus-alb-complete's
// greenfield apply stopped on "Missing required list argument" for
// aws_lb_listener and aws_lb_listener_rule. The estate's tag index finds the
// type's objects by marker instead, with no list call.

const listenerARN = "arn:aws:elasticloadbalancing:us-east-1:000000000000:listener/app/main/50dc6c495c0c9188/f2f7dc8efc522ab2"

func listenerNeedsParent(cloud *fakeCloud) {
	cloud.withRequiredListAttr("aws_lb_listener", "load_balancer_arn")
}

func TestDeclaredTypeNeedingAParentIsFoundByMarker(t *testing.T) {
	cloud := newFakeCloud()
	ownAllDiscovered(cloud)
	listenerNeedsParent(cloud)

	srv := &taggingServer{}
	markedARN(srv, listenerARN, `aws_lb_listener.app`)

	res, diags := discoverFixture(t, cloud, taggingRequest(t, srv))
	assertNoErrors(t, diags)

	if ps := problemsOfKind(res, ProblemListFailed); len(ps) != 0 {
		t.Fatalf("the declared listener still failed to list:\n%s", renderProblems(ps))
	}
	b, ok := res.BindingFor(mustAddr(t, `aws_lb_listener.app`))
	if !ok {
		t.Fatalf("aws_lb_listener.app is not bound; the replan would propose a second listener:\n%s", res)
	}
	if b.ImportID != listenerARN {
		t.Errorf("aws_lb_listener.app bound to %q, want %q", b.ImportID, listenerARN)
	}
	scan, ok := res.ScanFor("aws_lb_listener")
	if !ok || scan.Source != SourceTagging {
		t.Errorf("aws_lb_listener scan = %+v, want Source=%s", scan, SourceTagging)
	}
	if _, asked := cloud.requestFor("aws_lb_listener"); asked {
		t.Error("the provider was asked to list aws_lb_listener with its required parent unset")
	}
}

// Greenfield: nothing carries the marker yet, so the instance is unbound
// (the plan creates it) and nothing fails.
func TestDeclaredTypeNeedingAParentPlansCreateOnGreenfield(t *testing.T) {
	cloud := newFakeCloud()
	ownAllDiscovered(cloud)
	listenerNeedsParent(cloud)

	res, diags := discoverFixture(t, cloud, taggingRequest(t, &taggingServer{}))
	assertNoErrors(t, diags)

	want := `aws_lb_listener.app`
	if _, ok := res.BindingFor(mustAddr(t, want)); ok {
		t.Fatal("aws_lb_listener.app bound with an empty tag index")
	}
	if !containsAddr(res.Unbound, want) {
		t.Errorf("aws_lb_listener.app is not unbound, so the plan would not create it:\n%s", res)
	}
}

// With no tag index there is nothing to find the listener with, and the
// refusal stands rather than a silent create beside a live listener.
func TestDeclaredTypeNeedingAParentStillRefusesWithoutAnIndex(t *testing.T) {
	cloud := newFakeCloud()
	ownAllDiscovered(cloud)
	listenerNeedsParent(cloud)

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("no error without a tag index:\n%s", res)
	}
	if !strings.Contains(renderDiags(diags), "Missing required list argument") {
		t.Errorf("want the missing-argument refusal, got:\n%s", renderDiags(diags))
	}
	if containsAddr(res.Unbound, `aws_lb_listener.app`) {
		t.Error("aws_lb_listener.app reported unbound although nobody looked")
	}
}
