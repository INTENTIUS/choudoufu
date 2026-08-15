// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The identity cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableIdentity = []string{
	// Registry-ratified identity batch (#40, #44, #65): Cognito, IAM
	// leftovers, SSO Admin. See live/e2e/estates/identity/README.md.
	"aws_cognito_identity_pool",
	"aws_cognito_user_pool",
	"aws_iam_openid_connect_provider",
	"aws_iam_policy",
	"aws_iam_server_certificate",
	"aws_ssoadmin_application",
	"aws_ssoadmin_permission_set",
}

var untaggableIdentity = []string{
	// Registry-ratified identity batch (#40, #44, #65): Cognito, IAM
	// leftovers, SSO Admin. Fifteen untaggable types, confirmed against
	// the real provider's documented Argument Reference for each - see
	// live/e2e/estates/identity/README.md, "Untaggable types".
	"aws_cognito_identity_pool_provider_principal_tag",
	"aws_cognito_identity_pool_roles_attachment",
	"aws_cognito_identity_provider",
	"aws_cognito_resource_server",
	"aws_cognito_user",
	"aws_cognito_user_group",
	"aws_cognito_user_in_group",
	"aws_cognito_user_pool_domain",
	"aws_iam_group_policy",
	"aws_iam_group_policy_attachment",
	"aws_iam_user_policy",
	"aws_iam_user_policy_attachment",
	"aws_ssoadmin_account_assignment",
	"aws_ssoadmin_application_assignment",
	"aws_ssoadmin_instance_access_control_attributes",
	// #175 ratification batch (PROPOSE, issue #65), 2026-08-15:
	// taggability per the provider schema survey (live/survey-full.json,
	// v6.59.0 signals.taggable).
	"aws_ssoadmin_customer_managed_policy_attachments_exclusive",
	"aws_ssoadmin_managed_policy_attachments_exclusive",
	"aws_ssoadmin_permission_set_inline_policy",
	"aws_ssoadmin_permissions_boundary_attachment",
}

func init() {
	registerCohortStamp(taggableIdentity, untaggableIdentity, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified identity batch (#40, #44, #65): Cognito, IAM
			// leftovers, SSO Admin. Taggable/untaggable per the real provider's
			// documented Argument Reference for each type - the fifteen
			// untaggable rows are exactly this batch's own "Untaggable types"
			// list in live/e2e/estates/identity/README.md.
			"aws_cognito_identity_pool":                        taggedSchema("id", "identity_pool_name"),
			"aws_cognito_identity_pool_provider_principal_tag": untaggedSchema("id", "identity_pool_id", "identity_provider_name"),
			"aws_cognito_identity_pool_roles_attachment":       untaggedSchema("id", "identity_pool_id"),
			"aws_cognito_identity_provider":                    untaggedSchema("id", "user_pool_id", "provider_name", "provider_type"),
			"aws_cognito_resource_server":                      untaggedSchema("id", "user_pool_id", "identifier", "name"),
			"aws_cognito_user":                                 untaggedSchema("id", "user_pool_id", "username"),
			"aws_cognito_user_group":                           untaggedSchema("id", "user_pool_id", "name"),
			"aws_cognito_user_in_group":                        untaggedSchema("id", "user_pool_id", "group_name", "username"),
			"aws_cognito_user_pool":                            taggedSchema("id", "arn", "name"),
			"aws_cognito_user_pool_domain":                     untaggedSchema("id", "domain", "user_pool_id"),
			"aws_iam_group_policy":                             untaggedSchema("id", "group", "name", "policy"),
			"aws_iam_group_policy_attachment":                  untaggedSchema("id", "group", "policy_arn"),
			"aws_iam_openid_connect_provider":                  taggedSchema("id", "arn", "url"),
			"aws_iam_policy":                                   taggedSchema("id", "arn", "name"),
			"aws_iam_server_certificate":                       taggedSchema("id", "arn", "name"),
			"aws_iam_user_policy":                              untaggedSchema("id", "user", "name", "policy"),
			"aws_iam_user_policy_attachment":                   untaggedSchema("id", "user", "policy_arn"),
			"aws_ssoadmin_account_assignment":                  untaggedSchema("id", "instance_arn", "permission_set_arn", "principal_id", "principal_type", "target_id", "target_type"),
			"aws_ssoadmin_application":                         taggedSchema("id", "arn", "name", "instance_arn", "application_provider_arn"),
			"aws_ssoadmin_application_assignment":              untaggedSchema("id", "application_arn", "principal_id", "principal_type"),
			"aws_ssoadmin_instance_access_control_attributes":  untaggedSchema("id", "instance_arn"),
			"aws_ssoadmin_permission_set":                      taggedSchema("id", "arn", "name", "instance_arn"),
			// #175 ratification batch (PROPOSE, issue #65), 2026-08-15.
			"aws_ssoadmin_customer_managed_policy_attachments_exclusive": untaggedSchema("id", "instance_arn", "permission_set_arn"),
			"aws_ssoadmin_managed_policy_attachments_exclusive":          untaggedSchema("id", "instance_arn", "permission_set_arn"),
			"aws_ssoadmin_permission_set_inline_policy":                  untaggedSchema("id", "instance_arn", "permission_set_arn"),
			"aws_ssoadmin_permissions_boundary_attachment":               untaggedSchema("id", "instance_arn", "permission_set_arn"),
		})
	})
}
