// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesDevtools is the devtools cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesDevtools = map[string]typeOverride{
	// Developer tools batch (issue #65). Every argument below is
	// Required-but-Optional-shaped in the wire schema, validated against a
	// closed enum or a real-value format check the schema alone does not
	// carry - the same failure shape issue #56 already named for the
	// batches above.
	"aws_codeartifact_repository_permissions_policy": {
		Reasons: []string{
			`"policy_document" is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "\"policy_document\" contains an invalid JSON"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy_document", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "codeartifact:ReadFromRepository"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_codebuild_fleet": {
		Reasons: []string{
			`base_capacity is Required and the provider validates it is at least 1 (validate: "expected base_capacity to be at least (1), got 0"), but the schema types it only as a number, so the generic pass's zero placeholder fails; compute_type and environment_type are both Required strings the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("base_capacity", exprTokens(`1`))
			body.SetAttributeRaw("compute_type", exprTokens(`"BUILD_GENERAL1_SMALL"`))
			body.SetAttributeRaw("environment_type", exprTokens(`"LINUX_CONTAINER"`))
		},
	},
	"aws_codebuild_project": {
		Reasons: []string{
			`"service_role" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), the same isRoleArg gap aws_codedeploy_deployment_group's own service_role_arn does not have (this argument name lacks the "_role_arn" suffix the alias matches); artifacts.type, environment.compute_type, environment.type and source.type are all required strings the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]"); source.buildspec is Optional in the schema, but the provider requires it when source.type is NO_SOURCE (apply-time only: "buildspec must be set when source's type is NO_SOURCE", not caught by "terraform validate")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if roleRef, ok := g.iamRoleRefExpr(); ok {
				body.SetAttributeRaw("service_role", exprTokens(roleRef))
			}
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "artifacts":
					blk.Body().SetAttributeRaw("type", exprTokens(`"NO_ARTIFACTS"`))
				case "environment":
					blk.Body().SetAttributeRaw("compute_type", exprTokens(`"BUILD_GENERAL1_SMALL"`))
					blk.Body().SetAttributeRaw("type", exprTokens(`"LINUX_CONTAINER"`))
				case "source":
					blk.Body().SetAttributeRaw("type", exprTokens(`"NO_SOURCE"`))
					blk.Body().SetAttributeRaw("buildspec", exprTokens(`"version: 0.2"`))
				}
			}
		},
	},

	"aws_codepipeline": {
		Reasons: []string{
			`artifact_store.type is a required string the provider validates against a one-member enum (validate: "expected type to be one of [\"S3\"]"); "role_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"), the same isRoleArg gap as aws_codebuild_project's service_role above ("role_arn" alone does not end "_role_arn"); each stage's action.category, action.owner and action.version are required strings the schema does not constrain to an enum or a length range, but the provider validates each (validate: "expected category to be one of [...]", "expected owner to be one of [...]", "expected length of ... version to be in the range (1 - 9)")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if roleRef, ok := g.iamRoleRefExpr(); ok {
				body.SetAttributeRaw("role_arn", exprTokens(roleRef))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "artifact_store" {
					blk.Body().SetAttributeRaw("type", exprTokens(`"S3"`))
				}
			}
			stageActions := []struct{ category, owner, provider string }{
				{"Source", "AWS", "S3"},
				{"Approval", "AWS", "Manual"},
			}
			i := 0
			for _, blk := range body.Blocks() {
				if blk.Type() != "stage" {
					continue
				}
				for _, action := range blk.Body().Blocks() {
					if action.Type() != "action" || i >= len(stageActions) {
						continue
					}
					sa := stageActions[i]
					action.Body().SetAttributeRaw("category", exprTokens(fmt.Sprintf("%q", sa.category)))
					action.Body().SetAttributeRaw("owner", exprTokens(fmt.Sprintf("%q", sa.owner)))
					action.Body().SetAttributeRaw("provider", exprTokens(fmt.Sprintf("%q", sa.provider)))
					action.Body().SetAttributeRaw("version", exprTokens(`"1"`))
					i++
				}
			}
		},
	},
	"aws_codeconnections_connection": {
		Reasons: []string{
			`"name" is a required string; the generic pass's cross-resource-reference heuristic points it at this cohort's own aws_codebuild_fleet.app.name (both arguments happen to be named "name"), but the provider validates connection names to at most 32 characters (apply: "Attribute name string length must be between 1 and 32") and the fleet's own identity-argument placeholder is longer than that - not caught by "terraform validate" itself (the referenced value is already known at plan time, but the provider's ValidateFunc only runs at apply), only surfaced at apply`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-connection"`, g.cohort)))
		},
	},
	"aws_codestarconnections_connection": {
		Reasons: []string{
			`Same cross-reference-length gap as aws_codeconnections_connection above, its CodeStarConnections predecessor - the provider validates this type's name to the same 32-character limit.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-starconn"`, g.cohort)))
		},
	},
	"aws_codebuild_report_group": {
		Reasons: []string{
			`type is a required string the provider validates against a fixed enum (validate: "expected type to be one of [\"TEST\" \"CODE_COVERAGE\"]"); export_config.type is likewise a required, enum-validated string (validate: "expected type to be one of [\"S3\" \"NO_EXPORT\"]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"TEST"`))
			for _, blk := range body.Blocks() {
				if blk.Type() == "export_config" {
					blk.Body().SetAttributeRaw("type", exprTokens(`"NO_EXPORT"`))
				}
			}
		},
	},
	"aws_codepipeline_custom_action_type": {
		Reasons: []string{
			`category is a required string the provider validates against a fixed enum (validate: "expected category to be one of [...]"); provider_name is this type's own identity argument (internal/live/identity/table.go), but the generic pass's tofu-<cohort>-<type> placeholder exceeds the provider's documented 35-character limit (validate: "expected length of provider_name to be in the range (1 - 35)"); version is a required string the provider validates is 1-9 characters (validate: "expected length of version to be in the range (1 - 9)")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("category", exprTokens(`"Build"`))
			body.SetAttributeRaw("provider_name", exprTokens(fmt.Sprintf(`"tofu-%s-action"`, g.cohort)))
			body.SetAttributeRaw("version", exprTokens(`"1"`))
		},
	},
	"aws_codedeploy_deployment_group": {
		Reasons: []string{
			`app_name is a required string, but this type's identity is a two-attribute composite (internal/live/identity/table.go's Components: attr("app_name"), sep(":"), attr("deployment_group_name")), so identityArgName's own-identity convention does not fire (it only names a single-component identity's argument) and the generic pass left app_name as its own tofu-<cohort>-<type> placeholder instead of the sibling aws_codedeploy_app.app's real name - not a validate error (both are plain strings), but a plan-then-apply-time one (apply: "ApplicationDoesNotExistException: Application does not exist: ..."), so this override wires the cross-resource reference issue #56 asks for by hand.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if app, ok := g.byType["aws_codedeploy_app"]; ok {
				body.SetAttributeRaw("app_name", exprTokens(fmt.Sprintf("%s.name", app)))
			}
		},
	},
	"aws_codepipeline_webhook": {
		Reasons: []string{
			`authentication is a required string the provider validates against a fixed enum (validate: "expected authentication to be one of [...]"); UNAUTHENTICATED needs no matching auth_configuration block, unlike GITHUB_HMAC (a secret_token) or IP (an allowed_ip_range), keeping this override to the one attribute. "name" is also overridden away from the generic pass's cross-reference to aws_codebuild_fleet.app.name: CodeBuild::Fleet has no working create handler at all against the pinned floci image (see this cohort's README, "Verifying by hand"), so that reference made every plan against this fixture depend on a resource guaranteed to fail create, hiding this type's own result behind an unrelated one.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("authentication", exprTokens(`"UNAUTHENTICATED"`))
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-webhook"`, g.cohort)))
		},
	},
	"aws_codestarnotifications_notification_rule": {
		Reasons: []string{
			`detail_type is a required string the provider validates against a fixed enum (validate: "expected detail_type to be one of [\"BASIC\" \"FULL\"]"); "resource" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN") - overridden to this cohort's own aws_codebuild_project.app.arn, the cross-resource reference issue #56 asks for, since this argument names an arbitrary already-admitted resource to watch rather than matching identityArgName's own-identity convention`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("detail_type", exprTokens(`"BASIC"`))
			if project, ok := g.byType["aws_codebuild_project"]; ok {
				body.SetAttributeRaw("resource", exprTokens(fmt.Sprintf("%s.arn", project)))
			} else {
				body.SetAttributeRaw("resource", exprTokens(fmt.Sprintf(
					`"arn:aws:codebuild:us-east-1:000000000000:project/tofu-%s-cohort"`, g.cohort)))
			}
		},
	},
	"aws_ecrpublic_repository_policy": {
		Reasons: []string{
			`"policy" is a required string the schema does not constrain, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "ecr-public:DescribeRepositories"
    }]
  })`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesDevtools) }
