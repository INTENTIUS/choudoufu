// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/funcs"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// estateDir is the P0.1 fixture, which hand-writes its markers on every
// taggable resource. Stamping it has to be a no-op, and that is a test rather
// than an aspiration: see TestStamp_estateFixtureIsUntouched.
func estateDir(t *testing.T) string {
	return flocitest.EstateDir(t)
}

// ---------------------------------------------------------------------------
// The main case: a bare resource gains both markers
// ---------------------------------------------------------------------------

func TestStamp_bareResourceGainsMarkers(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	if len(res.Stamped) != 1 {
		t.Fatalf("stamped %d resources, want 1: %+v", len(res.Stamped), res.Stamped)
	}
	if got := res.Stamped[0].Addr.String(); got != "aws_vpc.main" {
		t.Errorf("stamped %s, want aws_vpc.main", got)
	}
	if got := strings.Join(res.Stamped[0].Keys, ","); got != "tofu-estate,tofu-address" {
		t.Errorf("stamped keys %q, want tofu-estate,tofu-address", got)
	}
	if res.Stamped[0].PerInstance {
		t.Errorf("a resource with neither count nor for_each was stamped per instance")
	}

	// The claim that matters: evaluating the rewritten body produces the two
	// markers. Everything else in this package is bookkeeping around that.
	tags := evalTags(t, cfg, "aws_vpc.main", nil)
	assertTags(t, tags, map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
	})
}

// TestStamp_existingTagsAreKept: a resource that already sets tags of its own
// keeps them and gains the markers alongside.
func TestStamp_existingTagsAreKept(t *testing.T) {
	cfg := loadSource(t, `
locals {
  team = "platform"
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    Name  = "primary"
    owner = local.team
  }
}
`)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	assertTags(t, evalTags(t, cfg, "aws_vpc.main", localsData(map[string]string{"team": "platform"})), map[string]string{
		"Name":         "primary",
		"owner":        "platform",
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
	})
}

// TestStamp_correctMarkersAreNoOp: markers already written, and written
// right, are left exactly as they are. The estate fixture is this case, and
// so is any configuration a previous stamped apply produced.
func TestStamp_correctMarkersAreNoOp(t *testing.T) {
	cfg := loadSource(t, `
locals {
  estate = "stamp-unit"
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate  = local.estate
    tofu-address = "aws_vpc.main"
  }
}
`)

	before := tagsSource(t, cfg, "aws_vpc.main")

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	if len(res.Stamped) != 0 {
		t.Errorf("a correctly marked resource was stamped anyway: %+v", res.Stamped)
	}
	if !hasSkip(res, "aws_vpc.main", SkipAlreadyStamped) {
		t.Errorf("the no-op was not recorded as such: %v", res.Skipped)
	}
	if after := tagsSource(t, cfg, "aws_vpc.main"); after != before {
		t.Errorf("the tags argument was rewritten:\nbefore %d entries\nafter  %d entries", before, after)
	}
	assertTags(t, evalTags(t, cfg, "aws_vpc.main", localsData(map[string]string{"estate": "stamp-unit"})), map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
	})
}

// TestStamp_partialMarkersAreCompleted: one marker written, the other
// missing. Only the missing one is added.
func TestStamp_partialMarkersAreCompleted(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate = "stamp-unit"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	if len(res.Stamped) != 1 || strings.Join(res.Stamped[0].Keys, ",") != "tofu-address" {
		t.Fatalf("stamped %+v, want tofu-address alone", res.Stamped)
	}
	assertTags(t, evalTags(t, cfg, "aws_vpc.main", nil), map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
	})
}

// ---------------------------------------------------------------------------
// Conflicts
// ---------------------------------------------------------------------------

// TestStamp_wrongEstateIsAnError: a marker claiming another estate is never
// overwritten. That would be a transfer of ownership performed as a side
// effect of running a plan.
func TestStamp_wrongEstateIsAnError(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate  = "other-estate"
    tofu-address = "aws_vpc.main"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	if !diags.HasErrors() {
		t.Fatalf("a marker naming another estate was accepted: %+v", res)
	}
	assertDiagContains(t, diags, "Ownership marker conflict", "other-estate", "stamp-unit", "aws_vpc.main")

	// Nothing was rewritten: the operator has to read the resource, and it
	// should read the way they wrote it.
	assertTags(t, evalTags(t, cfg, "aws_vpc.main", nil), map[string]string{
		"tofu-estate":  "other-estate",
		"tofu-address": "aws_vpc.main",
	})
}

// TestStamp_wrongAddressIsAnError: a marker naming another address is a
// rename, and renames belong to live-mv.
func TestStamp_wrongAddressIsAnError(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_vpc.old_name"
  }
}
`)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	if !diags.HasErrors() {
		t.Fatal("a marker naming another address was accepted")
	}
	assertDiagContains(t, diags, "Ownership marker conflict", "aws_vpc.old_name", "live-mv")
}

// TestStamp_conflictStopsEveryRewrite: one conflict anywhere leaves the whole
// configuration as it was, rather than a half-stamped one the operator has to
// reason about.
func TestStamp_conflictStopsEveryRewrite(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

resource "aws_s3_bucket" "data" {
  bucket = "b"

  tags = {
    tofu-estate = "somebody-else"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	if !diags.HasErrors() {
		t.Fatal("the conflicting resource was accepted")
	}
	if len(res.Stamped) != 0 {
		t.Errorf("a stamped list survived a failed pass: %+v", res.Stamped)
	}
	if tags := evalTags(t, cfg, "aws_vpc.main", nil); len(tags) != 0 {
		t.Errorf("the bare resource was rewritten even though the pass failed: %v", tags)
	}
}

// TestStamp_unreadableMarkerIsWarnedNotOverwritten: a marker built from
// something this pass cannot evaluate is left alone, with a warning. Silence
// would be a lie in either direction - claiming it is right, or overwriting
// something that was.
func TestStamp_unreadableMarkerIsWarnedNotOverwritten(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = aws_s3_bucket.data.bucket
  }
}

resource "aws_s3_bucket" "data" {
  bucket = "b"

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_s3_bucket.data"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(diags) == 0 {
		t.Fatal("an unreadable marker produced no diagnostic at all")
	}
	assertDiagContains(t, diags, "Ownership marker could not be checked", "aws_vpc.main")
	if !hasSkip(res, "aws_vpc.main", SkipMarkerUnreadable) {
		t.Errorf("the unreadable marker is not in the skip list: %v", res.Skipped)
	}
	if len(res.Stamped) != 0 {
		t.Errorf("an unreadable marker was stamped over: %+v", res.Stamped)
	}
}

// TestStamp_variableTagsAreMergedInto: a tags argument the pass cannot read
// entry by entry gets the markers merged onto it rather than being skipped.
//
// This asserted the opposite until the audit (finding C2): the skip was a
// warning, the run continued, and a resource whose identity is server-assigned
// was applied with no marker at all - unfindable ever after. What the pass
// cannot read it now merges into, and merge's last argument wins, so the
// markers on the applied resource are the ones this run stamped.
func TestStamp_variableTagsAreMergedInto(t *testing.T) {
	cfg := loadSource(t, `
variable "tags" {
  type    = map(string)
  default = { team = "platform" }
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
  tags       = var.tags
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(diags) != 0 {
		t.Errorf("merging into a variable tags argument warned about it: %s", diags.ErrWithWarnings())
	}
	if hasSkip(res, "aws_vpc.main", SkipTagsUnreadable) {
		t.Errorf("the tags argument was skipped instead of merged into: %v", res.Skipped)
	}
	if len(res.Stamped) != 1 || !res.Stamped[0].Merged {
		t.Errorf("the pass does not report a merged stamping: %+v", res.Stamped)
	}

	assertTags(t, evalTags(t, cfg, "aws_vpc.main", map[string]cty.Value{
		"var": cty.ObjectVal(map[string]cty.Value{
			"tags": cty.MapVal(map[string]cty.Value{"team": cty.StringVal("platform")}),
		}),
	}), map[string]string{
		"team":         "platform",
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
	})
}

// ---------------------------------------------------------------------------
// Taggability comes from the schema
// ---------------------------------------------------------------------------

// TestStamp_untaggableTypesAreSkippedSilently: a type whose schema has no
// tags attribute is not a gap in the estate's records. It is skipped, with no
// diagnostic at all.
func TestStamp_untaggableTypesAreSkippedSilently(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_route" "r" {
  route_table_id = "rtb-1"
}

resource "aws_computed_tags" "c" {
  name = "x"
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(diags) != 0 {
		t.Errorf("an untaggable type produced diagnostics: %s", diags.ErrWithWarnings())
	}
	if len(res.Stamped) != 0 {
		t.Fatalf("an untaggable type was stamped: %+v", res.Stamped)
	}
	for _, addr := range []string{"aws_route.r", "aws_computed_tags.c"} {
		if !hasSkip(res, addr, SkipUntaggable) {
			t.Errorf("%s is not recorded as untaggable: %v", addr, res.Skipped)
		}
	}
}

// TestTaggable is the schema rule on its own: a top-level tags attribute of a
// map type that configuration may set.
func TestTaggable(t *testing.T) {
	for name, tc := range map[string]struct {
		attr *configschema.Attribute
		want bool
	}{
		"optional map of string": {&configschema.Attribute{Type: cty.Map(cty.String), Optional: true}, true},
		"required map of string": {&configschema.Attribute{Type: cty.Map(cty.String), Required: true}, true},
		"map of dynamic":         {&configschema.Attribute{Type: cty.Map(cty.DynamicPseudoType), Optional: true}, true},
		"computed only":          {&configschema.Attribute{Type: cty.Map(cty.String), Computed: true}, false},
		"a string":               {&configschema.Attribute{Type: cty.String, Optional: true}, false},
		"a list":                 {&configschema.Attribute{Type: cty.List(cty.String), Optional: true}, false},
		"an object":              {&configschema.Attribute{Type: cty.Object(map[string]cty.Type{"a": cty.String}), Optional: true}, false},
		"absent":                 {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			block := &configschema.Block{Attributes: map[string]*configschema.Attribute{}}
			if tc.attr != nil {
				block.Attributes["tags"] = tc.attr
			}
			if got := taggable(block); got != tc.want {
				t.Errorf("taggable = %v, want %v", got, tc.want)
			}
		})
	}
}

// The taggability pin: the v0 admission table, split by what the AWS
// provider's schema answers for each type. Whether a type can carry a marker
// is read from the schema at runtime (taggable), never from a list, so the
// admission table itself does not record the answer - and without this pin a
// provider release that adds a tags argument to aws_route, or removes one
// from a type that has it, would change SkipUntaggable behavior with no test
// failing. live/LIMITATIONS.md's "Untaggable types cannot be removed by the
// sweep" entry names this same set, rendered from live/survey-full.json
// (issue #54, tools/survey-gen/untaggable_render.go) rather than the
// curated 68 live/survey.json measures - which is what let
// untaggableAdmittedTypes fold back into one list after the registry-ratified
// batches added untaggable types outside that curated roster. It used to
// carry a second, split-out list (untaggableOutsideCuratedSurvey) for
// exactly those types, worked around because the doc's own derivation
// couldn't see past the curated 68 yet; now that it can, the split is gone.
// TestUntaggableTypesMatchLimitationsDoc keeps this list and the doc the
// same.
var (
	taggableAdmittedTypes = []string{
		"aws_vpc",
		"aws_subnet",
		"aws_security_group",
		"aws_route_table",
		"aws_internet_gateway",
		"aws_eip",
		"aws_s3_bucket",
		"aws_iam_role",
		"aws_cloudwatch_log_group",
		"aws_ssm_parameter",
		"aws_dynamodb_table",
		"aws_ecs_cluster",
		"aws_kms_key",
		"aws_route53_zone",
		"aws_cloudwatch_metric_alarm",
		"aws_lb",
		"aws_lb_target_group",
		"aws_lb_listener",
		"aws_sns_topic",
		"aws_vpc_security_group_ingress_rule",
		"aws_vpc_security_group_egress_rule",
		"aws_launch_template",
		"aws_acm_certificate",
		"aws_sfn_state_machine",
		"aws_ebs_volume",
		// Registry-ratified Lambda batch (#40, #44).
		"aws_lambda_capacity_provider",
		"aws_lambda_code_signing_config",
		"aws_lambda_event_source_mapping",
		"aws_lambda_function",
		// Registry-ratified IAM and ECR batch (#40, #44, issue #26).
		"aws_ecr_repository",
		"aws_iam_instance_profile",
		"aws_iam_service_linked_role",
		"aws_iam_user",
		// Registry-ratified messaging batch (#40, #44).
		"aws_cloudwatch_composite_alarm",
		"aws_cloudwatch_metric_stream",
		"aws_sqs_queue",
		// Registry-ratified EC2 core batch (#40, #44, issue #65). See
		// live/e2e/estates/ec2-core/README.md, "Untaggable types", for
		// the five untaggable types this batch also admits.
		"aws_instance",
		"aws_key_pair",
		"aws_placement_group",
		"aws_ec2_fleet",
		"aws_ec2_capacity_reservation",
		"aws_ec2_host",
		"aws_network_interface",
		"aws_spot_fleet_request",
		// Registry-ratified DynamoDB periphery and ElastiCache batch (#40,
		// #44, issue #65). Taggable per the real provider's documented
		// Argument Reference for each type.
		"aws_elasticache_cluster",
		"aws_elasticache_parameter_group",
		"aws_elasticache_replication_group",
		"aws_elasticache_serverless_cache",
		"aws_elasticache_subnet_group",
		"aws_elasticache_user",
		"aws_elasticache_user_group",
		// Registry-ratified API Gateway v1/v2 batch (#40, #44, issue #65).
		"aws_api_gateway_api_key",
		"aws_api_gateway_client_certificate",
		"aws_api_gateway_domain_name",
		"aws_api_gateway_domain_name_access_association",
		"aws_api_gateway_rest_api",
		"aws_api_gateway_stage",
		"aws_api_gateway_usage_plan",
		"aws_api_gateway_vpc_link",
		"aws_apigatewayv2_api",
		"aws_apigatewayv2_domain_name",
		"aws_apigatewayv2_stage",
		"aws_apigatewayv2_vpc_link",
		// Registry-ratified RDS batch (#40, #44, issue #65's ratification
		// campaign). aws_db_instance_role_association, aws_db_proxy_default_target_group
		// and aws_rds_cluster_role_association are this batch's untaggable
		// types, below.
		"aws_db_event_subscription",
		"aws_db_instance",
		"aws_db_option_group",
		"aws_db_parameter_group",
		"aws_db_proxy",
		"aws_db_proxy_endpoint",
		"aws_db_subnet_group",
		"aws_rds_cluster",
		"aws_rds_cluster_instance",
		"aws_rds_cluster_parameter_group",
		"aws_rds_custom_db_engine_version",
		"aws_rds_global_cluster",
		"aws_rds_integration",
		"aws_rds_shard_group",
		// Registry-ratified ECS/EKS batch (#40, #44, issue #65).
		"aws_ecs_daemon",
		"aws_eks_access_entry",
		"aws_eks_addon",
		"aws_eks_capability",
		"aws_eks_cluster",
		"aws_eks_fargate_profile",
		"aws_eks_node_group",
		// Registry-ratified storage batch (#40, #44, issue #65): EFS, FSx,
		// Backup. See live/e2e/estates/storage/README.md.
		"aws_efs_access_point",
		"aws_efs_file_system",
		"aws_fsx_lustre_file_system",
		"aws_fsx_ontap_file_system",
		"aws_fsx_windows_file_system",
		"aws_fsx_openzfs_file_system",
		"aws_fsx_ontap_storage_virtual_machine",
		"aws_fsx_ontap_volume",
		"aws_fsx_openzfs_volume",
		"aws_fsx_openzfs_snapshot",
		"aws_fsx_data_repository_association",
		"aws_backup_plan",
		"aws_backup_vault",
		"aws_backup_framework",
		"aws_backup_report_plan",
		"aws_backup_restore_testing_plan",
		"aws_backup_logically_air_gapped_vault",
		// Registry-ratified data-plane batch (#40, #44, issue #65): Kinesis,
		// KinesisFirehose, Glue and Athena types with a top-level tags
		// argument in the pinned provider's own wire schema.
		"aws_kinesis_stream",
		"aws_kinesis_stream_consumer",
		"aws_kinesis_firehose_delivery_stream",
		"aws_glue_catalog_database",
		"aws_glue_registry",
		"aws_glue_job",
		"aws_glue_crawler",
		"aws_glue_connection",
		"aws_glue_trigger",
		"aws_glue_ml_transform",
		"aws_athena_workgroup",
		"aws_athena_data_catalog",
		// Registry-ratified Route53 remainder and CloudFront batch (#40,
		// #44, #65). See live/e2e/estates/route53-cloudfront/README.md.
		"aws_route53_health_check",
		"aws_route53profiles_association",
		"aws_route53profiles_profile",
		"aws_route53recoverycontrolconfig_cluster",
		"aws_route53recoverycontrolconfig_control_panel",
		"aws_route53recoverycontrolconfig_safety_rule",
		"aws_route53_resolver_endpoint",
		"aws_route53_resolver_firewall_domain_list",
		"aws_route53_resolver_firewall_rule_group",
		"aws_route53_resolver_firewall_rule_group_association",
		"aws_route53_resolver_query_log_config",
		"aws_route53_resolver_rule",
		"aws_cloudfront_anycast_ip_list",
		"aws_cloudfront_connection_function",
		"aws_cloudfront_connection_group",
		"aws_cloudfront_distribution",
		"aws_cloudfront_distribution_tenant",
		"aws_cloudfront_function",
		"aws_cloudfront_key_value_store",
		"aws_cloudfront_multitenant_distribution",
		"aws_cloudfront_trust_store",
		"aws_cloudfront_vpc_origin",
		// Registry-ratified identity batch (#40, #44, #65): Cognito, IAM
		// leftovers, SSO Admin. See live/e2e/estates/identity/README.md.
		"aws_cognito_identity_pool",
		"aws_cognito_user_pool",
		"aws_iam_openid_connect_provider",
		"aws_iam_policy",
		"aws_iam_server_certificate",
		"aws_ssoadmin_application",
		"aws_ssoadmin_permission_set",
	}
	untaggableAdmittedTypes = []string{
		"aws_route",
		"aws_route_table_association",
		"aws_s3_bucket_policy",
		"aws_iam_role_policy_attachment",
		"aws_s3_bucket_versioning",
		"aws_s3_bucket_public_access_block",
		"aws_s3_bucket_server_side_encryption_configuration",
		"aws_s3_bucket_lifecycle_configuration",
		"aws_iam_role_policy",
		"aws_kms_alias",
		"aws_route53_record",
		"aws_lb_target_group_attachment",
		// Registry-ratified Lambda batch (#40, #44): the batch's one
		// untaggable type. See live/e2e/estates/lambda/README.md,
		// "Untaggable types".
		"aws_lambda_layer_version",
		// Registry-ratified IAM and ECR batch (#40, #44, issue #26): three
		// singleton-per-account ECR types with no tags argument at all. See
		// live/e2e/estates/iam-ecr/README.md, "Untaggable types".
		"aws_ecr_registry_policy",
		"aws_ecr_registry_scanning_configuration",
		"aws_ecr_replication_configuration",
		// Registry-ratified messaging batch (#40, #44). See
		// live/e2e/estates/messaging/README.md, "Untaggable types", for why
		// aws_sns_topic_subscription — untaggable and inside the curated 68
		// — is still not admitted (issue #65 re-examined and confirmed the
		// deferral, for a mechanism reason rather than the doc gate #54
		// already closed).
		"aws_cloudwatch_dashboard",
		"aws_sns_topic_policy",
		"aws_sqs_queue_policy",
		// Registry-ratified EC2 core batch (#40, #44, issue #65): five
		// untaggable types — four whose Argument Reference names no tags
		// block at all, plus aws_ebs_snapshot_block_public_access, a
		// per-region singleton with no arguments at all beyond `state`. See
		// live/e2e/estates/ec2-core/README.md, "Untaggable types".
		"aws_network_interface_attachment",
		"aws_network_interface_permission",
		"aws_eip_association",
		"aws_volume_attachment",
		"aws_ebs_snapshot_block_public_access",
		// Registry-ratified DynamoDB periphery and ElastiCache batch (#40,
		// #44, issue #65): both types' Argument Reference names no tags
		// block at all. See live/e2e/estates/dynamodb-elasticache/README.md,
		// "Untaggable types".
		"aws_dynamodb_global_table",
		"aws_dynamodb_resource_policy",
		// Registry-ratified API Gateway v1/v2 batch (#40, #44, issue #65):
		// nine types with no tags argument at all, confirmed against the
		// provider's documented Argument Reference for each. See
		// live/e2e/estates/apigateway/README.md, "Untaggable types".
		"aws_api_gateway_account",
		"aws_api_gateway_base_path_mapping",
		"aws_api_gateway_documentation_version",
		"aws_api_gateway_gateway_response",
		"aws_api_gateway_method",
		"aws_api_gateway_model",
		"aws_api_gateway_rest_api_policy",
		"aws_api_gateway_usage_plan_key",
		"aws_apigatewayv2_routing_rule",
		// Registry-ratified RDS batch (#40, #44, issue #65's ratification
		// campaign): three types with no tags argument at all. See
		// live/e2e/estates/rds/README.md, "Untaggable types".
		"aws_db_instance_role_association",
		"aws_db_proxy_default_target_group",
		"aws_rds_cluster_role_association",
		// Registry-ratified ECS/EKS batch (#40, #44, issue #65): the
		// deferred aws_iam_group (its own old blocker, the doc gate, closed
		// by #54) plus this batch's two untaggable ECS/EKS rows. See
		// live/e2e/estates/ecs-eks/README.md, "Untaggable types".
		"aws_iam_group",
		"aws_ecs_cluster_capacity_providers",
		"aws_eks_access_policy_association",
		// Registry-ratified storage batch (#40, #44, issue #65): the one
		// untaggable type this batch ratified — a client-named FSx
		// attachment with no tags argument at all. See
		// live/e2e/estates/storage/README.md, "Untaggable types".
		"aws_fsx_s3_access_point_attachment",
		// Registry-ratified data-plane batch (#40, #44, issue #65): three
		// types with no top-level tags argument in the pinned provider's own
		// wire schema — aws_glue_catalog_table and aws_glue_classifier
		// mirror aws_cloudwatch_dashboard's shape (a plain client-named
		// identity, just an untaggable one); aws_glue_data_catalog_encryption_settings
		// is a singleton-per-account type, the same shape as the IAM/ECR
		// batch's three ECR registry singletons above.
		"aws_glue_catalog_table",
		"aws_glue_classifier",
		"aws_glue_data_catalog_encryption_settings",
		// Registry-ratified Route53 remainder and CloudFront batch (#40,
		// #44, #65). aws_cloudfront_origin_access_control is untaggable and
		// inside the curated 68 - the same shape the messaging batch's
		// aws_sns_topic_subscription hit and deferred over, but issue #54
		// landed since then and live/LIMITATIONS.md's untaggable-admitted
		// span now derives from live/survey-full.json across the whole
		// registry-backed roster rather than the curated 68 intersected
		// with the admission table, so it joins this list rather than being
		// deferred. See live/e2e/estates/route53-cloudfront/README.md.
		"aws_route53_hosted_zone_dnssec",
		"aws_route53_key_signing_key",
		"aws_route53_zone_association",
		"aws_route53_resolver_firewall_rule",
		"aws_route53_resolver_rule_association",
		"aws_cloudfront_monitoring_subscription",
		"aws_cloudfront_origin_access_control",
		"aws_cloudfront_realtime_log_config",
		// Registry-ratified identity batch (#40, #44, #65): Cognito, IAM
		// leftovers, SSO Admin. Fifteen untaggable types, confirmed against
		// the real provider's documented Argument Reference for each - see
		// live/e2e/estates/identity/README.md, "Untaggable types".
		"aws_cognito_identity_pool_provider_principal_tag",
		"aws_cognito_identity_pool_roles_attachment",
		"aws_cognito_identity_provider",
		"aws_cognito_resource_server",
		"aws_cognito_user",
		"aws_cognito_user_group",
		"aws_cognito_user_in_group",
		"aws_cognito_user_pool_domain",
		"aws_iam_group_policy",
		"aws_iam_group_policy_attachment",
		"aws_iam_user_policy",
		"aws_iam_user_policy_attachment",
		"aws_ssoadmin_account_assignment",
		"aws_ssoadmin_application_assignment",
		"aws_ssoadmin_instance_access_control_attributes",
	}
)

// TestTaggableSetCoversAdmissionTable is the taggability half of lint's
// TestAdmissionTableCoversEstate: that test pins which types are admitted,
// and this one pins which of the admitted types can carry a marker. The two
// pinned lists must cover identity.AdmittedTypes exactly, in both
// directions, so a type added to the admission table has to be classified
// here before this passes.
//
// The schemas are the caricature in testSchemas rather than the real AWS
// provider's, the same trade every unit test in this file makes.
// TestTaggableSetAgainstRealSchemas in stamp_live_test.go is the same pin
// against the schema the real provider serves.
func TestTaggableSetCoversAdmissionTable(t *testing.T) {
	pinned := make(map[string]bool, len(taggableAdmittedTypes)+len(untaggableAdmittedTypes))
	for _, resourceType := range taggableAdmittedTypes {
		pinned[resourceType] = true
	}
	for _, resourceType := range untaggableAdmittedTypes {
		if pinned[resourceType] {
			t.Errorf("%s is pinned as both taggable and untaggable", resourceType)
		}
		pinned[resourceType] = true
	}
	for _, resourceType := range identity.AdmittedTypes() {
		if !pinned[resourceType] {
			t.Errorf("%s is in the v0 admission table but its taggability is not pinned here", resourceType)
		}
		delete(pinned, resourceType)
	}
	for resourceType := range pinned {
		t.Errorf("%s has its taggability pinned here but is not in the v0 admission table", resourceType)
	}

	schemas := testSchemas()
	check := func(types []string, want bool) {
		for _, resourceType := range types {
			schema, _ := schemas.ResourceTypeConfig(addrs.NewDefaultProvider("aws"), addrs.ManagedResourceMode, resourceType)
			if schema == nil || schema.Block == nil {
				t.Errorf("the test schemas have no entry for admitted type %s", resourceType)
				continue
			}
			if got := taggable(schema.Block); got != want {
				t.Errorf("taggable(%s) = %v, want %v", resourceType, got, want)
			}
		}
	}
	check(taggableAdmittedTypes, true)
	check(untaggableAdmittedTypes, false)
}

// TestUntaggableTypesMatchLimitationsDoc: live/LIMITATIONS.md tells the
// operator which types the sweep cannot remove because they carry no tags,
// and that list is the untaggable pin above in prose form. Drift between the
// two would mean the doc names a type the code stamps, or stays silent about
// one it skips, so the doc's own entry is held to the pinned set.
func TestUntaggableTypesMatchLimitationsDoc(t *testing.T) {
	doc := filepath.Join(flocitest.RepoRoot(t), "live", "LIMITATIONS.md")
	content, err := os.ReadFile(doc) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v", doc, err)
	}

	const heading = "**Untaggable types carry no ownership marker of their own.**"
	_, entry, found := strings.Cut(string(content), heading)
	if !found {
		t.Fatalf("live/LIMITATIONS.md has no %q entry", heading)
	}
	if end := strings.Index(entry, "\n\n"); end >= 0 {
		entry = entry[:end]
	}

	var docTypes []string
	for _, m := range regexp.MustCompile("`([a-z0-9_]+)`").FindAllStringSubmatch(entry, -1) {
		docTypes = append(docTypes, m[1])
	}
	sort.Strings(docTypes)

	want := append([]string(nil), untaggableAdmittedTypes...)
	sort.Strings(want)

	if strings.Join(docTypes, " ") != strings.Join(want, " ") {
		t.Errorf("the doc entry names %v, want exactly the pinned untaggable set %v", docTypes, want)
	}
}

// TestStamp_tagsAsNestedBlockIsNotStamped: the tags-as-repeated-blocks shape
// is not the tag map the marker spec describes, and is left alone.
func TestStamp_tagsAsNestedBlockIsNotStamped(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_tag_blocks" "asg" {
  name = "app"

  tag {
    key   = "Name"
    value = "app"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(res.Stamped) != 0 {
		t.Errorf("a type whose tags are blocks was stamped: %+v", res.Stamped)
	}
}

// ---------------------------------------------------------------------------
// count and for_each
// ---------------------------------------------------------------------------

// TestStamp_countInstancesGetEscapedAddresses: every instance of a count
// resource gets its own escaped address, which is exactly what discovery
// normalizes an observed marker to.
func TestStamp_countInstancesGetEscapedAddresses(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 3
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(res.Stamped) != 1 || !res.Stamped[0].PerInstance {
		t.Fatalf("stamped %+v, want one per-instance entry", res.Stamped)
	}

	for i, want := range map[int]string{0: "aws_eip.pool:0", 1: "aws_eip.pool:1", 2: "aws_eip.pool:2"} {
		tags := evalTags(t, cfg, "aws_eip.pool", countData(i))
		assertTags(t, tags, map[string]string{
			"tofu-estate":  "stamp-unit",
			"tofu-address": want,
		})
	}

	// No slot table in the request, so no slot marker: a run that did not
	// read the live estate has nothing to say about which member is which.
	if tags := evalTags(t, cfg, "aws_eip.pool", countData(0)); tags["tofu-slot"] != "" {
		t.Errorf("a slot marker was stamped with no slot table: %v", tags)
	}
}

// TestStamp_forEachInstancesGetEscapedAddresses: the same for for_each, where
// the escaping rule drops the quotes around the key.
func TestStamp_forEachInstancesGetEscapedAddresses(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_subnet" "this" {
  for_each   = { a = "10.42.1.0/24", b = "10.42.2.0/24" }
  cidr_block = each.value
}
`)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	for _, key := range []string{"a", "b"} {
		assertTags(t, evalTags(t, cfg, "aws_subnet.this", eachData(key)), map[string]string{
			"tofu-estate":  "stamp-unit",
			"tofu-address": "aws_subnet.this:" + key,
		})
	}
}

// TestStamp_handWrittenInstanceAddressIsRecognized: the shape the estate
// fixture writes by hand is recognized as already correct, structurally,
// rather than being warned about as unreadable.
func TestStamp_handWrittenInstanceAddressIsRecognized(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 3

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_eip.pool:${count.index}"
  }
}

resource "aws_subnet" "this" {
  for_each = { a = "10.42.1.0/24" }

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_subnet.this:${each.key}"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(diags) != 0 {
		t.Errorf("the hand-written instance markers produced diagnostics: %s", diags.ErrWithWarnings())
	}
	if len(res.Stamped) != 0 {
		t.Errorf("hand-written instance markers were stamped over: %+v", res.Stamped)
	}
}

// TestStamp_constantAddressOnACountResourceIsAnError: one address shared by
// three instances is a collision waiting to happen, and MARKERS.md calls it
// one. It is named rather than silently replaced.
func TestStamp_constantAddressOnACountResourceIsAnError(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 3

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_eip.pool"
  }
}
`)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	if !diags.HasErrors() {
		t.Fatal("a constant address on a count resource was accepted")
	}
	assertDiagContains(t, diags, "Ownership marker conflict", "count", "aws_eip.pool:count.index")
}

// ---------------------------------------------------------------------------
// Inputs that are not a configuration to stamp
// ---------------------------------------------------------------------------

func TestStamp_refusesBadInput(t *testing.T) {
	cfg := loadSource(t, `resource "aws_vpc" "main" {}`)

	for name, tc := range map[string]struct {
		req  Request
		want string
	}{
		"no estate name": {
			Request{Estate: "", Config: cfg, Schemas: testSchemas()},
			"No estate name to stamp with",
		},
		"an estate name outside the grammar": {
			Request{Estate: "Not_An_Estate", Config: cfg, Schemas: testSchemas()},
			"No estate name to stamp with",
		},
		"no configuration": {
			Request{Estate: "stamp-unit", Schemas: testSchemas()},
			"No configuration to stamp",
		},
		"no schemas": {
			Request{Estate: "stamp-unit", Config: cfg},
			"No provider schemas for marker stamping",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, diags := Stamp(t.Context(), tc.req)
			if !diags.HasErrors() {
				t.Fatalf("no error for %s", name)
			}
			assertDiagContains(t, diags, tc.want)
		})
	}

	// Whatever it refused, it must not have written anything.
	if tags := evalTags(t, cfg, "aws_vpc.main", nil); len(tags) != 0 {
		t.Errorf("a refused pass rewrote the configuration anyway: %v", tags)
	}
}

// ---------------------------------------------------------------------------
// The estate fixture
// ---------------------------------------------------------------------------

// TestStamp_estateFixtureIsUntouched is the no-op case at fixture scale: the
// P0.1 estate hand-writes both markers on every taggable resource, so a
// stamping pass over it adds nothing, warns about nothing, and leaves a plan
// with no marker changes in it.
//
// The schemas here are the caricature below rather than the real AWS
// provider's, so the set of "taggable" types is the set this test declares.
// The integration test runs the same assertion against the real schemas.
func TestStamp_estateFixtureIsUntouched(t *testing.T) {
	cfg := loadDir(t, estateDir(t))

	res, diags := Stamp(t.Context(), Request{Estate: "stateless-e2e", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(diags) != 0 {
		t.Errorf("stamping the estate fixture produced diagnostics: %s", diags.ErrWithWarnings())
	}
	if len(res.Stamped) != 0 {
		t.Errorf("stamping the estate fixture changed it: %+v", res.Stamped)
	}

	// And the no-op is because the markers are right, not because nothing was
	// looked at.
	var alreadyStamped int
	for _, s := range res.Skipped {
		if s.Reason == SkipAlreadyStamped {
			alreadyStamped++
		}
	}
	if alreadyStamped == 0 {
		t.Errorf("no resource in the estate fixture was recognized as already stamped: %v", res.Skipped)
	}
}

// TestStamp_estateFixtureUnderAnotherEstateConflicts: the same fixture, asked
// for by the wrong estate name, is a conflict on every marked resource rather
// than a mass rewrite.
func TestStamp_estateFixtureUnderAnotherEstateConflicts(t *testing.T) {
	cfg := loadDir(t, estateDir(t))

	_, diags := Stamp(t.Context(), Request{Estate: "somebody-else", Config: cfg, Schemas: testSchemas()})
	if !diags.HasErrors() {
		t.Fatal("stamping another estate's name over the fixture was accepted")
	}
	assertDiagContains(t, diags, "Ownership marker conflict", "stateless-e2e")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// testSchemaSource is a Schemas over a plain map of resource type to block.
type testSchemaSource map[string]*configschema.Block

func (s testSchemaSource) ResourceTypeConfig(_ addrs.Provider, mode addrs.ResourceMode, typeName string) (*providers.Schema, uint64) {
	if mode != addrs.ManagedResourceMode {
		return nil, 0
	}
	block, ok := s[typeName]
	if !ok {
		return nil, 0
	}
	return &providers.Schema{Block: block}, 0
}

// testSchemas is a caricature of the AWS provider: the types the fixtures in
// this file and in live/e2e/estate use, tagged or not as the real
// provider has them.
func testSchemas() Schemas {
	tagged := func(names ...string) *configschema.Block {
		attrs := map[string]*configschema.Attribute{
			"tags": {Type: cty.Map(cty.String), Optional: true},
		}
		for _, n := range names {
			attrs[n] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
		}
		return &configschema.Block{Attributes: attrs}
	}
	untagged := func(names ...string) *configschema.Block {
		attrs := map[string]*configschema.Attribute{}
		for _, n := range names {
			attrs[n] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
		}
		return &configschema.Block{Attributes: attrs}
	}

	return testSchemaSource{
		// Taggable, as the real AWS provider has them.
		"aws_vpc":                  tagged("id", "cidr_block"),
		"aws_subnet":               tagged("id", "vpc_id", "cidr_block", "availability_zone"),
		"aws_security_group":       tagged("id", "name", "description", "vpc_id"),
		"aws_route_table":          tagged("id", "vpc_id"),
		"aws_internet_gateway":     tagged("id", "vpc_id"),
		"aws_eip":                  tagged("id", "domain"),
		"aws_s3_bucket":            tagged("id", "bucket"),
		"aws_iam_role":             tagged("id", "name", "assume_role_policy"),
		"aws_cloudwatch_log_group": tagged("id", "name", "retention_in_days"),
		"aws_ssm_parameter":        tagged("id", "name", "type", "value"),
		"aws_dynamodb_table":       tagged("id", "name", "billing_mode", "hash_key"),
		"aws_ecs_cluster":          tagged("id", "name", "arn"),
		"aws_kms_key":              tagged("id", "key_id", "description"),
		"aws_route53_zone":         tagged("id", "zone_id", "name"),
		"aws_cloudwatch_metric_alarm": tagged("id", "alarm_name", "comparison_operator", "evaluation_periods",
			"metric_name", "namespace", "period", "statistic", "threshold"),

		// Untaggable, likewise.
		"aws_route":                                          untagged("route_table_id", "destination_cidr_block", "gateway_id"),
		"aws_route_table_association":                        untagged("subnet_id", "route_table_id"),
		"aws_s3_bucket_policy":                               untagged("bucket", "policy"),
		"aws_iam_role_policy_attachment":                     untagged("role", "policy_arn"),
		"aws_s3_bucket_versioning":                           untagged("id", "bucket"),
		"aws_s3_bucket_public_access_block":                  untagged("id", "bucket", "block_public_acls", "block_public_policy"),
		"aws_s3_bucket_server_side_encryption_configuration": untagged("id", "bucket"),
		"aws_s3_bucket_lifecycle_configuration":              untagged("id", "bucket"),
		"aws_iam_role_policy":                                untagged("id", "role", "name", "policy"),
		"aws_kms_alias":                                      untagged("id", "name", "target_key_id"),
		"aws_route53_record":                                 untagged("id", "zone_id", "name", "type", "ttl"),
		"aws_lb":                                             tagged("id", "arn", "name", "internal"),
		"aws_lb_target_group":                                tagged("id", "arn", "name", "port", "protocol", "vpc_id"),
		"aws_lb_listener":                                    tagged("id", "arn", "load_balancer_arn", "port", "protocol"),
		"aws_sns_topic":                                      tagged("id", "arn", "name"),
		"aws_vpc_security_group_ingress_rule":                tagged("id", "arn", "security_group_rule_id", "security_group_id", "cidr_ipv4", "from_port", "to_port", "ip_protocol"),
		"aws_vpc_security_group_egress_rule":                 tagged("id", "arn", "security_group_rule_id", "security_group_id", "cidr_ipv4", "ip_protocol"),
		"aws_launch_template":                                tagged("id", "arn", "name", "image_id", "instance_type"),
		"aws_acm_certificate":                                tagged("id", "arn", "domain_name", "validation_method"),
		"aws_sfn_state_machine":                              tagged("id", "arn", "name", "role_arn", "definition"),
		"aws_ebs_volume":                                     tagged("id", "arn", "availability_zone", "size"),

		"aws_lb_target_group_attachment": untagged("id", "target_group_arn", "target_id", "port"),

		// Registry-ratified Lambda batch (#40, #44). Taggable per the real
		// provider's documented Argument Reference; aws_lambda_layer_version
		// is the batch's one untaggable type — its Argument Reference names
		// no tags block at all.
		"aws_lambda_capacity_provider":    tagged("id", "arn", "name"),
		"aws_lambda_code_signing_config":  tagged("id", "arn", "config_id"),
		"aws_lambda_event_source_mapping": tagged("id", "uuid", "arn", "function_arn"),
		"aws_lambda_function":             tagged("id", "arn", "function_name"),
		"aws_lambda_layer_version":        untagged("id", "arn", "layer_arn", "layer_name", "version"),

		// Registry-ratified IAM and ECR batch (#40, #44, issue #26). Taggable
		// per the real provider's documented Argument Reference, except the
		// three ECR registry-level singletons, whose Argument Reference names
		// no tags block at all.
		"aws_ecr_repository":                      tagged("id", "arn", "name", "registry_id", "repository_url"),
		"aws_ecr_registry_policy":                 untagged("id", "registry_id", "policy"),
		"aws_ecr_registry_scanning_configuration": untagged("id", "registry_id", "scan_type"),
		"aws_ecr_replication_configuration":       untagged("id", "registry_id"),
		"aws_iam_instance_profile":                tagged("id", "arn", "name", "role"),
		"aws_iam_service_linked_role":             tagged("id", "arn", "name", "aws_service_name"),
		"aws_iam_user":                            tagged("id", "arn", "name"),
		// Registry-ratified messaging batch (#40, #44). Taggable/untaggable
		// per the real provider's documented Argument Reference for each
		// type: aws_cloudwatch_dashboard, aws_sns_topic_policy and
		// aws_sqs_queue_policy carry no tags argument at all.
		"aws_cloudwatch_composite_alarm": tagged("id", "arn", "alarm_name", "alarm_rule"),
		"aws_cloudwatch_dashboard":       untagged("dashboard_arn", "dashboard_name", "dashboard_body"),
		"aws_cloudwatch_metric_stream":   tagged("id", "arn", "name"),
		"aws_sns_topic_policy":           untagged("id", "arn", "policy"),
		"aws_sqs_queue":                  tagged("id", "arn", "url", "name"),
		"aws_sqs_queue_policy":           untagged("id", "queue_url", "policy"),

		// Registry-ratified EC2 core batch (#40, #44, issue #65).
		// Taggable/untaggable per the real provider's documented Argument
		// Reference for each type: aws_network_interface_attachment,
		// aws_network_interface_permission, aws_eip_association and
		// aws_volume_attachment carry no tags argument at all, and
		// aws_ebs_snapshot_block_public_access carries no argument beyond
		// `state`.
		"aws_instance":                         tagged("id", "arn", "ami", "instance_type"),
		"aws_key_pair":                         tagged("id", "arn", "key_name", "public_key"),
		"aws_placement_group":                  tagged("id", "arn", "name", "strategy"),
		"aws_ec2_fleet":                        tagged("id", "arn"),
		"aws_ec2_capacity_reservation":         tagged("id", "arn", "instance_type", "availability_zone"),
		"aws_ec2_host":                         tagged("id", "arn", "availability_zone"),
		"aws_network_interface":                tagged("id", "arn", "subnet_id"),
		"aws_network_interface_attachment":     untagged("instance_id", "network_interface_id", "attachment_id", "device_index"),
		"aws_network_interface_permission":     untagged("network_interface_id", "aws_account_id", "permission", "network_interface_permission_id"),
		"aws_eip_association":                  untagged("id", "allocation_id", "instance_id"),
		"aws_volume_attachment":                untagged("device_name", "instance_id", "volume_id"),
		"aws_spot_fleet_request":               tagged("id", "arn"),
		"aws_ebs_snapshot_block_public_access": untagged("state"),
		// Registry-ratified DynamoDB periphery and ElastiCache batch (#40,
		// #44, issue #65). Taggable/untaggable per the real provider's
		// documented Argument Reference for each type: the two DynamoDB
		// types carry no tags argument at all, the seven ElastiCache types
		// all do.
		"aws_dynamodb_global_table":         untagged("id", "name"),
		"aws_dynamodb_resource_policy":      untagged("id", "resource_arn", "policy"),
		"aws_elasticache_cluster":           tagged("id", "arn", "cluster_id", "engine"),
		"aws_elasticache_parameter_group":   tagged("id", "arn", "name", "family"),
		"aws_elasticache_replication_group": tagged("id", "arn", "replication_group_id"),
		"aws_elasticache_serverless_cache":  tagged("id", "arn", "name", "engine"),
		"aws_elasticache_subnet_group":      tagged("id", "arn", "name"),
		"aws_elasticache_user":              tagged("id", "arn", "user_id", "user_name"),
		"aws_elasticache_user_group":        tagged("id", "arn", "user_group_id"),
		// Registry-ratified API Gateway v1/v2 batch (#40, #44, issue #65).
		// Taggable/untaggable per the real provider's documented Argument
		// Reference for each type: aws_api_gateway_account,
		// _base_path_mapping, _documentation_version, _gateway_response,
		// _method, _model, _rest_api_policy, _usage_plan_key and
		// aws_apigatewayv2_routing_rule carry no tags argument at all.
		"aws_api_gateway_account":                        untagged("id"),
		"aws_api_gateway_api_key":                        tagged("id", "name", "value"),
		"aws_api_gateway_base_path_mapping":              untagged("id", "api_id", "domain_name", "base_path"),
		"aws_api_gateway_client_certificate":             tagged("id", "description"),
		"aws_api_gateway_documentation_version":          untagged("id", "rest_api_id", "version"),
		"aws_api_gateway_domain_name":                    tagged("id", "domain_name"),
		"aws_api_gateway_domain_name_access_association": tagged("id", "arn", "domain_name_arn"),
		"aws_api_gateway_gateway_response":               untagged("id", "rest_api_id", "response_type"),
		"aws_api_gateway_method":                         untagged("id", "rest_api_id", "resource_id", "http_method"),
		"aws_api_gateway_model":                          untagged("id", "rest_api_id", "name"),
		"aws_api_gateway_rest_api":                       tagged("id", "arn", "name"),
		"aws_api_gateway_rest_api_policy":                untagged("id", "rest_api_id", "policy"),
		"aws_api_gateway_stage":                          tagged("id", "arn", "rest_api_id", "stage_name", "deployment_id"),
		"aws_api_gateway_usage_plan":                     tagged("id", "name"),
		"aws_api_gateway_usage_plan_key":                 untagged("id", "usage_plan_id", "key_id", "key_type"),
		"aws_api_gateway_vpc_link":                       tagged("id", "name", "target_arns"),
		"aws_apigatewayv2_api":                           tagged("id", "arn", "name", "protocol_type"),
		"aws_apigatewayv2_domain_name":                   tagged("id", "domain_name"),
		"aws_apigatewayv2_routing_rule":                  untagged("id", "domain_name", "action", "condition"),
		"aws_apigatewayv2_stage":                         tagged("id", "arn", "api_id", "name"),
		"aws_apigatewayv2_vpc_link":                      tagged("id", "name", "security_group_ids", "subnet_ids"),
		// Registry-ratified RDS batch (#40, #44, issue #65's ratification
		// campaign). Taggable/untaggable per the real provider's documented
		// Argument Reference for each type: aws_db_instance_role_association,
		// aws_db_proxy_default_target_group and
		// aws_rds_cluster_role_association carry no tags argument at all.
		"aws_db_event_subscription":         tagged("id", "arn", "name", "sns_topic"),
		"aws_db_instance":                   tagged("id", "identifier", "instance_class"),
		"aws_db_instance_role_association":  untagged("id", "db_instance_identifier", "feature_name", "role_arn"),
		"aws_db_option_group":               tagged("id", "arn", "name", "engine_name", "major_engine_version"),
		"aws_db_parameter_group":            tagged("id", "arn", "name", "family"),
		"aws_db_proxy":                      tagged("id", "arn", "name", "engine_family", "role_arn"),
		"aws_db_proxy_default_target_group": untagged("id", "arn", "name", "db_proxy_name"),
		"aws_db_proxy_endpoint":             tagged("id", "arn", "db_proxy_name", "db_proxy_endpoint_name"),
		"aws_db_subnet_group":               tagged("id", "arn", "name", "subnet_ids"),
		"aws_rds_cluster":                   tagged("id", "arn", "cluster_identifier", "engine"),
		"aws_rds_cluster_instance":          tagged("id", "arn", "identifier", "cluster_identifier", "engine", "instance_class"),
		"aws_rds_cluster_parameter_group":   tagged("id", "arn", "name", "family"),
		"aws_rds_cluster_role_association":  untagged("id", "db_cluster_identifier", "feature_name", "role_arn"),
		"aws_rds_custom_db_engine_version":  tagged("arn", "engine", "engine_version"),
		"aws_rds_global_cluster":            tagged("id", "arn", "global_cluster_identifier"),
		"aws_rds_integration":               tagged("id", "arn", "integration_name", "source_arn", "target_arn"),
		"aws_rds_shard_group":               tagged("arn", "db_shard_group_identifier", "db_cluster_identifier", "max_acu"),
		// Registry-ratified ECS/EKS batch (#40, #44, issue #65). Taggable
		// per the real provider's documented Argument Reference, except
		// aws_ecs_cluster_capacity_providers and
		// aws_eks_access_policy_association, whose Argument Reference names
		// no tags block at all, and the deferred aws_iam_group (#54
		// unblocked it; IAM groups have no TagGroup API to begin with).
		"aws_iam_group":                      untagged("id", "arn", "name"),
		"aws_ecs_cluster_capacity_providers": untagged("id", "cluster_name", "capacity_providers"),
		"aws_ecs_daemon":                     tagged("id", "arn", "name", "cluster_arn"),
		"aws_eks_access_entry":               tagged("cluster_name", "principal_arn"),
		"aws_eks_access_policy_association":  untagged("cluster_name", "principal_arn", "policy_arn"),
		"aws_eks_addon":                      tagged("id", "arn", "cluster_name", "addon_name"),
		"aws_eks_capability":                 tagged("arn", "cluster_name", "capability_name"),
		"aws_eks_cluster":                    tagged("id", "arn", "name", "role_arn"),
		"aws_eks_fargate_profile":            tagged("id", "arn", "cluster_name", "fargate_profile_name"),
		"aws_eks_node_group":                 tagged("id", "arn", "cluster_name", "node_group_name"),
		// Registry-ratified storage batch (#40, #44, issue #65): EFS, FSx,
		// Backup. Taggable/untaggable per the real provider's documented
		// Argument Reference for each type; aws_fsx_s3_access_point_attachment
		// is the batch's one untaggable type — its Argument Reference names
		// no tags block at all.
		"aws_efs_file_system":                   tagged("id", "arn", "creation_token"),
		"aws_efs_access_point":                  tagged("id", "arn", "file_system_id"),
		"aws_fsx_lustre_file_system":            tagged("id", "arn", "subnet_ids"),
		"aws_fsx_ontap_file_system":             tagged("id", "arn", "subnet_ids", "deployment_type", "preferred_subnet_id", "storage_capacity"),
		"aws_fsx_windows_file_system":           tagged("id", "arn", "subnet_ids", "throughput_capacity"),
		"aws_fsx_openzfs_file_system":           tagged("id", "arn", "subnet_ids", "deployment_type", "throughput_capacity"),
		"aws_fsx_ontap_storage_virtual_machine": tagged("id", "arn", "file_system_id", "name"),
		"aws_fsx_ontap_volume":                  tagged("id", "arn", "name", "storage_virtual_machine_id"),
		"aws_fsx_openzfs_volume":                tagged("id", "arn", "name", "parent_volume_id"),
		"aws_fsx_openzfs_snapshot":              tagged("id", "arn", "name", "volume_id"),
		"aws_fsx_data_repository_association":   tagged("id", "arn", "association_id", "file_system_id", "file_system_path", "data_repository_path"),
		"aws_fsx_s3_access_point_attachment":    untagged("name", "type", "s3_access_point_arn", "s3_access_point_alias"),
		"aws_backup_plan":                       tagged("id", "arn", "name", "version"),
		"aws_backup_vault":                      tagged("id", "arn", "name"),
		"aws_backup_framework":                  tagged("id", "arn", "name"),
		"aws_backup_report_plan":                tagged("id", "arn", "name"),
		"aws_backup_restore_testing_plan":       tagged("arn", "name", "schedule_expression"),
		"aws_backup_logically_air_gapped_vault": tagged("id", "arn", "name", "min_retention_days", "max_retention_days"),
		// Registry-ratified data-plane batch (#40, #44, issue #65):
		// Kinesis, KinesisFirehose, Glue and Athena. Taggable/untaggable per
		// the pinned provider's own wire schema (`terraform providers schema
		// -json` against the real hashicorp/aws 6.58.0 binary) for each
		// type: aws_glue_catalog_table, aws_glue_classifier and
		// aws_glue_data_catalog_encryption_settings carry no tags argument
		// at all.
		"aws_kinesis_stream":                        tagged("id", "arn", "name"),
		"aws_kinesis_stream_consumer":               tagged("id", "arn", "name", "stream_arn"),
		"aws_kinesis_firehose_delivery_stream":      tagged("id", "arn", "name"),
		"aws_glue_catalog_database":                 tagged("id", "arn", "name", "catalog_id"),
		"aws_glue_catalog_table":                    untagged("id", "arn", "name", "database_name", "catalog_id"),
		"aws_glue_registry":                         tagged("id", "arn", "registry_name"),
		"aws_glue_job":                              tagged("id", "arn", "name", "role_arn"),
		"aws_glue_crawler":                          tagged("id", "arn", "name", "database_name", "role"),
		"aws_glue_connection":                       tagged("id", "arn", "name", "catalog_id"),
		"aws_glue_classifier":                       untagged("id", "name"),
		"aws_glue_data_catalog_encryption_settings": untagged("id", "catalog_id"),
		"aws_glue_trigger":                          tagged("id", "arn", "name", "type"),
		"aws_glue_ml_transform":                     tagged("id", "arn", "name", "role_arn"),
		"aws_athena_workgroup":                      tagged("id", "arn", "name"),
		"aws_athena_data_catalog":                   tagged("id", "arn", "name", "type"),
		// Registry-ratified Route53 remainder and CloudFront batch (#40,
		// #44, #65). Taggable/untaggable per the real provider's documented
		// Argument Reference for each type; the eight untaggable rows are
		// exactly this batch's own "Untaggable types" list in
		// live/e2e/estates/route53-cloudfront/README.md.
		"aws_route53_health_check":                             tagged("id", "type"),
		"aws_route53_hosted_zone_dnssec":                       untagged("id", "hosted_zone_id", "signing_status"),
		"aws_route53_key_signing_key":                          untagged("id", "hosted_zone_id", "name", "key_management_service_arn"),
		"aws_route53_zone_association":                         untagged("id", "zone_id", "vpc_id"),
		"aws_route53profiles_association":                      tagged("id", "arn", "name", "profile_id", "resource_id"),
		"aws_route53profiles_profile":                          tagged("id", "arn", "name"),
		"aws_route53recoverycontrolconfig_cluster":             tagged("id", "arn", "name"),
		"aws_route53recoverycontrolconfig_control_panel":       tagged("id", "arn", "name", "cluster_arn"),
		"aws_route53recoverycontrolconfig_safety_rule":         tagged("id", "arn", "name", "control_panel_arn"),
		"aws_route53_resolver_endpoint":                        tagged("id", "arn", "direction"),
		"aws_route53_resolver_firewall_domain_list":            tagged("id", "arn", "name"),
		"aws_route53_resolver_firewall_rule":                   untagged("id", "name", "action", "firewall_rule_group_id", "firewall_domain_list_id", "priority"),
		"aws_route53_resolver_firewall_rule_group":             tagged("id", "arn", "name"),
		"aws_route53_resolver_firewall_rule_group_association": tagged("id", "arn", "name", "firewall_rule_group_id", "vpc_id", "priority"),
		"aws_route53_resolver_query_log_config":                tagged("id", "arn", "name", "destination_arn"),
		"aws_route53_resolver_rule":                            tagged("id", "arn", "domain_name", "rule_type"),
		"aws_route53_resolver_rule_association":                untagged("id", "resolver_rule_id", "vpc_id"),
		"aws_cloudfront_anycast_ip_list":                       tagged("id", "arn", "name", "ip_count"),
		"aws_cloudfront_connection_function":                   tagged("id", "arn", "name", "connection_function_code"),
		"aws_cloudfront_connection_group":                      tagged("id", "arn", "name"),
		"aws_cloudfront_distribution":                          tagged("id", "arn", "enabled"),
		"aws_cloudfront_distribution_tenant":                   tagged("id", "arn", "name", "distribution_id"),
		"aws_cloudfront_function":                              tagged("id", "arn", "name", "runtime", "code"),
		"aws_cloudfront_key_value_store":                       tagged("id", "arn", "name"),
		"aws_cloudfront_monitoring_subscription":               untagged("id", "distribution_id"),
		"aws_cloudfront_multitenant_distribution":              tagged("id", "arn", "enabled"),
		"aws_cloudfront_origin_access_control":                 untagged("id", "arn", "name", "origin_access_control_origin_type", "signing_behavior", "signing_protocol"),
		"aws_cloudfront_realtime_log_config":                   untagged("id", "arn", "name", "sampling_rate"),
		"aws_cloudfront_trust_store":                           tagged("id", "arn", "name"),
		"aws_cloudfront_vpc_origin":                            tagged("id", "arn"),
		// Registry-ratified identity batch (#40, #44, #65): Cognito, IAM
		// leftovers, SSO Admin. Taggable/untaggable per the real provider's
		// documented Argument Reference for each type - the fifteen
		// untaggable rows are exactly this batch's own "Untaggable types"
		// list in live/e2e/estates/identity/README.md.
		"aws_cognito_identity_pool":                        tagged("id", "identity_pool_name"),
		"aws_cognito_identity_pool_provider_principal_tag": untagged("id", "identity_pool_id", "identity_provider_name"),
		"aws_cognito_identity_pool_roles_attachment":       untagged("id", "identity_pool_id"),
		"aws_cognito_identity_provider":                    untagged("id", "user_pool_id", "provider_name", "provider_type"),
		"aws_cognito_resource_server":                      untagged("id", "user_pool_id", "identifier", "name"),
		"aws_cognito_user":                                 untagged("id", "user_pool_id", "username"),
		"aws_cognito_user_group":                           untagged("id", "user_pool_id", "name"),
		"aws_cognito_user_in_group":                        untagged("id", "user_pool_id", "group_name", "username"),
		"aws_cognito_user_pool":                            tagged("id", "arn", "name"),
		"aws_cognito_user_pool_domain":                     untagged("id", "domain", "user_pool_id"),
		"aws_iam_group_policy":                             untagged("id", "group", "name", "policy"),
		"aws_iam_group_policy_attachment":                  untagged("id", "group", "policy_arn"),
		"aws_iam_openid_connect_provider":                  tagged("id", "arn", "url"),
		"aws_iam_policy":                                   tagged("id", "arn", "name"),
		"aws_iam_server_certificate":                       tagged("id", "arn", "name"),
		"aws_iam_user_policy":                              untagged("id", "user", "name", "policy"),
		"aws_iam_user_policy_attachment":                   untagged("id", "user", "policy_arn"),
		"aws_ssoadmin_account_assignment":                  untagged("id", "instance_arn", "permission_set_arn", "principal_id", "principal_type", "target_id", "target_type"),
		"aws_ssoadmin_application":                         tagged("id", "arn", "name", "instance_arn", "application_provider_arn"),
		"aws_ssoadmin_application_assignment":              untagged("id", "application_arn", "principal_id", "principal_type"),
		"aws_ssoadmin_instance_access_control_attributes":  untagged("id", "instance_arn"),
		"aws_ssoadmin_permission_set":                      tagged("id", "arn", "name", "instance_arn"),

		// Two shapes that are not the marker tag map: a computed-only tags
		// attribute, and tags carried as repeated blocks.
		"aws_computed_tags": {Attributes: map[string]*configschema.Attribute{
			"name": {Type: cty.String, Optional: true},
			"tags": {Type: cty.Map(cty.String), Computed: true},
		}},
		"aws_tag_blocks": {
			Attributes: map[string]*configschema.Attribute{
				"name": {Type: cty.String, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"tag": {
					Nesting: configschema.NestingList,
					Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
						"key":   {Type: cty.String, Required: true},
						"value": {Type: cty.String, Required: true},
					}},
				},
			},
		},
	}
}

// loadSource writes one configuration file to a scratch directory and loads
// it the way the command does, static evaluator and all.
func loadSource(t *testing.T, src string) *configs.Config {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return loadDir(t, dir)
}

func loadDir(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)
	mod, diags := parser.LoadConfigDir(dir, call)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(t.Context(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			t.Fatalf("fixture %s unexpectedly calls module %q", dir, req.Name)
			return nil, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}

// evalTags decodes a resource body's tags argument the way the plan engine
// would, with the given instance-key variables in scope, and returns the tags
// as plain strings.
//
// This is the assertion that matters throughout this file: not "the AST has
// the right shape" but "evaluating the configuration produces the right
// tags", which is what the plan will do with it.
func evalTags(t *testing.T, cfg *configs.Config, addr string, vars map[string]cty.Value) map[string]string {
	t.Helper()

	rc, ok := cfg.Module.ManagedResources[addr]
	if !ok {
		t.Fatalf("no resource %s in the configuration", addr)
	}
	body, ok := rc.Config.(*hclsyntax.Body)
	if !ok {
		t.Fatalf("%s is not in HCL native syntax", addr)
	}
	attr, ok := body.Attributes["tags"]
	if !ok {
		return nil
	}

	val, diags := attr.Expr.Value(&hcl.EvalContext{Variables: vars, Functions: tagFunctions()})
	if diags.HasErrors() {
		t.Fatalf("evaluating %s's tags: %s", addr, diags.Error())
	}
	if val.IsNull() {
		return nil
	}

	out := make(map[string]string)
	for it := val.ElementIterator(); it.Next(); {
		k, v := it.Element()
		if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			t.Fatalf("%s's tag %s did not evaluate to a known string", addr, k.AsString())
		}
		out[k.AsString()] = v.AsString()
	}
	return out
}

// tagFunctions is the language functions a stamped tags argument can call.
// Only one is ever needed - the lookup that reads a slot out of the table
// this package writes - and it is the language's own implementation rather
// than a stand-in, so a test evaluating a stamped tag is evaluating what the
// plan will.
func tagFunctions() map[string]function.Function {
	return map[string]function.Function{
		"lookup": funcs.LookupFunc,
		// merge is the other one a stamped tags argument can call: the pass
		// injects markers into an expression it cannot read entry by entry by
		// merging its own object onto it. Same reasoning as lookup - the
		// language's own implementation, so evaluating a stamped tag here is
		// evaluating what the plan will.
		"merge": stdlib.MergeFunc,
	}
}

// localsData puts a locals object in scope, for the fixtures whose tags refer
// to one.
func localsData(pairs map[string]string) map[string]cty.Value {
	vals := make(map[string]cty.Value, len(pairs))
	for k, v := range pairs {
		vals[k] = cty.StringVal(v)
	}
	return map[string]cty.Value{"local": cty.ObjectVal(vals)}
}

// countData and eachData are the instance-key variables the evaluator puts in
// scope for a count or for_each instance.
func countData(i int) map[string]cty.Value {
	return map[string]cty.Value{
		"count": cty.ObjectVal(map[string]cty.Value{"index": cty.NumberIntVal(int64(i))}),
	}
}

func eachData(key string) map[string]cty.Value {
	return map[string]cty.Value{
		"each": cty.ObjectVal(map[string]cty.Value{
			"key":   cty.StringVal(key),
			"value": cty.StringVal(key),
		}),
	}
}

// tagsSource counts the entries in a resource's tags object literal, as a
// cheap "was this rewritten" probe.
func tagsSource(t *testing.T, cfg *configs.Config, addr string) int {
	t.Helper()

	rc := cfg.Module.ManagedResources[addr]
	body := rc.Config.(*hclsyntax.Body)
	attr, ok := body.Attributes["tags"]
	if !ok {
		return 0
	}
	obj, ok := attr.Expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return -1
	}
	return len(obj.Items)
}

func hasSkip(res *Result, addr string, reason SkipReason) bool {
	for _, s := range res.Skipped {
		if s.Addr.String() == addr && s.Reason == reason {
			return true
		}
	}
	return false
}

func assertTags(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("tags %v, want %v", got, want)
		return
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if got[k] != want[k] {
			t.Errorf("tag %s = %q, want %q (all tags: %v)", k, got[k], want[k], got)
		}
	}
}

func assertNoErrors(t *testing.T, diags tfdiags.Diagnostics) {
	t.Helper()
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
}

func assertDiagContains(t *testing.T, diags tfdiags.Diagnostics, wants ...string) {
	t.Helper()

	var text strings.Builder
	for _, d := range diags {
		desc := d.Description()
		text.WriteString(desc.Summary)
		text.WriteString("\n")
		text.WriteString(desc.Detail)
		text.WriteString("\n")
	}
	for _, want := range wants {
		if !strings.Contains(text.String(), want) {
			t.Errorf("no diagnostic mentioning %q:\n%s", want, text.String())
		}
	}
}
