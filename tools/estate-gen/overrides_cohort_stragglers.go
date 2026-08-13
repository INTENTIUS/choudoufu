// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesStragglers is the stragglers cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesStragglers = map[string]typeOverride{
	// ---- stragglers batch (issue #65's ratification campaign) -----------
	"aws_ecr_lifecycle_policy": {
		Reasons: []string{
			`"policy" is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    rules = [{
      rulePriority = 1
      description  = "expire untagged images"
      selection = {
        tagStatus   = "untagged"
        countType   = "imageCountMoreThan"
        countNumber = 1
      }
      action = {
        type = "expire"
      }
    }]
  })`))
		},
	},
	"aws_ecr_pull_through_cache_rule": {
		Reasons: []string{
			`"ecr_repository_prefix" is a required string the provider length-constrains to 2-30 characters (validate: "expected length of ecr_repository_prefix to be in the range (2 - 30)"); the generic tofu-<cohort>-cohort-<type> placeholder is 44 characters`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("ecr_repository_prefix", exprTokens(`"tofu-stragglers"`))
		},
	},
	"aws_ecr_pull_time_update_exclusion": {
		Reasons: []string{
			`"principal_arn" is a required string the provider validates is a well-formed ARN (validate: "The provided value cannot be parsed as an ARN"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("principal_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-ecr-pull-exclusion"`, g.cohort)))
		},
	},
	"aws_ecr_repository_creation_template": {
		Reasons: []string{
			`"applied_for" is a required set of strings the provider validates against a fixed set (validate: "expected applied_for to be one of [\"REPLICATION\" \"PULL_THROUGH_CACHE\" \"CREATE_ON_PUSH\"]"); the generic placeholder string matches none of them`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("applied_for", exprTokens(`["CREATE_ON_PUSH"]`))
		},
	},
	"aws_ecr_repository_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON"), the same shape as aws_s3_bucket_policy above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy", exprTokens(fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AllowPull"
      Effect    = "Allow"
      Principal = "*"
      Action    = ["ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage"]
      Resource  = "arn:aws:ecr:us-east-1:000000000000:repository/tofu-%s-cohort-ecr-repository-policy"
    }]
  })`, g.cohort)))
		},
	},
	"aws_networkmanager_core_network_policy_attachment": {
		Reasons: []string{
			`"core_network_id" is a required string the provider length- and format-constrains (validate: "expected length of core_network_id to be in the range (0 - 50)" and "invalid value for core_network_id (must be a valid Core Network ID)"); the generic tofu-<cohort>-cohort-<type> placeholder matches neither. "policy_document" is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "\"policy_document\" contains an invalid JSON") - set to the shape the provider's own Core Network Policy documentation describes (version, core-network-configuration, segments), the same minimal-but-well-formed-JSON standard aws_networkfirewall_rule_group's own override uses for its own service-specific document shape.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("core_network_id", exprTokens(`"core-network-0123456789abcdef0"`))
			body.SetAttributeRaw("policy_document", exprTokens(`jsonencode({
    version = "2021.12"
    "core-network-configuration" = {
      "vpn-ecmp-support" = false
      "asn-ranges"        = ["64512-64555"]
    }
    segments = [{
      name = "segment1"
    }]
  })`))
		},
	},
	"aws_storagegateway_tape_pool": {
		Reasons: []string{
			`"storage_class" is a required string the provider validates against a fixed set (validate: "expected storage_class to be one of [\"DEEP_ARCHIVE\" \"GLACIER\"]"); the generic placeholder string matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("storage_class", exprTokens(`"GLACIER"`))
		},
	},
	"aws_transfer_agreement": {
		Reasons: []string{
			`"access_role" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "\"access_role\" (placeholder) is an invalid ARN: arn: invalid prefix"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("access_role", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-transfer-agreement"`, g.cohort)))
		},
	},
	"aws_transfer_certificate": {
		Reasons: []string{
			`"usage" is a required string the provider validates against a fixed set (validate: "expected usage to be one of [\"SIGNING\" \"ENCRYPTION\" \"TLS\"]"); the generic placeholder string matches none of them`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("usage", exprTokens(`"SIGNING"`))
		},
	},
	"aws_transfer_profile": {
		Reasons: []string{
			`"profile_type" is a required string the provider validates against a fixed set (validate: "expected profile_type to be one of [\"LOCAL\" \"PARTNER\"]"); the generic placeholder string matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("profile_type", exprTokens(`"LOCAL"`))
		},
	},
	"aws_transfer_web_app": {
		Reasons: []string{
			`identity_provider_details is a required block appearing zero times in the generic pass's output (the wire schema's own MinItems does not force it, unlike a required string argument) - not caught by the generic required-only pass, only surfaced by "terraform validate" ("Block identity_provider_details must have a configuration value as the provider has marked it as required"). A wholly empty block satisfies validate, but not apply: hand-verifying this cohort against the pinned floci image surfaced a provider-side panic expanding an empty identity_provider_details ("Expanding ...webAppIdentityProviderDetailsModel returned nil"), reachable independently of floci since it happens client-side during request marshaling, before any HTTP call. identity_center_config's own fields are Optional, but populating them with placeholder values avoids the empty-block panic entirely.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			details := body.AppendNewBlock("identity_provider_details", nil)
			idc := details.Body().AppendNewBlock("identity_center_config", nil)
			idc.Body().SetAttributeRaw("instance_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:sso:::instance/ssoins-%s"`, "1234567890abcdef")))
			idc.Body().SetAttributeRaw("role", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-transfer-web-app"`, g.cohort)))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesStragglers) }
