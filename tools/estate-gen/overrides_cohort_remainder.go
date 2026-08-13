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
	"aws_arcregionswitch_plan": {
		Reasons: []string{
			`execution_role is Required and validated as a well-formed ARN (validate: "value must be a valid ARN"); the generic placeholder string is not one`,
			`recovery_approach is Required and validated against a closed enum (validate: "does not match any valid values", valid: activeActive, activePassive); the generic placeholder string satisfies neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("execution_role", exprTokens(ref))
			body.SetAttributeRaw("recovery_approach", exprTokens(`"activeActive"`))
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
	"aws_datazone_domain": {
		Reasons: []string{
			`domain_execution_role is Required and validated as a well-formed ARN (validate: "cannot be parsed as an ARN"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("domain_execution_role", exprTokens(ref))
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
