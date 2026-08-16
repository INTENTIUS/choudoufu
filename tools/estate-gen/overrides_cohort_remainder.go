// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesRemainder is the remainder cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesRemainder = map[string]typeOverride{
	// REMAINDER ratification batch (issue #65) overrides, live/e2e/estates/remainder.
	// #175 ratification batch, 2026-08-15: three of the batch's types carry
	// plan-time validators the wire schema does not express, the documented
	// override class.
	"aws_appconfig_deployment": {
		Reasons: []string{
			`deployment_strategy_id is validated against a pattern (validate: "expected value of deployment_strategy_id to match regular expression \"(^[0-9a-z]{4,7}$|^AppConfig\\.[0-9A-Za-z]{9,40}$)\""); no strategy type is admitted to reference, so the predefined AppConfig.AllAtOnce strategy the service ships is the literal`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("deployment_strategy_id", exprTokens(`"AppConfig.AllAtOnce"`))
		},
	},
	"aws_datazone_form_type": {
		Reasons: []string{
			`domain_identifier is validated against the DataZone domain-ID pattern (validate: "Attribute domain_identifier ^dzd[-_][a-zA-Z0-9_-]{1,36}$, got: placeholder"); wired to this cohort's own aws_datazone_domain - this type is server-assigned in the identity table, so the argument is not identity-bound and the reference is plain apply-correctness`,
			`model is a required nested block the generic required-only pass does not emit (validate: "Block model must have a configuration value as the provider has marked it as required"); its one member is a Smithy model document, supplied here as the minimal structure the service accepts`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			domainExpr := `"dzd_placeholder"`
			if domain, ok := g.byType["aws_datazone_domain"]; ok {
				domainExpr = fmt.Sprintf("%s.id", domain)
			}
			body.SetAttributeRaw("domain_identifier", exprTokens(domainExpr))
			model := body.AppendNewBlock("model", nil)
			model.Body().SetAttributeRaw("smithy", exprTokens(`"structure exampleForm { }"`))
		},
	},
	"aws_paymentcryptography_key_alias": {
		Reasons: []string{
			`alias_name is validated against the service's alias/ prefix rule (validate: "An alias must begin with alias/ followed by a name"); the generic tofu-<cohort>-cohort-<type> literal lacks the prefix`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("alias_name", exprTokens(`"alias/tofu-remainder-cohort-key-alias"`))
		},
	},
	"aws_arcregionswitch_plan": {
		Reasons: []string{
			`execution_role is Required and validated as a well-formed ARN (validate: "value must be a valid ARN"); the generic placeholder string is not one`,
			`recovery_approach is Required and validated against a closed enum (validate: "does not match any valid values", valid: activeActive, activePassive); the generic placeholder string satisfies neither`,
			`regions is validated against a two-element floor the wire schema does not express (validate at 6.59.0: "Attribute regions list must contain at least 2 elements, got: 1"); the generic single-element placeholder list is one short`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("execution_role", exprTokens(ref))
			body.SetAttributeRaw("recovery_approach", exprTokens(`"activeActive"`))
			body.SetAttributeRaw("regions", exprTokens(`["us-east-1", "us-west-2"]`))
		},
	},
	"aws_cloudfront_cache_policy": {
		Reasons: []string{
			`the parameters_in_cache_key_and_forwarded_to_origin block's cookies_config.cookie_behavior and query_strings_config.query_string_behavior are each Required and validated against a closed enum (validate: "expected ... to be one of [...], got placeholder"); the generic placeholder string satisfies neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() != "parameters_in_cache_key_and_forwarded_to_origin" {
					continue
				}
				for _, inner := range blk.Body().Blocks() {
					switch inner.Type() {
					case "cookies_config":
						inner.Body().SetAttributeRaw("cookie_behavior", exprTokens(`"none"`))
					case "query_strings_config":
						inner.Body().SetAttributeRaw("query_string_behavior", exprTokens(`"none"`))
					}
				}
			}
		},
	},
	"aws_dx_connection": {
		Reasons: []string{
			`bandwidth is Required and validated against a closed enum of real Direct Connect port speeds (validate: "expected bandwidth to be one of [...], got placeholder"); the generic placeholder string satisfies none of them`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("bandwidth", exprTokens(`"1Gbps"`))
		},
	},
	"aws_gamelift_fleet": {
		Reasons: []string{
			`schema shows build_id and script_id as both Optional, but the provider requires exactly one (validate: "one of build_id,script_id must be specified"); this cohort has no admitted GameLift build/script sibling to reference, so build_id is set to a placeholder build id`,
			`ec2_instance_type is Required and validated against a closed enum of real EC2 instance types (validate: "expected ec2_instance_type to be one of [...], got placeholder"); the generic placeholder string satisfies none of them`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("build_id", exprTokens(`"build-0123456789abcdef0"`))
			body.SetAttributeRaw("ec2_instance_type", exprTokens(`"c5.large"`))
		},
	},
	"aws_imagebuilder_image_pipeline": {
		Reasons: []string{
			`schema shows container_recipe_arn and image_recipe_arn as both Optional, but the provider requires exactly one (validate: "one of container_recipe_arn,image_recipe_arn must be specified"); this cohort has no admitted Image Builder recipe sibling to reference, so image_recipe_arn is set to a placeholder recipe ARN`,
			`infrastructure_configuration_arn is Required and validated as a real Image Builder infrastructure-configuration ARN shape (validate: "valid infrastructure configuration ARN must be provided"); the generic placeholder string does not match it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("image_recipe_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:imagebuilder:us-east-1:000000000000:image-recipe/tofu-%s-cohort-recipe/1.0.0"`, g.cohort)))
			body.SetAttributeRaw("infrastructure_configuration_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:imagebuilder:us-east-1:000000000000:infrastructure-configuration/tofu-%s-cohort-infra-config"`, g.cohort)))
		},
	},
	"aws_cloud9_environment_ec2": {
		Reasons: []string{
			`image_id is Required and validated against a closed enum of real Cloud9 AMI aliases (validate: "expected image_id to be one of [...], got placeholder"); the generic placeholder string satisfies none of them`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("image_id", exprTokens(`"amazonlinux-2023-x86_64"`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesRemainder) }
