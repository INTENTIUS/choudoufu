// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesIot is the iot cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesIot = map[string]typeOverride{
	// IoT core batch (issue #65).
	"aws_iot_authorizer": {
		Reasons: []string{
			`"authorizer_function_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); this argument names a Lambda function, not a role, so isRoleArg's cross-reference does not apply (this cohort requests no aws_lambda_function) - a literal, well-formed placeholder ARN is enough to satisfy the format check alone. "signing_disabled" is Optional and defaults to false (signing enabled), and the provider then requires "token_key_name" and "token_signing_public_keys" (apply: "\"token_key_name\" is required when signing is enabled", not caught by "terraform validate" - a real API-facing requirement, not a floci gap) - overridden to true so this fixture needs neither.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("authorizer_function_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:lambda:us-east-1:000000000000:function:tofu-%s-authorizer"`, g.cohort)))
			body.SetAttributeRaw("signing_disabled", exprTokens(`true`))
		},
	},
	"aws_iot_policy": {
		Reasons: []string{
			`"policy" is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = ["iot:Publish"]
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_iot_provisioning_template": {
		Reasons: []string{
			`"name" is this type's own identity argument (internal/live/identity/table.go), but the generic pass's tofu-<cohort>-<type> placeholder ("tofu-iot-cohort-provisioning-template", 37 characters) exceeds the provider's documented 36-character limit (validate: "expected length of name to be in the range (1 - 36)"); "template_body" is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "\"template_body\" contains an invalid JSON") - the generic placeholder string is neither short enough nor JSON.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-provtmpl"`, g.cohort)))
			body.SetAttributeRaw("template_body", exprTokens(`jsonencode({
    Parameters = {
      "AWS::IoT::Certificate::Id" = { Type = "String" }
    }
    Resources = {
      certificate = {
        Type = "AWS::IoT::Certificate"
        Properties = {
          CertificateId = { Ref = "AWS::IoT::Certificate::Id" }
          Status        = "ACTIVE"
        }
      }
      thing = {
        Type = "AWS::IoT::Thing"
        Properties = {
          ThingName = { Ref = "AWS::IoT::Certificate::Id" }
        }
      }
    }
  })`))
		},
	},
	"aws_iot_role_alias": {
		Reasons: []string{
			`"role_arn" is a required string the schema does not constrain and (unlike aws_codepipeline's own "role_arn" above) the provider ships no ARN-format validator on this particular attribute, so the generic placeholder passes "terraform validate" unchanged - but it is still the same bare isRoleArg gap named on aws_codepipeline and aws_codebuild_project above ("role_arn" alone does not end "_role_arn"), and a non-ARN placeholder would fail at apply against a real IoT role_alias create, so this override wires the real cross-resource reference anyway rather than leaving a validate-clean but apply-broken row.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if roleRef, ok := g.iamRoleRefExpr(); ok {
				body.SetAttributeRaw("role_arn", exprTokens(roleRef))
			}
		},
	},
	"aws_iot_topic_rule": {
		Reasons: []string{
			`"name" is this type's own identity argument (internal/live/identity/table.go), but the generic pass's tofu-<cohort>-<type> placeholder uses hyphens, and the provider validates topic rule names against ^[0-9A-Za-z_]+$ (validate: "Name must match the pattern ^[0-9A-Za-z_]+$") - hyphens are not in that set, unlike every other client-named IoT type in this cohort.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_topic_rule"`, strings.ReplaceAll(g.cohort, "-", "_"))))
		},
	},
	"aws_iot_topic_rule_destination": {
		Reasons: []string{
			`"vpc_configuration.role_arn" is a required string nested inside the vpc_configuration block; the schema does not constrain it, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN") - isRoleArg's generic cross-reference only scans each type's top-level required arguments (planCohort's requiredArgNames pass), not nested block arguments, so this nested role_arn is never auto-wired the way aws_iot_provisioning_template's top-level provisioning_role_arn is.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			roleRef, ok := g.iamRoleRefExpr()
			if !ok {
				return
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "vpc_configuration" {
					blk.Body().SetAttributeRaw("role_arn", exprTokens(roleRef))
				}
			}
		},
	},
}

func init() { registerCohortOverrides(typeOverridesIot) }
