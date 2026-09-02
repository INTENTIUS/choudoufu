// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The devtools cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableDevtools = []string{
	// Registry-ratified developer tools batch (#40, #44, issue #65):
	// CodeArtifact, CodeBuild, CodeCommit, CodeConnections/
	// CodeStarConnections, CodeStarNotifications, CodeDeploy,
	// CodePipeline and the ECR-public leftover types with a top-level
	// tags argument in the pinned provider's own wire schema. See
	// live/e2e/estates/devtools/README.md.
	"aws_codeartifact_domain",
	"aws_codeartifact_repository",
	"aws_codebuild_fleet",
	"aws_codebuild_project",
	"aws_codebuild_report_group",
	"aws_codecommit_repository",
	"aws_codeconnections_connection",
	"aws_codestarconnections_connection",
	"aws_codestarnotifications_notification_rule",
	"aws_codedeploy_app",
	"aws_codedeploy_deployment_group",
	"aws_codepipeline",
	"aws_codepipeline_custom_action_type",
	"aws_codepipeline_webhook",
	"aws_ecrpublic_repository",
}

var untaggableDevtools = []string{
	// Registry-ratified developer tools batch (#40, #44, issue #65):
	// four types with no tags argument at all, confirmed against the
	// provider's documented Argument Reference for each —
	// aws_codebuild_webhook and aws_codedeploy_deployment_config are
	// explicit in the provider's own docs ("This resource does not
	// support tags"); the three permission-policy folds
	// (aws_codeartifact_domain_permissions_policy,
	// aws_codeartifact_repository_permissions_policy,
	// aws_ecrpublic_repository_policy) carry only a policy document and
	// a parent reference, the same shape as aws_sns_topic_policy and
	// aws_sqs_queue_policy above. See
	// live/e2e/estates/devtools/README.md, "Untaggable types".
	"aws_codeartifact_domain_permissions_policy",
	"aws_codeartifact_repository_permissions_policy",
	"aws_codebuild_webhook",
	"aws_codedeploy_deployment_config",
	"aws_ecrpublic_repository_policy",
}

func init() {
	registerCohortStamp(taggableDevtools, untaggableDevtools, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified developer tools batch (#40, #44, issue #65).
			// Taggable/untaggable per the real provider's documented Argument
			// Reference for each type: aws_codebuild_webhook and
			// aws_codedeploy_deployment_config carry no tags argument at all
			// (the provider's own docs say so explicitly), and the three
			// permission-policy folds
			// (aws_codeartifact_domain_permissions_policy,
			// aws_codeartifact_repository_permissions_policy,
			// aws_ecrpublic_repository_policy) carry only a policy document and
			// a parent reference, the same shape as aws_sns_topic_policy above.
			"aws_codeartifact_domain":                        taggedSchema("id", "arn", "domain"),
			"aws_codeartifact_domain_permissions_policy":     untaggedSchema("id", "domain", "policy_document"),
			"aws_codeartifact_repository":                    taggedSchema("id", "arn", "domain", "repository"),
			"aws_codeartifact_repository_permissions_policy": untaggedSchema("id", "domain", "repository", "policy_document"),
			"aws_codebuild_fleet":                            taggedSchema("id", "arn", "name"),
			"aws_codebuild_project":                          taggedSchema("id", "arn", "name"),
			"aws_codebuild_report_group":                     taggedSchema("id", "arn", "name"),
			"aws_codebuild_webhook":                          untaggedSchema("id", "project_name"),
			"aws_codecommit_repository":                      taggedSchema("id", "arn", "repository_id", "repository_name"),
			"aws_codeconnections_connection":                 taggedSchema("id", "arn", "name"),
			"aws_codestarconnections_connection":             taggedSchema("id", "arn", "name"),
			"aws_codestarnotifications_notification_rule":    taggedSchema("id", "arn", "name", "resource"),
			"aws_codedeploy_app":                             taggedSchema("id", "name"),
			"aws_codedeploy_deployment_config":               untaggedSchema("id", "deployment_config_name"),
			"aws_codedeploy_deployment_group":                taggedSchema("id", "app_name", "deployment_group_name"),
			"aws_codepipeline":                               taggedSchema("id", "arn", "name"),
			"aws_codepipeline_custom_action_type":            taggedSchema("id", "category", "provider_name", "version"),
			"aws_codepipeline_webhook":                       taggedSchema("id", "arn", "name"),
			"aws_ecrpublic_repository":                       taggedSchema("id", "arn", "repository_name"),
			"aws_ecrpublic_repository_policy":                untaggedSchema("registry_id", "repository_name", "policy"),
		})
	})
}
