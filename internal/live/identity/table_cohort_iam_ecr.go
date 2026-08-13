// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableIamEcr is the iam-ecr cohort's slice of [DefaultTable]:
// the identity rows the iam-ecr ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableIamEcr = buildTable(
	// ---- Registry-ratified (#40, #44): second batch, IAM and ECR (#26) --
	//
	// Same method as the first Lambda batch above: every row started as a
	// tools/row-gen proposal from live/registry.json, and every row that
	// landed here was independently cross-checked against the AWS
	// provider's documented import behaviour, not accepted on the
	// registry's classification alone. Cohort estate:
	// live/e2e/estates/iam-ecr. Issue #26 named two blocked-emulator types,
	// aws_ecr_repository and aws_iam_user, as unblocked by the pinned
	// floci image's IAM-tag and ECR fixes; both are ratified below.
	//
	// Rejected, and deliberately absent from this table — the same
	// registry-says-server-assigned-but-the-provider-disagrees shape the
	// Lambda batch's two rejections established, all three confirmed by
	// reading the provider's own Argument Reference, not just its Import
	// section:
	//
	//   - aws_iam_policy: row-gen proposed server-assigned via the
	//     registry's opaque "Id". The provider disagrees: its documented
	//     import ID is the policy's ARN, and the ARN embeds the `name` and
	//     `path` arguments the resource's own Argument Reference lists as
	//     configuration (name is optional — Terraform assigns a random one
	//     when omitted — but when set, it is what the ARN's final path
	//     segment literally is). "Id" is CloudFormation's own read-only
	//     projection of that same composite, not a value this provider
	//     mints independently. SURVEY.md already carries this type as
	//     client-named, account-derived (the same CloudContext mechanism
	//     aws_sns_topic uses); wiring it that way is follow-on work this
	//     batch does not attempt.
	//   - aws_iam_saml_provider: row-gen proposed server-assigned via the
	//     registry's "Arn" (read-only in the registry). The provider's
	//     documented import ID is that same ARN, but `name` is a *required*
	//     configuration argument with no generated fallback, and the ARN's
	//     final path segment is that name verbatim
	//     (arn:aws:iam::ACCOUNT:saml-provider/NAME). Same failure shape as
	//     aws_lambda_alias: a read-only CFN field that is really a
	//     composite of an argument already in configuration.
	//   - aws_iam_virtual_mfa_device: row-gen proposed server-assigned via
	//     the registry's "SerialNumber". The provider's own docs say the
	//     serial number *is* the ARN
	//     (arn:aws:iam::ACCOUNT:mfa/NAME), and NAME is the required
	//     `virtual_mfa_device_name` argument verbatim — the same composite
	//     shape as the SAML provider above. (The type also mints a secret,
	//     base_32_string_seed, that can never be read back after create —
	//     a second, independent reason it would need care beyond this
	//     batch's scope even had its identity checked out.)
	//   - aws_iam_access_key: row-gen proposed server-assigned via the
	//     registry's opaque "Id", and the classification itself is not in
	//     question — but this type is one of the three SURVEY.md's "rule
	//     excludes" permanently, not merely leaves unwired: an access key
	//     is a credential born server-side alongside a secret
	//     (SecretAccessKey) that can never be read again, forwarded to the
	//     lifecycle layer by the fork's own architecture rather than
	//     modeled as an ordinary resource. Admitting it here would reverse
	//     that standing decision, which is out of scope for a row-gen
	//     ratification batch.
	//
	// Deferred, identity confirmed correct, but not wired this batch:
	//
	//   - aws_iam_group: row-gen correctly proposed client-named via `name`
	//     (confirmed against the provider's documented import, which sets
	//     id to the group name verbatim). live/survey.json — the curated
	//     68 this survey measures — already carries this type, and its own
	//     signal says untaggable (IAM has no TagGroup API). Admitting it
	//     would move it into
	//     tools/survey-gen/limitations_test.go's TestLimitationsDocAgainstSurvey
	//     derived set (admitted ∩ curated-68 ∩ untaggable), which requires
	//     live/LIMITATIONS.md's "Untaggable types cannot be removed by the
	//     sweep" entry to name it — an edit to the curated-68 apparatus
	//     this batch's mandate leaves untouched (unlike
	//     aws_lambda_layer_version, which sidesteps the same doc by being
	//     outside the curated 68 entirely, aws_iam_group cannot dodge it
	//     that way). Left for a batch prepared to move that doc.
	//
	// Not this batch's to decide, same as aws_lambda_permission in the
	// first batch: aws_iam_group_policy, aws_iam_role_policy and
	// aws_iam_user_policy are all needs-hand-separator (composite
	// PolicyName+GroupName/RoleName/UserName primary identifiers with no
	// separator in any schema); aws_iam_role_policy is wired already, via
	// this table's own #19 slice above, not via row-gen. aws_iam_role
	// itself was row-gen's eighth IAM proposal and is skipped here for the
	// same reason: already wired via this table's own #19 slice, not via
	// the registry. The remaining row-gen output for both services
	// (aws_ecr_lifecycle_policy, aws_ecr_pull_through_cache_rule,
	// aws_ecr_pull_time_update_exclusion,
	// aws_ecr_repository_creation_template, aws_ecr_repository_policy,
	// aws_iam_role_policy_attachment, aws_iam_server_certificate) is
	// evidence-only per #44's own non-goals — no pastable row was ever
	// generated for any of them.

	serverAssigned("aws_ecr_registry_policy",
		"the registry policy is a singleton per AWS account: its identity is the account's own ECR registry ID, which pre-exists the resource and is never supplied by a configuration argument — the resource's only argument, policy, sets the document content, not an identifying name.",
		"REGISTRYID", "registry_id"),
	serverAssigned("aws_ecr_registry_scanning_configuration",
		"the scanning configuration is a singleton per AWS account: its identity is the account's own ECR registry ID, which pre-exists the resource and is never supplied by a configuration argument.",
		"REGISTRYID", "registry_id"),
	serverAssigned("aws_ecr_replication_configuration",
		"the replication configuration is a singleton per AWS account: its identity is the account's own ECR registry ID, which pre-exists the resource and is never supplied by a configuration argument.",
		"REGISTRYID", "registry_id"),
	serverAssigned("aws_iam_service_linked_role",
		"IAM computes the service-linked role's name from aws_service_name using its own internal per-service convention (for example elasticbeanstalk.amazonaws.com becomes AWSServiceRoleForElasticBeanstalk), not a string transform of any configured argument; the provider's own docs say the role name is not an argument you provide. The documented import ID is the role's ARN, not the bare RoleName the registry reports as primaryIdentifier.",
		"ARN", "arn", "id"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[RepositoryName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed directly against the provider's own identity schema
		// (live/survey-full.json: required=[name]) and against the
		// documented import command, which sets id to the repository name
		// verbatim. Issue #26's first named type: floci's ecr:CreateRepository
		// no longer needs a Docker daemon, so the earlier blocked-emulator
		// note no longer holds.
		Type:          "aws_ecr_repository",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; id is the registry_id, not the name
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[InstanceProfileName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's own identity schema
		// (live/survey-full.json: required=[name]) and against the
		// documented import command, which sets id to the instance
		// profile's name verbatim.
		Type:          "aws_iam_instance_profile",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[UserName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed directly against the provider's own identity schema
		// (live/survey-full.json: required=[name]) and against the
		// documented import command, which sets id to the user name
		// verbatim. Issue #26's second named type: floci's iam:GetUser now
		// returns Tags, so the earlier blocked-emulator note no longer
		// holds.
		Type:          "aws_iam_user",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// aws_iam_group: this batch's own deferral note above named it
		// correctly the first time — client-named via name, no identity
		// schema in v6.58.0, the documented import command (terraform
		// import aws_iam_group.developers developers) sets id to the group
		// name verbatim — and deferred it anyway because admitting an
		// untaggable curated-68 type obligated live/LIMITATIONS.md's
		// "Untaggable types" entry, which this batch's mandate left
		// untouched. #54 regeneralized that entry's derivation past the
		// curated 68 (tools/survey-gen/untaggable_render.go), so the ECS/EKS
		// batch (#65) ratifies the deferral here rather than opening a
		// second cohort for one already-settled type.
		Type:          "aws_iam_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "GROUP_NAME",
		IdentityAttrs: []string{"id", "name"},
	},
)

func init() { registerCohortTable(identityTableIamEcr) }
