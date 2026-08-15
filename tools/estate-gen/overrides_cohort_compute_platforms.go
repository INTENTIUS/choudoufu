// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesComputePlatforms is the compute-platforms cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesComputePlatforms = map[string]typeOverride{
	// ---- compute-platforms cohort (fifth ratification batch) -----------
	"aws_apprunner_service": {
		Reasons: []string{
			`source_configuration is a required block with no required attributes the schema itself names inside it, but the provider requires exactly one of source_configuration.code_repository or source_configuration.image_repository set (validate: "one of ... must be specified"); the generic pass emits the outer block empty`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() != "source_configuration" {
					continue
				}
				img := blk.Body().AppendNewBlock("image_repository", nil)
				img.Body().SetAttributeRaw("image_identifier", exprTokens(`"public.ecr.aws/aws-containers/hello-app-runner:latest"`))
				img.Body().SetAttributeRaw("image_repository_type", exprTokens(`"ECR_PUBLIC"`))
			}
		},
	},
	"aws_apprunner_vpc_connector": {
		Reasons: []string{
			`vpc_connector_name is a required string the schema does not length-constrain, but the provider validates it is at most 40 characters (validate: "expected length of vpc_connector_name to be in the range (4 - 40)"); the generic "tofu-<cohort>-cohort-<type>" name is 44`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("vpc_connector_name", exprTokens(`"tofu-cp-apprunner-vpc-conn"`))
		},
	},
	"aws_apprunner_vpc_ingress_connection": {
		Reasons: []string{
			`service_arn is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("service_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:apprunner:us-east-1:000000000000:service/tofu-%s-cohort-apprunner-service/00000000000000000000000000000000"`, g.cohort)))
		},
	},
	"aws_batch_compute_environment": {
		Reasons: []string{
			`type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [MANAGED UNMANAGED]"); UNMANAGED needs no further compute_resources block, the smaller of the two shapes`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"UNMANAGED"`))
		},
	},
	"aws_batch_job_definition": {
		Reasons: []string{
			`type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [container multinode]"); a container job definition also needs container_properties, a JSON string the schema does not otherwise require, with at least image and a resource sizing (validate: "invalid JSON")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"container"`))
			body.SetAttributeRaw("container_properties", exprTokens(`jsonencode({
    image  = "busybox"
    vcpus  = 1
    memory = 512
  })`))
		},
	},
	"aws_batch_job_queue": {
		Reasons: []string{
			`state is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "Attribute state value must be one of: [\"ENABLED\" \"DISABLED\"]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("state", exprTokens(`"ENABLED"`))
		},
	},
	"aws_emr_security_configuration": {
		Reasons: []string{
			`configuration is a required string the schema does not otherwise constrain, but the provider validates it is well-formed JSON (validate: "contains an invalid JSON"); the generic string placeholder is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("configuration", exprTokens(`jsonencode({
    EncryptionConfiguration = {
      EnableInTransitEncryption = false
      EnableAtRestEncryption    = false
    }
  })`))
		},
	},
	"aws_emr_studio": {
		Reasons: []string{
			`auth_mode is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected auth_mode to be one of [SSO IAM]"); service_role is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("auth_mode", exprTokens(`"SSO"`))
			body.SetAttributeRaw("service_role", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-emr-studio"`, g.cohort)))
		},
	},
	"aws_emrcontainers_virtual_cluster": {
		Reasons: []string{
			`container_provider.type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [EKS]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() != "container_provider" {
					continue
				}
				blk.Body().SetAttributeRaw("type", exprTokens(`"EKS"`))
			}
		},
	},
	"aws_lightsail_container_service": {
		Reasons: []string{
			`power is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected power to be one of [...]"); scale is a required number the schema does not range-constrain, but the provider validates it is between 1 and 20 (validate: "expected scale to be in the range (1 - 20), got 0")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("power", exprTokens(`"nano"`))
			body.SetAttributeRaw("scale", exprTokens(`1`))
		},
	},
	"aws_lightsail_database": {
		Reasons: []string{
			`master_database_name is a required string the schema does not otherwise constrain, but the provider validates it against a stricter character set than the generic "tofu-<cohort>-cohort-<type>" placeholder's hyphens allow (validate: "Subsequent characters can be letters, underscores, or digits")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("master_database_name", exprTokens(`"tofu_compute_platforms_database"`))
		},
	},
	"aws_lightsail_instance": {
		Reasons: []string{
			`availability_zone is a required string the schema does not constrain, but the provider validates it names an AZ within the configured provider region (validate/plan: "availability_zone must be within the same region as provider region: us-east-1"); the generic placeholder string does not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("availability_zone", exprTokens(`"us-east-1a"`))
		},
	},
	"aws_lightsail_distribution": {
		Reasons: []string{
			`default_cache_behavior.behavior is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected default_cache_behavior.0.behavior to be one of [dont-cache cache]"); origin.region_name is a required string the schema does not constrain, but the provider validates it looks like an AWS region (validate: "doesn't look like AWS Region")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "default_cache_behavior":
					blk.Body().SetAttributeRaw("behavior", exprTokens(`"cache"`))
				case "origin":
					blk.Body().SetAttributeRaw("region_name", exprTokens(`"us-east-1"`))
				}
			}
		},
	},
}

func init() { registerCohortOverrides(typeOverridesComputePlatforms) }
