// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesSecurity is the security cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesSecurity = map[string]typeOverride{
	// ---- Sixth batch, security and secrets (issue #65). Two shapes of
	// ---- fix: enum/format validators the generic pass's placeholders
	// ---- cannot satisfy, and cross-references the generic pass's
	// ---- parentRef wiring gets wrong within this cohort - several of this
	// ---- batch's own parent-derived rows (aws_guardduty_organization_configuration,
	// ---- aws_guardduty_filter/ipset/threatintelset/publishing_destination/member)
	// ---- have a plain "detector_id" argument that coincidentally matches
	// ---- aws_guardduty_organization_configuration's own identity argument
	// ---- name too (Components: []Component{attr("detector_id")}), so
	// ---- parentRef wires every one of them to that resource's own
	// ---- detector_id echo instead of the real aws_guardduty_detector
	// ---- marker - the same collision class as aws_glue_catalog_database's
	// ---- entry above, fixed the same way.
	"aws_acmpca_certificate_authority": {
		Reasons: []string{
			`certificate_authority_configuration.key_algorithm and .signing_algorithm are plain strings in the schema but the provider validates both against fixed enums (validate: "expected key_algorithm/signing_algorithm to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "certificate_authority_configuration" {
					blk.Body().SetAttributeRaw("key_algorithm", exprTokens(`"RSA_2048"`))
					blk.Body().SetAttributeRaw("signing_algorithm", exprTokens(`"SHA256WITHRSA"`))
				}
			}
		},
	},
	"aws_acmpca_certificate_authority_certificate": {
		Reasons: []string{
			`certificate_authority_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), which the generic pass's placeholder name is not - a real cross-reference to aws_acmpca_certificate_authority.app's own arn is both the fix and the point of this coverage row (the parent-derived composite this batch ratified)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if ca, ok := g.byType["aws_acmpca_certificate_authority"]; ok {
				body.SetAttributeRaw("certificate_authority_arn", exprTokens(fmt.Sprintf("%s.arn", ca)))
			}
		},
	},
	"aws_acmpca_policy": {
		Reasons: []string{
			`resource_arn is not wired to any resource by the generic pass (aws_acmpca_certificate_authority is server-assigned, so parentRef's identity-argument match never fires - see this file's batch header comment); policy is a plain string in the schema but the provider validates it is well-formed JSON (validate: "contains an invalid JSON")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if ca, ok := g.byType["aws_acmpca_certificate_authority"]; ok {
				body.SetAttributeRaw("resource_arn", exprTokens(fmt.Sprintf("%s.arn", ca)))
			}
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::000000000000:root" }
      Action    = "acm-pca:IssueCertificate"
    }]
  })`))
		},
	},
	"aws_guardduty_organization_configuration": {
		Reasons: []string{
			`auto_enable_organization_members is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected auto_enable_organization_members to be one of [NEW ALL NONE]"); detector_id collides with this same type's own identity argument name (see this file's batch header comment), so the generic pass gives it a placeholder string instead of the real aws_guardduty_detector.app.id it should carry`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("auto_enable_organization_members", exprTokens(`"NEW"`))
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
		},
	},
	"aws_guardduty_filter": {
		Reasons: []string{
			`detector_id collides with aws_guardduty_organization_configuration's own identity argument name (see this file's batch header comment), so the generic pass wires it to that unrelated resource's own detector_id echo instead of the real aws_guardduty_detector.app.id; action is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected action to be one of [NOOP ARCHIVE]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
			body.SetAttributeRaw("action", exprTokens(`"NOOP"`))
		},
	},
	"aws_guardduty_ipset": {
		Reasons: []string{
			`detector_id collides with aws_guardduty_organization_configuration's own identity argument name (see this file's batch header comment), so the generic pass wires it to that unrelated resource's own detector_id echo instead of the real aws_guardduty_detector.app.id; format is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected format to be one of [TXT STIX OTX_CSV ALIEN_VAULT PROOF_POINT FIRE_EYE]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
			body.SetAttributeRaw("format", exprTokens(`"TXT"`))
		},
	},
	"aws_guardduty_threatintelset": {
		Reasons: []string{
			`detector_id collides with aws_guardduty_organization_configuration's own identity argument name (see this file's batch header comment), so the generic pass wires it to that unrelated resource's own detector_id echo instead of the real aws_guardduty_detector.app.id; format is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected format to be one of [TXT STIX OTX_CSV ALIEN_VAULT PROOF_POINT FIRE_EYE]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
			body.SetAttributeRaw("format", exprTokens(`"TXT"`))
		},
	},
	"aws_guardduty_malware_protection_plan": {
		Reasons: []string{
			`protected_resource is a required block the schema does not mark Required at the wire level in a way the generic pass fills (validate: "Block protected_resource must have a configuration value"), and its own nested s3_bucket.bucket_name is required with no schema-visible default`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			pr := body.AppendNewBlock("protected_resource", nil)
			s3 := pr.Body().AppendNewBlock("s3_bucket", nil)
			s3.Body().SetAttributeRaw("bucket_name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-malware-bucket"`, g.cohort)))
		},
	},
	"aws_guardduty_member": {
		Reasons: []string{
			`detector_id collides with aws_guardduty_organization_configuration's own identity argument name (see this file's batch header comment), so the generic pass wires it to that unrelated resource's own detector_id echo instead of the real aws_guardduty_detector.app.id; account_id is a plain string in the schema but the provider validates it is exactly 12 digits (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
			body.SetAttributeRaw("account_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_guardduty_organization_admin_account": {
		Reasons: []string{
			`admin_account_id is a plain string in the schema but the provider validates it is exactly 12 digits (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("admin_account_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_guardduty_publishing_destination": {
		Reasons: []string{
			`detector_id collides with aws_guardduty_organization_configuration's own identity argument name (see this file's batch header comment), so the generic pass wires it to that unrelated resource's own detector_id echo instead of the real aws_guardduty_detector.app.id; destination_arn and kms_key_arn are plain strings in the schema but the provider validates both are well-formed ARNs (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if det, ok := g.byType["aws_guardduty_detector"]; ok {
				body.SetAttributeRaw("detector_id", exprTokens(fmt.Sprintf("%s.id", det)))
			}
			body.SetAttributeRaw("destination_arn", exprTokens(`"arn:aws:s3:::tofu-security-cohort-guardduty-findings-bucket"`))
			body.SetAttributeRaw("kms_key_arn", exprTokens(`"arn:aws:kms:us-east-1:000000000000:key/1234abcd-12ab-34cd-56ef-1234567890ab"`))
		},
	},
	"aws_inspector2_filter": {
		Reasons: []string{
			`filter_criteria is a required block the schema does not mark Required at the wire level in a way the generic pass fills (validate: "Block filter_criteria must have a configuration value"); action is a plain string in the schema but the provider validates it against a fixed enum (validate: "Invalid String Enum Value", valid values [NONE SUPPRESS])`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("action", exprTokens(`"NONE"`))
			body.AppendNewBlock("filter_criteria", nil)
		},
	},
	"aws_inspector2_member_association": {
		Reasons: []string{
			`account_id is a plain string in the schema but the provider validates it is exactly 12 digits (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("account_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_kms_replica_key": {
		Reasons: []string{
			`primary_key_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); no aws_kms_key/aws_kms_external_key coverage row exists in this cohort to reference (KMS's own marker types are covered by the pre-registry v0 table and the KMS-remainder rows above), so this is a realistic literal rather than a cross-reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("primary_key_arn", exprTokens(`"arn:aws:kms:us-west-2:000000000000:key/1234abcd-12ab-34cd-56ef-1234567890ab"`))
		},
	},
	"aws_macie2_classification_job": {
		Reasons: []string{
			`job_type is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected job_type to be one of [ONE_TIME SCHEDULED]"); s3_job_definition.bucket_definitions.account_id/.buckets are required with no schema-visible default once the parent block is present`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("job_type", exprTokens(`"ONE_TIME"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "s3_job_definition" {
					bd := blk.Body().AppendNewBlock("bucket_definitions", nil)
					bd.Body().SetAttributeRaw("account_id", exprTokens(`"000000000000"`))
					bd.Body().SetAttributeRaw("buckets", exprTokens(fmt.Sprintf(`["tofu-%s-cohort-macie-bucket"]`, g.cohort)))
				}
			}
		},
	},
	"aws_macie2_findings_filter": {
		Reasons: []string{
			`action is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected action to be one of [ARCHIVE NOOP]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("action", exprTokens(`"ARCHIVE"`))
		},
	},
	"aws_secretsmanager_secret_policy": {
		Reasons: []string{
			`secret_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), which the generic pass's placeholder name is not - a real cross-reference to aws_secretsmanager_secret.app's own arn is both the fix and the point of this coverage row (the parent-derived composite this batch ratified); policy is a plain string in the schema but the provider validates it is well-formed JSON (validate: "contains an invalid JSON")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if secret, ok := g.byType["aws_secretsmanager_secret"]; ok {
				body.SetAttributeRaw("secret_arn", exprTokens(fmt.Sprintf("%s.arn", secret)))
			}
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::000000000000:root" }
      Action    = "secretsmanager:GetSecretValue"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_secretsmanager_secret_rotation": {
		Reasons: []string{
			`secret_id is not wired to any resource by the generic pass (aws_secretsmanager_secret is a marker type with no Components, so parentRef's identity-argument match never fires); rotation_rules is present but empty, and the provider requires one of automatically_after_days/schedule_expression set (validate: "one of ... must be specified")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if secret, ok := g.byType["aws_secretsmanager_secret"]; ok {
				body.SetAttributeRaw("secret_id", exprTokens(fmt.Sprintf("%s.id", secret)))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "rotation_rules" {
					blk.Body().SetAttributeRaw("automatically_after_days", exprTokens(`30`))
				}
			}
		},
	},
	"aws_securityhub_automation_rule": {
		Reasons: []string{
			`actions and criteria are both required blocks the schema does not mark Required at the wire level in a way the generic pass fills (validate: "Block actions/criteria must have a configuration value"); every field inside both is itself optional, so empty blocks are enough`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.AppendNewBlock("actions", nil)
			body.AppendNewBlock("criteria", nil)
		},
	},
	"aws_securityhub_automation_rule_v2": {
		Reasons: []string{
			`action and criteria are both required blocks the schema does not mark Required at the wire level in a way the generic pass fills (validate: "Block criteria/action must have a configuration value"); action.type and criteria.ocsf_finding_criteria_json are each required once their parent block is present; rule_order is a plain number in the schema but the provider validates it against a 1-1000 range (validate: "value must be between 1.000000 and 1000.000000", the generic pass's zero placeholder is out of range)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("rule_order", exprTokens(`1`))
			action := body.AppendNewBlock("action", nil)
			action.Body().SetAttributeRaw("type", exprTokens(`"FINDING_FIELDS_UPDATE"`))
			criteria := body.AppendNewBlock("criteria", nil)
			criteria.Body().SetAttributeRaw("ocsf_finding_criteria_json", exprTokens(`"{}"`))
		},
	},
	"aws_securityhub_configuration_policy_association": {
		Reasons: []string{
			`target_id is a plain string in the schema but the provider validates it is a root, OU or account id (validate: "Target ID must be a valid root, organizational unit or account id"); policy_id is a plain string in the schema but the provider validates it is either a UUID or the literal "SELF_MANAGED_SECURITY_HUB" (validate: "expected \"policy_id\" to be a valid UUID" / "expected policy_id to be one of [SELF_MANAGED_SECURITY_HUB]"), and this batch does not ratify aws_securityhub_configuration_policy (see the cohort README), so there is no real policy id to reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("target_id", exprTokens(`"000000000000"`))
			body.SetAttributeRaw("policy_id", exprTokens(`"SELF_MANAGED_SECURITY_HUB"`))
		},
	},
	"aws_securityhub_connector_v2": {
		Reasons: []string{
			`connector_provider is a required block the schema does not mark Required at the wire level in a way the generic pass fills (validate: "Block connector_provider must have a configuration value"); its own service_now.instance_name/.secret_arn are required once the block is present - secret_arn references aws_secretsmanager_secret.app's own arn, a real in-cohort value rather than a second placeholder ARN`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			cp := body.AppendNewBlock("connector_provider", nil)
			sn := cp.Body().AppendNewBlock("service_now", nil)
			sn.Body().SetAttributeRaw("instance_name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort"`, g.cohort)))
			if secret, ok := g.byType["aws_secretsmanager_secret"]; ok {
				sn.Body().SetAttributeRaw("secret_arn", exprTokens(fmt.Sprintf("%s.arn", secret)))
			}
		},
	},
	"aws_securityhub_member": {
		Reasons: []string{
			`account_id is a plain string in the schema but the provider validates it is exactly 12 digits (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("account_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_securityhub_organization_admin_account": {
		Reasons: []string{
			`admin_account_id is a plain string in the schema but the provider validates it is exactly 12 digits (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("admin_account_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_securityhub_standards_control": {
		Reasons: []string{
			`standards_control_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); control_status is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected control_status to be one of [ENABLED DISABLED]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("standards_control_arn", exprTokens(`"arn:aws:securityhub:us-east-1:000000000000:control/cis-aws-foundations-benchmark/v/1.2.0/1.10"`))
			body.SetAttributeRaw("control_status", exprTokens(`"ENABLED"`))
		},
	},
	"aws_securityhub_standards_control_association": {
		Reasons: []string{
			`association_status is a plain string in the schema but the provider validates it against a fixed enum (validate: "Invalid String Enum Value", valid values [ENABLED DISABLED]); standards_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "Invalid ARN Value")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("association_status", exprTokens(`"ENABLED"`))
			body.SetAttributeRaw("standards_arn", exprTokens(`"arn:aws:securityhub:us-east-1:000000000000:control/cis-aws-foundations-benchmark/v/1.2.0"`))
		},
	},
	"aws_ssm_patch_group": {
		Reasons: []string{
			`baseline_id is not wired to any resource by the generic pass (aws_ssm_patch_baseline is a marker type with no Components, so parentRef's identity-argument match never fires) - a real cross-reference to aws_ssm_patch_baseline.app's own id is both the fix and the point of this coverage row (the parent-derived composite this batch ratified)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if baseline, ok := g.byType["aws_ssm_patch_baseline"]; ok {
				body.SetAttributeRaw("baseline_id", exprTokens(fmt.Sprintf("%s.id", baseline)))
			}
		},
	},
	"aws_ssm_resource_data_sync": {
		Reasons: []string{
			`s3_destination.region is a plain string in the schema but the provider validates it looks like an AWS region (validate: "doesn't look like AWS Region")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "s3_destination" {
					blk.Body().SetAttributeRaw("region", exprTokens(`"us-east-1"`))
				}
			}
		},
	},
	"aws_ssm_service_setting": {
		Reasons: []string{
			`setting_id is a plain string in the schema but the provider validates it against two rules at once: it must begin with "/ssm/" and (per a separate check) parse as an ARN once the AWS provider prefixes it - a bare "/ssm/..." path is what the resource's own documented example uses`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("setting_id", exprTokens(`"/ssm/parameter-store/high-throughput-enabled"`))
		},
	},
	"aws_wafv2_ip_set": {
		Reasons: []string{
			`ip_address_version and scope are plain strings in the schema but the provider validates both against fixed enums (validate: "expected ip_address_version to be one of [IPV4 IPV6]" / "expected scope to be one of [CLOUDFRONT REGIONAL]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("ip_address_version", exprTokens(`"IPV4"`))
			body.SetAttributeRaw("scope", exprTokens(`"REGIONAL"`))
		},
	},
	"aws_wafv2_regex_pattern_set": {
		Reasons: []string{
			`scope is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected scope to be one of [CLOUDFRONT REGIONAL]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("scope", exprTokens(`"REGIONAL"`))
		},
	},
	"aws_wafv2_rule_group": {
		Reasons: []string{
			`scope is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected scope to be one of [CLOUDFRONT REGIONAL]"); capacity is optional/computed in the schema, rendered as the generic pass's numeric zero placeholder, but the provider validates it is at least 1 (validate: "expected capacity to be at least (1), got 0")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("scope", exprTokens(`"REGIONAL"`))
			body.SetAttributeRaw("capacity", exprTokens(`100`))
		},
	},
	"aws_wafv2_web_acl": {
		Reasons: []string{
			`scope is a plain string in the schema but the provider validates it against a fixed enum (validate: "expected scope to be one of [CLOUDFRONT REGIONAL]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("scope", exprTokens(`"REGIONAL"`))
		},
	},
	"aws_wafv2_web_acl_rule": {
		Reasons: []string{
			`web_acl_arn is a plain string in the schema but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), which the generic pass's placeholder name is not - a real cross-reference to aws_wafv2_web_acl.app's own arn is both the fix and the point of this coverage row (the parent-derived composite this batch ratified)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if acl, ok := g.byType["aws_wafv2_web_acl"]; ok {
				body.SetAttributeRaw("web_acl_arn", exprTokens(fmt.Sprintf("%s.arn", acl)))
			}
		},
	},
}

func init() { registerCohortOverrides(typeOverridesSecurity) }
