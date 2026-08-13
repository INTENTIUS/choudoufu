// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableIdentity is the identity cohort's slice of [DefaultTable]:
// the identity rows the identity ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableIdentity = buildTable(
	// ---- Registry-ratified (#40, #44, #65): fifth batch, identity
	// ---- (Cognito, IAM leftovers, SSO Admin) ------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented import behaviour — its real Import,
	// Argument Reference and Attribute Reference sections, fetched from
	// github.com/hashicorp/terraform-provider-aws at the pinned v6.59.0 tag
	// — rather than accepted on the registry's word alone. Cohort estate:
	// live/e2e/estates/identity.
	//
	// Rejected outright, on independent verification against the
	// provider's real docs:
	//
	//   - aws_iam_group_membership: row-gen proposed server-assigned via
	//     the registry's opaque "Id" (AWS::IAM::UserToGroupAddition, whose
	//     registry entry ships every handler false — create, read, update,
	//     delete and list — a stub CFN type with no working handler at
	//     all). The real provider docs settle it further than the registry
	//     even tries to: v6.59.0's website/docs/r/iam_group_membership
	//     carries no Import section whatsoever, meaning this type simply
	//     is not importable in the pinned provider release. Not a
	//     composite this table could hand-write a separator for, and not a
	//     marker candidate either — genuinely absent evidence, not weak
	//     evidence.
	//   - aws_cognito_managed_login_branding: row-gen filed this
	//     needs-hand-separator (UserPoolId, ManagedLoginBrandingId). The
	//     real import id is exactly that pair, comma-joined — but
	//     ManagedLoginBrandingId is the provider's own Attribute
	//     Reference-only output ("ID of the managed login branding
	//     style"), never a configuration argument, so only the
	//     user_pool_id half is composable. Same shape as
	//     aws_cognito_user_pool_client below.
	//   - aws_cognito_user_pool_client: row-gen filed this
	//     needs-hand-separator (UserPoolId, ClientId). The real import id
	//     is UserPoolId/ClientId, slash-joined, but ClientId is Cognito's
	//     own server-assigned output (Attribute Reference only, never an
	//     argument) — no config value reconstructs it. Compounding, not
	//     the deciding factor: when generate_secret is set, the same
	//     resource also mints a client_secret attribute a live read can
	//     never recover, the same credential shape that excludes
	//     aws_iam_access_key by SURVEY.md's standing rule.
	//   - aws_cognito_risk_configuration and
	//     aws_cognito_user_pool_ui_customization: both key on the same
	//     unadmitted client_id half aws_cognito_user_pool_client's own
	//     rejection just named — ui_customization requires it outright,
	//     and risk_configuration's own real import id
	//     (user_pool_id, or user_pool_id:client_id when client_id is set —
	//     the provider's Argument Reference marks client_id Optional) is a
	//     conditionally-shaped composite this table's Components
	//     vocabulary has no way to express even before client_id's own
	//     problem is reached.
	//   - aws_identitystore_group and aws_identitystore_group_membership:
	//     row-gen filed both needs-hand-separator
	//     (IdentityStoreId/GroupId, IdentityStoreId/MembershipId — both
	//     confirmed slash-joined against the real docs). GroupId and
	//     MembershipId are each the provider's own Attribute
	//     Reference-only output (a UUID IdentityStore mints at create
	//     time), so only the identity_store_id half is composable — the
	//     same shape as the two Cognito rejections above, and, like
	//     aws_ssoadmin_permission_set below, scoped by an IAM Identity
	//     Center singleton this fork has no admitted resource for. Neither
	//     is taggable (IdentityStore's tagging API is scoped to
	//     principals, not groups or memberships), so there is no marker
	//     path either. A batch that gives aws_ssoadmin_permission_set's
	//     own singleton-scope precedent a second, content-matched
	//     enumeration mechanism (rather than this batch's tag-filtered
	//     one) could pick these back up.
	//
	// Already rejected, and not re-litigated here: aws_iam_saml_provider
	// and aws_iam_virtual_mfa_device were both rejected by the IAM/ECR
	// batch above on ARN-embedding grounds (the registry's read-only Arn
	// field is really a composite of a required name argument this
	// provider does not treat as reconstructible). aws_iam_access_key is
	// excluded the same way that batch excluded it: SURVEY.md's standing
	// credential rule (a create-only secret a live read can never recover).
	// This batch's own independent look at all three found nothing that
	// changes the earlier verdict.
	//
	// Deferred, evidence-only per row-gen with no pastable row to
	// hand-verify against — the same "not this batch's to decide" standard
	// the IAM/ECR batch's own aws_iam_role_policy_attachment slice used
	// for its siblings: aws_ssoadmin_customer_managed_policy_attachment,
	// aws_ssoadmin_customer_managed_policy_attachments_exclusive,
	// aws_ssoadmin_managed_policy_attachment,
	// aws_ssoadmin_managed_policy_attachments_exclusive,
	// aws_ssoadmin_permission_set_inline_policy and
	// aws_ssoadmin_permissions_boundary_attachment are all property-children
	// of AWS::SSO::PermissionSet that row-gen never generated a row for
	// (its own note: "parent aws_ssoadmin_permission_set is not itself
	// proposed"). That parent is admitted below, but hand-composing six
	// children row-gen gives no evidence for at all is a bigger lift than
	// this batch's mandate. aws_cognito_log_delivery_configuration is the
	// same shape on the Cognito side: evidence-only, untaggable, unlistable,
	// no pastable row. aws_identitystore_user and
	// aws_cognito_managed_user_pool_client are outside row-gen's scope
	// entirely — live/mapping.json carries no CFN type for either (the
	// first is real IAM Identity Center surface CloudFormation's
	// IdentityStore coverage does not model; the second adopts an existing
	// Cognito-managed client rather than creating one, the same
	// default_*-adopter shape as aws_default_vpc) — so row-gen's
	// registry-driven analysis never runs on them at all.

	serverAssigned("aws_cognito_identity_pool",
		"Cognito Identity mints the identity pool's own id (a REGION:UUID string) at create time; the identity_pool_name argument is client-chosen but is not the import identity.",
		"IDENTITYPOOLID", "id"),
	serverAssigned("aws_cognito_user_pool",
		"Cognito mints the user pool's own id (region_XXXXXXXXX) at create time; the name argument is client-chosen but is not the import identity.",
		"USERPOOLID", "id"),

	TypeIdentity{
		// row-gen proposed this server-assigned via the registry's opaque
		// "Id" (AWS::Cognito::IdentityPoolRoleAttachment). The real,
		// documented import id is not an independent token at all: it is
		// literally the parent identity pool's own id, verbatim
		// (terraform import aws_cognito_identity_pool_roles_attachment.example
		// us-west-2:b64805ad-cb56-40ba-9ffc-f5d8207e6d42) — the same
		// named-singleton-child shape as aws_s3_bucket_policy, at most one
		// attachment per pool. Concrete whenever
		// aws_cognito_identity_pool above is, through its own marker.
		Type:          "aws_cognito_identity_pool_roles_attachment",
		Components:    []Component{attr("identity_pool_id")},
		ImportSyntax:  "IDENTITYPOOLID",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator
		// (IdentityPoolId, IdentityProviderName). The provider's real
		// Import section and Argument Reference confirm a colon-joined
		// composite of two Required arguments, identity_pool_id (the
		// already-admitted pool's own marker-discovered id) and
		// identity_provider_name (a literal string already in
		// configuration) — the same concrete-composite shape as
		// aws_iam_role_policy's ROLENAME:POLICYNAME.
		Type: "aws_cognito_identity_pool_provider_principal_tag",
		Components: []Component{
			attr("identity_pool_id"),
			sep(":"),
			attr("identity_provider_name"),
		},
		ImportSyntax:  "IDENTITYPOOLID:IDENTITYPROVIDERNAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (UserPoolId,
		// ProviderName). The provider's real Import section and Argument
		// Reference confirm a colon-joined composite of two Required
		// arguments, user_pool_id (the already-admitted pool's own
		// marker-discovered id) and provider_name (a literal string
		// already in configuration) — the same shape as the principal tag
		// above.
		Type: "aws_cognito_identity_provider",
		Components: []Component{
			attr("user_pool_id"),
			sep(":"),
			attr("provider_name"),
		},
		ImportSyntax:  "USERPOOLID:PROVIDERNAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (UserPoolId,
		// Identifier). The provider's real Import section documents a
		// pipe-joined composite ("us-west-2_abc123|https://example.com"),
		// an unusual separator character this table has not used before —
		// confirmed directly against the raw provider docs source, not
		// inferred — of two Required arguments already in configuration:
		// user_pool_id (the already-admitted pool's own marker-discovered
		// id) and identifier (a literal string, e.g. an API's URI).
		Type: "aws_cognito_resource_server",
		Components: []Component{
			attr("user_pool_id"),
			sep("|"),
			attr("identifier"),
		},
		ImportSyntax:  "USERPOOLID|IDENTIFIER",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (UserPoolId, Username).
		// The provider's real Import section confirms a slash-joined
		// composite of two Required arguments already in configuration:
		// user_pool_id (the already-admitted pool's own marker-discovered
		// id) and username (a literal string).
		Type: "aws_cognito_user",
		Components: []Component{
			attr("user_pool_id"),
			sep("/"),
			attr("username"),
		},
		ImportSyntax:  "USERPOOLID/USERNAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (UserPoolId, GroupName —
		// the registry's own field name; the provider's Argument Reference
		// calls the same argument "name"). The real Import section
		// confirms a slash-joined composite of two Required arguments
		// already in configuration: user_pool_id (the already-admitted
		// pool's own marker-discovered id) and name (a literal group
		// name).
		Type: "aws_cognito_user_group",
		Components: []Component{
			attr("user_pool_id"),
			sep("/"),
			attr("name"),
		},
		ImportSyntax:  "USERPOOLID/NAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (UserPoolId, GroupName,
		// Username — a three-part composite, beyond what any earlier batch
		// in this table has hand-wired). The provider's real Import
		// section confirms a comma-joined triple of three Required
		// arguments already in configuration: user_pool_id (the
		// already-admitted pool's own marker-discovered id), group_name
		// and username (both literal strings).
		Type: "aws_cognito_user_in_group",
		Components: []Component{
			attr("user_pool_id"),
			sep(","),
			attr("group_name"),
			sep(","),
			attr("username"),
		},
		ImportSyntax:  "USERPOOLID,GROUPNAME,USERNAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen proposed this needs-hand-separator on the registry's own
		// evidence (primaryIdentifier=[UserPoolId, Domain]). The real
		// provider docs disagree with the registry's own compound key: the
		// documented import command
		// (terraform import aws_cognito_user_pool_domain.main
		// auth.example.org) and Argument Reference both settle on the
		// domain argument alone — CloudFormation's AWS::Cognito::UserPoolDomain
		// models the domain as scoped by its pool, but the Terraform
		// resource's own import grammar does not require the scope at
		// all. Same shape as the RDS batch's aws_db_proxy_default_target_group
		// correction: the registry's compound key oversold what the
		// provider actually asks for.
		Type:          "aws_cognito_user_pool_domain",
		Components:    []Component{attr("domain")},
		ImportSyntax:  "DOMAIN",
		IdentityAttrs: []string{"domain"},
	},

	serverAssigned("aws_iam_openid_connect_provider",
		"IAM mints the OIDC provider's own ARN at create time, embedding the required url argument's host with its scheme stripped as a value the provider computes rather than one this table treats as reconstructible; the url argument itself is not the identity. Taggable, so recoverable by tag-filtered list — though the provider ships this type no native list resource in v6.59.0 (live/survey-full.json: list_resource=false), so today's only enumeration route for it is the same registry-only Cloud Control gap live/e2e/estates/storage/README.md documents for aws_efs_file_system, proven at internal/live/discovery's own test tier but not yet reachable from a real run.",
		"ARN", "arn", "id"),
	serverAssigned("aws_iam_policy",
		"IAM mints the policy's own ARN at create time — it embeds the name argument and, when path is set, that too, but the provider's identity schema requires the whole arn as one opaque string (required_for_import=[arn]), not built component-by-component the way aws_sns_topic's account-derived ARN is. Taggable and listable (live/survey-full.json: list_resource=true), so recoverable by ordinary tag-filtered list. row-gen's own registry evidence (Id) undersold this; the IAM/ECR batch above left it out as account-derived follow-on work, but the provider's own required_for_import already names the simpler, schema-literal marker path this row takes instead.",
		"ARN", "arn", "id"),
	TypeIdentity{
		// row-gen proposed this server-assigned via the registry's opaque
		// "Id"; the real, documented import id and Attribute Reference are
		// both the name argument alone (terraform import
		// aws_iam_server_certificate.certificate
		// example.com-certificate-until-2018), the same client-named shape
		// as aws_iam_role, aws_iam_user and aws_iam_group above.
		Type:          "aws_iam_server_certificate",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (PolicyName, GroupName).
		// The provider's real Import section and Argument Reference
		// confirm a colon-joined composite of two arguments already in
		// configuration, group and name (name Optional — Terraform assigns
		// a random one when omitted, the same idiom aws_iam_role_policy
		// above already accepts as "concrete in any realistic config") —
		// the group-policy sibling of that exact row.
		Type: "aws_iam_group_policy",
		Components: []Component{
			attr("group"),
			sep(":"),
			attr("name"),
		},
		ImportSyntax:  "GROUPNAME:POLICYNAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen filed this property-child fold evidence-only, keyed on
		// aws_iam_group once ratified (client-named already, before this
		// batch). The provider's real Import section and Argument
		// Reference confirm a slash-joined composite of two Required
		// arguments already in configuration, group and policy_arn — the
		// group-policy-attachment sibling of aws_iam_role_policy_attachment
		// above, same standard of care: the attachment's own id is
		// provider-internal and is not the import id, so nothing may
		// derive an identity from it.
		Type: "aws_iam_group_policy_attachment",
		Components: []Component{
			attr("group"),
			sep("/"),
			attr("policy_arn"),
		},
		ImportSyntax:  "GROUPNAME/POLICYARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Same shape as the group policy above, the user-policy sibling.
		// row-gen filed this needs-hand-separator (PolicyName, UserName);
		// the real Import section and Argument Reference confirm a
		// colon-joined composite of user and name (name Optional, same
		// idiom).
		Type: "aws_iam_user_policy",
		Components: []Component{
			attr("user"),
			sep(":"),
			attr("name"),
		},
		ImportSyntax:  "USERNAME:POLICYNAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// Same shape as the group policy attachment above, the
		// user-policy-attachment sibling. row-gen filed this property-child
		// fold evidence-only, keyed on aws_iam_user once ratified
		// (client-named already, before this batch); the real Import
		// section and Argument Reference confirm a slash-joined composite
		// of user and policy_arn.
		Type: "aws_iam_user_policy_attachment",
		Components: []Component{
			attr("user"),
			sep("/"),
			attr("policy_arn"),
		},
		ImportSyntax:  "USERNAME/POLICYARN",
		IdentityAttrs: nil,
	},

	serverAssigned("aws_ssoadmin_application",
		"SSO Admin mints the application's own ARN at create time, embedding a pre-existing IAM Identity Center instance ARN and a server-assigned application id; the name argument is client-chosen but is not the import identity. Taggable, so recoverable by tag-filtered list — the provider documents no dedicated data source for enumerating applications, the same enumeration caveat live/e2e/estates/storage/README.md already records for a type this table admits on identity grounds alone.",
		"APPLICATIONARN", "arn", "id"),
	TypeIdentity{
		// row-gen proposed this correctly the first time: the registry's
		// primaryIdentifier=[InstanceArn], entirely createOnlyProperties,
		// matches the provider's own real, documented import id (terraform
		// import aws_ssoadmin_instance_access_control_attributes.example
		// arn:aws:sso:::instance/ssoins-0123456789abcdef) — the instance_arn
		// argument alone. An IAM Identity Center instance is an
		// account-level singleton no CFN type and no provider resource
		// models (there is no aws_ssoadmin_instance type at all), so this
		// argument is always a literal string a configuration copies from
		// the aws_ssoadmin_instances data source rather than a reference to
		// any admitted resource — the same account-scoped-singleton shape
		// aws_ecr_registry_policy's own registry_id has above.
		Type:          "aws_ssoadmin_instance_access_control_attributes",
		Components:    []Component{attr("instance_arn")},
		ImportSyntax:  "INSTANCEARN",
		IdentityAttrs: []string{"instance_arn"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_ssoadmin_permission_set",
		"SSO Admin mints the permission set's own ARN at create time; the name argument is client-chosen but is not the import identity, and the provider's documented import string additionally requires the instance ARN, comma-joined — the same account-level-singleton precedent aws_ecr_registry_policy's own registry_id already set: an IAM Identity Center instance pre-exists any resource, is never created by this fork, and has no admitted resource type of its own to be parent-derived through (no AWS::SSO::Instance CFN type exists, and there is no aws_ssoadmin_instance resource type). No single attribute of this type equals the whole two-ARN import string, so none is offered as an identity source.",
		"PERMISSIONSETARN,INSTANCEARN"),
	TypeIdentity{
		// row-gen filed this needs-hand-separator (six-part primary
		// identifier, InstanceArn/TargetId/TargetType/PermissionSetArn/
		// PrincipalType/PrincipalId, all createOnlyProperties). The
		// provider's real Import section confirms a comma-joined sextuple
		// in a specific documented order — principal_id, principal_type,
		// target_id, target_type, permission_set_arn, instance_arn — of
		// six Required arguments already in configuration. permission_set_arn
		// and instance_arn are references to the two already-admitted
		// marker types just above (aws_ssoadmin_permission_set,
		// unadmitted-instance-singleton), the same "a live parent ARN
		// feeds a literal argument" shape aws_lb_target_group_attachment's
		// target_group_arn already has. live/survey-full.json's own
		// mechanical pass reaches "parent-derived, admission: schema"
		// independently (every required_for_import attribute is a
		// same-named Required argument) — the same double-confirmation
		// aws_dynamodb_resource_policy's own row above records — but a
		// hand row is still written here, the same way that one was, both
		// for the explicit, doc-verified field order and for the cohort
		// estate coverage row.
		Type: "aws_ssoadmin_account_assignment",
		Components: []Component{
			attr("principal_id"),
			sep(","),
			attr("principal_type"),
			sep(","),
			attr("target_id"),
			sep(","),
			attr("target_type"),
			sep(","),
			attr("permission_set_arn"),
			sep(","),
			attr("instance_arn"),
		},
		ImportSyntax:  "PRINCIPAL_ID,PRINCIPAL_TYPE,TARGET_ID,TARGET_TYPE,PERMISSION_SET_ARN,INSTANCE_ARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (ApplicationArn,
		// PrincipalType, PrincipalId). The provider's real Import section
		// confirms a comma-joined triple in a specific documented order —
		// application_arn, principal_id, principal_type — of three
		// Required arguments already in configuration. application_arn
		// is a reference to the already-admitted aws_ssoadmin_application
		// marker above, the same shape as permission_set_arn in the
		// account assignment just above.
		Type: "aws_ssoadmin_application_assignment",
		Components: []Component{
			attr("application_arn"),
			sep(","),
			attr("principal_id"),
			sep(","),
			attr("principal_type"),
		},
		ImportSyntax:  "APPLICATION_ARN,PRINCIPAL_ID,PRINCIPAL_TYPE",
		IdentityAttrs: nil,
	},
)

func init() { registerCohortTable(identityTableIdentity) }
