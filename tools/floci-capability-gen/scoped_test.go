// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// classifyScopedAgainst mirrors classifyAgainst (sweep_test.go) for the
// scoped classifier. fakeCloudControl needs no changes to serve a scoped
// request: it never reads a ResourceModel field off the wire, the same way
// floci's real ListResources handler never does (see scoped.go's package
// doc) - so a fake that proves the unscoped classifier correct proves the
// scoped one's request/response handling correct too, and these tests only
// need to exercise what differs: the call made and the population it draws
// from.
func classifyScopedAgainst(t *testing.T, f *fakeCloudControl, tfType, cfnType string, requiredInput []string) typeRow {
	t.Helper()
	server := f.start(t)
	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1})
	row, err := classifyListResourcesScoped(context.Background(), cc, newSeeder(server.URL), tfType, cfnType, requiredInput)
	if err != nil {
		t.Fatalf("classifyListResourcesScoped(%s): %v", cfnType, err)
	}
	if row.Mechanism != "cloudcontrol-list-scoped" {
		t.Errorf("Mechanism = %q, want cloudcontrol-list-scoped", row.Mechanism)
	}
	return row
}

// TestClassifyScopedRoundTripCloses is the scoped leg's counterpart to
// TestClassifyRoundTripCloses: the only shape that may read implemented is
// the created resource coming back out of ListResourcesScoped.
func TestClassifyScopedRoundTripCloses(t *testing.T) {
	f := newFakeCloudControl()
	f.enumerates["AWS::WAFv2::WebACL"] = true

	row := classifyScopedAgainst(t, f, "aws_wafv2_web_acl", "AWS::WAFv2::WebACL", []string{"Scope"})
	if row.Status != "implemented" {
		t.Errorf("Status = %q, want implemented (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "the round trip closed") {
		t.Errorf("Evidence does not say a round trip was checked: %q", row.Evidence)
	}
	if !strings.Contains(row.Evidence, "CreateResource") {
		t.Errorf("Evidence does not name the create it round-tripped through: %q", row.Evidence)
	}
}

// TestClassifyScopedNeedsNoParentObject is the central design question issue
// #277 raised, made concrete: this leg's round trip must succeed with only
// a synthetic placeholder scope, never a resolved parent instance - because
// floci's ListResources ignores ResourceModel scoping entirely (verified
// directly against a running container; see scoped.go's package doc). A
// fake that DID filter by the scope value would fail this test the same way
// it would fail against real floci if the parent-object premise were true,
// so this asserts the leg's actual precondition rather than assuming it.
func TestClassifyScopedNeedsNoParentObject(t *testing.T) {
	f := newFakeCloudControl()
	f.enumerates["AWS::Connect::User"] = true

	row := classifyScopedAgainst(t, f, "aws_connect_user", "AWS::Connect::User", []string{"InstanceArn"})
	if row.Status != "implemented" {
		t.Fatalf("Status = %q, want implemented - a parent-object-free synthetic scope should still close the round trip (%s)", row.Status, row.Evidence)
	}
}

// TestClassifyScopedMultiPropertyRequiredInput covers a type whose
// list_required_input names more than one property (e.g.
// aws_vpclattice_listener_rule needs [ServiceIdentifier,
// ListenerIdentifier]) - every required property gets a placeholder value,
// never just the first.
func TestClassifyScopedMultiPropertyRequiredInput(t *testing.T) {
	f := newFakeCloudControl()
	f.enumerates["AWS::VpcLattice::Rule"] = true

	row := classifyScopedAgainst(t, f, "aws_vpclattice_listener_rule", "AWS::VpcLattice::Rule",
		[]string{"ServiceIdentifier", "ListenerIdentifier"})
	if row.Status != "implemented" {
		t.Errorf("Status = %q, want implemented (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "ServiceIdentifier") || !strings.Contains(row.Evidence, "ListenerIdentifier") {
		t.Errorf("Evidence does not name both required scoping properties: %q", row.Evidence)
	}
}

// TestClassifyScopedEvidenceDoesNotVaryWithTheGeneratedIdentifier is
// TestEvidenceDoesNotVaryWithTheGeneratedIdentifier's counterpart: the same
// reproducibility property has to hold for this leg's rows, or a re-probe
// of the same image stops producing a diff-free file for this mechanism.
func TestClassifyScopedEvidenceDoesNotVaryWithTheGeneratedIdentifier(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enumerates bool
	}{
		{"round trip closes", true},
		{"list stays empty", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var evidence []string
			for run := 0; run < 2; run++ {
				f := newFakeCloudControl()
				f.enumerates["AWS::T::T"] = tc.enumerates
				f.seq = run * 1000
				evidence = append(evidence, classifyScopedAgainst(t, f, "aws_t", "AWS::T::T", []string{"ParentId"}).Evidence)
			}
			if evidence[0] != evidence[1] {
				t.Errorf("two runs produced different evidence:\n  %q\n  %q", evidence[0], evidence[1])
			}
		})
	}
}

// TestClassifyScopedCreateSucceedsListStaysEmpty is this leg's most common
// real outcome (87 of 91 types at the current pin): the create really does
// create, and ListResourcesScoped answers an empty list, cleanly, forever -
// no store backs the type at all.
func TestClassifyScopedCreateSucceedsListStaysEmpty(t *testing.T) {
	f := newFakeCloudControl()

	row := classifyScopedAgainst(t, f, "aws_organizations_organizational_unit", "AWS::Organizations::OrganizationalUnit", []string{"ParentId"})
	if row.Status != "unimplemented" {
		t.Errorf("Status = %q, want unimplemented (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "none of them the identifier the create had just named") {
		t.Errorf("Evidence does not say what was looked for and missed: %q", row.Evidence)
	}
}

// TestClassifyScopedEnumeratedWithBlankModelIsPartial mirrors
// TestClassifyEnumeratedWithBlankModelIsPartial for the scoped leg.
func TestClassifyScopedEnumeratedWithBlankModelIsPartial(t *testing.T) {
	f := newFakeCloudControl()
	f.enumerates["AWS::SSO::Application"] = true
	f.blankProperties["AWS::SSO::Application"] = true

	row := classifyScopedAgainst(t, f, "aws_ssoadmin_application", "AWS::SSO::Application", []string{"InstanceArn"})
	if row.Status != "partial" {
		t.Errorf("Status = %q, want partial (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "empty Properties model") {
		t.Errorf("Evidence does not say what was missing: %q", row.Evidence)
	}
}

// TestClassifyScopedCreateRefusedIsUnverified mirrors
// TestClassifyCreateRefusedIsUnverified - the shape 4 of 91 types hit at the
// current pin (aws_api_gateway_stage, aws_apigatewayv2_stage,
// aws_eks_node_group, aws_route: each has a hand-coded CreateResource that
// refuses an empty desired state).
func TestClassifyScopedCreateRefusedIsUnverified(t *testing.T) {
	f := newFakeCloudControl()
	f.createRefusal["AWS::EKS::Nodegroup"] = "ValidationException"

	row := classifyScopedAgainst(t, f, "aws_eks_node_group", "AWS::EKS::Nodegroup", []string{"ClusterName"})
	if row.Status != "unverified" {
		t.Errorf("Status = %q, want unverified (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "nothing could be created to prove it answers") {
		t.Errorf("Evidence does not say why nothing was established: %q", row.Evidence)
	}
}

// TestClassifyScopedUnsupportedOperation mirrors
// TestClassifyUnsupportedOperation for the scoped leg's own list call.
func TestClassifyScopedUnsupportedOperation(t *testing.T) {
	f := newFakeCloudControl()
	f.listStatus = http.StatusBadRequest
	f.listBody = `{"__type":"com.amazonaws.cloudformation#UnsupportedOperation","message":"ListResources is not supported"}`

	row := classifyScopedAgainst(t, f, "aws_connect_queue", "AWS::Connect::Queue", []string{"InstanceArn"})
	if row.Status != "unimplemented" {
		t.Errorf("Status = %q, want unimplemented (%s)", row.Status, row.Evidence)
	}
}

// TestClassifyScopedBrokenHandler mirrors TestClassifyBrokenHandler.
func TestClassifyScopedBrokenHandler(t *testing.T) {
	f := newFakeCloudControl()
	f.listStatus = http.StatusInternalServerError
	f.listBody = "<html>not json</html>"

	row := classifyScopedAgainst(t, f, "aws_networkmanager_device", "AWS::NetworkManager::Device", []string{"GlobalNetworkId"})
	if row.Status != "broken" {
		t.Errorf("Status = %q, want broken (%s)", row.Status, row.Evidence)
	}
}

// TestClassifyScopedOrdinaryListErrorIsUnverified mirrors
// TestClassifyOrdinaryListErrorIsUnverified.
func TestClassifyScopedOrdinaryListErrorIsUnverified(t *testing.T) {
	f := newFakeCloudControl()
	f.listStatus = http.StatusBadRequest
	f.listBody = `{"__type":"com.amazonaws.cloudformation#ValidationException","message":"1 validation error detected"}`

	row := classifyScopedAgainst(t, f, "aws_t", "AWS::T::T", []string{"ParentId"})
	if row.Status != "unverified" {
		t.Errorf("Status = %q, want unverified (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "enumerated nothing") {
		t.Errorf("Evidence does not say the handler enumerated nothing: %q", row.Evidence)
	}
}

// TestNoScopedEvidenceStringClaimsMoreThanItChecked mirrors
// TestNoEvidenceStringClaimsMoreThanItChecked for the scoped leg, plus one
// check unique to this leg: an implemented verdict's evidence must say the
// scope was synthetic, never let a reader believe floci's scoping filter
// itself was exercised.
func TestNoScopedEvidenceStringClaimsMoreThanItChecked(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeCloudControl)
	}{
		{"round trip closes", func(f *fakeCloudControl) { f.enumerates["AWS::T::T"] = true }},
		{"list stays empty", func(f *fakeCloudControl) {}},
		{"create refused", func(f *fakeCloudControl) { f.createRefusal["AWS::T::T"] = "ValidationException" }},
		{"router refuses list", func(f *fakeCloudControl) {
			f.listStatus = http.StatusBadRequest
			f.listBody = `{"__type":"UnsupportedOperation","message":"nope"}`
		}},
		{"broken handler", func(f *fakeCloudControl) {
			f.listStatus = http.StatusInternalServerError
			f.listBody = "<html/>"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCloudControl()
			tc.setup(f)
			row := classifyScopedAgainst(t, f, "aws_t", "AWS::T::T", []string{"ParentId"})
			if row.Evidence == "" {
				t.Fatal("Evidence is empty")
			}
			if !strings.Contains(row.Evidence, "AWS::T::T") {
				t.Errorf("Evidence does not name the type it called: %q", row.Evidence)
			}
			if !strings.Contains(row.Evidence, "ListResourcesScoped") {
				t.Errorf("Evidence does not name the call it made: %q", row.Evidence)
			}
			if row.Status == "implemented" {
				if !strings.Contains(row.Evidence, "CreateResource") {
					t.Errorf("an implemented verdict whose evidence does not cite a create is a bare-call claim: %q", row.Evidence)
				}
				if !strings.Contains(row.Evidence, "synthetic placeholder") {
					t.Errorf("an implemented verdict must say its scope was synthetic, not a real parent: %q", row.Evidence)
				}
			}
			if !strings.Contains(row.Source, "round trip") {
				t.Errorf("Source does not say what kind of probe wrote this: %q", row.Source)
			}
		})
	}
}
