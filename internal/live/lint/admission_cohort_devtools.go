// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesDevtools is the devtools cohort's slice of [admittedTypesV0]:
// the types the devtools ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesDevtools = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): seventh batch, developer tools
	// ---- (CodeArtifact, CodeBuild, CodeCommit, CodeConnections and its
	// ---- CodeStarConnections predecessor, CodeStarNotifications,
	// ---- CodeDeploy, CodePipeline, and the ECR-public leftover from the
	// ---- IAM/ECR batch's own ECR section). Same tools/row-gen pipeline as
	// ---- the batches above, cross-checked against the AWS provider's
	// ---- documented Argument/Attribute/Import sections fetched from the
	// ---- pinned v6.58.0 tag directly — several of these types' row-gen
	// ---- classification does not survive that check, including three
	// ---- CodeBuild types and CodeCommit whose CFN Registry ships every
	// ---- handler false, a corroboration gap row-gen's own schema-only
	// ---- evidence cannot see. See internal/live/identity/table.go for the
	// ---- per-type evidence, the one rejection, and the CodeGuru pair this
	// ---- batch left outside issue #65's named scope. Cohort estate:
	// ---- live/e2e/estates/devtools.
	"aws_codeartifact_domain":                        {},
	"aws_codeartifact_domain_permissions_policy":     {},
	"aws_codeartifact_repository":                    {},
	"aws_codeartifact_repository_permissions_policy": {},
	"aws_codebuild_fleet":                            {},
	"aws_codebuild_project":                          {},
	"aws_codebuild_report_group":                     {},
	"aws_codebuild_webhook":                          {},
	"aws_codecommit_repository":                      {},
	"aws_codeconnections_connection":                 {},
	"aws_codestarconnections_connection":             {},
	"aws_codestarnotifications_notification_rule":    {},
	"aws_codedeploy_app":                             {},
	"aws_codedeploy_deployment_config":               {},
	"aws_codedeploy_deployment_group":                {},
	"aws_codepipeline":                               {},
	"aws_codepipeline_custom_action_type":            {},
	"aws_codepipeline_webhook":                       {},
	"aws_ecrpublic_repository":                       {},
	"aws_ecrpublic_repository_policy":                {},
}

func init() { registerCohortAdmitted(admittedTypesDevtools) }
