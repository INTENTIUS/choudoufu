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
		// wall/servermint admitted four of tools/row-gen/rejected.json's
		// server-minted composites - a config-supplied parent scope beside
		// a segment the service mints - on the one fact that separates
		// them from the 38 that stayed refused: all four are taggable, so
		// the ownership marker is the discriminator and the parent scope
		// only narrows the candidate set. Taggability from
		// live/survey-full.json's signals.taggable at v6.59.0, which
		// TestPinnedTaggabilityMatchesTheSurvey re-reads rather than
		// trusting this comment; each doc page's Attribute Reference
		// exports tags_all to match.
		"aws_ecs_task_set",
		"aws_eks_pod_identity_association",
		"aws_prometheus_anomaly_detector",
		"aws_service_discovery_private_dns_namespace",
		// The fifth, once tools/importdocs-gen stopped dropping a
		// backtick-quoted segment name that carries a space ("using the
		// allocation `id` and `pool id`, separated by `_`").
		"aws_vpc_ipam_pool_cidr_allocation",
		// wall/rejected4 admitted the eighteen mapped WAF Classic and WAF
		// Classic Regional types the remainder batch had held out under
		// live/residue.go's DeprecatedServices roster. Taggability from two
		// sources that agree: live/survey-full.json's signals.taggable, and
		// each doc page's own Attribute Reference, where exactly these five
		// export tags_all.
		"aws_waf_rule",
		"aws_waf_web_acl",
		"aws_wafregional_rate_based_rule",
		"aws_wafregional_rule",
		"aws_wafregional_web_acl",
		// wall/rejected3 admitted 28 more types after verifying 65 against the
		// v6.59.0 doc cache - a check that caught 22 further bad classifier
		// proposals. Taggability read from live/survey-full.json signals.taggable
		// and independently re-verified at merge: 28 checked, 0 mismatches.
		"aws_appstream_fleet",
		"aws_appstream_image_builder",
		"aws_gamelift_game_server_group",
		"aws_glue_catalog",
		"aws_glue_dev_endpoint",
		"aws_networkflowmonitor_monitor",
		"aws_rds_cluster_endpoint",
		"aws_shield_protection_group",
		// Pinned when wall/rejected2 admitted 21 types whose rejections were
		// re-derived against the v6.59.0 doc cache. Taggability for each was
		// read from live/survey-full.json's own signals.taggable - the same
		// external source this file's other ratification comments cite -
		// rather than inferred from the type name.
		"aws_comprehend_entity_recognizer",
		"aws_inspector_resource_group",
		"aws_iot_ca_certificate",
		"aws_kinesisanalyticsv2_application",
		"aws_mailmanager_ingress_point",
		"aws_osis_pipeline",
		"aws_resiliencehubv2_service",
		"aws_resiliencehubv2_system",
		"aws_verifiedaccess_group",
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
		// aws_alb, aws_alb_target_group, aws_alb_listener: the provider's
		// own documented aliases of the three rows just above ("`aws_alb`
		// is known as `aws_lb`. The functionality is identical.", present
		// verbatim on the aws_lb/aws_lb_target_group/aws_lb_listener doc
		// pages) - same resource, same schema, so the same taggability.
		// Cross-checked against live/survey-full.json's own
		// signals.taggable for each (all true), the same source
		// stamp_cohort_remainder_test.go's #175 batch comment cites.
		"aws_alb",
		"aws_alb_target_group",
		"aws_alb_listener",
		"aws_sns_topic",
		"aws_vpc_security_group_ingress_rule",
		"aws_vpc_security_group_egress_rule",
		"aws_launch_template",
		"aws_acm_certificate",
		"aws_sfn_state_machine",
		"aws_ebs_volume",

		// Issue #245's "needs hand separator" slice, ratified into
		// tools/row-gen/ratified.json: ten of the batch's 24 rows are
		// taggable, per live/survey-full.json's signals.taggable.
		"aws_kendra_data_source",
		"aws_kendra_faq",
		"aws_quicksight_analysis",
		"aws_quicksight_custom_permissions",
		"aws_quicksight_dashboard",
		"aws_quicksight_data_set",
		"aws_quicksight_data_source",
		"aws_quicksight_template",
		"aws_quicksight_theme",
		"aws_quicksight_vpc_connection",

		// Issue #305: terraform-aws-vpc's "adopt the account's default
		// object instead of creating one" idiom, ratified server-assigned
		// the same shape as their non-default siblings just above
		// (aws_network_acl, aws_route_table, aws_security_group).
		// Taggability from live/survey-full.json's signals.taggable, true
		// for all three.
		"aws_default_network_acl",
		"aws_default_route_table",
		"aws_default_security_group",
	}
	// The markerless retraction (#249, 2026-08-16) removed 77 entries from
	// this list and from the per-cohort slices appended to it below: every
	// type that was both untaggable and ServerAssigned left the admission
	// table, because there is no marker to write and nothing else finds the
	// object again. What is left here is the other untaggable population -
	// types whose identity their own configuration names, which resolve
	// without discovery ever running and so never needed a marker.
	//
	// The per-batch comments below still describe the batch AS RATIFIED, so
	// a count in one of them ("five types with no tags argument at all") is
	// the batch's own number and no longer the number of lines under it.
	// They are left as written rather than recounted: the sentence is
	// evidence about a ratification that happened, and rewriting its
	// arithmetic to match a later retraction would erase what the batch
	// actually found.
	untaggableAdmittedTypes = []string{
		// Issue #272's four: untaggable and server-assigned, which is the
		// markerless veto's own predicate word for word, and admitted
		// nonetheless because the provider's argument reference and the
		// CloudFormation registry schema independently document their name
		// as unique per account and region. They carry no tags argument -
		// that is what put them in the veto - and discovery recognises them
		// by that name instead (internal/live/discovery/uniquename.go).
		"aws_cloudfront_cache_policy",
		"aws_cloudfront_origin_request_policy",
		"aws_cloudfront_response_headers_policy",
		"aws_route53_cidr_collection",

		// The other thirteen of wall/rejected4's WAF batch: no tags argument
		// in the pinned v6.59.0 Argument Reference, no tags_all in the
		// Attribute Reference, signals.taggable false in
		// live/survey-full.json.
		"aws_wafregional_web_acl_association",
		"aws_amplify_domain_association",
		"aws_backup_restore_testing_selection",
		"aws_bedrockagentcore_workload_identity",
		"aws_cognito_user_pool_ui_customization",
		"aws_emr_studio_session_mapping",
		"aws_glue_catalog_table_optimizer",
		"aws_glue_user_defined_function",
		"aws_lambda_layer_version_permission",
		"aws_network_interface_sg_attachment",
		"aws_pinpointsmsvoicev2_resource_policy",
		"aws_route53_cidr_location",
		"aws_scheduler_schedule",
		"aws_service_discovery_instance",
		"aws_ses_receipt_filter",
		"aws_ses_receipt_rule",
		"aws_ses_receipt_rule_set",
		"aws_ses_template",
		// Same batch as the taggable additions above; survey-full.json reports
		// signals.taggable false for each of these.
		"aws_dynamodb_global_secondary_index",
		"aws_dynamodb_kinesis_streaming_destination",
		"aws_elasticache_user_group_association",
		"aws_lightsail_domain",
		"aws_msk_single_scram_secret_association",
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
		// aws_alb_target_group_attachment: the provider's documented alias
		// of aws_lb_target_group_attachment just above, same reasoning as
		// the taggable trio in taggableAdmittedTypes.
		"aws_alb_target_group_attachment",
		// aws_lb_listener_certificate and its own alias
		// aws_alb_listener_certificate: newly admitted alongside the
		// aws_alb* family (issue #184 batch); the doc's Argument Reference
		// names only listener_arn and certificate_arn, no tags argument -
		// confirmed against live/survey-full.json's signals.taggable
		// (false for both).
		"aws_lb_listener_certificate",
		"aws_alb_listener_certificate",
		// The two cloud-singleton rows: a per-account, per-region setting
		// whose whole documented import ID is the Region name, admitted as
		// one Component{Cloud: "region"}. Untaggable in
		// live/survey-full.json's signals.taggable (false for both) and in
		// the v6.59.0 Argument Reference, which names no tags argument on
		// either page.
		//
		// Untaggable AND admitted is the combination markerless.go vetoes -
		// but only when the identity is server-minted, and neither of these
		// is. Both resolve CONCRETE from configuration alone (the resource's
		// own `region` argument, or the region its provider block declares),
		// so discovery never runs and there is no marker to be missing. That
		// is the same standing every other type on this list has.
		"aws_cloudwatch_otel_enrichment",
		"aws_vpc_block_public_access_options",

		// The other 14 of issue #245's "needs hand separator" slice
		// (tools/row-gen/ratified.json): untaggable per
		// live/survey-full.json's signals.taggable.
		"aws_appautoscaling_policy",
		"aws_ec2_local_gateway_route",
		"aws_internet_gateway_attachment",
		"aws_lakeformation_data_cells_filter",
		"aws_lb_trust_store_revocation",
		"aws_notifications_channel_association",
		"aws_quicksight_refresh_schedule",
		"aws_redshift_endpoint_authorization",
		"aws_servicecatalog_principal_portfolio_association",
		"aws_servicecatalog_product_portfolio_association",
		"aws_signer_signing_profile_permission",
		"aws_ssm_maintenance_window_target",
		"aws_vpc_route_server_propagation",
		"aws_vpc_route_server_vpc_association",

		// Issue #245's "fold-child" slice, ratified into
		// tools/row-gen/ratified.json: all 21 of the batch's new rows are
		// untaggable, per live/survey-full.json's signals.taggable - every
		// one is a property-child whose identity folds from an
		// already-admitted parent's own tuple plus the child's own
		// arguments, and none of them carries its own tags argument.
		"aws_app_cookie_stickiness_policy",
		"aws_shield_protection_health_check_association",
		"aws_datapipeline_pipeline_definition",
		"aws_efs_backup_policy",
		"aws_efs_file_system_policy",
		"aws_efs_replication_configuration",
		"aws_grafana_workspace_saml_configuration",
		"aws_iam_user_login_profile",
		"aws_lightsail_bucket_resource_access",
		"aws_lightsail_domain_entry",
		"aws_lightsail_lb_attachment",
		"aws_lightsail_lb_certificate_attachment",
		"aws_organizations_policy_attachment",
		"aws_ram_principal_association",
		"aws_redshift_logging",
		"aws_s3_bucket_analytics_configuration",
		"aws_s3_bucket_inventory",
		"aws_s3_bucket_metric",
		"aws_s3control_bucket_lifecycle_configuration",
		"aws_secretsmanager_tag",
		"aws_verifiedaccess_instance_logging_configuration",

		// Issue #245's "assembled" bucket, the fifth and last slice: 19
		// account/region-singleton types, ratified into
		// tools/row-gen/ratified.json - 16 whose entire documented import ID
		// is the run's own region (a Component{Cloud: "region"}, the same
		// shape aws_cloudwatch_otel_enrichment and
		// aws_vpc_block_public_access_options already hold above) and 3
		// whose import ID is a fixed literal word the docs state directly.
		// All 19 are untaggable per live/survey-full.json's signals.taggable;
		// a per-account, per-region singleton has nothing to tag.
		"aws_apprunner_default_auto_scaling_configuration_version",
		"aws_auditmanager_account_registration",
		"aws_devopsguru_event_sources_config",
		"aws_devopsguru_service_integration",
		"aws_ec2_allowed_images_settings",
		"aws_glue_resource_policy",
		"aws_iam_account_password_policy",
		"aws_iot_event_configurations",
		"aws_kinesis_account_settings",
		"aws_macie2_classification_export_configuration",
		"aws_observabilityadmin_telemetry_enrichment",
		"aws_observabilityadmin_telemetry_evaluation",
		"aws_observabilityadmin_telemetry_evaluation_for_organization",
		"aws_sagemaker_servicecatalog_portfolio_status",
		"aws_servicequotas_auto_management",
		"aws_sesv2_account_vdm_attributes",
		"aws_spot_datafeed_subscription",
		"aws_xray_encryption_config",
		"aws_xray_trace_segment_destination",
		// aws_s3_bucket_accelerate_configuration and
		// aws_s3_bucket_request_payment_configuration: ratified from
		// row-gen's client-named proposal (import-grammar precedence off a
		// single required "bucket" argument, same shape as the neighboring
		// aws_s3_bucket_* configuration types above); neither has a tags
		// argument, confirmed against live/survey-full.json's
		// signals.taggable false for both.
		"aws_s3_bucket_accelerate_configuration",
		"aws_s3_bucket_request_payment_configuration",

		// Issue #307: aws_vpc_security_group_rules_exclusive, ratified
		// client-named off its sole required, ForceNew security_group_id
		// argument (tools/row-gen/ratified.json) - the provider ships no
		// resource Identity Schema for this type, so the row is derived
		// from its own Import documentation's "using the
		// `security_group_id`" prose rather than from the wire schema, the
		// same doc-derived shape aws_vpc_security_group_vpc_association's
		// row already had at a schema wire, just here without one. No tags
		// argument in the pinned v6.59.0 Argument Reference; confirmed
		// against live/survey-full.json's signals.taggable (false).
		"aws_vpc_security_group_rules_exclusive",

		// Issue #310: aws_autoscaling_traffic_source_attachment, ratified
		// off its documented composite import ID
		// (autoscaling_group_name,traffic_source_type,traffic_source_identifier)
		// - the second and third components read out of a required,
		// max_items:1 traffic_source nested block via the new
		// identity.Component.Block field rather than as top-level
		// arguments. The provider ships no resource Identity Schema for
		// this type. No tags argument in the pinned v6.59.0 Argument
		// Reference; confirmed against live/survey-full.json's
		// signals.taggable (false).
		"aws_autoscaling_traffic_source_attachment",

		// Issue #334: the two IAM exclusive-set enforcers, ratified
		// client-named off their sole identity-bearing argument role_name,
		// which the provider's own Import documentation states is the
		// whole import ID ("% terraform import
		// aws_iam_role_policies_exclusive.example MyRole"). Same shape and
		// same row-gen rule as the #307 entry above - "import-grammar
		// precedence: composed_of_arguments, single argument, arity
		// confirmed against the example string" - and row-gen proposed
		// both of these rows verbatim all along; they were simply never
		// ratified. Neither ships a resource Identity Schema in v6.59.0
		// and neither has a tags argument in the pinned Argument
		// Reference; confirmed against live/survey-full.json's
		// signals.taggable (false for both).
		"aws_iam_role_policies_exclusive",
		"aws_iam_role_policy_attachments_exclusive",

		// Issue #326: the first four Kubernetes-provider types this table
		// admits, ratified straight from the real, current
		// hashicorp/kubernetes provider docs (tools/row-gen/annotations.json's
		// own rulings for the four, since they have no CFN evidence path for
		// row-gen to compare against at all). Confirmed via
		// markers.Taggable/TagSurface reading that the Kubernetes provider's
		// schemas carry no top-level "tags" attribute at all (0 of 0 per
		// #243's own recomputed table) - the same "admitted, unstamped,
		// plans anyway" shape the 11 google_* schema-fallback types already
		// have, reached here instead through a hand-ratified import-ID row
		// because api_version/kind are Go constants with no config source
		// (crossprovider_test.go's TestKubernetesAPIVersionAndKindHaveNoConfigSource),
		// so schema-fallback alone can never admit them.
		"kubernetes_cluster_role_binding",
		"kubernetes_config_map",
		"kubernetes_namespace",
		"kubernetes_storage_class",
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
		// GitHub issue #73's RECORD_ADMITTED logical types (null_resource,
		// terraform_data, the time_* and random_* rows
		// internal/live/identity/table_recordbacked.go admits) carry no AWS
		// tags argument to have or lack at all - they are not AWS resources,
		// and their identity is the persisted micro-state record itself, not
		// a marker. "Taggable" is not a question this pin needs to answer
		// for them, the same reason tools/survey-gen's own untaggable
		// derivation skips them (see untaggable_render.go).
		if entry, ok := identity.LookupType(resourceType); ok && entry.RecordBacked {
			continue
		}
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

// TestKubernetesConfigMapIsAdmittedButUntaggable is remaining-work item 3's
// "admitted-but-unstamped" assertion (issue #326), behaviorally rather than
// only via the pinned lists above: kubernetes_config_map resolves an
// identity and plans (internal/live/identity's own
// TestKubernetesConfigMapIdentity proves that half), but the Kubernetes
// provider's schema carries no top-level tags attribute at all, so the
// sweep has nothing to write an ownership marker into. This is the same
// "admitted, unstamped, plans anyway" shape google_workstations_workstation_
// cluster already has via schema fallback
// (TestStamp_exhibitedKeyFormatIsNotStamped, keyspace_test.go) - here
// reached instead through a hand-ratified import-ID row, because
// api_version/kind are Go constants with no config source
// (crossprovider_test.go's
// TestKubernetesAPIVersionAndKindHaveNoConfigSource), so schema-fallback
// alone could never have admitted this type.
func TestKubernetesConfigMapIsAdmittedButUntaggable(t *testing.T) {
	cfg := loadSource(t, `
resource "kubernetes_config_map" "cm" {
  metadata {
    name      = "my-config"
    namespace = "default"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	if len(res.Stamped) != 0 {
		t.Errorf("kubernetes_config_map was stamped, but its schema carries no tags attribute to write one into: %+v", res.Stamped)
	}
	assertSkippedUntaggable(t, res, "kubernetes_config_map.cm")
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
	// The remedy has to name the interpolation, not addressExpr's display
	// string. This used to assert "aws_eip.pool:count.index", which is what
	// the message told the user to write - and writing it literally sets the
	// same constant on all three instances, reproducing this very error.
	// See #101.
	assertDiagContains(t, diags, "Ownership marker conflict", "count", "${count.index}")

	// The rule is not "don't use this one verb", it is "never quote
	// addressExpr's display string as something to type": it has the
	// interpolation stripped out, so writing it sets the same constant on
	// every instance - the error being reported. An audit defeated the
	// earlier verb-specific check in one attempt by switching "Write it as"
	// to "Set the tag to". Assert the string is absent outright.
	const displayForm = "aws_eip.pool:count.index"
	for _, d := range diags {
		if strings.Contains(d.Description().Detail, displayForm) {
			t.Errorf("the remedy quotes addressExpr's display string %q, which has no interpolation in it and reproduces this very error if typed: %s", displayForm, d.Description().Detail)
		}
	}
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
// taggedSchema and untaggedSchema build one caricature resource schema, with
// and without the tags argument. They were closures inside testSchemas until
// the per-cohort split (contributing/LIVE-TABLES.md) gave the cohort
// fragments their own files, which need to call them too; hoisting them here
// is the only reason their names changed.
func taggedSchema(names ...string) *configschema.Block {
	attrs := map[string]*configschema.Attribute{
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}
	for _, n := range names {
		attrs[n] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	}
	return &configschema.Block{Attributes: attrs}
}

func untaggedSchema(names ...string) *configschema.Block {
	attrs := map[string]*configschema.Attribute{}
	for _, n := range names {
		attrs[n] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	}
	return &configschema.Block{Attributes: attrs}
}

// schemaFragments are the per-cohort contributions to [testSchemas], each
// appended by one stamp_cohort_*_test.go file's init.
var schemaFragments []func(testSchemaSource)

// registerCohortStamp folds one cohort's slice of all three pinned
// collections in. Package-level var initializers all run before any init
// func, so the core literals below are complete before the first fragment
// appends to them, and the order the fragments arrive in does not matter:
// every one of the three is consumed as a set (TestTaggableSetCoversAdmissionTable
// builds a map, TestUntaggableTypesMatchLimitationsDoc sorts before
// comparing).
func registerCohortStamp(taggable, untaggable []string, schemas func(testSchemaSource)) {
	taggableAdmittedTypes = append(taggableAdmittedTypes, taggable...)
	untaggableAdmittedTypes = append(untaggableAdmittedTypes, untaggable...)
	if schemas != nil {
		schemaFragments = append(schemaFragments, schemas)
	}
}

// mergeCohortSchemas adds one cohort's caricature schemas to the map being
// built, refusing a type two cohorts both describe rather than letting the
// later fragment silently win it - the add-only rule
// tools/mapping-gen/overlay.d applies to its own fragments.
func mergeCohortSchemas(dst, src testSchemaSource) {
	for k, v := range src {
		if _, dup := dst[k]; dup {
			panic("stamp: type " + k + " has a test schema in more than one cohort fragment")
		}
		dst[k] = v
	}
}

func testSchemas() Schemas {
	s := testSchemaSource{
		// Taggable, as the real AWS provider has them.
		"aws_vpc":                    taggedSchema("id", "cidr_block"),
		"aws_subnet":                 taggedSchema("id", "vpc_id", "cidr_block", "availability_zone"),
		"aws_security_group":         taggedSchema("id", "name", "description", "vpc_id"),
		"aws_route_table":            taggedSchema("id", "vpc_id"),
		"aws_default_network_acl":    taggedSchema("id", "default_network_acl_id"),
		"aws_default_route_table":    taggedSchema("id", "default_route_table_id"),
		"aws_default_security_group": taggedSchema("id", "vpc_id"),
		"aws_internet_gateway":       taggedSchema("id", "vpc_id"),
		"aws_eip":                    taggedSchema("id", "domain"),
		"aws_s3_bucket":              taggedSchema("id", "bucket"),
		"aws_iam_role":               taggedSchema("id", "name", "assume_role_policy"),
		"aws_cloudwatch_log_group":   taggedSchema("id", "name", "retention_in_days"),
		"aws_ssm_parameter":          taggedSchema("id", "name", "type", "value"),
		"aws_dynamodb_table":         taggedSchema("id", "name", "billing_mode", "hash_key"),
		"aws_ecs_cluster":            taggedSchema("id", "name", "arn"),
		"aws_kms_key":                taggedSchema("id", "key_id", "description"),
		"aws_route53_zone":           taggedSchema("id", "zone_id", "name"),
		"aws_cloudwatch_metric_alarm": taggedSchema("id", "alarm_name", "comparison_operator", "evaluation_periods",
			"metric_name", "namespace", "period", "statistic", "threshold"),

		// Untaggable, likewise.
		"aws_route":                                          untaggedSchema("route_table_id", "destination_cidr_block", "gateway_id"),
		"aws_route_table_association":                        untaggedSchema("subnet_id", "route_table_id"),
		"aws_s3_bucket_policy":                               untaggedSchema("bucket", "policy"),
		"aws_s3_bucket_accelerate_configuration":             untaggedSchema("bucket", "status"),
		"aws_s3_bucket_request_payment_configuration":        untaggedSchema("bucket", "payer"),
		"aws_iam_role_policy_attachment":                     untaggedSchema("role", "policy_arn"),
		"aws_s3_bucket_versioning":                           untaggedSchema("id", "bucket"),
		"aws_s3_bucket_public_access_block":                  untaggedSchema("id", "bucket", "block_public_acls", "block_public_policy"),
		"aws_s3_bucket_server_side_encryption_configuration": untaggedSchema("id", "bucket"),
		"aws_s3_bucket_lifecycle_configuration":              untaggedSchema("id", "bucket"),
		"aws_iam_role_policy":                                untaggedSchema("id", "role", "name", "policy"),
		"aws_kms_alias":                                      untaggedSchema("id", "name", "target_key_id"),
		"aws_route53_record":                                 untaggedSchema("id", "zone_id", "name", "type", "ttl"),
		"aws_lb":                                             taggedSchema("id", "arn", "name", "internal"),
		"aws_lb_target_group":                                taggedSchema("id", "arn", "name", "port", "protocol", "vpc_id"),
		"aws_lb_listener":                                    taggedSchema("id", "arn", "load_balancer_arn", "port", "protocol"),
		"aws_alb":                                            taggedSchema("id", "arn", "name", "internal"),
		"aws_appstream_fleet":                                taggedSchema("id", "arn", "name"),
		"aws_appstream_image_builder":                        taggedSchema("id", "arn", "name"),
		"aws_gamelift_game_server_group":                     taggedSchema("id", "arn", "name"),
		"aws_glue_catalog":                                   taggedSchema("id", "arn", "name"),
		"aws_glue_dev_endpoint":                              taggedSchema("id", "arn", "name"),
		"aws_networkflowmonitor_monitor":                     taggedSchema("id", "arn", "name"),
		"aws_rds_cluster_endpoint":                           taggedSchema("id", "arn", "name"),
		"aws_shield_protection_group":                        taggedSchema("id", "arn", "name"),
		"aws_amplify_domain_association":                     untaggedSchema("id"),
		"aws_backup_restore_testing_selection":               untaggedSchema("id"),
		"aws_bedrockagentcore_workload_identity":             untaggedSchema("id"),
		"aws_codebuild_source_credential":                    untaggedSchema("id"),
		"aws_cognito_user_pool_ui_customization":             untaggedSchema("id"),
		"aws_elasticache_global_replication_group":           untaggedSchema("id"),
		"aws_emr_studio_session_mapping":                     untaggedSchema("id"),
		"aws_glue_catalog_table_optimizer":                   untaggedSchema("id"),
		"aws_glue_user_defined_function":                     untaggedSchema("id"),
		"aws_lambda_layer_version_permission":                untaggedSchema("id"),
		"aws_network_interface_sg_attachment":                untaggedSchema("id"),
		"aws_pinpointsmsvoicev2_resource_policy":             untaggedSchema("id"),
		"aws_route53_cidr_location":                          untaggedSchema("id"),
		"aws_scheduler_schedule":                             untaggedSchema("id"),
		"aws_service_discovery_instance":                     untaggedSchema("id"),
		"aws_ses_receipt_filter":                             untaggedSchema("id"),
		"aws_ses_receipt_rule":                               untaggedSchema("id"),
		"aws_ses_receipt_rule_set":                           untaggedSchema("id"),
		"aws_ses_template":                                   untaggedSchema("id"),
		"aws_vpc_block_public_access_options":                untaggedSchema("id", "region", "internet_gateway_block_mode"),
		"aws_comprehend_entity_recognizer":                   taggedSchema("id", "arn", "name"),
		"aws_inspector_resource_group":                       taggedSchema("id", "arn"),
		"aws_iot_ca_certificate":                             taggedSchema("id", "arn"),
		"aws_kinesisanalyticsv2_application":                 taggedSchema("id", "arn", "name"),
		"aws_mailmanager_ingress_point":                      taggedSchema("id", "arn", "ingress_point_name"),
		"aws_osis_pipeline":                                  taggedSchema("id", "arn", "pipeline_name"),
		"aws_resiliencehubv2_service":                        taggedSchema("id", "arn", "name"),
		"aws_resiliencehubv2_system":                         taggedSchema("id", "arn", "name"),
		"aws_verifiedaccess_group":                           taggedSchema("id", "arn"),
		"aws_devopsguru_notification_channel":                untaggedSchema("id"),
		"aws_dx_hosted_private_virtual_interface":            untaggedSchema("id", "connection_id", "vlan"),
		"aws_dx_hosted_public_virtual_interface":             untaggedSchema("id", "connection_id", "vlan"),
		"aws_dx_hosted_transit_virtual_interface":            untaggedSchema("id", "connection_id", "vlan"),
		"aws_dynamodb_global_secondary_index":                untaggedSchema("id", "table_name", "name"),
		"aws_dynamodb_kinesis_streaming_destination":         untaggedSchema("id", "table_name", "stream_arn"),
		"aws_elasticache_user_group_association":             untaggedSchema("id", "user_group_id", "user_id"),
		"aws_lightsail_domain":                               untaggedSchema("id", "domain_name"),
		"aws_msk_single_scram_secret_association":            untaggedSchema("id", "cluster_arn", "secret_arn"),
		"aws_network_acl_association":                        untaggedSchema("id", "network_acl_id", "subnet_id"),
		"aws_s3outposts_endpoint":                            untaggedSchema("id", "outpost_id", "security_group_id"),
		"aws_vpc_endpoint_connection_notification":           untaggedSchema("id", "connection_notification_arn"),
		"aws_alb_target_group":                               taggedSchema("id", "arn", "name", "port", "protocol", "vpc_id"),
		"aws_alb_listener":                                   taggedSchema("id", "arn", "load_balancer_arn", "port", "protocol"),
		"aws_sns_topic":                                      taggedSchema("id", "arn", "name"),
		"aws_vpc_security_group_ingress_rule":                taggedSchema("id", "arn", "security_group_rule_id", "security_group_id", "cidr_ipv4", "from_port", "to_port", "ip_protocol"),
		"aws_vpc_security_group_egress_rule":                 taggedSchema("id", "arn", "security_group_rule_id", "security_group_id", "cidr_ipv4", "ip_protocol"),
		"aws_vpc_security_group_rules_exclusive":             untaggedSchema("security_group_id", "ingress_rule_ids", "egress_rule_ids"),
		"aws_iam_role_policies_exclusive":                    untaggedSchema("role_name", "policy_names"),
		"aws_iam_role_policy_attachments_exclusive":          untaggedSchema("role_name", "policy_arns"),
		"aws_autoscaling_traffic_source_attachment":          untaggedSchema("autoscaling_group_name", "traffic_source"),
		"kubernetes_cluster_role_binding":                    untaggedSchema("metadata", "role_ref", "subject"),
		"kubernetes_config_map":                              untaggedSchema("metadata", "data", "binary_data"),
		"kubernetes_namespace":                               untaggedSchema("metadata"),
		"kubernetes_storage_class":                           untaggedSchema("metadata", "storage_provisioner"),
		"aws_launch_template":                                taggedSchema("id", "arn", "name", "image_id", "instance_type"),
		"aws_acm_certificate":                                taggedSchema("id", "arn", "domain_name", "validation_method"),
		"aws_sfn_state_machine":                              taggedSchema("id", "arn", "name", "role_arn", "definition"),
		"aws_ebs_volume":                                     taggedSchema("id", "arn", "availability_zone", "size"),

		// The four server-minted composites wall/servermint admitted.
		// Attribute shapes follow each doc page's own Argument and
		// Attribute Reference; all four carry a top-level tags argument,
		// which is the whole reason they were admitted and the other 38 in
		// the same bucket were not.
		"aws_ecs_task_set":                            taggedSchema("id", "arn", "task_set_id", "service", "cluster", "task_definition"),
		"aws_eks_pod_identity_association":            taggedSchema("id", "association_arn", "association_id", "cluster_name", "namespace", "service_account", "role_arn"),
		"aws_prometheus_anomaly_detector":             taggedSchema("id", "arn", "workspace_id", "alias"),
		"aws_service_discovery_private_dns_namespace": taggedSchema("id", "arn", "name", "vpc", "hosted_zone"),
		"aws_vpc_ipam_pool_cidr_allocation":           taggedSchema("id", "ipam_pool_id", "cidr", "description"),

		// The WAF Classic and WAF Classic Regional batch (wall/rejected4).
		// Attribute shapes follow each doc page's own Argument/Attribute
		// Reference: every one exports id, the five taggable ones also
		// export arn and carry tags.
		"aws_waf_rule":                            taggedSchema("id", "arn", "name", "metric_name"),
		"aws_waf_web_acl":                         taggedSchema("id", "arn", "name", "metric_name"),
		"aws_wafregional_rate_based_rule":         taggedSchema("id", "arn", "name", "metric_name", "rate_key", "rate_limit"),
		"aws_wafregional_rule":                    taggedSchema("id", "arn", "name", "metric_name"),
		"aws_wafregional_web_acl":                 taggedSchema("id", "arn", "name", "metric_name"),
		"aws_waf_byte_match_set":                  untaggedSchema("id", "arn", "name"),
		"aws_waf_ipset":                           untaggedSchema("id", "arn", "name"),
		"aws_waf_size_constraint_set":             untaggedSchema("id", "arn", "name"),
		"aws_waf_sql_injection_match_set":         untaggedSchema("id", "arn", "name"),
		"aws_waf_xss_match_set":                   untaggedSchema("id", "arn", "name"),
		"aws_wafregional_byte_match_set":          untaggedSchema("id", "name"),
		"aws_wafregional_geo_match_set":           untaggedSchema("id", "name"),
		"aws_wafregional_ipset":                   untaggedSchema("id", "arn", "name"),
		"aws_wafregional_regex_pattern_set":       untaggedSchema("id", "name"),
		"aws_wafregional_size_constraint_set":     untaggedSchema("id", "name"),
		"aws_wafregional_sql_injection_match_set": untaggedSchema("id", "name"),
		"aws_wafregional_xss_match_set":           untaggedSchema("id", "name"),
		"aws_wafregional_web_acl_association":     untaggedSchema("id", "web_acl_id", "resource_arn"),

		"aws_lb_target_group_attachment":  untaggedSchema("id", "target_group_arn", "target_id", "port"),
		"aws_alb_target_group_attachment": untaggedSchema("id", "target_group_arn", "target_id", "port"),
		"aws_lb_listener_certificate":     untaggedSchema("id", "listener_arn", "certificate_arn"),
		"aws_alb_listener_certificate":    untaggedSchema("id", "listener_arn", "certificate_arn"),

		// Issue #245's "needs hand separator" slice, ratified into
		// tools/row-gen/ratified.json - taggability per
		// live/survey-full.json's signals.taggable.
		"aws_kendra_data_source":                             taggedSchema("id", "arn", "name"),
		"aws_kendra_faq":                                     taggedSchema("id", "arn", "name"),
		"aws_quicksight_analysis":                            taggedSchema("id", "arn", "analysis_id"),
		"aws_quicksight_custom_permissions":                  taggedSchema("id", "arn", "custom_permissions_name"),
		"aws_quicksight_dashboard":                           taggedSchema("id", "arn", "dashboard_id"),
		"aws_quicksight_data_set":                            taggedSchema("id", "arn", "data_set_id"),
		"aws_quicksight_data_source":                         taggedSchema("id", "arn", "data_source_id"),
		"aws_quicksight_template":                            taggedSchema("id", "arn", "template_id"),
		"aws_quicksight_theme":                               taggedSchema("id", "arn", "theme_id"),
		"aws_quicksight_vpc_connection":                      taggedSchema("id", "arn", "vpc_connection_id"),
		"aws_appautoscaling_policy":                          untaggedSchema("id", "arn", "name", "policy_type", "resource_id"),
		"aws_ec2_local_gateway_route":                        untaggedSchema("id", "local_gateway_route_table_id", "destination_cidr_block"),
		"aws_internet_gateway_attachment":                    untaggedSchema("id", "internet_gateway_id", "vpc_id"),
		"aws_lakeformation_data_cells_filter":                untaggedSchema("id", "table_data", "database_name", "table_name"),
		"aws_lb_trust_store_revocation":                      untaggedSchema("id", "trust_store_arn"),
		"aws_notifications_channel_association":              untaggedSchema("id", "arn", "notification_configuration_arn"),
		"aws_quicksight_refresh_schedule":                    untaggedSchema("id", "data_set_id", "schedule_id"),
		"aws_redshift_endpoint_authorization":                untaggedSchema("id", "account", "cluster_identifier"),
		"aws_servicecatalog_principal_portfolio_association": untaggedSchema("id", "portfolio_id", "principal_arn"),
		"aws_servicecatalog_product_portfolio_association":   untaggedSchema("id", "portfolio_id", "product_id"),
		"aws_signer_signing_profile_permission":              untaggedSchema("id", "profile_name", "action"),
		"aws_ssm_maintenance_window_target":                  untaggedSchema("id", "window_id", "resource_type"),
		"aws_vpc_route_server_propagation":                   untaggedSchema("id", "route_server_id", "route_table_id"),
		"aws_vpc_route_server_vpc_association":               untaggedSchema("id", "route_server_id", "vpc_id"),

		// Issue #245's "fold-child" slice, ratified into
		// tools/row-gen/ratified.json - all untaggable per
		// live/survey-full.json's signals.taggable.
		"aws_app_cookie_stickiness_policy":                  untaggedSchema("id", "load_balancer", "lb_port", "name", "cookie_name"),
		"aws_shield_protection_health_check_association":    untaggedSchema("id", "shield_protection_id", "health_check_arn"),
		"aws_datapipeline_pipeline_definition":              untaggedSchema("id", "pipeline_id"),
		"aws_efs_backup_policy":                             untaggedSchema("id", "file_system_id"),
		"aws_efs_file_system_policy":                        untaggedSchema("id", "file_system_id", "policy"),
		"aws_efs_replication_configuration":                 untaggedSchema("id", "source_file_system_id"),
		"aws_grafana_workspace_saml_configuration":          untaggedSchema("id", "workspace_id", "editor_role_values"),
		"aws_iam_user_login_profile":                        untaggedSchema("id", "user"),
		"aws_lightsail_bucket_resource_access":              untaggedSchema("id", "bucket_name", "resource_name"),
		"aws_lightsail_domain_entry":                        untaggedSchema("id", "domain_name", "name", "type", "target"),
		"aws_lightsail_lb_attachment":                       untaggedSchema("id", "lb_name", "instance_name"),
		"aws_lightsail_lb_certificate_attachment":           untaggedSchema("id", "lb_name", "certificate_name"),
		"aws_organizations_policy_attachment":               untaggedSchema("id", "policy_id", "target_id"),
		"aws_ram_principal_association":                     untaggedSchema("id", "resource_share_arn", "principal"),
		"aws_redshift_logging":                              untaggedSchema("id", "cluster_identifier"),
		"aws_s3_bucket_analytics_configuration":             untaggedSchema("id", "bucket", "name"),
		"aws_s3_bucket_inventory":                           untaggedSchema("id", "bucket", "name"),
		"aws_s3_bucket_metric":                              untaggedSchema("id", "bucket", "name"),
		"aws_s3control_bucket_lifecycle_configuration":      untaggedSchema("id", "bucket"),
		"aws_secretsmanager_tag":                            untaggedSchema("id", "secret_id", "key", "value"),
		"aws_verifiedaccess_instance_logging_configuration": untaggedSchema("id", "verifiedaccess_instance_id"),

		// Issue #245's "assembled" bucket: 16 account/region-singleton
		// types whose entire documented import ID is the run's own region,
		// the same shape as aws_vpc_block_public_access_options above.
		"aws_apprunner_default_auto_scaling_configuration_version":     untaggedSchema("id", "region"),
		"aws_auditmanager_account_registration":                        untaggedSchema("id", "region"),
		"aws_devopsguru_event_sources_config":                          untaggedSchema("id", "region"),
		"aws_devopsguru_service_integration":                           untaggedSchema("id", "region"),
		"aws_ec2_allowed_images_settings":                              untaggedSchema("id", "region"),
		"aws_glue_resource_policy":                                     untaggedSchema("id", "region"),
		"aws_iot_event_configurations":                                 untaggedSchema("id", "region"),
		"aws_kinesis_account_settings":                                 untaggedSchema("id", "region"),
		"aws_macie2_classification_export_configuration":               untaggedSchema("id", "region"),
		"aws_observabilityadmin_telemetry_enrichment":                  untaggedSchema("id", "region"),
		"aws_observabilityadmin_telemetry_evaluation":                  untaggedSchema("id", "region"),
		"aws_observabilityadmin_telemetry_evaluation_for_organization": untaggedSchema("id", "region"),
		"aws_sagemaker_servicecatalog_portfolio_status":                untaggedSchema("id", "region"),
		"aws_servicequotas_auto_management":                            untaggedSchema("id", "region"),
		"aws_xray_encryption_config":                                   untaggedSchema("id", "region"),
		"aws_xray_trace_segment_destination":                           untaggedSchema("id", "region"),
		// The other 2: a fixed literal word the docs state directly as the
		// whole import ID.
		"aws_iam_account_password_policy": untaggedSchema("id"),
		"aws_spot_datafeed_subscription":  untaggedSchema("id"),

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
	for _, f := range schemaFragments {
		f(s)
	}
	return s
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
		// substr is issue #71's addition: a per-instance address long enough
		// to need continuation tags is split with n independent substr()
		// calls (stamp.templateChunkMarkers). Same reasoning as the two
		// above.
		"substr": stdlib.SubstrFunc,
		// replace is issue #178's addition: a for_each block's each.key is
		// escaped through three nested replace() calls (stamp.addressExpr,
		// stamp.eachKeyEscapedExpr) rather than through Go code, because it
		// has to be evaluated at apply time with real repetition data. This
		// is internal/lang/functions.go's own "replace", not a stand-in, so
		// evaluating a stamped tag here is evaluating what the plan will.
		"replace": funcs.ReplaceFunc,
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
