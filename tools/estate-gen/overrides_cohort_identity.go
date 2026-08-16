// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesIdentity is the identity cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesIdentity = map[string]typeOverride{
	// Identity batch (issue #65). Every argument below is Required in the
	// wire schema (so the generic required-only pass already sets it), but
	// the provider's own plan-time validation rejects the generic
	// placeholder value on a format or enum ground the schema itself does
	// not carry - the same shape as the batches above.
	"aws_cognito_resource_server": {
		Reasons: []string{
			`"user_pool_id" is a required string the schema does not constrain, but the provider validates it against the documented region_id shape (validate: "must be the region name followed by an underscore and then alphanumeric pattern"); the generic placeholder string is not one - resolved by hand to the sibling aws_cognito_user_pool's own real id rather than a synthesized literal, so this type actually exercises against a live pool during a floci apply instead of failing "User pool not found". "name" no longer needs a fix for the accidental cross-type collision this Reasons string used to describe (gen.go's parentRef, #136: a bare "name" argument is never treated as a same-named sibling's parent) - kept set to its own short literal rather than the generic pass's own longer tofu-<cohort>-cohort-<type> placeholder, purely for readability`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("user_pool_id", exprTokens(cognitoUserPoolIDRef(g)))
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-resource-server"`, g.cohort)))
		},
	},
	"aws_cognito_user": {
		Reasons: []string{
			`"user_pool_id" is a required string the schema does not constrain, but the provider validates it against the documented region_id shape; the generic placeholder string is not one - resolved to the sibling aws_cognito_user_pool's own real id, same fix as aws_cognito_resource_server above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("user_pool_id", exprTokens(cognitoUserPoolIDRef(g)))
		},
	},
	"aws_cognito_user_group": {
		Reasons: []string{
			`"user_pool_id" is a required string the schema does not constrain, but the provider validates it against the documented region_id shape (validate: "must be the region name followed by an underscore and then alphanumeric pattern"); the generic placeholder string is not one - resolved to the sibling aws_cognito_user_pool's own real id, same fix as aws_cognito_resource_server above. "name" (the group's own name, distinct from user_pool_id) no longer needs a fix for the accidental cross-type collision this Reasons string used to describe (gen.go's parentRef, #136), and is kept set to its own short literal rather than the generic pass's own longer placeholder, purely for readability`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("user_pool_id", exprTokens(cognitoUserPoolIDRef(g)))
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-user-group"`, g.cohort)))
		},
	},
	"aws_cognito_user_in_group": {
		Reasons: []string{
			`"user_pool_id" is a required string the schema does not constrain, but the provider validates it against the documented region_id shape; the generic placeholder string is not one - resolved to the sibling aws_cognito_user_pool's own real id, same fix as aws_cognito_user_group above. "group_name" and "username" are both required strings the generic pass rendered as independent literals unrelated to the sibling aws_cognito_user_group and aws_cognito_user resources this same run also creates (neither is a single-component identity argument gen.go's parentRef links automatically: aws_cognito_user_group's own name is real but not the type identityArgName treats as its identity-bearing argument in isolation, and aws_cognito_user's identity is the two-component user_pool_id+username composite, not a single one) - resolved by hand to both siblings' own real attributes so this attaches a real user to a real group during a floci apply rather than naming two groups/users that were never created`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("user_pool_id", exprTokens(cognitoUserPoolIDRef(g)))
			body.SetAttributeRaw("group_name", exprTokens(cognitoUserGroupNameRef(g)))
			body.SetAttributeRaw("username", exprTokens(cognitoUsernameRef(g)))
		},
	},
	"aws_cognito_user_pool": {
		Reasons: []string{
			`"name" is a required string the schema does not constrain. The accidental cross-type collision this Reasons string used to describe (the generic pass pointing this type's own "name" at an unrelated sibling's, purely because both happened to take an argument spelled "name") no longer happens - gen.go's parentRef (#136) never treats a bare "name" argument as a same-named sibling's parent, since the word carries no hint of which sibling it would mean. Kept set to its own short literal rather than the generic pass's own longer tofu-<cohort>-cohort-<type> placeholder, purely for readability`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-user-pool"`, g.cohort)))
		},
	},
	"aws_cognito_identity_provider": {
		Reasons: []string{
			`"provider_name" is a required string the schema does not constrain, but the provider validates it is at most 32 UTF-8 characters (validate: "cannot be longer than 32 UTF-8 characters"); the generic placeholder-suffixed name is longer. "provider_type" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected provider_type to be one of [SAML Facebook Google LoginWithAmazon SignInWithApple OIDC]"). "user_pool_id" needs the same real-pool-reference fix as aws_cognito_resource_server above (its own generic placeholder does not match the documented region_id shape at all)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("user_pool_id", exprTokens(cognitoUserPoolIDRef(g)))
			body.SetAttributeRaw("provider_name", exprTokens(fmt.Sprintf(`"tofu-%s-idp"`, g.cohort)))
			body.SetAttributeRaw("provider_type", exprTokens(`"OIDC"`))
			body.SetAttributeRaw("provider_details", exprTokens(`{
    client_id                  = "placeholder"
    authorize_scopes           = "openid"
    attributes_request_method  = "GET"
    oidc_issuer                = "https://accounts.example.com"
  }`))
		},
	},
	"aws_iam_group_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON policy"); the generic string placeholder is not JSON - the group-policy sibling of aws_s3_bucket_policy's own override above`,
			`"group" must name an actual IAM group and no cohort type creates one (aws_iam_group is covered by the IAM/ECR batch, not here); a supporting aws_iam_group is generated (NeedsSupporting) and referenced - folds the hand-written iam.tf block #108 criterion 4 found, whose own comment recorded that the generic pass has no cross-type alias for "group"`,
		},
		NeedsSupporting: []string{"aws_iam_group"},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if sup, ok := g.byType["aws_iam_group"]; ok {
				body.SetAttributeRaw("group", exprTokens(sup.Type+"."+sup.Label+".name"))
			}
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "s3:ListAllMyBuckets"
      Resource = "*"
    }]
  })`))
		},
	},
	"aws_iam_user_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON; the generic string placeholder is not JSON - the user-policy sibling of aws_iam_group_policy's own override above`,
			`"user" must name an actual IAM user; a supporting aws_iam_user is generated and referenced, same fold as aws_iam_group_policy's "group"`,
		},
		NeedsSupporting: []string{"aws_iam_user"},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if sup, ok := g.byType["aws_iam_user"]; ok {
				body.SetAttributeRaw("user", exprTokens(sup.Type+"."+sup.Label+".name"))
			}
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "s3:ListAllMyBuckets"
      Resource = "*"
    }]
  })`))
		},
	},
	"aws_iam_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON; the generic string placeholder is not JSON - same fix as aws_iam_group_policy above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "s3:ListAllMyBuckets"
      Resource = "*"
    }]
  })`))
		},
	},
	"aws_iam_openid_connect_provider": {
		Reasons: []string{
			`"url" is a required string the schema does not constrain, but the provider validates it parses as a URL with a host (validate: "expected \"url\" to have a host"); the generic placeholder string has none`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("url", exprTokens(`"https://accounts.example.com"`))
		},
	},
	"aws_ssoadmin_instance_access_control_attributes": {
		Reasons: []string{
			`"instance_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the generic placeholder-suffixed name is not one. This is the type identityArgName treats as the single-component owner of "instance_arn" (see internal/live/identity/table.go's own entry), so every other type in this cohort that also takes an instance_arn argument (aws_ssoadmin_application, aws_ssoadmin_permission_set, aws_ssoadmin_account_assignment) already references this resource's own attribute through gen.go's parentRef rather than rendering a second, independent placeholder - fixing the ARN shape here is what fixes all four.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("instance_arn", exprTokens(`"arn:aws:sso:::instance/ssoins00000000001"`))
		},
	},
	"aws_cognito_identity_pool_roles_attachment": {
		Reasons: []string{
			`"identity_pool_id" is a required string the schema does not constrain, but the provider validates its length (1-55) and shape (a real identity pool id, "REGION:UUID") at apply time - not caught by "terraform validate" itself, only surfaced at apply (validate: "expected length of identity_pool_id to be in the range (1 - 55)"); the generic placeholder-suffixed name is both too long and the wrong shape. This is the type identityArgName treats as the single-component owner of "identity_pool_id" (its Components read the same-named argument), so aws_cognito_identity_pool_provider_principal_tag's own identity_pool_id already references this resource's attribute through gen.go's parentRef rather than rendering an independent placeholder - fixing the shape here is what fixes both. "roles" is a required map the schema leaves unconstrained (MinItems 0), but the provider requires at least the "authenticated" or "unauthenticated" key set (apply-time validate: "Either \"authenticated\" or \"unauthenticated\" must be defined") - not caught by "terraform validate" either, only surfaced at apply, the same "schema says Optional/unconstrained, provider requires it in practice" shape aws_s3_bucket_lifecycle_configuration's own override above already has.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("identity_pool_id", exprTokens(`"us-east-1:00000000-0000-0000-0000-000000000000"`))
			body.SetAttributeRaw("roles", exprTokens(fmt.Sprintf(
				`{ authenticated = "arn:aws:iam::000000000000:role/tofu-%s-cohort-authenticated" }`, g.cohort)))
		},
	},
	"aws_ssoadmin_permission_set": {
		Reasons: []string{
			`"name" is Required and the schema pins its own length range, but the generic pass's own tofu-<cohort>-cohort-<type> placeholder exceeds the provider's own 1-32 character limit (validate: "expected length of name to be in the range (1 - 32)") - the same class of gap #136's cohort/type-fix rule left in place for every type whose limit is tighter than the placeholder's own length, since the placeholder's length depends on the cohort name rather than on any per-type signal the schema carries`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-permset"`, g.cohort)))
		},
	},
	"aws_ssoadmin_application_assignment": {
		Reasons: []string{
			`"application_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN; the generic placeholder string is not one, and this type has no single-component identity entry for gen.go's parentRef to link automatically, unlike instance_arn above - resolved by hand to the sibling aws_ssoadmin_application's own arn attribute when this run renders one. "principal_type" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected principal_type to be one of [USER GROUP]"). "principal_id" is a required string the schema does not constrain, but the provider validates it looks like an Identity Store principal id, a GUID optionally prefixed by a 10-hex-digit domain segment; the generic placeholder string matches neither shape`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("application_arn", exprTokens(ssoadminApplicationArnRef(g)))
			body.SetAttributeRaw("principal_type", exprTokens(`"USER"`))
			body.SetAttributeRaw("principal_id", exprTokens(`"12345678-1234-1234-1234-123456789012"`))
		},
	},
	"aws_ssoadmin_account_assignment": {
		Reasons: []string{
			`"permission_set_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN; the generic placeholder string is not one, and (like application_arn above) this type has no single-component identity entry for parentRef to link automatically - resolved by hand to the sibling aws_ssoadmin_permission_set's own arn attribute. "principal_type" and "target_type" are both required strings the schema does not constrain to an enum, but the provider validates each against its own fixed set (validate: "expected principal_type to be one of [USER GROUP]", "expected target_type to be one of [AWS_ACCOUNT]"). "principal_id" needs the same Identity Store principal-id shape as aws_ssoadmin_application_assignment's own override above. "target_id" is a required string the schema does not constrain, but the provider validates it looks like a 12-digit AWS account id (validate: "doesn't look like AWS Account ID")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("permission_set_arn", exprTokens(ssoadminPermissionSetArnRef(g)))
			body.SetAttributeRaw("principal_type", exprTokens(`"GROUP"`))
			body.SetAttributeRaw("target_type", exprTokens(`"AWS_ACCOUNT"`))
			body.SetAttributeRaw("principal_id", exprTokens(`"12345678-1234-1234-1234-123456789012"`))
			body.SetAttributeRaw("target_id", exprTokens(`"000000000000"`))
		},
	},
	"aws_ssoadmin_application": {
		Reasons: []string{
			`"application_provider_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "Invalid ARN Value"); the generic placeholder string is not one - set to AWS's own built-in custom SAML application provider, a real, documented value (not account-specific) rather than a synthesized placeholder ARN. "name" no longer needs a fix for the accidental cross-type collision this Reasons string used to describe (gen.go's parentRef, #136), and is kept set to its own short literal rather than the generic pass's own longer placeholder, purely for readability`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("application_provider_arn", exprTokens(`"arn:aws:sso::aws:applicationProvider/custom-saml"`))
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-app"`, g.cohort)))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesIdentity) }
