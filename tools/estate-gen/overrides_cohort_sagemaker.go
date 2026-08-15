// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesSagemaker is the sagemaker cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesSagemaker = map[string]typeOverride{
	"aws_sagemaker_app_image_config": {
		Reasons: []string{
			`every image-config block is Optional in the wire schema, but the provider requires exactly one of code_editor_app_image_config, jupyter_lab_image_config or kernel_gateway_image_config (apply: "exactly one ... block must be configured") - found by the #108 acceptance tier; validate never runs this check`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			kg := body.AppendNewBlock("kernel_gateway_image_config", nil)
			spec := kg.Body().AppendNewBlock("kernel_spec", nil)
			spec.Body().SetAttributeRaw("name", exprTokens(`"tofu-sagemaker-cohort-kernel"`))
		},
	},
	// SageMaker batch (issue #65). Every argument below is Optional in the
	// wire schema (an ExactlyOneOf/enum/format the provider validates at
	// plan time, not a schema-Required field) or a required block the
	// generic pass leaves empty because none of its own children are
	// individually schema-Required - the same two failure shapes issue #56
	// already named for the earlier cohorts above. Three entries
	// (aws_sagemaker_endpoint_configuration, aws_sagemaker_mlflow_app,
	// aws_sagemaker_notebook_instance_lifecycle_configuration) fix a
	// different thing: gen.go's parentRef matches a required argument to
	// another resource's identity by argument *name* alone (see
	// identityArgName's doc comment), and this cohort admits several types
	// that all self-identify by a plain "name" argument - endpoint,
	// notebook_instance and the shared aws_iam_role among them - so the
	// generic pass wired each of the three affected types' own "name"
	// argument to whichever same-"name" resource it happened to encounter
	// first, not to itself. Same collision class as aws_glue_catalog_database's
	// entry above and the sagemaker cohort's own README, "A collision, three
	// times over."
	"aws_sagemaker_algorithm": {
		Reasons: []string{
			`training_specification is a required block the schema leaves entirely optional-shaped (validate: "Block training_specification must have a configuration value as the provider has marked it as required"); filled with the provider docs' own minimal worked example (one training channel, one supported instance type, a training image)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ts := body.AppendNewBlock("training_specification", nil)
			ts.Body().SetAttributeRaw("supported_training_instance_types", exprTokens(`["ml.m5.large"]`))
			ts.Body().SetAttributeRaw("training_image", exprTokens(fmt.Sprintf(
				`"123456789012.dkr.ecr.us-east-1.amazonaws.com/tofu-%s-cohort-training:latest"`, g.cohort)))
			tc := ts.Body().AppendNewBlock("training_channels", nil)
			tc.Body().SetAttributeRaw("name", exprTokens(`"train"`))
			tc.Body().SetAttributeRaw("supported_content_types", exprTokens(`["text/csv"]`))
			tc.Body().SetAttributeRaw("supported_input_modes", exprTokens(`["File"]`))
		},
	},
	"aws_sagemaker_app": {
		Reasons: []string{
			`schema marks both user_profile_name and space_name Optional, but the provider requires exactly one (validate: "one of ` + "`space_name,user_profile_name`" + ` must be specified"); app_type is Required and validated against a closed enum (validate: "expected app_type to be one of [...]"), and the generic placeholder string is not a member. domain_id is also wired to this cohort's own aws_sagemaker_domain.app.id and user_profile_name to aws_sagemaker_user_profile.app.user_profile_name in place of the generic placeholder strings - not required by validate (domain_id and user_profile_name carry no format check of their own), but the same cross-resource-reference improvement aws_eip_association's entry above makes: both sibling resources exist in this same cohort, so pointing at them is more real than an orphaned placeholder.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if domain, ok := g.byType["aws_sagemaker_domain"]; ok {
				body.SetAttributeRaw("domain_id", exprTokens(fmt.Sprintf("%s.id", domain)))
			}
			if profile, ok := g.byType["aws_sagemaker_user_profile"]; ok {
				body.SetAttributeRaw("user_profile_name", exprTokens(fmt.Sprintf("%s.user_profile_name", profile)))
			}
			body.SetAttributeRaw("app_type", exprTokens(`"JupyterServer"`))
		},
	},
	"aws_sagemaker_data_quality_job_definition": {
		Reasons: []string{
			`role_arn is Required and the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), the same shape as aws_db_proxy's role_arn above - wired to the shared aws_iam_role instead. The s3_output.s3_uri leaf is Required and validated against a "^(https|s3)://..." pattern (validate: "expected value ... to match regular expression"); cluster_config.instance_count is Required and validated at least 1 (validate: "expected ... to be at least (1), got 0"); cluster_config.volume_size_in_gb is Required and validated 1-512 (validate: "expected ... to be in the range (1 - 512), got 0"); cluster_config.instance_type is Required and validated against a closed enum (validate: "expected instance_type to be one of [...]")`,
		},
		NeedsIAMRole: true,
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "data_quality_job_output_config":
					for _, mo := range blk.Body().Blocks() {
						if mo.Type() != "monitoring_outputs" {
							continue
						}
						for _, s3out := range mo.Body().Blocks() {
							if s3out.Type() == "s3_output" {
								s3out.Body().SetAttributeRaw("s3_uri", exprTokens(fmt.Sprintf(
									`"s3://tofu-%s-cohort-bucket/data-quality-output"`, g.cohort)))
							}
						}
					}
				case "job_resources":
					for _, cc := range blk.Body().Blocks() {
						if cc.Type() == "cluster_config" {
							cc.Body().SetAttributeRaw("instance_count", exprTokens(`1`))
							cc.Body().SetAttributeRaw("instance_type", exprTokens(`"ml.t3.medium"`))
							cc.Body().SetAttributeRaw("volume_size_in_gb", exprTokens(`20`))
						}
					}
				}
			}
		},
	},
	"aws_sagemaker_endpoint": {
		Reasons: []string{
			`endpoint_config_name is Required but a generic-string placeholder, not a reference - overridden to point at this cohort's own aws_sagemaker_endpoint_configuration.app.name for the same cross-resource-reference reason as aws_eip_association's entry above (validate does not require this: endpoint_config_name carries no format check of its own)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if cfg, ok := g.byType["aws_sagemaker_endpoint_configuration"]; ok {
				body.SetAttributeRaw("endpoint_config_name", exprTokens(fmt.Sprintf("%s.name", cfg)))
			}
		},
	},
	"aws_sagemaker_endpoint_configuration": {
		Reasons: []string{
			`the generic pass's same-name parent search matches this type's own "name" argument against aws_sagemaker_endpoint (an unrelated sibling type that also self-identifies by a plain "name" argument), the same collision class as aws_glue_catalog_database's entry above; overridden back to a real placeholder name of its own`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-endpoint-configuration"`, g.cohort)))
		},
	},
	"aws_sagemaker_feature_group": {
		Reasons: []string{
			`role_arn is Required (needed "if an offline_store_config is provided") and the provider validates it is a well-formed ARN once set (validate: "is an invalid ARN"); schema marks offline_store_config and online_store_config both Optional, but the provider requires exactly one (validate: "one of ` + "`offline_store_config,online_store_config`" + ` must be specified") - online_store_config is chosen, with its own required security_config child added empty (its own kms_key_id leaf is genuinely Optional)`,
		},
		NeedsIAMRole: true,
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
			osc := body.AppendNewBlock("online_store_config", nil)
			osc.Body().AppendNewBlock("security_config", nil)
		},
	},
	"aws_sagemaker_mlflow_app": {
		Reasons: []string{
			`the generic pass's same-name parent search matches this type's own "name" argument against the shared aws_iam_role (which also self-identifies by a plain "name" argument), the same collision class as aws_sagemaker_endpoint_configuration above; overridden back to a real placeholder name of its own. artifact_store_uri is Required and the provider validates it is an HTTPS or S3 URI (validate: "invalid value for artifact_store_uri (must be HTTPS or Amazon S3 URI)"); role_arn is Required and the provider validates it is a well-formed ARN (validate: "Invalid ARN Value") - wired to the shared aws_iam_role`,
		},
		NeedsIAMRole: true,
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-mlflow-app"`, g.cohort)))
			body.SetAttributeRaw("artifact_store_uri", exprTokens(fmt.Sprintf(
				`"s3://tofu-%s-cohort-bucket/mlflow-app"`, g.cohort)))
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
		},
	},
	"aws_sagemaker_mlflow_tracking_server": {
		Reasons: []string{
			`artifact_store_uri is Required and the provider validates it is an HTTPS or S3 URI (validate: "invalid value for artifact_store_uri (must be HTTPS or Amazon S3 URI)"); role_arn is Required and the provider validates it is a well-formed ARN (validate: "is an invalid ARN") - wired to the shared aws_iam_role`,
		},
		NeedsIAMRole: true,
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("artifact_store_uri", exprTokens(fmt.Sprintf(
				`"s3://tofu-%s-cohort-bucket/mlflow-tracking-server"`, g.cohort)))
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
		},
	},
	"aws_sagemaker_model_card": {
		Reasons: []string{
			`content is Required and the provider validates it is well-formed JSON (validate: "A string value was provided that is not valid JSON string format"); model_card_status is Required and validated against a closed enum (validate: "The provided value does not match any valid values", valid values Draft/PendingReview/Approved/Archived)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("content", exprTokens(`jsonencode({
    model_overview = {
      model_description = "tofu cohort fixture model card"
    }
  })`))
			body.SetAttributeRaw("model_card_status", exprTokens(`"Draft"`))
		},
	},
	"aws_sagemaker_model_package_group_policy": {
		Reasons: []string{
			`resource_policy is Required and the provider validates it is well-formed JSON (validate: "\"resource_policy\" contains an invalid JSON"), the same shape as aws_s3_bucket_policy above; the policy's Resource element references the sibling aws_sagemaker_model_package_group.app.arn this cohort also admits`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			resourceExpr := fmt.Sprintf(`"arn:aws:sagemaker:us-east-1:000000000000:model-package-group/tofu-%s-cohort-model-package-group"`, g.cohort)
			if parent, ok := g.byType["aws_sagemaker_model_package_group"]; ok {
				resourceExpr = fmt.Sprintf(`"${%s.arn}"`, parent)
			}
			body.SetAttributeRaw("resource_policy", exprTokens(fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AddPermModelPackageGroup"
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::000000000000:root" }
      Action    = ["sagemaker:DescribeModelPackage", "sagemaker:ListModelPackages"]
      Resource  = %s
    }]
  })`, resourceExpr)))
		},
	},
	"aws_sagemaker_notebook_instance": {
		Reasons: []string{
			`instance_type is Required and the provider validates it against a closed enum (validate: "expected instance_type to be one of [...]"); role_arn is Required and the provider validates it is a well-formed ARN (validate: "is an invalid ARN") - wired to the shared aws_iam_role`,
		},
		NeedsIAMRole: true,
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("instance_type", exprTokens(`"ml.t3.medium"`))
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"arn:aws:iam::000000000000:role/placeholder"`
			}
			body.SetAttributeRaw("role_arn", exprTokens(ref))
		},
	},
	"aws_sagemaker_notebook_instance_lifecycle_configuration": {
		Reasons: []string{
			`the generic pass's same-name parent search matches this type's own "name" argument against aws_sagemaker_notebook_instance (an unrelated sibling type that also self-identifies by a plain "name" argument), the same collision class as aws_sagemaker_endpoint_configuration above; overridden back to a real placeholder name of its own`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-notebook-instance-lifecycle-configuration"`, g.cohort)))
		},
	},
	"aws_sagemaker_pipeline": {
		Reasons: []string{
			`schema marks both pipeline_definition and pipeline_definition_s3_location Optional, but the provider requires exactly one (validate: "one of ` + "`pipeline_definition,pipeline_definition_s3_location`" + ` must be specified"); pipeline_definition is filled with a minimal well-formed pipeline document (a single no-op Fail step, the provider docs' own worked example)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("pipeline_definition", exprTokens(`jsonencode({
    Version = "2020-12-01"
    Steps = [{
      Name = "Placeholder"
      Type = "Fail"
      Arguments = {
        ErrorMessage = "tofu cohort fixture pipeline"
      }
    }]
  })`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesSagemaker) }
