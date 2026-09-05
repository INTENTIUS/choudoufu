// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// ---------------------------------------------------------------------------
// Pure-function unit tests: no network, no provider, fast.
// ---------------------------------------------------------------------------

func TestArnTypeLabel(t *testing.T) {
	tests := map[string]struct {
		arn  string
		want string
	}{
		"typed resource":     {"arn:aws:iam::000000000000:role/demo", "iam:role"},
		"bare id, no type":   {"arn:aws:s3:::my-bucket", "s3"},
		"colon-typed":        {"arn:aws:logs:us-east-1:000000000000:log-group:my-group", "logs:log-group"},
		"not an arn at all":  {"not-an-arn", "unknown"},
		"too few arn fields": {"arn:aws:s3", "unknown"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := arnTypeLabel(tc.arn); got != tc.want {
				t.Errorf("arnTypeLabel(%q) = %q, want %q", tc.arn, got, tc.want)
			}
		})
	}
}

func TestLiveLsItemFromTags(t *testing.T) {
	t.Run("readable marker decodes the type and address", func(t *testing.T) {
		tags := map[string]string{
			"tofu-estate":  "prod",
			"tofu-address": "aws_s3_bucket.data",
			"tofu-slot":    "0",
		}
		item := liveLsItemFromTags("arn:aws:s3:::my-bucket", tags, "tagging")
		if item.Type != "aws_s3_bucket" {
			t.Errorf("Type = %q, want aws_s3_bucket", item.Type)
		}
		if item.Address != "aws_s3_bucket.data" {
			t.Errorf("Address = %q, want aws_s3_bucket.data", item.Address)
		}
		if item.Slot != "0" {
			t.Errorf("Slot = %q, want 0", item.Slot)
		}
		if item.Source != "tagging" {
			t.Errorf("Source = %q, want tagging", item.Source)
		}
	})

	t.Run("no marker falls back to the ARN-derived type, empty address", func(t *testing.T) {
		item := liveLsItemFromTags("arn:aws:iam::000000000000:role/orphan", map[string]string{"tofu-estate": "prod"}, "iam")
		if item.Type != "iam:role" {
			t.Errorf("Type = %q, want iam:role", item.Type)
		}
		if item.Address != "" {
			t.Errorf("Address = %q, want empty", item.Address)
		}
	})

	t.Run("malformed marker (corrupt continuation) falls back too", func(t *testing.T) {
		// tofu-address-2 present with no tofu-address is the corrupt shape
		// GatherAddress reports.
		item := liveLsItemFromTags("arn:aws:sns:us-east-1:000000000000:my-topic", map[string]string{
			"tofu-estate":    "prod",
			"tofu-address-2": "trailing",
		}, "tagging")
		if item.Address != "" {
			t.Errorf("Address = %q, want empty for a corrupt marker", item.Address)
		}
		if item.Type != "sns" {
			t.Errorf("Type = %q, want sns", item.Type)
		}
	})

	t.Run("indexed instance address round-trips", func(t *testing.T) {
		tags := map[string]string{
			"tofu-estate":  "prod",
			"tofu-address": "aws_instance.pool:0",
		}
		item := liveLsItemFromTags("arn:aws:ec2:us-east-1:000000000000:instance/i-abc", tags, "tagging")
		if item.Address != `aws_instance.pool[0]` {
			t.Errorf("Address = %q, want aws_instance.pool[0]", item.Address)
		}
		if item.Type != "aws_instance" {
			t.Errorf("Type = %q, want aws_instance", item.Type)
		}
	})
}

func TestSortLiveLsItems(t *testing.T) {
	items := []views.LiveLsItem{
		{Type: "aws_s3_bucket", Address: "aws_s3_bucket.b", ID: "z"},
		{Type: "aws_iam_role", Address: "aws_iam_role.a", ID: "y"},
		{Type: "aws_s3_bucket", Address: "aws_s3_bucket.a", ID: "a"},
		{Type: "aws_s3_bucket", Address: "aws_s3_bucket.a", ID: "b"},
	}
	sortLiveLsItems(items)
	want := []string{"aws_iam_role", "aws_s3_bucket", "aws_s3_bucket", "aws_s3_bucket"}
	for i, w := range want {
		if items[i].Type != w {
			t.Fatalf("item %d type = %q, want %q (full: %+v)", i, items[i].Type, w, items)
		}
	}
	// Same type and address: broken by ID.
	if items[1].ID != "a" || items[2].ID != "b" {
		t.Errorf("tie-break by ID failed: got IDs %q, %q, want a, b", items[1].ID, items[2].ID)
	}
}

func liveLsTestResolution(t *testing.T, addr string, class identity.Class) identity.Resolution {
	t.Helper()
	a, diags := addrs.ParseAbsResourceInstanceStr(addr)
	if diags.HasErrors() {
		t.Fatalf("parsing %q: %s", addr, diags.Err())
	}
	return identity.Resolution{Addr: a, Class: class}
}

func TestLiveLsRung(t *testing.T) {
	schemas := map[string]providers.Schema{
		"aws_s3_bucket": {}, // Block is nil: markers.Taggable(nil) is false.
	}

	t.Run("record-backed", func(t *testing.T) {
		res := liveLsTestResolution(t, "aws_s3_bucket.data", identity.ClassRecordBacked)
		rung, detail, ok := liveLsRung(res, schemas)
		if !ok || rung != "record" || detail == "" {
			t.Fatalf("got rung=%q detail=%q ok=%v, want record/non-empty/true", rung, detail, ok)
		}
	})

	t.Run("record-located", func(t *testing.T) {
		res := liveLsTestResolution(t, "aws_s3_bucket.data", identity.ClassRecordLocated)
		rung, _, ok := liveLsRung(res, schemas)
		if !ok || rung != "record" {
			t.Fatalf("got rung=%q ok=%v, want record/true", rung, ok)
		}
	})

	t.Run("untaggable schema is declaration-carried", func(t *testing.T) {
		res := liveLsTestResolution(t, "aws_s3_bucket.data", identity.ClassConcrete)
		rung, detail, ok := liveLsRung(res, schemas)
		if !ok || rung != "declaration-carried" || detail == "" {
			t.Fatalf("got rung=%q detail=%q ok=%v, want declaration-carried/non-empty/true", rung, detail, ok)
		}
	})

	t.Run("no schema at all: not classified, not a gap", func(t *testing.T) {
		res := liveLsTestResolution(t, "aws_s3_bucket.data", identity.ClassNeedsDiscovery)
		_, _, ok := liveLsRung(res, map[string]providers.Schema{})
		if ok {
			t.Fatal("expected ok=false when no schema is available to check taggability")
		}
	})
}

// TestPollConsistent drives pollConsistentEvery directly, on a
// microsecond-scale interval and a small attempt bound, rather than
// pollConsistent itself: the two differ only in which constants they pass,
// and this is what keeps this test from actually waiting out
// consistentPollInterval/consistentMaxAttempts's real, minutes-scale bound.
func TestPollConsistent(t *testing.T) {
	const testInterval = time.Microsecond
	const testMaxAttempts = 5

	t.Run("agrees on the second read", func(t *testing.T) {
		calls := 0
		read := func(context.Context) ([]views.LiveLsItem, tfdiags.Diagnostics) {
			calls++
			return []views.LiveLsItem{{ID: "stable"}}, nil
		}
		items, attempts, stabilized, diags := pollConsistentEvery(context.Background(), read, testInterval, testMaxAttempts)
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %v", diags.Err())
		}
		if !stabilized {
			t.Error("expected stabilized = true")
		}
		if attempts != 2 {
			t.Errorf("attempts = %d, want 2 (a single read has nothing to agree with)", attempts)
		}
		if len(items) != 1 || items[0].ID != "stable" {
			t.Errorf("items = %+v", items)
		}
		if calls != 2 {
			t.Errorf("read was called %d time(s), want 2", calls)
		}
	})

	t.Run("never agrees: exhausts attempts and returns the last read", func(t *testing.T) {
		n := 0
		read := func(context.Context) ([]views.LiveLsItem, tfdiags.Diagnostics) {
			n++
			return []views.LiveLsItem{{ID: fmt.Sprintf("v%d", n)}}, nil
		}
		items, attempts, stabilized, _ := pollConsistentEvery(context.Background(), read, testInterval, testMaxAttempts)
		if stabilized {
			t.Error("expected stabilized = false")
		}
		if attempts != testMaxAttempts {
			t.Errorf("attempts = %d, want %d", attempts, testMaxAttempts)
		}
		if len(items) != 1 || items[0].ID != fmt.Sprintf("v%d", testMaxAttempts) {
			t.Errorf("items = %+v, want the last read", items)
		}
	})

	t.Run("canceled context stops the wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		n := 0
		read := func(context.Context) ([]views.LiveLsItem, tfdiags.Diagnostics) {
			n++
			if n == 1 {
				cancel()
			}
			return []views.LiveLsItem{{ID: fmt.Sprintf("v%d", n)}}, nil
		}
		_, _, stabilized, diags := pollConsistentEvery(ctx, read, time.Hour, testMaxAttempts)
		if stabilized {
			t.Error("expected stabilized = false")
		}
		if !diags.HasErrors() {
			t.Error("expected a diagnostic naming the cancellation")
		}
		if n != 1 {
			t.Errorf("read was called %d time(s) after cancellation, want 1", n)
		}
	})
}

// ---------------------------------------------------------------------------
// liveLsRead against a fake combined endpoint: real wire protocol for both
// passes (the Resource Groups Tagging API's JSON RPC, and IAM's query/XML
// protocol), one httptest server routing between them the way floci and
// real AWS both let AWS_ENDPOINT_URL route every service to one host.
//
// This is what exercises the IAM path GitHub issue #789 asks to have
// covered: real iam:ListRoles and iam:ListRoleTags requests, decoded by the
// real aws-sdk-go-v2/service/iam client this command builds, against
// responses shaped exactly as IAM's own query protocol wire format - the
// same fake-server technique internal/live/staterecord's ssm_test.go and
// s3_test.go already use for the same reason (a real account is not
// available to a unit test, and floci's own IAM support is exercised
// separately by TestLiveLsIAMAgainstFloci, gated behind Docker).
// ---------------------------------------------------------------------------

type fakeLiveLsRole struct {
	name string
	arn  string
	tags map[string]string
}

type fakeLiveLsServer struct {
	t *testing.T

	// tagged is the Resource Groups Tagging API's canned answer - already
	// curated to the estate under test, since this fake does not implement
	// GetResources' own server-side tag filtering.
	tagged []cloudcontrol.TaggedResource

	roles []fakeLiveLsRole

	// refuseRoleTagsFor fails the test outright if iam:ListRoleTags is ever
	// called for one of these role names - the assertion that
	// liveLsIAMRoles skips a role the tagging pass already found, rather
	// than reading its tags a second time.
	refuseRoleTagsFor map[string]bool

	roleTagsCalls map[string]int
}

func (s *fakeLiveLsServer) start() *httptest.Server {
	s.t.Helper()
	s.roleTagsCalls = map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if target := r.Header.Get("X-Amz-Target"); target != "" {
			s.handleTagging(w, r)
			return
		}
		s.handleIAM(w, r)
	}))
	s.t.Cleanup(server.Close)
	return server
}

func (s *fakeLiveLsServer) handleTagging(w http.ResponseWriter, r *http.Request) {
	var mappings []map[string]any
	for _, tr := range s.tagged {
		var tagList []map[string]string
		for k, v := range tr.Tags {
			tagList = append(tagList, map[string]string{"Key": k, "Value": v})
		}
		mappings = append(mappings, map[string]any{"ResourceARN": tr.ResourceARN, "Tags": tagList})
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	_ = json.NewEncoder(w).Encode(map[string]any{"ResourceTagMappingList": mappings})
}

func (s *fakeLiveLsServer) handleIAM(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/xml")

	switch r.PostForm.Get("Action") {
	case "ListRoles":
		var members strings.Builder
		for _, role := range s.roles {
			fmt.Fprintf(&members, `<member><Path>/</Path><RoleName>%s</RoleName><RoleId>AROAEXAMPLE</RoleId><Arn>%s</Arn><CreateDate>2024-01-01T00:00:00Z</CreateDate></member>`,
				role.name, role.arn)
		}
		fmt.Fprintf(w, `<ListRolesResponse xmlns="https://iam.amazonaws.com/doc/2010-05-08/">`+
			`<ListRolesResult><IsTruncated>false</IsTruncated><Roles>%s</Roles></ListRolesResult>`+
			`<ResponseMetadata><RequestId>req-list-roles</RequestId></ResponseMetadata></ListRolesResponse>`,
			members.String())

	case "ListRoleTags":
		roleName := r.PostForm.Get("RoleName")
		if s.refuseRoleTagsFor[roleName] {
			s.t.Errorf("iam:ListRoleTags was called for %q, which the tagging pass already found - liveLsIAMRoles should have skipped it", roleName)
		}
		s.roleTagsCalls[roleName]++
		var tags map[string]string
		for _, role := range s.roles {
			if role.name == roleName {
				tags = role.tags
			}
		}
		var members strings.Builder
		for k, v := range tags {
			fmt.Fprintf(&members, `<member><Key>%s</Key><Value>%s</Value></member>`, k, v)
		}
		fmt.Fprintf(w, `<ListRoleTagsResponse xmlns="https://iam.amazonaws.com/doc/2010-05-08/">`+
			`<ListRoleTagsResult><Tags>%s</Tags><IsTruncated>false</IsTruncated></ListRoleTagsResult>`+
			`<ResponseMetadata><RequestId>req-list-role-tags</RequestId></ResponseMetadata></ListRoleTagsResponse>`,
			members.String())

	default:
		http.Error(w, fmt.Sprintf("fakeLiveLsServer: unhandled IAM action %q", r.PostForm.Get("Action")), http.StatusBadRequest)
	}
}

// liveLsTestClients builds a tagging client and an IAM client both pointed
// at endpoint, the way LiveLsCommand.liveLs builds them in production - see
// that function's own comment on why the IAM client takes no explicit
// endpoint override.
func liveLsTestClients(t *testing.T, endpoint string) (*cloudcontrol.Client, *iam.Client) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")

	tagging := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: endpoint, Region: "us-east-1"})

	awsCfg, err := liveLsAWSConfig(context.Background(), "us-east-1")
	if err != nil {
		t.Fatalf("loading the AWS config: %v", err)
	}
	iamClient := iam.NewFromConfig(awsCfg, func(o *iam.Options) {
		// The one departure from production: BaseEndpoint here rather than
		// through AWS_ENDPOINT_URL, so this test does not depend on env var
		// propagation timing between t.Setenv and config load ordering.
		// TestLiveLsCommand_Run below is the one that goes through the real
		// AWS_ENDPOINT_URL path end to end.
		o.BaseEndpoint = aws.String(endpoint)
	})
	return tagging, iamClient
}

func TestLiveLsRead_mergesTaggingAndIAM(t *testing.T) {
	srv := &fakeLiveLsServer{
		t: t,
		tagged: []cloudcontrol.TaggedResource{
			{
				ResourceARN: "arn:aws:s3:::my-bucket",
				Tags:        map[string]string{"tofu-estate": "prod", "tofu-address": "aws_s3_bucket.data"},
			},
			{
				// Simulates an emulator (or a future real-AWS fix) whose
				// tagging index already unions in IAM: this role must NOT
				// be read a second time through ListRoleTags.
				ResourceARN: "arn:aws:iam::000000000000:role/shared-role",
				Tags:        map[string]string{"tofu-estate": "prod", "tofu-address": "aws_iam_role.shared"},
			},
		},
		roles: []fakeLiveLsRole{
			{name: "shared-role", arn: "arn:aws:iam::000000000000:role/shared-role", tags: map[string]string{"tofu-estate": "prod", "tofu-address": "aws_iam_role.shared"}},
			{name: "iam-only-role", arn: "arn:aws:iam::000000000000:role/iam-only-role", tags: map[string]string{"tofu-estate": "prod", "tofu-address": "aws_iam_role.only_here"}},
			{name: "other-estate-role", arn: "arn:aws:iam::000000000000:role/other-estate-role", tags: map[string]string{"tofu-estate": "staging"}},
			{name: "untagged-role", arn: "arn:aws:iam::000000000000:role/untagged-role", tags: map[string]string{}},
		},
		refuseRoleTagsFor: map[string]bool{"shared-role": true},
	}
	server := srv.start()

	tagging, iamClient := liveLsTestClients(t, server.URL)

	c := &LiveLsCommand{}
	items, diags := c.liveLsRead(context.Background(), "prod", tagging, iamClient)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diags.Err())
	}

	if len(items) != 3 {
		t.Fatalf("got %d item(s), want 3:\n%+v", len(items), items)
	}

	byID := map[string]views.LiveLsItem{}
	for _, item := range items {
		byID[item.ID] = item
	}

	bucket, ok := byID["arn:aws:s3:::my-bucket"]
	if !ok {
		t.Fatal("the tagged S3 bucket is missing from the listing")
	}
	if bucket.Address != "aws_s3_bucket.data" || bucket.Source != "tagging" {
		t.Errorf("bucket = %+v, want address aws_s3_bucket.data via tagging", bucket)
	}

	shared, ok := byID["arn:aws:iam::000000000000:role/shared-role"]
	if !ok {
		t.Fatal("the shared role is missing from the listing")
	}
	if shared.Source != "tagging" {
		t.Errorf("shared role Source = %q, want tagging (found by the tagging pass first)", shared.Source)
	}

	iamOnly, ok := byID["arn:aws:iam::000000000000:role/iam-only-role"]
	if !ok {
		t.Fatal("the IAM-only role is missing from the listing")
	}
	if iamOnly.Source != "iam" || iamOnly.Address != "aws_iam_role.only_here" {
		t.Errorf("iam-only role = %+v, want Source=iam and the decoded address", iamOnly)
	}

	if _, present := byID["arn:aws:iam::000000000000:role/other-estate-role"]; present {
		t.Error("a role tagged for a different estate leaked into the listing")
	}
	if _, present := byID["arn:aws:iam::000000000000:role/untagged-role"]; present {
		t.Error("an untagged role leaked into the listing")
	}

	if got := srv.roleTagsCalls["iam-only-role"]; got != 1 {
		t.Errorf("iam:ListRoleTags was called %d time(s) for iam-only-role, want 1", got)
	}
	if got := srv.roleTagsCalls["shared-role"]; got != 0 {
		t.Errorf("iam:ListRoleTags was called %d time(s) for the already-tagged shared-role, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// The whole command, through Run(), with -json - argument parsing, view
// selection, and the report both views render, all exercised together.
// ---------------------------------------------------------------------------

func TestLiveLsCommand_Run_json(t *testing.T) {
	srv := &fakeLiveLsServer{
		t: t,
		tagged: []cloudcontrol.TaggedResource{
			{
				ResourceARN: "arn:aws:s3:::my-bucket",
				Tags:        map[string]string{"tofu-estate": "prod", "tofu-address": "aws_s3_bucket.data"},
			},
		},
	}
	server := srv.start()

	t.Setenv("TOFU_LIVE_CLOUDCONTROL", "")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")

	view, done := testView(t)
	c := &LiveLsCommand{Meta: Meta{WorkingDir: workdir.NewDir(t.TempDir()), View: view}}

	code := c.Run([]string{"-no-color", "-json", "-estate=prod"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	var report struct {
		Estate string `json:"estate"`
		Items  []struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Address string `json:"address"`
			Source  string `json:"source"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(output.Stdout()), &report); err != nil {
		t.Fatalf("stdout is not the expected JSON report: %v\n%s", err, output.Stdout())
	}
	if report.Estate != "prod" {
		t.Errorf("estate = %q, want prod", report.Estate)
	}
	if len(report.Items) != 1 || report.Items[0].Type != "aws_s3_bucket" || report.Items[0].Address != "aws_s3_bucket.data" {
		t.Fatalf("items = %+v, want one aws_s3_bucket at aws_s3_bucket.data", report.Items)
	}
}

func TestLiveLsCommand_Run_noEstate(t *testing.T) {
	view, done := testView(t)
	c := &LiveLsCommand{Meta: Meta{WorkingDir: workdir.NewDir(t.TempDir()), View: view}}

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code == 0 {
		t.Fatalf("exit code 0, want non-zero for a missing -estate\nstdout:\n%s\nstderr:\n%s", output.Stdout(), output.Stderr())
	}
	if !strings.Contains(output.Stderr(), "No estate named") {
		t.Errorf("stderr does not explain the missing -estate:\n%s", output.Stderr())
	}
}
