// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
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
		tags             map[string]string
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
			// [gauntlet:corpus-ec2-instance-complete/day2_remove]: an
			// instance profile's ARN resource-type segment ("instance-
			// profile", unambiguous, no second CFN type sharing it the way
			// "role" does) was entirely absent from arnJoinTable before
			// this row's own entry, so the estate-wide removal sweep could
			// never propose destroying one whose declaring block was
			// deleted - even though it is taggable and migrate stamps it
			// exactly like any other named IAM resource.
			name:             "iam instance profile: name is the ARN's resource id",
			arn:              "arn:aws:iam::123456789012:instance-profile/ex-complete",
			wantTypeName:     "aws_iam_instance_profile",
			wantIdentityAttr: "id",
			wantImportID:     "ex-complete",
			wantOK:           true,
		},
		{
			// Issue #293: an ordinary role and a service-linked role share
			// the ARN's "role" resource-type segment, and before
			// [iamRoleEntry] existed this ARN resolved to AWS::IAM::Role
			// unconditionally - crossing issue #293's own
			// service-linked-roles corpus estate against floci produced
			// exactly this: a "Malformed ownership marker" error over a
			// live role whose tofu-address correctly named
			// aws_iam_service_linked_role. The "aws-service-role/" prefix
			// is IAM's own, real ARN grammar for the family
			// (confirmed against the ARN floci itself returned for a real
			// aws_iam_service_linked_role), not a guess.
			name:             "iam service-linked role: aws-service-role/ prefix disambiguates from an ordinary role",
			arn:              "arn:aws:iam::123456789012:role/aws-service-role/es.amazonaws.com/AWSServiceRoleForEs",
			wantTypeName:     "aws_iam_service_linked_role",
			wantIdentityAttr: "arn",
			wantImportIsARN:  true,
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
			// aws_alb_listener joined identity.DefaultTable alongside
			// aws_lb_listener (issue #184 batch, the same aws_alb* alias
			// family as the load balancer and target group cases above),
			// so this ARN is exactly as ambiguous by TF-type count as
			// those two - and resolves the same way, by
			// [resolveDocumentedAlias] picking the canonical name.
			name:             "elasticloadbalancing listener: alias family's third arnJoinTable entry",
			arn:              "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/main/50dc6c495c0c9188/f2f7dc8efc522ab2",
			wantTypeName:     "aws_lb_listener",
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
			// Genuinely ambiguous since the security batch (issue #65)
			// admitted aws_kms_external_key alongside aws_kms_key: both map
			// to AWS::KMS::Key in live/mapping.json, and a plain KMS key ARN
			// (arn:...:key/UUID) carries no signal distinguishing a
			// customer-managed key from an external (BYOK) one - that
			// distinction lives only in the key's Origin, which the ARN
			// does not carry. Same shape as the security-group-rule case
			// below, one CFN type mapped from two admitted TF types.
			name:          "kms key: genuinely ambiguous between a customer-managed and an external key, no marker to break the tie",
			arn:           "arn:aws:kms:us-east-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab",
			wantOK:        false,
			wantReasonHas: []string{"aws_kms_external_key", "aws_kms_key", "more than one TF type"},
		},
		{
			// Same ARN shape as the row above, but this key's own marker
			// says which of the two admitted TF types it is - the
			// mapping-side counterpart of the cloudwatch alarm and
			// security-group-rule cases: one CFN type, two admitted TF
			// types, settled by the object's own marker instead of the
			// ARN.
			name: "kms key: CFN type is unambiguous, TF type is not, but the object's own marker names the external key",
			arn:  "arn:aws:kms:us-east-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab",
			tags: map[string]string{
				TagEstate:  "demo",
				TagAddress: "aws_kms_external_key.byok",
			},
			wantTypeName:     "aws_kms_external_key",
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
			name:          "ec2 security group rule: genuinely ambiguous between ingress and egress, no marker to break the tie",
			arn:           "arn:aws:ec2:us-east-1:123456789012:security-group-rule/sgr-0123456789abcdef0",
			wantOK:        false,
			wantReasonHas: []string{"AWS::EC2::SecurityGroupEgress", "AWS::EC2::SecurityGroupIngress", "more than one CFN type"},
		},
		{
			// Same ARN shape as the row above, but this object carries its
			// own tofu-address marker (GetResources returns tags inline) -
			// ground truth about which of the two rule types it is. The
			// marker breaks the ARN-shape tie instead of the whole object
			// being refused as unresolved.
			name: "ec2 security group rule: ARN alone is ambiguous, but the object's own marker names the egress side",
			arn:  "arn:aws:ec2:us-east-1:123456789012:security-group-rule/sgr-0123456789abcdef0",
			tags: map[string]string{
				TagEstate:  "demo",
				TagAddress: "aws_vpc_security_group_egress_rule.out",
			},
			wantTypeName:     "aws_vpc_security_group_egress_rule",
			wantIdentityAttr: "id",
			wantImportID:     "sgr-0123456789abcdef0",
			wantOK:           true,
		},
		{
			name:          "cloudwatch alarm: genuinely ambiguous between a metric alarm and a composite alarm, no marker to break the tie",
			arn:           "arn:aws:cloudwatch:us-east-1:123456789012:alarm:tofu-app-cloudwatch-alarm",
			wantOK:        false,
			wantReasonHas: []string{"AWS::CloudWatch::Alarm", "AWS::CloudWatch::CompositeAlarm", "more than one CFN type"},
		},
		{
			// Issue: TestCohortAcceptance's messaging cohort found this for
			// real - a composite alarm's own live object, correctly tagged
			// with its own address, refused as a "malformed marker" because
			// the ARN-shape join alone could not tell it apart from a
			// metric alarm and guessed wrong. The object's own marker
			// settles it.
			name: "cloudwatch alarm: ARN alone is ambiguous, but the object's own marker names the composite alarm",
			arn:  "arn:aws:cloudwatch:us-east-1:123456789012:alarm:tofu-messaging-cohort-cloudwatch-composite-alarm",
			tags: map[string]string{
				TagEstate:  "messaging-cohort",
				TagAddress: "aws_cloudwatch_composite_alarm.app",
			},
			wantTypeName:     "aws_cloudwatch_composite_alarm",
			wantIdentityAttr: "id",
			wantImportID:     "tofu-messaging-cohort-cloudwatch-composite-alarm",
			wantOK:           true,
		},
		{
			// The other side of the same tie: a metric alarm's own marker
			// resolves to the metric alarm CFN type just as cleanly.
			name: "cloudwatch alarm: ARN alone is ambiguous, but the object's own marker names the metric alarm",
			arn:  "arn:aws:cloudwatch:us-east-1:123456789012:alarm:tofu-messaging-cohort-cloudwatch-metric-alarm",
			tags: map[string]string{
				TagEstate:  "messaging-cohort",
				TagAddress: "aws_cloudwatch_metric_alarm.messaging",
			},
			wantTypeName:     "aws_cloudwatch_metric_alarm",
			wantIdentityAttr: "id",
			wantImportID:     "tofu-messaging-cohort-cloudwatch-metric-alarm",
			wantOK:           true,
		},
		{
			name:          "ec2 carrier gateway: mapped in live/mapping.json but not in identity.DefaultTable",
			arn:           "arn:aws:ec2:us-east-1:123456789012:carrier-gateway/cagw-0123456789abcdef0",
			wantOK:        false,
			wantReasonHas: []string{"aws_ec2_carrier_gateway", "internal/live/identity's table"},
		},
		{
			// aws_lambda_function joined the identity table when the first
			// registry-ratified batch admitted it; the mapped-but-unadmitted
			// case lived on with aws_instance until the EC2 core batch
			// (issue #65) admitted it, then with aws_nat_gateway until the
			// EC2 networking batch (issue #65) admitted it too, and now
			// lives on with aws_ec2_carrier_gateway above.
			name:             "lambda function: mapped and admitted, function name binds",
			arn:              "arn:aws:lambda:us-east-1:123456789012:function:my-function",
			wantTypeName:     "aws_lambda_function",
			wantIdentityAttr: "id",
			wantImportID:     "my-function",
			wantOK:           true,
		},
		{
			// This ARN shape is the one .corpus/mastino/prod-eu-west's
			// sitemaps-generator estate carries for real (issue #298's
			// e2e crossing found it), and it used to warn "no CFN type is
			// known for ARN service \"ecs\" and resource segment
			// \"task-definition\"" from the tagging sweep because
			// arnJoinTable had a row for ecs's "cluster" segment but not
			// its "task-definition" one - a coverage gap in this table,
			// not an inherent hand-maintenance need (#298 fixed the
			// separate, already-admitted-instance identity-composition
			// path; this is the sweep's join over an ARN it merely
			// stumbles across). aws_ecs_task_definition's identity IS its
			// ARN here, the same as aws_lambda_function is not: see
			// [importIDFromARN]'s ImportSyntax-based
			// "TASKDEFINITIONARN" rule, not this table.
			name:             "ecs task definition: was the arnJoinTable coverage gap issue #298's own agent found",
			arn:              "arn:aws:ecs:eu-west-1:000000000000:task-definition/sitemaps-generator:1",
			wantTypeName:     "aws_ecs_task_definition",
			wantIdentityAttr: "arn",
			wantImportIsARN:  true,
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
			got := joinTaggedResource(roster, tt.arn, tt.tags)
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

	got := joinTaggedResource(roster, "arn:aws:iam::123456789012:role/deploy", nil)
	if got.ok {
		t.Fatalf("an ambiguous CFN-to-TF mapping resolved anyway: %+v", got)
	}
	for _, frag := range []string{"aws_thing_a", "aws_thing_b", "more than one TF type"} {
		if !strings.Contains(got.reason, frag) {
			t.Errorf("reason %q does not mention %q", got.reason, frag)
		}
	}
}

// TestResolveDocumentedAlias exercises [resolveDocumentedAlias] directly
// against the real identity table (issue #184's follow-up: two TF types
// admitted for one CFN type is not automatically safe to disambiguate - see
// the load balancer/target group/listener cases above, which are, and the
// kms case in TestJoinTaggedResourceRealArtifacts, which is not).
func TestResolveDocumentedAlias(t *testing.T) {
	tests := []struct {
		name          string
		admitted      []string
		wantCanonical string
		wantOK        bool
	}{
		{
			name:          "aws_alb/aws_lb: the documented alias pair itself",
			admitted:      []string{"aws_alb", "aws_lb"},
			wantCanonical: "aws_lb",
			wantOK:        true,
		},
		{
			name:          "order does not matter",
			admitted:      []string{"aws_lb", "aws_alb"},
			wantCanonical: "aws_lb",
			wantOK:        true,
		},
		{
			name:          "aws_alb_listener/aws_lb_listener: a second alias pair",
			admitted:      []string{"aws_alb_listener", "aws_lb_listener"},
			wantCanonical: "aws_lb_listener",
			wantOK:        true,
		},
		{
			name:          "aws_alb_target_group/aws_lb_target_group: a third alias pair",
			admitted:      []string{"aws_alb_target_group", "aws_lb_target_group"},
			wantCanonical: "aws_lb_target_group",
			wantOK:        true,
		},
		{
			name:     "aws_kms_external_key/aws_kms_key: structurally identical, never declared aliases",
			admitted: []string{"aws_kms_external_key", "aws_kms_key"},
			wantOK:   false,
		},
		{
			name:     "aws_db_instance/aws_rds_cluster_instance: another genuine multi-candidate CFN type",
			admitted: []string{"aws_db_instance", "aws_rds_cluster_instance"},
			wantOK:   false,
		},
		{
			name: "a candidate's alias note names something outside this candidate set: not trusted",
			// aws_alb's Reason names aws_lb, which is not offered here - so
			// the one remaining "no marker" candidate (aws_kms_key) is not
			// trusted as what aws_alb is an alias of.
			admitted: []string{"aws_alb", "aws_kms_key"},
			wantOK:   false,
		},
		{
			name: "three candidates, only two of which agree on a canonical: refused",
			admitted: []string{
				"aws_alb_listener", "aws_lb_listener", "aws_kms_key",
			},
			wantOK: false,
		},
		{
			name:     "a type absent from the identity table at all: refused, not a panic",
			admitted: []string{"aws_alb", "aws_this_type_does_not_exist"},
			wantOK:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCanonical, gotOK := resolveDocumentedAlias(tt.admitted)
			if gotOK != tt.wantOK {
				t.Fatalf("resolveDocumentedAlias(%v) ok = %v, want %v", tt.admitted, gotOK, tt.wantOK)
			}
			if gotOK && gotCanonical != tt.wantCanonical {
				t.Errorf("resolveDocumentedAlias(%v) = %q, want %q", tt.admitted, gotCanonical, tt.wantCanonical)
			}
		})
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

// TestTaggingSweepContinuationGapIsMalformed is the tag-sweep path's
// sibling of markers_test.go's TestContinuationGapIsMalformed and
// cloudcontrol_test.go's TestCloudControlContinuationGapIsMalformed:
// fileTaggingCandidate (this file) has its own copy of the same corrupt
// check scanType and scanTypeCloudControl each carry, so a gapped
// continuation chain arriving through the estate-wide tag sweep has to be
// pinned independently too.
func TestTaggingSweepContinuationGapIsMalformed(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)

	arn := "arn:aws:logs:us-east-1:123456789012:log-group:/estate/gapped"
	srv := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{
			arn: {
				TagEstate:          estateName,
				TagAddress:         strings.Repeat("a", 256),
				ContinuationTag(3): strings.Repeat("b", 10), // tofu-address-2 is missing.
			},
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
	if !diags.HasErrors() {
		t.Fatalf("a gapped continuation chain arriving via the tag sweep produced no error:\n%s", res)
	}
	problems := res.ProblemsOfKind(ProblemMalformedMarker)
	if len(problems) != 1 {
		t.Fatalf("want exactly one malformed-marker problem for the gapped chain, got %d:\n%s", len(problems), res)
	}
	if !strings.Contains(problems[0].Detail, "continuation") {
		t.Errorf("the malformed-marker detail does not mention the continuation gap: %q", problems[0].Detail)
	}
	rm := removalsByAddr(res)
	if _, ok := rm[`aws_cloudwatch_log_group.gapped`]; ok {
		t.Error("a gapped continuation chain was treated as a removal candidate rather than malformed")
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

// TestTaggingSweepNoARNJoinTypeFallsBackToNative: a type the join table
// cannot ever resolve to is not tag-swept at all any more -
// [partitionSweepTypes] (gauntlet:record-located-destroy, found via
// corpus-rds-complete-postgres's day2_remove unit) routes it to the native
// per-type sweep instead, before [sweepViaTagging] ever sees it, the same
// way a companion-pair type needing its own resource object already did.
// This fixture's fakeCloud registers no native list route for
// aws_s3_bucket_policy either (it is never `.own`ed or `.withAttr`ed), so
// the native leg's own refusal chain is what actually reports the gap here
// - TYPE_NOT_LISTABLE, not NO_ARN_JOIN. That is still a named gap, not
// silence, which is this test's own point; it is a DIFFERENT reason than
// before this fix because it comes from a different leg now.
// [TestTaggingSweepNoARNJoinTypeFoundNatively] is this fix's positive case:
// the same no-ARN-join type WITH a native list route is found, where
// before this fix it never would have been.
func TestTaggingSweepNoARNJoinTypeFallsBackToNative(t *testing.T) {
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
			if g.Reason != SweepGapNotListable {
				t.Errorf("reason = %s, want %s (routed to the native leg by partitionSweepTypes, which has no ARN to fail joining)", g.Reason, SweepGapNotListable)
			}
		}
	}
	if !found {
		t.Errorf("aws_s3_bucket_policy is not reported as a gap:\n%s", res)
	}
}

// TestTaggingSweepNoARNJoinTypeFoundNatively is the fix's own positive case,
// reproducing corpus-rds-complete-postgres's day2_remove regression at unit
// scale: a type with no [arnJoinTable] row (aws_db_instance's own shape) but
// a real native list route must still be found by the estate-wide sweep -
// before this fix it fell into [SweepGapNoARNJoin] and [sweepViaTagging]
// never tried the native route at all, so an orphan of such a type (a
// resource block deleted from the configuration) was invisible to every
// leg and no destroy was ever proposed for it.
func TestTaggingSweepNoARNJoinTypeFoundNatively(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	// A live, undeclared aws_s3_bucket_policy: no configuration block names
	// it, exactly the shape a removed resource block leaves behind. listable
	// first - own alone does not put the type in the fake provider's own
	// schema set, and a type outside it is [SweepGapNotListable] regardless
	// of what live objects exist.
	cloud.listable("aws_s3_bucket_policy")
	cloud.own("aws_s3_bucket_policy", "my-bucket", `aws_s3_bucket_policy.orphaned`)

	srv := &taggingServer{}
	server := srv.start(t)
	defer server.Close()

	req := Request{
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		Roster:       taggingRoster(t, "aws_s3_bucket_policy", "AWS::S3::BucketPolicy", true),
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	for _, g := range res.SweepGaps {
		if g.TypeName == "aws_s3_bucket_policy" {
			t.Errorf("aws_s3_bucket_policy reported a sweep gap (%s) even though the fake cloud lists it natively:\n%s", g.Reason, res)
		}
	}

	scan, ok := res.ScanFor("aws_s3_bucket_policy")
	if !ok || scan.Source != SourceProvider {
		t.Errorf("aws_s3_bucket_policy's scan = %+v, want it swept the native way (Source=provider), not left in the tagging universe it has no ARN join for", scan)
	}

	var found bool
	for _, o := range res.Orphans {
		if o.TypeName == "aws_s3_bucket_policy" && o.ImportID == "my-bucket" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the undeclared aws_s3_bucket_policy did not surface as an orphan at all - the exact silent-loss shape this fix closes:\n%s", res)
	}
}

// TestTaggingSweepRDSClusterInstanceCompanionRoutesNative is
// [TestTaggingSweepNoARNJoinTypeFoundNatively]'s own mutation control: what
// happens when partitionSweepTypes's native fallback finds a live object
// under one RDS type's list call whose marker names a genuine companion
// (rdsClusterInstanceSibling, corpus-rds-complete-postgres's day2_remove
// unit) rather than a wholly unrelated one. live/mapping.json joins both
// aws_db_instance and aws_rds_cluster_instance to the one CFN type
// AWS::RDS::DBInstance, so once this fix routes aws_rds_cluster_instance's
// own sweep to the native leg (it has no arnJoinTable row either), its list
// call returns the very same live object aws_db_instance's own declared
// instance already claims - a real database, not a stray. Before
// rdsClusterInstanceSibling existed this reported ProblemMalformedMarker
// ("names a aws_db_instance rather than a aws_rds_cluster_instance") and
// failed the whole plan; this test proves it now recomposes the identity
// under aws_db_instance's own row instead, the same way the route-table and
// role/service-linked-role pairs already do.
func TestTaggingSweepRDSClusterInstanceCompanionRoutesNative(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	// The live object is returned by aws_rds_cluster_instance's OWN list
	// call (exactly how AWS's shared DescribeDBInstances reproduces the
	// bug), carrying a marker for its actual type, aws_db_instance. identifier
	// is on the object because aws_db_instance's ratified row reads it
	// first (IdentityAttrs[0]) - recomposing needs it on whichever side's
	// schema the object happened to come back under.
	cloud.listable("aws_rds_cluster_instance")
	cloud.withAttr("aws_rds_cluster_instance", "identifier")
	cloud.ownWithAttrs("aws_rds_cluster_instance", "db-rtb-should-not-matter", `aws_db_instance.this`,
		map[string]string{"identifier": "complete-postgresql-1"})

	srv := &taggingServer{}
	server := srv.start(t)
	defer server.Close()

	cfg := loadConfig(t, "testdata/default-adopter-sweep-orphan")
	res, diags := Discover(context.Background(), Request{
		Estate:       estateName,
		Config:       cfg,
		Resolutions:  resolveOrFail(t, cfg).All(),
		Provider:     cloud,
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		Roster: ccRoster(t,
			map[string]string{
				"aws_db_instance":          "AWS::RDS::DBInstance",
				"aws_rds_cluster_instance": "AWS::RDS::DBInstance",
			},
			nil,
			map[string]bool{"AWS::RDS::DBInstance": true},
		),
	})
	if diags.HasErrors() {
		t.Fatalf("the aws_db_instance/aws_rds_cluster_instance companion pair was reported as an error:\n%s\n%s", res, renderDiags(diags))
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 0 {
		t.Fatalf("the companion pair's own sighting was reported a malformed marker instead of recomposed:\n%s", res)
	}

	scan, ok := res.ScanFor("aws_rds_cluster_instance")
	if !ok || scan.Source != SourceProvider {
		t.Errorf("aws_rds_cluster_instance's scan = %+v, want it swept the native way (Source=provider), not left in the tagging universe it has no ARN join for", scan)
	}

	var found bool
	for _, o := range res.Orphans {
		if o.TypeName != "aws_db_instance" {
			continue
		}
		found = true
		if o.ImportID != "complete-postgresql-1" {
			t.Errorf("orphan's ImportID is %q, want complete-postgresql-1 (aws_db_instance's own identifier, recomposed from the object aws_rds_cluster_instance's list call happened to surface)", o.ImportID)
		}
	}
	if !found {
		t.Fatalf("the aws_db_instance object did not surface as an orphan at all - it must not vanish, and must not be misreported as an aws_rds_cluster_instance:\n%s", res)
	}
	for _, o := range res.Orphans {
		if o.TypeName == "aws_rds_cluster_instance" {
			t.Errorf("the object was also filed as an aws_rds_cluster_instance orphan - one live object, one type, the one its own marker names:\n%s", res)
		}
	}
}

// TestTaggingSweepReportsNotTaggableGapWithNoCandidates is the ordinary case
// noRegistryRowOrUntaggable exists for, unaffected by
// TestTaggingSweepFindsCandidatesDespiteUntaggableRegistryRow below: a type
// live/registry.json records untaggable, and GetResources genuinely returned
// nothing to join to it either, still reports the same SweepGapNotTaggable
// gap it always has.
func TestTaggingSweepReportsNotTaggableGapWithNoCandidates(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)

	srv := &taggingServer{}
	server := srv.start(t)
	defer server.Close()

	req := Request{
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		Roster:       taggingRoster(t, "aws_cloudwatch_log_group", "AWS::Logs::LogGroup", false),
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	var found bool
	for _, g := range res.SweepGaps {
		if g.TypeName == "aws_cloudwatch_log_group" {
			found = true
			if g.Reason != SweepGapNotTaggable {
				t.Errorf("reason = %s, want %s", g.Reason, SweepGapNotTaggable)
			}
		}
	}
	if !found {
		t.Errorf("aws_cloudwatch_log_group is not reported as a not-taggable gap:\n%s", res)
	}
}

// TestTaggingSweepFindsCandidatesDespiteUntaggableRegistryRow is the #388
// default-flip's own regression (corpus-iam-policy's day2_remove stage):
// live/registry.json records AWS::IAM::Policy taggable=false (CloudFormation's
// own claim about ITS update-tags API, list=false and read=false besides -
// IAM has never been well represented in the Cloud Control registry), but
// GetResources (the Resource Groups Tagging API, an entirely different AWS
// service) reliably returns its tags anyway, as this test's own fixture
// does. Before this fix, the untaggable-per-registry gap discarded an
// already ARN-joined, already tag-bearing candidate outright - byType had
// it, but the switch above never let fileTaggingCandidate see it - which
// went unnoticed for as long as every type this shape could apply to also
// had a genuine config-driven scan demand (sweepTypes only ever included a
// type the config-driven scan does NOT already cover), until GitHub issue
// #388's record-backed exclusion could empty that demand for a type with no
// OTHER declared instance in the estate, making the tag sweep the type's
// only path to its own orphan detection.
func TestTaggingSweepFindsCandidatesDespiteUntaggableRegistryRow(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)

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
		// The only difference from TestTaggingSweepFindsDeletedBlock: this
		// type is recorded untaggable.
		Roster: taggingRoster(t, "aws_cloudwatch_log_group", "AWS::Logs::LogGroup", false),
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	rm := removalsByAddr(res)
	o, ok := rm[`aws_cloudwatch_log_group.deleted`]
	if !ok {
		t.Fatalf("the deleted block's resource is not a removal - the untaggable-registry gap discarded a real, already-joined candidate:\n%s", res)
	}
	if o.ImportID != "/estate/deleted" {
		t.Errorf("ImportID = %q, want /estate/deleted", o.ImportID)
	}
	if !o.Swept {
		t.Error("the removal is not marked as found by the sweep")
	}

	for _, g := range res.SweepGaps {
		if g.TypeName == "aws_cloudwatch_log_group" {
			t.Errorf("a type with a real candidate in hand was still reported as a sweep gap: %+v", g)
		}
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

// TestTaggingSweepReportsUnsweepableOwnedType is GitHub issue #107's
// population, reported rather than described as something else.
//
// A type absent from the generated admission table can still be admitted for
// planning, when the provider's identity schema or the configuration's own
// arguments settle it. The estate-wide sweep draws its universe from that
// table, so deleting the last block of such a type leaves the live resource
// in the account with no run that will ever propose removing it.
//
// The tagging sweep is the one path that sees it at all - it asks the cloud
// for everything carrying the marker rather than asking per type - and it
// was calling this an ARN it could not place. It could place it perfectly
// well; what it could not do was remove it. aws_ec2_carrier_gateway is the
// mapped-but-unadmitted case arnJoinTable's own comment keeps for exactly
// this purpose.
func TestTaggingSweepReportsUnsweepableOwnedType(t *testing.T) {
	const unswept = "aws_ec2_carrier_gateway"
	if _, inTable := identity.LookupType(unswept); inTable {
		t.Skipf("%s has joined the admission table; arnJoinTable's comment names the replacement case", unswept)
	}

	cloud := newFakeCloud()
	ownWholeEstate(cloud)

	arn := "arn:aws:ec2:us-east-1:123456789012:carrier-gateway/cagw-0123456789abcdef0"
	srv := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{
			arn: {TagEstate: estateName, TagAddress: unswept + ".deleted"},
		},
	}
	server := srv.start(t)
	defer server.Close()

	req := Request{
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		// The roster has to carry the carrier-gateway mapping for the join
		// to reach the admission-table check at all; without it the ARN
		// fails one step earlier, on live/mapping.json rather than on the
		// identity table. live/mapping.json does carry this row - see
		// arnJoinTable's own comment - so this mirrors the real artifacts.
		Roster: ccRoster(t,
			map[string]string{
				"aws_cloudwatch_log_group": "AWS::Logs::LogGroup",
				unswept:                    "AWS::EC2::CarrierGateway",
			},
			nil,
			map[string]bool{"AWS::Logs::LogGroup": true, "AWS::EC2::CarrierGateway": true},
		),
	}
	res, diags := discoverFixture(t, cloud, req)
	// A warning: nothing about this run is wrong, one live resource is
	// simply outside removal coverage.
	assertNoErrors(t, diags)

	problems := res.ProblemsOfKind(ProblemUnsweepableOwnedType)
	if len(problems) != 1 {
		t.Fatalf("want one unsweepable-owned-type problem, got %d:\n%s", len(problems), res)
	}
	if !strings.Contains(problems[0].Detail, unswept) {
		t.Errorf("the problem does not name the type: %q", problems[0].Detail)
	}
	if !strings.Contains(problems[0].Detail, "not planned for destruction") {
		t.Errorf("the problem does not say what will not happen: %q", problems[0].Detail)
	}

	// It must not still be filed as an ARN nobody could place. That was the
	// old message, and it described the wrong thing: the ARN placed fine.
	if unplaceable := res.ProblemsOfKind(ProblemUnresolvedTaggedARN); len(unplaceable) != 0 {
		t.Errorf("still reported as an unplaceable ARN: %+v", unplaceable)
	}

	// And it must not be proposed for destruction, which it has no table row
	// to carry out.
	for addr := range removalsByAddr(res) {
		if strings.HasPrefix(addr, unswept) {
			t.Errorf("%s was proposed for destruction with no import identity to do it with", addr)
		}
	}
}
