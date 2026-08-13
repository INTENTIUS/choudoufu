// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesIdentity is the identity cohort's slice of [admittedTypesV0]:
// the types the identity ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesIdentity = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): fifth batch, identity
	// ---- (Cognito, IAM leftovers, SSO Admin; issue #65's ratification
	// ---- campaign). Same tools/row-gen pipeline and verification standard
	// ---- as the batches above, cross-checked against the AWS provider's
	// ---- documented import behaviour (its own Argument/Attribute/Import
	// ---- sections, fetched from the pinned v6.59.0 tag) rather than
	// ---- accepted on row-gen's classification alone — several rows below
	// ---- correct a row-gen "needs hand separator" or "evidence-only"
	// ---- verdict, the same way the route53-cloudfront and RDS batches
	// ---- did. Two row-gen proposals this batch does not re-litigate,
	// ---- aws_iam_saml_provider and aws_iam_virtual_mfa_device, were
	// ---- already rejected by the IAM/ECR batch above on ARN-embedding
	// ---- grounds; aws_iam_access_key is excluded the same way that
	// ---- batch excluded it, per SURVEY.md's standing credential rule. See
	// ---- internal/live/identity/table.go for the per-type evidence and
	// ---- for every row this batch rejected or deferred. Cohort estate:
	// ---- live/e2e/estates/identity.
	"aws_cognito_identity_pool":                        {},
	"aws_cognito_identity_pool_provider_principal_tag": {},
	"aws_cognito_identity_pool_roles_attachment":       {},
	"aws_cognito_identity_provider":                    {},
	"aws_cognito_resource_server":                      {},
	"aws_cognito_user":                                 {},
	"aws_cognito_user_group":                           {},
	"aws_cognito_user_in_group":                        {},
	"aws_cognito_user_pool":                            {},
	"aws_cognito_user_pool_domain":                     {},
	"aws_iam_group_policy":                             {},
	"aws_iam_group_policy_attachment":                  {},
	"aws_iam_openid_connect_provider":                  {},
	"aws_iam_policy":                                   {},
	"aws_iam_server_certificate":                       {},
	"aws_iam_user_policy":                              {},
	"aws_iam_user_policy_attachment":                   {},
	"aws_ssoadmin_account_assignment":                  {},
	"aws_ssoadmin_application":                         {},
	"aws_ssoadmin_application_assignment":              {},
	"aws_ssoadmin_instance_access_control_attributes":  {},
	"aws_ssoadmin_permission_set":                      {},
}

func init() { registerCohortAdmitted(admittedTypesIdentity) }
