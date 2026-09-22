// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestCloudControlAccessDeniedSweepGapsGroup is GitHub issue #1052: a
// least-privilege role that cannot call each Cloud Control list handler's
// own read gets AccessDeniedException per type, and the run used to print
// one "Incomplete sweep" warning per type - hundreds, burying the one
// fatal error underneath them.
//
// The ruling (2026-09-21): one warning, grouped by cause. Every
// AccessDenied Cloud Control type collapses to one warning with the count,
// the first five type names and the one IAM action pattern to grant. Other
// causes keep their own line. The full list goes to the log. Nothing is
// hidden: [Result.SweepGaps] still holds every gap.
//
// 300 denied types, each denial naming a different <service>:<Action>, plus
// one throttled type in the same run. Two warnings, 301 gaps, and every
// denied type by name in the log.
func TestCloudControlAccessDeniedSweepGapsGroup(t *testing.T) {
	srv := newCCServer(t)

	mapped := map[string]string{}
	listable := map[string]bool{}
	var sweepTypes []string
	var deniedCFN []string
	add := func(tf, cfn, action string) {
		mapped[tf] = cfn
		listable[cfn] = true
		sweepTypes = append(sweepTypes, tf)
		deniedCFN = append(deniedCFN, cfn)
		srv.listErr[cfn] = cloudcontrol.CodeAccessDenied
		srv.listErrMessage[cfn] = fmt.Sprintf(
			"User: arn:aws:sts::123456789012:assumed-role/choudoufu-plan/run is not authorized to perform: %s on resource: * because no identity-based policy allows the %s action",
			action, action)
	}
	// Three from the issue's own report, then enough synthetic ones to
	// reach 300, every one naming a service of its own.
	add("aws_xray_group", "AWS::XRay::Group", "xray:GetGroups")
	add("aws_accessanalyzer_analyzer", "AWS::AccessAnalyzer::Analyzer", "access-analyzer:ListAnalyzers")
	add("aws_workspacesweb_portal", "AWS::WorkSpacesWeb::Portal", "workspaces-web:ListPortals")
	for i := 0; len(deniedCFN) < 300; i++ {
		add(fmt.Sprintf("aws_svc%03d_thing", i), fmt.Sprintf("AWS::Svc%03d::Thing", i), fmt.Sprintf("svc%03d:ListThings", i))
	}
	// One more type, throttled rather than denied: a different fact, and
	// it keeps its own line.
	mapped["aws_efs_file_system"] = "AWS::EFS::FileSystem"
	listable["AWS::EFS::FileSystem"] = true
	sweepTypes = append(sweepTypes, "aws_efs_file_system")
	srv.listErr["AWS::EFS::FileSystem"] = cloudcontrol.CodeThrottlingError

	server := srv.start()
	t.Cleanup(server.Close)

	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prev) })

	// MaxAttempts: 1 - the throttled type must not sit through backoff.
	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1})
	req := Request{
		Estate:       ccEstate,
		Config:       ccConfig(),
		Provider:     newFakeCloud(),
		CloudControl: cc,
		Roster:       ccRoster(t, mapped, listable, nil),
		Sweep:        true,
		SweepTypes:   sweepTypes,
	}
	res, diags := Discover(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("a denied sweep listing must never be an error: %s", diags.Err())
	}

	var sweepWarnings []tfdiags.Diagnostic
	for _, d := range diags {
		if d.Description().Summary == SummaryIncompleteSweep {
			sweepWarnings = append(sweepWarnings, d)
		}
	}
	if len(sweepWarnings) != 2 {
		var summaries []string
		for _, d := range sweepWarnings {
			summaries = append(summaries, d.Description().Detail)
		}
		t.Fatalf("want 2 %q warnings (one grouped AccessDenied, one throttled), got %d:\n%s",
			SummaryIncompleteSweep, len(sweepWarnings), strings.Join(summaries, "\n"))
	}

	var grouped, throttled string
	for _, d := range sweepWarnings {
		detail := d.Description().Detail
		if strings.Contains(detail, "AWS::EFS::FileSystem") {
			throttled = detail
		} else {
			grouped = detail
		}
	}
	if throttled == "" || !strings.Contains(throttled, cloudcontrol.CodeThrottlingError) {
		t.Errorf("the throttled type did not keep its own warning line naming ThrottlingException:\n%s", throttled)
	}
	if grouped == "" {
		t.Fatal("no grouped AccessDenied warning")
	}
	t.Logf("grouped warning:\n%s", grouped)

	sort.Strings(deniedCFN)
	for _, want := range append([]string{"300"}, deniedCFN[:5]...) {
		if !strings.Contains(grouped, want) {
			t.Errorf("the grouped warning does not carry %q:\n%s", want, grouped)
		}
	}
	if !strings.Contains(grouped, "295 more") {
		t.Errorf("the grouped warning does not say how many types it did not name:\n%s", grouped)
	}
	for _, cfn := range deniedCFN[5:] {
		if strings.Contains(grouped, cfn) {
			t.Errorf("the grouped warning names %s, past the first five:\n%s", cfn, grouped)
		}
	}
	// The pattern to grant: one action per service, 300 services, too many
	// to list, so the collapsed per-service pattern for the first five and
	// the count of the rest. TestGrantPattern pins the small sets.
	for _, want := range []string{"access-analyzer:List*", "svc000:List*", "and 295 more per-service patterns"} {
		if !strings.Contains(grouped, want) {
			t.Errorf("the grouped warning does not name the pattern %q to grant:\n%s", want, grouped)
		}
	}

	// Nothing is hidden: every gap is still recorded, all LIST_FAILED.
	if len(res.SweepGaps) != 301 {
		t.Errorf("want 301 sweep gaps recorded (300 denied + 1 throttled), got %d", len(res.SweepGaps))
	}
	for _, g := range res.SweepGaps {
		if g.Reason != SweepGapListFailed {
			t.Errorf("gap %s has reason %s, want %s", g.TypeName, g.Reason, SweepGapListFailed)
		}
	}

	// The full list goes to the log: every denied type by name, and the
	// action its denial named.
	logged := logBuf.String()
	for _, cfn := range deniedCFN {
		if !strings.Contains(logged, cfn) {
			t.Errorf("the log does not name denied type %s", cfn)
		}
	}
	for _, action := range []string{"xray:GetGroups", "access-analyzer:ListAnalyzers", "svc296:ListThings"} {
		if !strings.Contains(logged, action) {
			t.Errorf("the log does not name the denied action %s", action)
		}
	}
	if !strings.Contains(logged, "300") {
		t.Error("the log does not carry the count of denied types")
	}
}

// TestCloudControlAccessDeniedFewServices is the issue's own three types:
// with few services, the one warning names every per-service pattern, verbs
// collapsed, and no "and N more".
func TestCloudControlAccessDeniedFewServices(t *testing.T) {
	srv := newCCServer(t)
	deny := func(cfn, action string) {
		srv.listErr[cfn] = cloudcontrol.CodeAccessDenied
		srv.listErrMessage[cfn] = fmt.Sprintf("User: arn:aws:sts::123456789012:assumed-role/plan/run is not authorized to perform: %s on resource: *", action)
	}
	deny("AWS::XRay::Group", "xray:GetGroups")
	deny("AWS::XRay::SamplingRule", "xray:GetSamplingRules")
	deny("AWS::AccessAnalyzer::Analyzer", "access-analyzer:ListAnalyzers")
	deny("AWS::WorkSpacesWeb::Portal", "workspaces-web:ListPortals")
	server := srv.start()
	t.Cleanup(server.Close)

	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prev) })

	mapped := map[string]string{
		"aws_xray_group":              "AWS::XRay::Group",
		"aws_xray_sampling_rule":      "AWS::XRay::SamplingRule",
		"aws_accessanalyzer_analyzer": "AWS::AccessAnalyzer::Analyzer",
		"aws_workspacesweb_portal":    "AWS::WorkSpacesWeb::Portal",
	}
	listable := map[string]bool{}
	var sweepTypes []string
	for tf, cfn := range mapped {
		listable[cfn] = true
		sweepTypes = append(sweepTypes, tf)
	}
	sort.Strings(sweepTypes)
	req := Request{
		Estate:       ccEstate,
		Config:       ccConfig(),
		Provider:     newFakeCloud(),
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1}),
		Roster:       ccRoster(t, mapped, listable, nil),
		Sweep:        true,
		SweepTypes:   sweepTypes,
	}
	res, diags := Discover(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("unexpected error: %s", diags.Err())
	}
	var details []string
	for _, d := range diags {
		if d.Description().Summary == SummaryIncompleteSweep {
			details = append(details, d.Description().Detail)
		}
	}
	if len(details) != 1 {
		t.Fatalf("want one grouped warning, got %d:\n%s", len(details), strings.Join(details, "\n"))
	}
	t.Logf("grouped warning:\n%s", details[0])
	want := "Cloud Control ListResources was denied for 4 of the types the sweep covers (AWS::AccessAnalyzer::Analyzer, AWS::WorkSpacesWeb::Portal, AWS::XRay::Group, AWS::XRay::SamplingRule), so a resource of any of those types this estate owns but no longer declares WILL NOT be proposed for destruction by this run. Each denial names the read its type's list handler makes. Grant the role access-analyzer:List*, workspaces-web:List*, xray:Get*, then re-run. Every denied type and the action it named is one [WARN] line in the log: run with TF_LOG=WARN, or TF_LOG_PATH to write it to a file."
	if details[0] != want {
		t.Errorf("grouped warning text differs.\n got: %s\nwant: %s", details[0], want)
	}
	if len(res.SweepGaps) != 4 {
		t.Errorf("want 4 sweep gaps, got %d", len(res.SweepGaps))
	}
	for _, line := range []string{
		"[WARN] stateless/discovery: sweep denied: Cloud Control ListResources on AWS::XRay::Group (for aws_xray_group) needs xray:GetGroups: cloudcontrol: ListResources: AccessDeniedException (HTTP 400)",
		"[WARN] stateless/discovery: the sweep was denied Cloud Control ListResources on 4 types; each is logged above with the action its denial named. Denied: AWS::AccessAnalyzer::Analyzer, AWS::WorkSpacesWeb::Portal, AWS::XRay::Group, AWS::XRay::SamplingRule",
	} {
		if !strings.Contains(logBuf.String(), line) {
			t.Errorf("the log lacks the line %q; log:\n%s", line, logBuf.String())
		}
	}
}

func TestGrantPattern(t *testing.T) {
	for _, tc := range []struct {
		actions []string
		want    string
	}{
		{nil, "the action each denial names (in the log)"},
		{[]string{"xray:GetGroups"}, "xray:GetGroups"},
		{[]string{"xray:GetGroups", "xray:GetSamplingRules"}, "xray:Get*"},
		{[]string{"ec2:DescribeVpcs", "xray:GetGroups", "xray:ListTagsForResource"}, "ec2:Describe*, xray:Get*, xray:List*"},
		// A bare verb, or an action with no verb prefix, stays as it is.
		{[]string{"s3:ListAllMyBuckets", "sts:*"}, "s3:List*, sts:*"},
		// Six patterns still fit; seven collapse to five and a count.
		{[]string{"a:ListX", "b:ListX", "c:ListX", "d:ListX", "e:ListX", "f:ListX"}, "a:List*, b:List*, c:List*, d:List*, e:List*, f:List*"},
		{[]string{"a:ListX", "b:ListX", "c:ListX", "d:ListX", "e:ListX", "f:ListX", "g:ListX"}, "a:List*, b:List*, c:List*, d:List*, e:List* and 2 more per-service patterns (every one is in the log), or AWS's managed ReadOnlyAccess policy if it may read the whole account"},
	} {
		if got := grantPattern(tc.actions); got != tc.want {
			t.Errorf("grantPattern(%v)\n got: %s\nwant: %s", tc.actions, got, tc.want)
		}
	}
}

func TestDeniedAction(t *testing.T) {
	err := &cloudcontrol.APIError{Op: "ListResources", StatusCode: 400, Code: cloudcontrol.CodeAccessDenied,
		Message: "User: arn:aws:sts::1:assumed-role/p/r is not authorized to perform: workspaces-web:ListPortals on resource: arn:aws:workspaces-web:us-east-1:1:portal/* because no identity-based policy allows the workspaces-web:ListPortals action"}
	if got := deniedAction(err); got != "workspaces-web:ListPortals" {
		t.Errorf("got %q", got)
	}
	if got := deniedAction(fmt.Errorf("cloudcontrol: ListResources: %w", err)); got != "workspaces-web:ListPortals" {
		t.Errorf("wrapped: got %q", got)
	}
	// An emulator's bare code names nothing, and the group still forms.
	bare := &cloudcontrol.APIError{Op: "ListResources", StatusCode: 400, Code: cloudcontrol.CodeAccessDenied, Message: cloudcontrol.CodeAccessDenied}
	if got := deniedAction(bare); got != "" {
		t.Errorf("bare code: got %q, want empty", got)
	}
	if got := deniedAction(nil); got != "" {
		t.Errorf("nil: got %q", got)
	}
}
