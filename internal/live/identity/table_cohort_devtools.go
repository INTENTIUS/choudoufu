// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableDevtools is the devtools cohort's slice of [DefaultTable]:
// the identity rows the devtools ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableDevtools = buildTable(
	// aws_wafv2_web_acl_association (untaggable, composite of two external
	// ARNs neither this table admits), aws_wafv2_web_acl_logging_configuration
	// (untaggable, argument name guessed rather than schema-backed) and
	// aws_wafv2_web_acl_rule_group_association (a branching, mode-dependent
	// four-part composite with no identity schema at all — "custom" vs.
	// "managed" rule groups document different grammars, and guessing which
	// one applies is not this batch's job) are not ratified — see the
	// cohort README.
	// ---- Registry-ratified (#40, #44, #65): seventh batch, developer tools
	// ---- (issue #65) ------------------------------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented Argument Reference, Attribute Reference
	// and Import section (fetched from the provider's own website/docs/r/
	// source at the pinned v6.58.0 tag), not accepted on the registry's
	// classification alone. Four of these types — CodeBuild::Project,
	// ::ReportGroup and ::SourceCredential, and CodeCommit::Repository —
	// ship with every CFN Registry handler false (create, read, update,
	// delete, list): row-gen still emits a proposal for each, built from
	// primaryIdentifier/readOnlyProperties/createOnlyProperties evidence
	// that exists in the registry schema regardless of the handlers, but
	// that evidence carries no corroborating registry capability, which is
	// exactly what row-gen's own "not listable" annotation on each is
	// flagging. The provider's documented Import section is the stronger,
	// provider-native signal for all four, and it disagrees with row-gen's
	// server-assigned proposal for three of them. Cohort estate:
	// live/e2e/estates/devtools.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_codebuild_source_credential: row-gen proposed server-assigned
	//     via the registry's opaque "Id", and the provider's own documented
	//     import command confirms an ARN
	//     (arn:aws:codebuild:REGION:ACCOUNT:token/SERVERTYPE) — but unlike
	//     aws_codebuild_report_group below, no argument reconstructs it:
	//     server_type and auth_type name the kind of credential, not an
	//     identifying name, and token holds the secret value itself, never
	//     echoed back. The provider's own Argument Reference carries no
	//     tags block for this type at all (untaggable), and
	//     live/survey-full.json's own signal agrees there is no native list
	//     resource either — no admission path recovers a pre-existing
	//     instance.
	//
	// Out of this batch's named scope: issue #65's own recipe names
	// CodeArtifact, CodeBuild, CodeCommit, CodeConnections (with its
	// CodeStarConnections predecessor), CodeStarNotifications, CodeDeploy,
	// CodePipeline and the ECR-public leftover — not CodeGuru. row-gen's
	// CodeGuruProfiler and CodeGuruReviewer sections (2 types total) are
	// left for a future batch. The earlier IAM/ECR batch's own deferred ECR
	// rows (aws_ecr_lifecycle_policy, aws_ecr_pull_through_cache_rule,
	// aws_ecr_pull_time_update_exclusion,
	// aws_ecr_repository_creation_template, aws_ecr_repository_policy) are
	// unchanged by this batch; only the ECR-public pair below is this
	// batch's to decide.

	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Arn"
		// — right that the ARN is the identity, but the flat
		// serverAssigned() template undersells it the same way it
		// undersold aws_sns_topic, aws_sqs_queue and
		// aws_cloudfront_realtime_log_config above: this is the
		// account-derived shape, not an opaque ID. The provider's own
		// Identity Schema requires exactly one field, arn, and its
		// documented import command (terraform import
		// aws_codeartifact_domain.example
		// arn:aws:codeartifact:us-west-2:012345678912:domain/tf-acc-test)
		// confirms the value is built from the required "domain" argument
		// plus the region and account of the cloud the run is against.
		// CFN Registry AWS::CodeArtifact::Domain reports handlers.list =
		// true with no required list input — the same Cloud Control
		// fallback shape as aws_efs_file_system in the storage batch
		// above, since the pinned provider itself has no native list
		// resource for this type (live/survey-full.json:
		// aws_codeartifact_domain.signals.list_resource is false).
		Type: "aws_codeartifact_domain",
		Components: []Component{
			inAttr("arn", sep("arn:aws:codeartifact:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":domain/")),
			inAttr("arn", attr("domain")),
		},
		ImportSyntax:  "arn:aws:codeartifact:REGION:ACCOUNT:domain/DOMAIN",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// A named-singleton-child of the domain, the same shape as
		// aws_api_gateway_rest_api_policy and aws_s3_bucket_policy above:
		// row-gen marked this "(property-child of AWS::CodeArtifact::Domain)
		// [evidence-only]", but the identity needs no new mechanism — its
		// own required "domain" argument reconstructs the same
		// account-derived ARN as aws_codeartifact_domain just above, and
		// the provider's documented import command uses exactly that ARN
		// (terraform import aws_codeartifact_domain_permissions_policy.example
		// arn:aws:codeartifact:us-west-2:012345678912:domain/tf-acc-test).
		// This type's own "id" is documented as the domain's bare name, not
		// its ARN, so it is not claimed as an identity source here.
		Type: "aws_codeartifact_domain_permissions_policy",
		Components: []Component{
			sep("arn:aws:codeartifact:"),
			cloud(CloudRegion),
			sep(":"),
			cloud(CloudAccountID),
			sep(":domain/"),
			attr("domain"),
		},
		ImportSyntax:  "arn:aws:codeartifact:REGION:ACCOUNT:domain/DOMAIN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Same correction as aws_codeartifact_domain above, one level
		// deeper: the provider's documented import command (terraform
		// import aws_codeartifact_repository.example
		// arn:aws:codeartifact:us-west-2:012345678912:repository/tf-acc-test/tf-acc-test)
		// confirms the ARN is built from the required "domain" and
		// "repository" arguments plus the region and account of the cloud
		// the run is against. Same Cloud Control fallback shape as the
		// domain above (live/survey-full.json:
		// aws_codeartifact_repository.signals.list_resource is false;
		// CFN Registry AWS::CodeArtifact::Repository reports handlers.list
		// = true with no required list input).
		Type: "aws_codeartifact_repository",
		Components: []Component{
			inAttr("arn", sep("arn:aws:codeartifact:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":repository/")),
			inAttr("arn", attr("domain")),
			inAttr("arn", sep("/")),
			inAttr("arn", attr("repository")),
		},
		ImportSyntax:  "arn:aws:codeartifact:REGION:ACCOUNT:repository/DOMAIN/REPOSITORY",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// A named-singleton-child of the repository, the same shape as
		// aws_codeartifact_domain_permissions_policy above: row-gen marked
		// this "(property-child of AWS::CodeArtifact::Repository)
		// [evidence-only]", but its required "domain" and "repository"
		// arguments reconstruct the same ARN as aws_codeartifact_repository
		// just above, and the provider's documented import command uses
		// exactly that ARN.
		Type: "aws_codeartifact_repository_permissions_policy",
		Components: []Component{
			sep("arn:aws:codeartifact:"),
			cloud(CloudRegion),
			sep(":"),
			cloud(CloudAccountID),
			sep(":repository/"),
			attr("domain"),
			sep("/"),
			attr("repository"),
		},
		ImportSyntax:  "arn:aws:codeartifact:REGION:ACCOUNT:repository/DOMAIN/REPOSITORY",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Arn"
		// — the registry-says-server-assigned-but-the-provider-disagrees
		// shape, the opposite correction from aws_ecs_capacity_provider's
		// rejection earlier (that one's classic import command is ARN-only;
		// this one's is not). CodeBuild::Fleet is one of the few CodeBuild
		// CFN types that actually ships full registry handlers (create,
		// read, update, delete, list all true — unlike Project,
		// ReportGroup and SourceCredential below), but the provider's own
		// documented Import section offers the ARN only as the newer
		// identity-block form; its classic import command (terraform
		// import aws_codebuild_fleet.name fleet-name) uses the bare,
		// required "name" argument verbatim.
		Type:          "aws_codebuild_fleet",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; the provider documents id as the fleet's ARN always, not name
	},
	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Id"
		// — but AWS::CodeBuild::Project ships every CFN Registry handler
		// false (create, read, update, delete, list), so row-gen's own
		// enumeration note for it ("not listable -> client-named only") is
		// flagging that the registry has nothing to say about this type
		// beyond its bare schema fields. The provider's own documented
		// Import section settles it independently: the classic import
		// command (terraform import aws_codebuild_project.name
		// project-name) uses the bare, required "name" argument, and the
		// provider's own Attribute Reference is explicit that id is "Name
		// (if imported via name) or ARN (if created via Terraform or
		// imported via ARN)" — an id that is not reliably the name, which
		// is why it is left out of IdentityAttrs below even though name
		// itself is claimed.
		Type:          "aws_codebuild_project",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Id"
		// (itself the ARN, per the provider's own Attribute Reference:
		// "id - The ARN of Report Group"). AWS::CodeBuild::ReportGroup
		// ships every CFN Registry handler false, the same
		// registry-handler-less shape as Project above, so again the
		// provider's own documented Import section is the deciding
		// evidence rather than the registry's. Unlike Project,
		// ReportGroup's classic import command (terraform import
		// aws_codebuild_report_group.example
		// arn:aws:codebuild:us-west-2:123456789:report-group/report-group-name)
		// offers no bare-name alternative anywhere in the provider's docs
		// — but the ARN it names is built entirely from the required
		// "name" argument plus the region and account of the cloud the run
		// is against, the same account-derived shape as
		// aws_codeartifact_domain above, not an opaque ID. Taggable, but
		// with no CFN Registry list handler and no native provider list
		// resource either (live/survey-full.json:
		// aws_codebuild_report_group.signals.list_resource is false), so
		// this type reaches a live resource only when a caller supplies a
		// CloudContext; a context-less run finds nothing to enumerate it
		// against, the same open gap this table's aws_sns_topic comment
		// already documents for the account-derived category as a whole.
		Type: "aws_codebuild_report_group",
		Components: []Component{
			inAttr("arn", sep("arn:aws:codebuild:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":report-group/")),
			inAttr("arn", attr("name")),
		},
		ImportSyntax:  "arn:aws:codebuild:REGION:ACCOUNT:report-group/NAME",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// A named-singleton-child of the project: row-gen marked this
		// "(property-child of AWS::CodeBuild::Project) [evidence-only]",
		// but the shape needs no new mechanism — its sole required
		// argument, project_name, is documented as literally equal to this
		// type's own id ("id - The name of the build project"), and the
		// provider's documented import command (terraform import
		// aws_codebuild_webhook.example MyProjectName) uses that same
		// value verbatim. Concrete whenever aws_codebuild_project above is.
		Type:          "aws_codebuild_webhook",
		Components:    []Component{attr("project_name")},
		ImportSyntax:  "PROJECTNAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Id"
		// — but AWS::CodeCommit::Repository ships every CFN Registry
		// handler false, the same registry-handler-less shape as the
		// CodeBuild trio above, so the registry's classification carries no
		// corroborating capability. The provider's own documented Import
		// section settles it independently: the classic import command
		// (terraform import aws_codecommit_repository.imported
		// ExistingRepo) uses the bare, required "repository_name" argument
		// verbatim. The provider exports repository_id as a separate,
		// distinct attribute ("The ID of the repository"), so it is not
		// claimed here.
		Type:          "aws_codecommit_repository",
		Components:    []Component{attr("repository_name")},
		ImportSyntax:  "REPOSITORYNAME",
		IdentityAttrs: []string{"repository_name"},
	},

	serverAssigned("aws_codeconnections_connection",
		"CodeConnections mints the connection's own ARN at create time, embedding a random connection ID the required \"name\" argument does not reconstruct (a fresh connection is also created PENDING and needs a manual console step to reach AVAILABLE, independent of its identity). CFN Registry AWS::CodeConnections::Connection reports handlers.list = true with no required list input, the same Cloud Control fallback shape as aws_efs_file_system in the storage batch, since the pinned provider itself has no native list resource for this type.",
		"arn:aws:codeconnections:REGION:ACCOUNT:connection/CONNECTIONID", "arn", "id"),
	serverAssigned("aws_codestarconnections_connection",
		"The predecessor CFN type of aws_codeconnections_connection above, from before the CodeStar Connections service was renamed CodeConnections; not deprecated as of the pinned v6.58.0 (confirmed against the provider's own docs, which carry no deprecation notice for either the resource or its attributes). Same shape: CodeStarConnections mints the connection's own ARN at create time, embedding a random connection ID the required \"name\" argument does not reconstruct. CFN Registry AWS::CodeStarConnections::Connection reports handlers.list = true with no required list input, the same Cloud Control fallback as its successor.",
		"arn:aws:codestar-connections:REGION:ACCOUNT:connection/CONNECTIONID", "arn", "id"),
	serverAssigned("aws_codestarnotifications_notification_rule",
		"CodeStar Notifications mints the rule's own ARN at create time, embedding a random rule ID; the required \"name\" and \"resource\" arguments (a display name and the ARN of the thing being watched) do not reconstruct it. CFN Registry AWS::CodeStarNotifications::NotificationRule reports handlers.list = true with no required list input, the same Cloud Control fallback shape as aws_efs_file_system in the storage batch.",
		"arn:aws:codestar-notifications:REGION:ACCOUNT:notificationrule/RULEID", "arn", "id"),

	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// the registry's own createOnlyProperties "ApplicationName" —
		// confirmed against the provider's documented import command
		// (terraform import aws_codedeploy_app.example my-application),
		// which uses the required "name" argument verbatim.
		Type:          "aws_codedeploy_app",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; the provider documents it as CodeDeploy's own assigned application ID, not the name
	},
	TypeIdentity{
		// row-gen proposed this correctly: client-named via the registry's
		// own createOnlyProperties "DeploymentConfigName" — confirmed
		// against the provider's documented import command (terraform
		// import aws_codedeploy_deployment_config.example
		// my-deployment-config), which uses the required
		// "deployment_config_name" argument verbatim, and its Attribute
		// Reference ("id - The deployment group's config name."), which is
		// the same string.
		Type:          "aws_codedeploy_deployment_config",
		Components:    []Component{attr("deployment_config_name")},
		ImportSyntax:  "DEPLOYMENT_CONFIG_NAME",
		IdentityAttrs: []string{"id", "deployment_config_name"},
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (registry
		// primaryIdentifier ["ApplicationName", "DeploymentGroupName"],
		// composite, no separator in any schema). live/import-grammar.json's
		// own scraped Import section supplies the separator row-gen would
		// not guess: a colon, between the two required arguments already in
		// configuration (terraform import
		// aws_codedeploy_deployment_group.example
		// my-application:my-deployment-group), confirmed against the
		// provider's Argument Reference (required: app_name,
		// deployment_group_name, service_role_arn) and Attribute Reference
		// ("id - Application name and deployment group name.").
		Type: "aws_codedeploy_deployment_group",
		Components: []Component{
			attr("app_name"),
			sep(":"),
			attr("deployment_group_name"),
		},
		ImportSyntax:  "APP_NAME:DEPLOYMENT_GROUP_NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// the provider's own identity schema (required: name), confirmed
		// against the documented import command (terraform import
		// aws_codepipeline.example example-pipeline), which uses the
		// required "name" argument verbatim.
		Type:          "aws_codepipeline",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (registry
		// primaryIdentifier ["Category", "Provider", "Version"], composite,
		// no separator in any schema). live/import-grammar.json's own
		// scraped Import section supplies both the separator and a
		// correction row-gen would not guess from the registry alone: a
		// colon joins all three (terraform import
		// aws_codepipeline_custom_action_type.example Build:terraform:1),
		// confirmed against the provider's Argument Reference (required:
		// category, provider_name, version — "provider_name", not the
		// registry's "Provider") and Attribute Reference ("id - Composed of
		// category, provider and version. For example, Build:terraform:1").
		Type: "aws_codepipeline_custom_action_type",
		Components: []Component{
			attr("category"),
			sep(":"),
			attr("provider_name"),
			sep(":"),
			attr("version"),
		},
		ImportSyntax:  "CATEGORY:PROVIDER:VERSION",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Id"
		// — the flat serverAssigned() template undersells it the same way
		// it undersold aws_sns_topic and aws_sqs_queue above: the
		// provider's own Identity Schema requires exactly one field, arn,
		// and its documented import command (terraform import
		// aws_codepipeline_webhook.example
		// arn:aws:codepipeline:us-west-2:123456789012:webhook:example)
		// confirms the value is built from the required "name" argument
		// plus the region and account of the cloud the run is against —
		// joined with a colon, not a slash, unlike the CodeArtifact ARNs
		// above.
		Type: "aws_codepipeline_webhook",
		Components: []Component{
			inAttr("arn", sep("arn:aws:codepipeline:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":webhook:")),
			inAttr("arn", attr("name")),
		},
		ImportSyntax:  "arn:aws:codepipeline:REGION:ACCOUNT:webhook:NAME",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_ecrpublic_repository.example example), which uses the
		// required "repository_name" argument verbatim, and its Attribute
		// Reference ("id - The repository name.") — the ECR-public
		// leftover from the IAM/ECR batch's own ECR section, which ratified
		// the registry-backed aws_ecr_repository but not its public
		// counterpart.
		Type:          "aws_ecrpublic_repository",
		Components:    []Component{attr("repository_name")},
		ImportSyntax:  "REPOSITORY_NAME",
		IdentityAttrs: []string{"id", "repository_name"},
	},
	TypeIdentity{
		// A named-singleton-child of the public repository, the same shape
		// as aws_ecr_repository_policy's own deferred entry in the IAM/ECR
		// batch — but this batch's own scope names the ECR-public leftover
		// explicitly, and the identity needs no new mechanism: its sole
		// required argument, repository_name, is what the provider's
		// documented import command (terraform import
		// aws_ecrpublic_repository_policy.example example) uses verbatim.
		// Concrete whenever aws_ecrpublic_repository above is.
		Type:          "aws_ecrpublic_repository_policy",
		Components:    []Component{attr("repository_name")},
		ImportSyntax:  "REPOSITORY_NAME",
		IdentityAttrs: nil,
	},
)

func init() { registerCohortTable(identityTableDevtools) }
