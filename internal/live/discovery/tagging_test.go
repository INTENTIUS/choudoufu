// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// ---------------------------------------------------------------------------
// The ARN join, against the real committed artifacts: this is the
// "which ARN shapes resolve" question issue #51 asks for, answered against
// live/mapping.json and live/registry.json rather than a hand-built fixture,
// so a drift between the join table and the real artifacts shows up here.
// ---------------------------------------------------------------------------

func realRoster(t *testing.T) *registry.Roster {
	t.Helper()
	root := flocitest.RepoRoot(t)
	mappingPath := filepath.Join(root, "live", "mapping.json")
	registryPath := filepath.Join(root, "live", "registry.json")
	if _, err := os.Stat(mappingPath); err != nil {
		t.Skipf("live/mapping.json not present in this checkout: %v", err)
	}
	if _, err := os.Stat(registryPath); err != nil {
		t.Skipf("live/registry.json not present in this checkout: %v", err)
	}
	r, err := registry.Load(mappingPath, registryPath)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	return r
}

// TestJoinTaggedResourceRealArtifacts is the table-driven coverage the issue
// asks for, one row per required ARN shape (plus the service-name-mismatch
// and ambiguity cases the ARN join table exists to handle), joined against
// the real live/mapping.json and live/registry.json.
func TestJoinTaggedResourceRealArtifacts(t *testing.T) {
	roster := realRoster(t)

	tests := []struct {
		name             string
		arn              string
		wantTypeName     string
		wantIdentityAttr string
		// wantImportID is checked verbatim when set; wantImportIsARN checks
		// the import ID equals the ARN itself instead (the arn-identity
		// types, whose exact ARN this test does not want to duplicate).
		wantImportID    string
		wantImportIsARN bool
		wantOK          bool
		wantReasonHas   []string
	}{
		{
			name:             "iam role: name is the ARN's resource id",
			arn:              "arn:aws:iam::123456789012:role/deploy",
			wantTypeName:     "aws_iam_role",
			wantIdentityAttr: "id",
			wantImportID:     "deploy",
			wantOK:           true,
		},
		{
			name:             "s3 bucket: bare ARN, no resource-type segment",
			arn:              "arn:aws:s3:::my-estate-bucket",
			wantTypeName:     "aws_s3_bucket",
			wantIdentityAttr: "id",
			wantImportID:     "my-estate-bucket",
			wantOK:           true,
		},
		{
			name:             "dynamodb table",
			arn:              "arn:aws:dynamodb:us-east-1:123456789012:table/orders",
			wantTypeName:     "aws_dynamodb_table",
			wantIdentityAttr: "id",
			wantImportID:     "orders",
			wantOK:           true,
		},
		{
			name:             "logs log-group: type:id, id itself starting with /",
			arn:              "arn:aws:logs:us-east-1:123456789012:log-group:/estate/app",
			wantTypeName:     "aws_cloudwatch_log_group",
			wantIdentityAttr: "id",
			wantImportID:     "/estate/app",
			wantOK:           true,
		},
		{
			name:             "sns topic: identity IS the arn",
			arn:              "arn:aws:sns:us-east-1:123456789012:alerts",
			wantTypeName:     "aws_sns_topic",
			wantIdentityAttr: "arn",
			wantImportIsARN:  true,
			wantOK:           true,
		},
		{
			name:             "elasticloadbalancing target group: identity IS the arn",
			arn:              "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/app-tg/6d0ecf831eec9f09",
			wantTypeName:     "aws_lb_target_group",
			wantIdentityAttr: "arn",
			wantImportIsARN:  true,
			wantOK:           true,
		},
		{
			name:             "elasticloadbalancing v2 load balancer: 3-part id resolves to V2",
			arn:              "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/main/50dc6c495c0c9188",
			wantTypeName:     "aws_lb",
			wantIdentityAttr: "arn",
			wantImportIsARN:  true,
			wantOK:           true,
		},
		{
			name:          "elasticloadbalancing classic load balancer: 1-part id, unmapped CFN type",
			arn:           "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/classic-name",
			wantOK:        false,
			wantReasonHas: []string{"AWS::ElasticLoadBalancing::LoadBalancer", "no live/mapping.json row"},
		},
		{
			name:             "ec2 vpc",
			arn:              "arn:aws:ec2:us-east-1:123456789012:vpc/vpc-0123456789abcdef0",
			wantTypeName:     "aws_vpc",
			wantIdentityAttr: "id",
			wantImportID:     "vpc-0123456789abcdef0",
			wantOK:           true,
		},
		{
			name:             "kms key",
			arn:              "arn:aws:kms:us-east-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab",
			wantTypeName:     "aws_kms_key",
			wantIdentityAttr: "id",
			wantImportID:     "1234abcd-12ab-34cd-56ef-1234567890ab",
			wantOK:           true,
		},
		{
			name:             "route53 hosted zone: global, no region or account in the ARN",
			arn:              "arn:aws:route53:::hostedzone/Z1D633PJN98FT9",
			wantTypeName:     "aws_route53_zone",
			wantIdentityAttr: "id",
			wantImportID:     "Z1D633PJN98FT9",
			wantOK:           true,
		},
		{
			name:             "acm certificate: ARN service (acm) differs from the CFN service segment (CertificateManager)",
			arn:              "arn:aws:acm:us-east-1:123456789012:certificate/8f9c1b2e-0000-0000-0000-000000000000",
			wantTypeName:     "aws_acm_certificate",
			wantIdentityAttr: "arn",
			wantImportIsARN:  true,
			wantOK:           true,
		},
		{
			name:             "step functions state machine: ARN service (states) differs from the CFN service segment (StepFunctions)",
			arn:              "arn:aws:states:us-east-1:123456789012:stateMachine:pipeline",
			wantTypeName:     "aws_sfn_state_machine",
			wantIdentityAttr: "arn",
			wantImportIsARN:  true,
			wantOK:           true,
		},
		{
			name:          "ec2 security group rule: genuinely ambiguous between ingress and egress",
			arn:           "arn:aws:ec2:us-east-1:123456789012:security-group-rule/sgr-0123456789abcdef0",
			wantOK:        false,
			wantReasonHas: []string{"AWS::EC2::SecurityGroupEgress", "AWS::EC2::SecurityGroupIngress", "more than one CFN type"},
		},
		{
			name:          "ec2 nat gateway: mapped in live/mapping.json but not in identity.DefaultTable",
			arn:           "arn:aws:ec2:us-east-1:123456789012:natgateway/nat-0123456789abcdef0",
			wantOK:        false,
			wantReasonHas: []string{"aws_nat_gateway", "internal/live/identity's table"},
		},
		{
			// aws_lambda_function joined the identity table when the first
			// registry-ratified batch admitted it; the mapped-but-unadmitted
			// case lived on with aws_instance until the EC2 core batch
			// (issue #65) admitted it too, and now lives on with
			// aws_nat_gateway above.
			name:             "lambda function: mapped and admitted, function name binds",
			arn:              "arn:aws:lambda:us-east-1:123456789012:function:my-function",
			wantTypeName:     "aws_lambda_function",
			wantIdentityAttr: "id",
			wantImportID:     "my-function",
			wantOK:           true,
		},
		{
			name:          "unknown service entirely",
			arn:           "arn:aws:glue:us-east-1:123456789012:table/db/tbl",
			wantOK:        false,
			wantReasonHas: []string{`service "glue"`, `resource segment "table"`},
		},
		{
			name:          "not an ARN at all",
			arn:           "not-an-arn",
			wantOK:        false,
			wantReasonHas: []string{"does not have the arn:partition:service:region:account:resource shape"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := joinTaggedResource(roster, tt.arn)
			if got.ok != tt.wantOK {
				t.Fatalf("joinTaggedResource(%q).ok = %v, want %v (reason: %s)", tt.arn, got.ok, tt.wantOK, got.reason)
			}
			if !tt.wantOK {
				for _, frag := range tt.wantReasonHas {
					if !strings.Contains(got.reason, frag) {
						t.Errorf("reason %q does not mention %q", got.reason, frag)
					}
				}
				if got.importID != "" {
					t.Errorf("an unresolved join still produced an import ID: %q", got.importID)
				}
				return
			}
			if got.typeName != tt.wantTypeName {
				t.Errorf("typeName = %q, want %q", got.typeName, tt.wantTypeName)
			}
			if got.identityAttr != tt.wantIdentityAttr {
				t.Errorf("identityAttr = %q, want %q", got.identityAttr, tt.wantIdentityAttr)
			}
			if tt.wantImportIsARN {
				if got.importID != tt.arn {
					t.Errorf("importID = %q, want the raw ARN %q (identity IS the arn)", got.importID, tt.arn)
				}
			} else if got.importID != tt.wantImportID {
				t.Errorf("importID = %q, want %q", got.importID, tt.wantImportID)
			}
		})
	}
}

// TestJoinTaggedResourceAmbiguousRosterMapping covers the reverse-join
// hazard [registry.Roster.TFTypesForCFNType] itself names: a CFN type the
// ARN side resolves uniquely, but that live/mapping.json maps from more than
// one TF type. Today's real live/mapping.json never produces this (see
// TestTFTypesForCFNTypeAmbiguous in the registry package), so it is
// exercised here with a small fixture roster instead.
func TestJoinTaggedResourceAmbiguousRosterMapping(t *testing.T) {
	mapping := `{"generated_by": "test", "counts": {}, "rows": [
		{"tf_type": "aws_thing_a", "cfn_type": "AWS::IAM::Role", "via": "name", "fold_parent": null, "note": null},
		{"tf_type": "aws_thing_b", "cfn_type": "AWS::IAM::Role", "via": "alias", "fold_parent": null, "note": null}
	]}`
	registryJSON := `{"pin": {}, "generated_by": "test", "counts": {}, "types": []}`
	roster, err := registry.Parse([]byte(mapping), []byte(registryJSON))
	if err != nil {
		t.Fatalf("registry.Parse: %v", err)
	}

	got := joinTaggedResource(roster, "arn:aws:iam::123456789012:role/deploy")
	if got.ok {
		t.Fatalf("an ambiguous CFN-to-TF mapping resolved anyway: %+v", got)
	}
	for _, frag := range []string{"aws_thing_a", "aws_thing_b", "more than one TF type"} {
		if !strings.Contains(got.reason, frag) {
			t.Errorf("reason %q does not mention %q", got.reason, frag)
		}
	}
}

// ---------------------------------------------------------------------------
// Wiring into the sweep: a fake Tagging API endpoint, end to end through
// Discover.
// ---------------------------------------------------------------------------

// taggingServer is a fake Resource Groups Tagging API endpoint: one
// GetResources page, returned verbatim regardless of the request body
// beyond recording it was called.
type taggingServer struct {
	arns  []string
	tags  map[string]map[string]string // ARN -> tags
	calls int
}

func (s *taggingServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls++
		var mappings []map[string]any
		for _, arn := range s.arns {
			var tagList []map[string]string
			for k, v := range s.tags[arn] {
				tagList = append(tagList, map[string]string{"Key": k, "Value": v})
			}
			mappings = append(mappings, map[string]any{"ResourceARN": arn, "Tags": tagList})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceTagMappingList": mappings})
	}))
}

// taggingRoster builds a Roster mapping the given tf_type to cfnType,
// taggable, for the sweep-wiring tests: they only need enough of
// live/mapping.json and live/registry.json to reach [arnJoinCovers] and
// [registry.Roster.Taggable], not the real committed artifacts.
func taggingRoster(t *testing.T, tfType, cfnType string, taggable bool) *registry.Roster {
	t.Helper()
	return ccRoster(t,
		map[string]string{tfType: cfnType},
		nil,
		map[string]bool{cfnType: taggable},
	)
}

// TestTaggingSweepFindsDeletedBlock mirrors sweep_test.go's
// TestSweepFindsDeletedBlock, but through the Tagging API path: a live
// aws_cloudwatch_log_group the configuration no longer declares, found by
// one GetResources call rather than a per-type ListResources.
func TestTaggingSweepFindsDeletedBlock(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	// Deliberately not cloud.listable("aws_cloudwatch_log_group"): if the
	// code fell back to the old per-type provider sweep, this type would be
	// unlistable and reported as a gap rather than found.

	arn := "arn:aws:logs:us-east-1:123456789012:log-group:/estate/deleted"
	srv := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{
			arn: {TagEstate: estateName, TagAddress: `aws_cloudwatch_log_group.deleted`},
		},
	}
	server := srv.start(t)
	defer server.Close()

	req := Request{
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		Roster:       taggingRoster(t, "aws_cloudwatch_log_group", "AWS::Logs::LogGroup", true),
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	if srv.calls != 1 {
		t.Errorf("GetResources was called %d times, want exactly 1", srv.calls)
	}

	rm := removalsByAddr(res)
	o, ok := rm[`aws_cloudwatch_log_group.deleted`]
	if !ok {
		t.Fatalf("the deleted block's resource is not a removal:\n%s", res)
	}
	if o.ImportID != "/estate/deleted" {
		t.Errorf("ImportID = %q, want /estate/deleted", o.ImportID)
	}
	if !o.Swept {
		t.Error("the removal is not marked as found by the sweep")
	}

	scan, ok := res.ScanFor("aws_cloudwatch_log_group")
	if !ok || scan.Source != SourceTagging || scan.CFNType != "AWS::Logs::LogGroup" {
		t.Errorf("scan = %+v, want Source=TAGGING_API CFNType=AWS::Logs::LogGroup", scan)
	}
	var covered bool
	for _, tn := range res.SweepCovered {
		if tn == "aws_cloudwatch_log_group" {
			covered = true
		}
	}
	if !covered {
		t.Errorf("aws_cloudwatch_log_group is not in SweepCovered: %v", res.SweepCovered)
	}
}

// TestTaggingSweepReportsUnresolvedARN: a resource carrying this estate's
// marker whose ARN the join table cannot place is named, not silently
// dropped.
func TestTaggingSweepReportsUnresolvedARN(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)

	arn := "arn:aws:glue:us-east-1:123456789012:table/db/tbl"
	srv := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{
			arn: {TagEstate: estateName, TagAddress: `aws_glue_catalog_table.deleted`},
		},
	}
	server := srv.start(t)
	defer server.Close()

	req := Request{
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		Roster:       taggingRoster(t, "aws_cloudwatch_log_group", "AWS::Logs::LogGroup", true),
	}
	res, diags := discoverFixture(t, cloud, req)
	// A warning, not an error: the run is still fine, one live resource is
	// simply outside this pass's removal coverage (see
	// ProblemUnresolvedTaggedARN's doc comment).
	assertNoErrors(t, diags)
	problems := res.ProblemsOfKind(ProblemUnresolvedTaggedARN)
	if len(problems) != 1 {
		t.Fatalf("want one unresolved-tagged-ARN problem, got %d:\n%s", len(problems), res)
	}
	if !strings.Contains(problems[0].Detail, arn) {
		t.Errorf("the problem does not name the ARN: %s", problems[0].Detail)
	}
	if ProblemUnresolvedTaggedARN.Severity() != SeverityWarning {
		t.Error("an unresolved tagged ARN must be a warning, not an error that blocks the run")
	}
}

// TestTaggingSweepReportsNoARNJoinGap: a type the join table cannot ever
// resolve to is a named gap, not silence.
func TestTaggingSweepReportsNoARNJoinGap(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)

	srv := &taggingServer{}
	server := srv.start(t)
	defer server.Close()

	req := Request{
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		// aws_s3_bucket_policy has no entry in arnJoinTable at all (no
		// standalone ARN of its own), so even though it is mapped and
		// taggable it is unreachable through the ARN join.
		Roster: taggingRoster(t, "aws_s3_bucket_policy", "AWS::S3::BucketPolicy", true),
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	var found bool
	for _, g := range res.SweepGaps {
		if g.TypeName == "aws_s3_bucket_policy" {
			found = true
			if g.Reason != SweepGapNoARNJoin {
				t.Errorf("reason = %s, want %s", g.Reason, SweepGapNoARNJoin)
			}
		}
	}
	if !found {
		t.Errorf("aws_s3_bucket_policy is not reported as a gap:\n%s", res)
	}
}

// TestTaggingSweepOffIsByteIdentical is requirement 4: with the flag off, or
// with either of its two required companions (Tagging, Roster) left nil,
// behavior is exactly the pre-#51 per-type sweep - not merely "no crash".
func TestTaggingSweepOffIsByteIdentical(t *testing.T) {
	build := func() *fakeCloud {
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		cloud.listable("aws_cloudwatch_log_group")
		cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)
		return cloud
	}

	base, diags := discoverFixture(t, build(), Request{Sweep: true})
	assertNoErrors(t, diags)
	baseStr := base.String()

	tagging := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: "http://127.0.0.1:1"}) // never dialed if the flag truly does nothing
	roster := taggingRoster(t, "aws_cloudwatch_log_group", "AWS::Logs::LogGroup", true)

	variants := map[string]Request{
		"TaggingSweep true, Tagging nil":  {Sweep: true, TaggingSweep: true, Roster: roster},
		"TaggingSweep false, Tagging set": {Sweep: true, TaggingSweep: false, Tagging: tagging, Roster: roster},
		"TaggingSweep true, Roster nil":   {Sweep: true, TaggingSweep: true, Tagging: tagging},
	}
	for name, req := range variants {
		t.Run(name, func(t *testing.T) {
			res, diags := discoverFixture(t, build(), req)
			assertNoErrors(t, diags)
			if got := res.String(); got != baseStr {
				t.Errorf("behavior changed with the tagging sweep left off:\nbase:\n%s\ngot:\n%s", baseStr, got)
			}
		})
	}
}
