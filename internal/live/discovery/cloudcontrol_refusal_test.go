// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/pkg/cloudcontrol"
)

// Cloud Control's ListResources refusing a type outright. Real AWS answers
// UnsupportedActionException for a type whose handler does not support
// LIST, and floci answers it from lex00/floci 5cf10ece1 (upstream #3141,
// pinned at 920d475 by #2428) for every type its read side does not
// enumerate, where the previous pin returned an empty list.
//
// The empty list was the worse answer: the Cloud Control leg read it as
// "this estate owns none", so a declared needs-discovery instance planned
// a create and the sweep recorded the type as covered. A refusal says
// something else, that this source cannot list the type, and the leg
// treats it exactly as it treats a type [cloudControlSource] never offered
// to Cloud Control at all: [scanType]'s remaining routes are tried (the
// service's own list API, then the estate's tag index or the record store
// for a declared instance), and with none of them the run refuses with
// [ProblemTypeNotListable] or files [SweepGapNotListable]. Neither the
// refusal nor the gap ever reads as "nothing exists".

// refusingCCRequest is a Request whose Cloud Control client refuses to
// list cfnType, with typeName mapped to it as a listable registry type.
func refusingCCRequest(t *testing.T, typeName, cfnType string) (Request, *ccServer) {
	t.Helper()
	srv := newCCServer(t)
	srv.listErr[cfnType] = cloudcontrol.CodeUnsupportedAction
	srv.listErrMessage[cfnType] = "ListResources is not supported for resource type " + cfnType + "."
	server := srv.start()
	t.Cleanup(server.Close)
	return Request{
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1}),
		Roster: ccRoster(t,
			map[string]string{typeName: cfnType},
			map[string]bool{cfnType: true},
			nil,
		),
	}, srv
}

func assertListedOnce(t *testing.T, srv *ccServer, cfnType string) {
	t.Helper()
	n := 0
	for _, c := range srv.calls {
		if c == "ListResources:"+cfnType {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want exactly one ListResources on %s, the refused one; calls were %v", cfnType, srv.calls)
	}
}

// A declared, taggable type Cloud Control refuses to list is found by the
// estate's tag index, the route a type with no list route at all already
// takes (issue #293). Before, the refusal was ProblemListFailed, an error
// that aborted the plan over a type the index could answer.
func TestCloudControlListRefusalFallsThroughToTheTagIndex(t *testing.T) {
	cloud := newFakeCloud()
	want := ownAllDiscovered(cloud)
	cloud.unlistable("aws_route_table")

	tagSrv := &taggingServer{}
	markedARN(tagSrv, "arn:aws:ec2:us-east-1:000000000000:route-table/rtb-1", `aws_route_table.main`)
	req := taggingRequest(t, tagSrv)
	ccReq, ccSrv := refusingCCRequest(t, "aws_route_table", "AWS::EC2::RouteTable")
	req.CloudControl, req.Roster = ccReq.CloudControl, ccReq.Roster

	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)
	assertBound(t, res, want)
	assertListedOnce(t, ccSrv, "AWS::EC2::RouteTable")

	if p := problemsOfKind(res, ProblemListFailed); len(p) != 0 {
		t.Errorf("a list refusal was reported as a failed list:\n%s", renderProblems(p))
	}
	scan, ok := res.ScanFor("aws_route_table")
	if !ok || scan.Source != SourceTagging || scan.Listed != 1 {
		t.Errorf("aws_route_table scan = %+v, want Source=%s Listed=1", scan, SourceTagging)
	}
}

// With no other route the refusal stands, as the known-unlistable refusal
// and naming Cloud Control's answer, and the instance is neither bound nor
// reported as absent.
func TestCloudControlListRefusalWithNoOtherRouteRefuses(t *testing.T) {
	cloud := newFakeCloud()
	ownAllDiscovered(cloud)
	cloud.unlistable("aws_route_table")

	req, ccSrv := refusingCCRequest(t, "aws_route_table", "AWS::EC2::RouteTable")
	res, diags := discoverFixture(t, cloud, req)
	if !diags.HasErrors() {
		t.Fatalf("a declared type no source can list produced no error:\n%s", res)
	}
	assertListedOnce(t, ccSrv, "AWS::EC2::RouteTable")

	if p := problemsOfKind(res, ProblemListFailed); len(p) != 0 {
		t.Errorf("a list refusal was reported as a failed list:\n%s", renderProblems(p))
	}
	problems := problemsOfKind(res, ProblemTypeNotListable)
	if len(problems) != 1 || problems[0].TypeName != "aws_route_table" {
		t.Fatalf("want one type-not-listable problem for aws_route_table:\n%s", res)
	}
	for _, want := range []string{"AWS::EC2::RouteTable", cloudcontrol.CodeUnsupportedAction} {
		if !strings.Contains(problems[0].Detail, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, problems[0].Detail)
		}
	}
	if _, ok := res.BindingFor(mustAddr(t, `aws_route_table.main`)); ok {
		t.Error("aws_route_table.main bound with no source able to list it")
	}
	if containsAddr(res.Unbound, `aws_route_table.main`) {
		t.Error("a list refusal was reported as the instance being absent")
	}
}

// The sweep files the not-listable gap and never records the type as
// covered: "nothing to destroy" must not be read off a refusal.
func TestCloudControlListRefusalSweepIsNotListableNotCovered(t *testing.T) {
	req, ccSrv := refusingCCRequest(t, "aws_efs_file_system", "AWS::EFS::FileSystem")
	req.Estate = ccEstate
	req.Config = ccConfig("aws_efs_file_system")
	req.Provider = newFakeCloud()
	req.Sweep = true
	req.SweepTypes = []string{"aws_efs_file_system"}

	res, diags := Discover(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("a refused sweep listing must not be an error: %s", diags.Err())
	}
	assertListedOnce(t, ccSrv, "AWS::EFS::FileSystem")

	if sweepCovered(res, "aws_efs_file_system") {
		t.Error("the sweep recorded a type Cloud Control refused to list as covered")
	}
	var gaps []SweepGap
	for _, g := range res.SweepGaps {
		if g.TypeName == "aws_efs_file_system" {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) != 1 || gaps[0].Reason != SweepGapNotListable {
		t.Fatalf("want one %s gap for aws_efs_file_system, got %+v", SweepGapNotListable, gaps)
	}
	if !strings.Contains(gaps[0].Detail, cloudcontrol.CodeUnsupportedAction) {
		t.Errorf("the gap does not name Cloud Control's refusal:\n%s", gaps[0].Detail)
	}
}
