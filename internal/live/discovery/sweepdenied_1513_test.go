// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// perParentCCServer answers a parent-scoped Cloud Control ListResources
// with the error code its parent value maps to, and an empty listing for a
// parent it does not name. [ccServer] keys its errors by CFN type alone,
// which cannot say "denied under one parent, throttled under another".
func perParentCCServer(t *testing.T, scopeProperty string, codeByParent map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TypeName      string `json:"TypeName"`
			ResourceModel string `json:"ResourceModel"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		var model map[string]string
		_ = json.Unmarshal([]byte(body.ResourceModel), &model)
		code, ok := codeByParent[model[scopeProperty]]
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []any{}})
			return
		}
		msg := code
		if code == cloudcontrol.CodeAccessDenied {
			msg = "User: arn:aws:sts::123456789012:assumed-role/plan/run is not authorized to perform: test:ListScopedChildren on resource: *"
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"__type": "com.amazonaws.cloudformation#" + code, "message": msg})
	}))
}

// TestParentScopedDeniedJoinsTheGroupOnlyWhenEveryParentIsDenied pins
// cloudcontrol_scoped.go's rule for GitHub issue #1052's grouping (#1513
// item 3): a parent-scoped type joins the one AccessDenied warning only
// when every parent's call was denied. One parent denied and another
// throttled keeps the type's own "Incomplete sweep" line, whose detail
// names both errors, and records no denial for the group.
func TestParentScopedDeniedJoinsTheGroupOnlyWhenEveryParentIsDenied(t *testing.T) {
	const cfnType = "AWS::Test::ScopedChild"
	spec := ParentScopedChildSpec{TypeName: "aws_test_scoped_child", Parent: "aws_test_parent", CFNScopeProperty: "ChildId"}

	run := func(t *testing.T, codes map[string]string) (*Result, []string) {
		t.Helper()
		server := perParentCCServer(t, spec.CFNScopeProperty, codes)
		t.Cleanup(server.Close)
		req := Request{
			Estate:       estateName,
			Config:       ccConfig("aws_test_parent", "aws_test_scoped_child"),
			CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1}),
			Roster:       scopedRoster(t, spec.TypeName, cfnType, []string{"ChildId"}, []string{"ChildId"}, false),
		}
		res := &Result{Verdicts: Verdicts{Resolutions: []identity.Resolution{
			{Addr: mustAddr(t, "aws_test_parent.a"), Class: identity.ClassConcrete, ImportID: "parent-a"},
			{Addr: mustAddr(t, "aws_test_parent.b"), Class: identity.ClassConcrete, ImportID: "parent-b"},
		}}}
		diags := parentScopedCloudControlSweepType(context.Background(), req, spec, res)
		if diags.HasErrors() {
			t.Fatalf("a failed parent-scoped listing must never be an error: %s", diags.Err())
		}
		var own []string
		for _, d := range diags {
			if d.Description().Summary == SummaryIncompleteSweep {
				own = append(own, d.Description().Detail)
			}
		}
		if _, ok := findSweepGap(res, spec.TypeName); !ok {
			t.Errorf("no sweep gap recorded for %s; the view's sweep-coverage section would lose it", spec.TypeName)
		}
		return res, own
	}

	t.Run("every parent denied joins the group", func(t *testing.T) {
		res, own := run(t, map[string]string{
			"parent-a": cloudcontrol.CodeAccessDenied,
			"parent-b": cloudcontrol.CodeAccessDenied,
		})
		if len(own) != 0 {
			t.Errorf("an all-denied parent-scoped type raised its own warning, want it grouped:\n%s", strings.Join(own, "\n"))
		}
		if len(res.sweepDenied) != 1 || res.sweepDenied[0].cfnType != cfnType || res.sweepDenied[0].action != "test:ListScopedChildren" {
			t.Fatalf("want one recorded denial for %s naming test:ListScopedChildren, got %+v", cfnType, res.sweepDenied)
		}
		grouped := deniedSweepDiag(res.sweepDenied)
		if len(grouped) != 1 || !strings.Contains(grouped[0].Description().Detail, "denied for 1 of the type the sweep covers ("+cfnType+")") {
			t.Errorf("the group does not carry the parent-scoped type:\n%v", grouped)
		}
	})

	t.Run("one parent denied and another throttled keeps its own line", func(t *testing.T) {
		res, own := run(t, map[string]string{
			"parent-a": cloudcontrol.CodeAccessDenied,
			"parent-b": cloudcontrol.CodeThrottlingError,
		})
		if len(res.sweepDenied) != 0 {
			t.Errorf("a mixed set joined the AccessDenied group, which names one cause of two: %+v", res.sweepDenied)
		}
		if len(own) != 1 {
			t.Fatalf("want the type's own warning line, got %d:\n%s", len(own), strings.Join(own, "\n"))
		}
		for _, want := range []string{"parent-a", cloudcontrol.CodeAccessDenied, "parent-b", cloudcontrol.CodeThrottlingError, "failed for 2 parent instance(s)"} {
			if !strings.Contains(own[0], want) {
				t.Errorf("the own line does not name %q:\n%s", want, own[0])
			}
		}
	})
}

// TestDeniedSweepWarningIsOneAcrossPasses is GitHub issue #1513 at the
// package boundary: two passes, each through its own provider
// configuration and each refused on its own type, deferred with
// [Request.DeferDeniedSweepWarning]. Neither Discover raises the warning;
// [DeniedSweepWarning] over both raises one, whose count, names and grant
// pattern cover both passes and whose text names both configurations. Each
// pass still records every gap, and the log names each type's
// configuration.
func TestDeniedSweepWarningIsOneAcrossPasses(t *testing.T) {
	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prev) })

	pass := func(t *testing.T, provider addrs.AbsProviderConfig, tf, cfn, action string) *Result {
		t.Helper()
		srv := newCCServer(t)
		srv.listErr[cfn] = cloudcontrol.CodeAccessDenied
		srv.listErrMessage[cfn] = fmt.Sprintf("User: arn:aws:sts::1:assumed-role/p/r is not authorized to perform: %s on resource: *", action)
		server := srv.start()
		t.Cleanup(server.Close)
		res, diags := Discover(context.Background(), Request{
			Estate:                  ccEstate,
			Config:                  ccConfig(),
			Provider:                newFakeCloud(),
			CloudControl:            cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1}),
			Roster:                  ccRoster(t, map[string]string{tf: cfn}, map[string]bool{cfn: true}, nil),
			Sweep:                   true,
			SweepTypes:              []string{tf},
			VouchProvider:           provider,
			DeferDeniedSweepWarning: true,
		})
		if diags.HasErrors() {
			t.Fatalf("unexpected error: %s", diags.Err())
		}
		for _, d := range diags {
			if d.Description().Summary == SummaryIncompleteSweep {
				t.Errorf("a deferred pass raised its own warning:\n%s", d.Description().Detail)
			}
		}
		if len(res.SweepGaps) != 1 {
			t.Errorf("want the pass's one gap recorded, got %v", res.SweepGaps)
		}
		return res
	}
	aws := addrs.NewDefaultProvider("aws")
	east := pass(t, addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: aws}, "aws_xray_group", "AWS::XRay::Group", "xray:GetGroups")
	west := pass(t, addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: aws, Alias: "west"}, "aws_accessanalyzer_analyzer", "AWS::AccessAnalyzer::Analyzer", "access-analyzer:ListAnalyzers")

	diags := DeniedSweepWarning(east, nil, west)
	if len(diags) != 1 {
		t.Fatalf("want one warning over both passes, got %d: %v", len(diags), diags)
	}
	got := diags[0].Description().Detail
	want := "Cloud Control ListResources was denied for 2 of the types the sweep covers (AWS::AccessAnalyzer::Analyzer, AWS::XRay::Group) through provider configurations aws and aws.west, so a resource of any of those types this estate owns but no longer declares WILL NOT be proposed for destruction by this run. Each denial names the read its type's list handler makes. Grant their roles access-analyzer:List*, xray:Get*, then re-run. Every denied type, the configuration it went through and the action it named is one [WARN] line in the log: run with TF_LOG=WARN, or TF_LOG_PATH to write it to a file."
	if got != want {
		t.Errorf("warning text differs.\n got: %s\nwant: %s", got, want)
	}
	for _, line := range []string{
		"sweep denied: Cloud Control ListResources on AWS::XRay::Group (for aws_xray_group, through provider aws) needs xray:GetGroups",
		"sweep denied: Cloud Control ListResources on AWS::AccessAnalyzer::Analyzer (for aws_accessanalyzer_analyzer, through provider aws.west) needs access-analyzer:ListAnalyzers",
	} {
		if !strings.Contains(logBuf.String(), line) {
			t.Errorf("the log lacks %q:\n%s", line, logBuf.String())
		}
	}
	if len(DeniedSweepWarning()) != 0 || len(DeniedSweepWarning(nil)) != 0 {
		t.Error("no passes, or no denials, must raise nothing")
	}
}

func TestProviderConfigLabel(t *testing.T) {
	aws := addrs.NewDefaultProvider("aws")
	for _, tc := range []struct {
		p    addrs.AbsProviderConfig
		want string
	}{
		{addrs.AbsProviderConfig{}, ""},
		{addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: aws}, "aws"},
		{addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: aws, Alias: "west"}, "aws.west"},
		{addrs.AbsProviderConfig{Module: addrs.Module{"net"}, Provider: aws, Alias: "west"}, "module.net.aws.west"},
	} {
		if got := providerConfigLabel(tc.p); got != tc.want {
			t.Errorf("providerConfigLabel(%s) = %q, want %q", tc.p, got, tc.want)
		}
	}
}
