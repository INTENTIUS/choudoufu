// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesGovernance is the governance cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesGovernance = map[string]typeOverride{
	"aws_config_conformance_pack": {
		Reasons: []string{
			`schema requires neither template_body nor template_s3_uri, but the provider requires exactly one of them in practice (validate: "one of template_body,template_s3_uri must be specified"); the generic pass sets neither. template_body is set to a minimal, syntactically valid Config conformance pack template wrapping one managed rule.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("template_body", exprTokens(`<<-TEMPLATE
  Resources:
    ConformancePackVersioning:
      Type: AWS::Config::ConfigRule
      Properties:
        Source:
          Owner: AWS
          SourceIdentifier: S3_BUCKET_VERSIONING_ENABLED
  TEMPLATE
`))
		},
	},
	"aws_config_remediation_configuration": {
		Reasons: []string{
			`target_type is Required and the provider validates it against a closed enum with exactly one member (validate: "expected target_type to be one of [\"SSM_DOCUMENT\"]"); the generic placeholder string is not it.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("target_type", exprTokens(`"SSM_DOCUMENT"`))
			body.SetAttributeRaw("target_id", exprTokens(`"AWS-PublishSNSNotification"`))
		},
	},
	"aws_controltower_control": {
		Reasons: []string{
			`control_identifier and target_identifier are both Required and validated as well-formed ARNs (validate: "is an invalid ARN: arn: invalid prefix"); the generic placeholder string is neither. Set to the shapes the provider's own documented Import example uses: target_identifier an organizational unit ARN, control_identifier an AWS-defined control ARN - neither references a real sibling resource, the same "no real sibling to reference" shape several other overrides in this file accept.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("target_identifier", exprTokens(
				`"arn:aws:organizations::123456789012:ou/o-exampleorgid/ou-exampleroot-exampleouid1"`))
			body.SetAttributeRaw("control_identifier", exprTokens(
				`"arn:aws:controltower:us-east-1::control/AWS-GR_S3_BUCKET_VERSIONING_ENABLED"`))
		},
	},
	"aws_controltower_landing_zone": {
		Reasons: []string{
			`schema requires manifest_json as a plain string, but the provider validates it is well-formed JSON (validate: "\"manifest_json\" contains an invalid JSON"); the generic string placeholder is not JSON.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("manifest_json", exprTokens(`jsonencode({
    governedRegions = ["us-east-1"]
    organizationStructure = {
      security = { name = "Security" }
      sandbox  = { name = "Sandbox" }
    }
    centralizedLogging = {
      accountId = "123456789012"
      configurations = {
        loggingBucket   = { retentionDays = 365 }
        accessLoggingBucket = { retentionDays = 3650 }
      }
    }
  })`))
		},
	},
	"aws_organizations_resource_policy": {
		Reasons: []string{
			`schema requires content as a plain string, but the provider validates it is well-formed JSON (validate: "\"content\" contains an invalid JSON"); the generic string placeholder is not JSON.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("content", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "organizations:DescribeOrganization"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_servicecatalog_portfolio_share": {
		Reasons: []string{
			`type and principal_id are both Required; type is validated against a closed enum (validate: "expected type to be one of [\"ACCOUNT\" \"ORGANIZATION\" \"ORGANIZATIONAL_UNIT\" \"ORGANIZATION_MEMBER_ACCOUNT\"]") and, for the ACCOUNT case this override picks, principal_id is validated as a 12-digit account ID (validate: "must be a valid account ID, organization ARN/ID, or organizational unit ARN/ID") - the generic placeholder string satisfies neither.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"ACCOUNT"`))
			body.SetAttributeRaw("principal_id", exprTokens(`"123456789012"`))
		},
	},
	"aws_auditmanager_assessment": {
		Reasons: []string{
			`roles is Optional-shaped in nothing else - the provider requires at least one roles block in practice (validate: "Block roles must have a configuration value as the provider has marked it as required"), with role_arn and role_type both Required inside it (schema doesn't surface either as top-level Required).`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			roles := body.AppendNewBlock("roles", nil)
			roles.Body().SetAttributeRaw("role_arn", exprTokens(ref))
			roles.Body().SetAttributeRaw("role_type", exprTokens(`"PROCESS_OWNER"`))
		},
	},
	"aws_organizations_account": {
		Reasons: []string{
			`email is Required and the provider validates it is a well-formed email address (validate: "invalid value for email (must be a valid email address)"); the generic placeholder string is not one.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("email", exprTokens(fmt.Sprintf(`"tofu-%s-cohort@example.com"`, g.cohort)))
		},
	},
	"aws_organizations_organizational_unit": {
		Reasons: []string{
			`parent_id is Required and the provider validates it is a well-formed root or OU identifier (validate: "invalid value for parent_id"); the generic placeholder string is neither. Set to a syntactically valid organization root ID - no real root or parent OU is part of this cohort to reference.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("parent_id", exprTokens(`"r-a1b2"`))
		},
	},
	"aws_organizations_policy": {
		Reasons: []string{
			`schema requires content as a plain string, but the provider validates it is well-formed JSON (validate: "\"content\" contains an invalid JSON"); the generic string placeholder is not JSON.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("content", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "*"
      Resource = "*"
    }]
  })`))
		},
	},
	"aws_servicecatalog_product": {
		Reasons: []string{
			`type is Required and the provider validates it against a closed enum (validate: "expected type to be one of [\"CLOUD_FORMATION_TEMPLATE\" \"MARKETPLACE\" \"TERRAFORM_OPEN_SOURCE\" \"TERRAFORM_CLOUD\" \"EXTERNAL\"]"); the generic placeholder string is not a member. provisioning_artifact_parameters is a required block whose own template_physical_id and template_url are both Optional in the schema, but the provider requires exactly one of them in practice (validate: "one of ... must be specified"), and the generic pass sets neither.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"CLOUD_FORMATION_TEMPLATE"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "provisioning_artifact_parameters" {
					blk.Body().SetAttributeRaw("template_url", exprTokens(fmt.Sprintf(
						`"https://s3.amazonaws.com/tofu-%s-cohort/servicecatalog-product-template.json"`, g.cohort)))
				}
			}
		},
	},
	"aws_servicecatalog_provisioned_product": {
		Reasons: []string{
			`schema requires none of product_id/product_name or provisioning_artifact_id/provisioning_artifact_name; the provider requires exactly one of each pair in practice (validate: "one of ... must be specified" ×4), and the generic pass sets none of the four. product_id is wired to this cohort's own aws_servicecatalog_product; provisioning_artifact_name stays a literal, since no admitted type in this cohort represents a specific provisioning artifact.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			productIDExpr := fmt.Sprintf(`"prod-tofu-%s-cohort-placeholder"`, g.cohort)
			if product, ok := g.byType["aws_servicecatalog_product"]; ok {
				productIDExpr = product.String() + ".id"
			}
			body.SetAttributeRaw("product_id", exprTokens(productIDExpr))
			body.SetAttributeRaw("provisioning_artifact_name", exprTokens(`"v1"`))
		},
	},
	"aws_servicecatalogappregistry_attribute_group": {
		Reasons: []string{
			`schema requires attributes as a plain string, but the provider validates it is well-formed JSON (validate: "Invalid JSON String Value"); the generic string placeholder is not JSON.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("attributes", exprTokens(`jsonencode({
    environment = "governance"
  })`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesGovernance) }
