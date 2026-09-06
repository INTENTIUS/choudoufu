# identity cohort

Cohort: `identity`. Ratified by: the fifth registry-backed ratification
batch against #40's strategy and #44's row generator, covering issue #65's
"identity services" batch — Cognito (user pools and identity pools), IAM
leftovers `tools/row-gen` still proposed after the IAM/ECR batch, and SSO
Admin (permission sets, applications, account and application assignments).
Mechanism: #48 (see `live/e2e/estates/example/README.md` for the
proof-of-mechanism cohort, and `live/e2e/estates/lambda/README.md` for the
first registry-backed cohort this one follows).

This cohort exercises every type this batch ratified into
`internal/live/lint/admission.go` and `internal/live/identity/table.go`: 22
types across three CFN services (Cognito, IAM, SSO), rejecting or deferring
15 more. Two row-gen proposals, `aws_iam_saml_provider` and
`aws_iam_virtual_mfa_device`, were already rejected by the IAM/ECR batch on
ARN-embedding grounds and are not re-litigated here; `aws_iam_access_key` is
excluded the same way that batch excluded it, per `live/SURVEY.md`'s
standing credential rule. See `internal/live/identity/table.go` for the
per-type evidence.

Identity Store (`aws_identitystore_group`, `aws_identitystore_group_membership`)
is in this batch's scope but ratifies nothing: both types' server-assigned
half (`GroupId`, `MembershipId`) is never a configuration argument, and
neither is taggable, so no admission path recovers either — see "Rejected"
below.

## Coverage map

### Marker path (server-assigned, taggable)

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| `aws_cognito_identity_pool.app` | Cognito Identity mints the identity pool's own id (a `REGION:UUID` string) at create time; the `identity_pool_name` argument is client-chosen but is not the import identity. |
| `aws_cognito_user_pool.app` | Cognito mints the user pool's own id (`region_XXXXXXXXX`) at create time; the `name` argument is client-chosen but is not the import identity. |
| `aws_iam_openid_connect_provider.app` | IAM mints the OIDC provider's own ARN at create time, embedding the required `url` argument's host as a value the provider computes rather than one this table treats as reconstructible. Taggable, but the provider ships this type no native list resource in v6.59.0 (`live/survey-full.json`: `list_resource=false`) — see "A floci gap this batch found" below. |
| `aws_iam_policy.app` | IAM mints the policy's own ARN at create time; the provider's identity schema requires the whole `arn` as one opaque string (`required_for_import=[arn]`), not built component-by-component the way `aws_sns_topic`'s account-derived ARN is. Taggable and listable. row-gen's own registry evidence (`Id`) undersold this; the IAM/ECR batch above left it out as account-derived follow-on work, but the provider's own `required_for_import` already names the simpler, schema-literal marker path this row takes instead. |
| `aws_ssoadmin_application.app` | SSO Admin mints the application's own ARN at create time, embedding a pre-existing IAM Identity Center instance ARN and a server-assigned application id; `name` is client-chosen but is not the import identity. Taggable, but the provider documents no dedicated data source for enumerating applications — see "A floci gap this batch found" below. |
| `aws_ssoadmin_permission_set.app` | SSO Admin mints the permission set's own ARN at create time; `name` is client-chosen but is not the import identity, and the provider's documented import string additionally requires the instance ARN, comma-joined — the same account-level-singleton precedent `aws_ecr_registry_policy`'s own `registry_id` already set: an IAM Identity Center instance pre-exists any resource, is never created by this fork, and has no admitted resource type of its own (no `AWS::SSO::Instance` CFN type exists, and there is no `aws_ssoadmin_instance` resource type). |

### Client-named path

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| `aws_iam_server_certificate.app` | row-gen proposed this server-assigned via the registry's opaque `Id`; the real, documented import id and Attribute Reference are both the `name` argument alone, the same client-named shape as `aws_iam_role`, `aws_iam_user` and `aws_iam_group` above. |
| `aws_ssoadmin_instance_access_control_attributes.app` | row-gen proposed this correctly the first time: the registry's `primaryIdentifier=[InstanceArn]`, entirely `createOnlyProperties`, matches the provider's own real, documented import id — the `instance_arn` argument alone. An IAM Identity Center instance is an account-level singleton no CFN type and no provider resource models, so this argument is always a literal string a configuration copies from the `aws_ssoadmin_instances` data source rather than a reference to any admitted resource — the same account-scoped-singleton shape `aws_ecr_registry_policy`'s own `registry_id` has above. |
| `aws_cognito_user_pool_domain.app` | row-gen proposed this needs-hand-separator on the registry's own evidence (`primaryIdentifier=[UserPoolId, Domain]`). The real provider docs disagree with the registry's own compound key: the documented import command and Argument Reference both settle on the `domain` argument alone — CloudFormation models the domain as scoped by its pool, but the Terraform resource's own import grammar does not require the scope at all. Same shape as the RDS batch's `aws_db_proxy_default_target_group` correction. |

### Parent-derived path (composite, doc-verified separator)

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| `aws_cognito_identity_pool_roles_attachment.app` | row-gen proposed this server-assigned via the registry's opaque `Id`. The real, documented import id is not an independent token at all: it is literally the parent identity pool's own id, verbatim — the same named-singleton-child shape as `aws_s3_bucket_policy`, at most one attachment per pool. |
| `aws_cognito_identity_pool_provider_principal_tag.app` | row-gen filed this needs-hand-separator (`IdentityPoolId`, `IdentityProviderName`). The provider's real Import section and Argument Reference confirm a colon-joined composite of two Required arguments, `identity_pool_id` (the already-admitted pool's own marker-discovered id) and `identity_provider_name` (a literal string) — the same concrete-composite shape as `aws_iam_role_policy`'s `ROLENAME:POLICYNAME`. |
| `aws_cognito_identity_provider.app` | row-gen filed this needs-hand-separator (`UserPoolId`, `ProviderName`). The real Import section and Argument Reference confirm a colon-joined composite of `user_pool_id` and `provider_name`, the same shape as the principal tag above. |
| `aws_cognito_resource_server.app` | row-gen filed this needs-hand-separator (`UserPoolId`, `Identifier`). The provider's real Import section documents a pipe-joined composite (`us-west-2_abc123|https://example.com`) — an unusual separator character, confirmed directly against the raw provider docs source, not inferred. |
| `aws_cognito_user.app` | row-gen filed this needs-hand-separator (`UserPoolId`, `Username`). The real Import section confirms a slash-joined composite. |
| `aws_cognito_user_group.app` | row-gen filed this needs-hand-separator (`UserPoolId`, `GroupName` — the registry's own field name; the provider's Argument Reference calls the same argument `name`). The real Import section confirms a slash-joined composite. |
| `aws_cognito_user_in_group.app` | row-gen filed this needs-hand-separator (`UserPoolId`, `GroupName`, `Username` — a three-part composite, beyond what any earlier batch in this table has hand-wired). The real Import section confirms a comma-joined triple. |
| `aws_iam_group_policy.app` | row-gen filed this needs-hand-separator (`PolicyName`, `GroupName`). The real Import section and Argument Reference confirm a colon-joined composite of `group` and `name` (`name` Optional — Terraform assigns a random one when omitted, the same idiom `aws_iam_role_policy` already accepts as "concrete in any realistic config") — the group-policy sibling of that exact row. |
| `aws_iam_group_policy_attachment.app` | row-gen filed this property-child fold evidence-only, keyed on `aws_iam_group` once ratified. The real Import section and Argument Reference confirm a slash-joined composite of `group` and `policy_arn` — the group-policy-attachment sibling of `aws_iam_role_policy_attachment`, same standard of care: the attachment's own id is provider-internal and is not the import id. |
| `aws_iam_user_policy.app` | Same shape as the group policy above, the user-policy sibling: colon-joined `user` and `name`. |
| `aws_iam_user_policy_attachment.app` | Same shape as the group policy attachment above, the user-policy-attachment sibling: slash-joined `user` and `policy_arn`. |
| `aws_ssoadmin_account_assignment.app` | row-gen filed this needs-hand-separator (a six-part primary identifier, all `createOnlyProperties`). The provider's real Import section confirms a comma-joined sextuple in a specific documented order — `principal_id, principal_type, target_id, target_type, permission_set_arn, instance_arn`. `permission_set_arn` and `instance_arn` are references to the two marker types above, the same "a live parent ARN feeds a literal argument" shape `aws_lb_target_group_attachment`'s `target_group_arn` already has. `live/survey-full.json`'s own mechanical pass reaches "parent-derived, admission: schema" independently — the same double-confirmation `aws_dynamodb_resource_policy`'s own row has — but a hand row is still written, both for the explicit field order and for this cohort's own coverage row. |
| `aws_ssoadmin_application_assignment.app` | row-gen filed this needs-hand-separator (`ApplicationArn`, `PrincipalType`, `PrincipalId`). The provider's real Import section confirms a comma-joined triple in a specific documented order — `application_arn, principal_id, principal_type`. `application_arn` is a reference to the already-admitted `aws_ssoadmin_application` marker above. |

## Rejected

`tools/row-gen`'s Cognito, IAM and SSO service sections proposed several
more types this batch's independent verification rejects, every one
confirmed against the provider's real Import, Argument Reference and
Attribute Reference sections at the pinned v6.59.0 tag
(`github.com/hashicorp/terraform-provider-aws`), not accepted on the
registry's word alone:

- **`aws_iam_group_membership`** — row-gen proposed server-assigned via the
  registry's opaque `Id` (`AWS::IAM::UserToGroupAddition`, whose registry
  entry ships every handler false — create, read, update, delete and
  list — a stub CFN type with no working handler at all). The real
  provider docs settle it further than the registry even tries to:
  v6.59.0's `website/docs/r/iam_group_membership` carries no Import
  section whatsoever, meaning this type simply is not importable in the
  pinned provider release.
- **`aws_cognito_managed_login_branding`** — row-gen filed this
  needs-hand-separator (`UserPoolId`, `ManagedLoginBrandingId`). The real
  import id is exactly that pair, comma-joined — but
  `ManagedLoginBrandingId` is the provider's own Attribute Reference-only
  output ("ID of the managed login branding style"), never a
  configuration argument, so only the `user_pool_id` half is composable.
- **`aws_cognito_user_pool_client`** — row-gen filed this
  needs-hand-separator (`UserPoolId`, `ClientId`). The real import id is
  `UserPoolId/ClientId`, slash-joined, but `ClientId` is Cognito's own
  server-assigned output — no config value reconstructs it. Compounding,
  not the deciding factor: when `generate_secret` is set, the same
  resource also mints a `client_secret` attribute a live read can never
  recover, the same credential shape that excludes `aws_iam_access_key`
  by `SURVEY.md`'s standing rule.
- **`aws_cognito_risk_configuration`** and
  **`aws_cognito_user_pool_ui_customization`** — both key on the same
  unadmitted `client_id` half `aws_cognito_user_pool_client`'s own
  rejection just named — `ui_customization` requires it outright, and
  `risk_configuration`'s own real import id (`user_pool_id`, or
  `user_pool_id:client_id` when `client_id` is set — the provider's
  Argument Reference marks `client_id` Optional) is a conditionally-shaped
  composite this table's `Components` vocabulary has no way to express
  even before `client_id`'s own problem is reached.
- **`aws_identitystore_group`** and **`aws_identitystore_group_membership`**
  — row-gen filed both needs-hand-separator (`IdentityStoreId/GroupId`,
  `IdentityStoreId/MembershipId` — both confirmed slash-joined against the
  real docs). `GroupId` and `MembershipId` are each the provider's own
  Attribute Reference-only output (a UUID IdentityStore mints at create
  time), so only the `identity_store_id` half is composable — the same
  shape as the two Cognito rejections above, and, like
  `aws_ssoadmin_permission_set` above, scoped by an IAM Identity Center
  singleton this fork has no admitted resource for. Neither is taggable
  (IdentityStore's tagging API is scoped to principals, not groups or
  memberships), so there is no marker path either.

Already rejected, and not re-litigated here: `aws_iam_saml_provider` and
`aws_iam_virtual_mfa_device` were both rejected by the IAM/ECR batch on
ARN-embedding grounds (the registry's read-only `Arn` field is really a
composite of a required `name` argument this provider does not treat as
reconstructible). `aws_iam_access_key` is excluded the same way that batch
excluded it: `SURVEY.md`'s standing credential rule (a create-only secret a
live read can never recover). This batch's own independent look at all
three found nothing that changes the earlier verdict.

Deferred, evidence-only per row-gen with no pastable row to hand-verify
against — the same "not this batch's to decide" standard the IAM/ECR
batch's own `aws_iam_role_policy_attachment` slice used for its siblings:
`aws_ssoadmin_customer_managed_policy_attachment`,
`aws_ssoadmin_customer_managed_policy_attachments_exclusive`,
`aws_ssoadmin_managed_policy_attachment`,
`aws_ssoadmin_managed_policy_attachments_exclusive`,
`aws_ssoadmin_permission_set_inline_policy` and
`aws_ssoadmin_permissions_boundary_attachment` are all property-children of
`AWS::SSO::PermissionSet` that row-gen never generated a row for (its own
note: "parent `aws_ssoadmin_permission_set` is not itself proposed"). That
parent is admitted above, but hand-composing six children row-gen gives no
evidence for at all is a bigger lift than this batch's mandate.
`aws_cognito_log_delivery_configuration` is the same shape on the Cognito
side: evidence-only, untaggable, unlistable, no pastable row.
`aws_identitystore_user` and `aws_cognito_managed_user_pool_client` are
outside row-gen's scope entirely — `live/mapping.json` carries no CFN type
for either (the first is real IAM Identity Center surface CloudFormation's
IdentityStore coverage does not model; the second adopts an existing
Cognito-managed client rather than creating one, the same
`default_*`-adopter shape as `aws_default_vpc`).

## Untaggable types

Fifteen types in the coverage map above carry no `tags` argument in the
AWS provider, confirmed against each one's real Argument Reference:
`aws_cognito_identity_pool_provider_principal_tag`,
`aws_cognito_identity_pool_roles_attachment`, `aws_cognito_identity_provider`,
`aws_cognito_resource_server`, `aws_cognito_user`, `aws_cognito_user_group`,
`aws_cognito_user_in_group`, `aws_cognito_user_pool_domain`,
`aws_iam_group_policy`, `aws_iam_group_policy_attachment`,
`aws_iam_user_policy`, `aws_iam_user_policy_attachment`,
`aws_ssoadmin_account_assignment`, `aws_ssoadmin_application_assignment` and
`aws_ssoadmin_instance_access_control_attributes`. `tools/survey-gen -render`
folds all fifteen into `live/LIMITATIONS.md`'s "Untaggable types" span, the
same generalized-past-the-curated-68 derivation #54 built
(`tools/survey-gen/untaggable_render.go`) and the ECS/EKS and storage
batches' own untaggable rows already use.

## Provenance

| Resource | Kind | Overrides |
|---|---|---|
| `aws_cognito_identity_pool.app` | coverage | none |
| `aws_cognito_identity_pool_provider_principal_tag.app` | coverage | none |
| `aws_cognito_identity_pool_roles_attachment.app` | coverage | `identity_pool_id` and `roles` need apply-time-only fixes `terraform validate` does not catch — see `tools/estate-gen/overrides.go`. |
| `aws_cognito_identity_provider.app` | coverage | `provider_name`, `provider_type`, `provider_details` and `user_pool_id` — see `tools/estate-gen/overrides.go`. |
| `aws_cognito_resource_server.app` | coverage | `user_pool_id` and `name` — see `tools/estate-gen/overrides.go`. |
| `aws_cognito_user.app` | coverage | `user_pool_id` — see `tools/estate-gen/overrides.go`. |
| `aws_cognito_user_group.app` | coverage | `user_pool_id` and `name` — see `tools/estate-gen/overrides.go`. |
| `aws_cognito_user_in_group.app` | coverage | `user_pool_id`, `group_name` and `username` — see `tools/estate-gen/overrides.go`. |
| `aws_cognito_user_pool.app` | coverage | `name` — see `tools/estate-gen/overrides.go`. |
| `aws_cognito_user_pool_domain.app` | coverage | none |
| `aws_iam_group_policy.app` | coverage | `policy` (well-formed JSON) — see `tools/estate-gen/overrides.go`. |
| `aws_iam_group_policy_attachment.app` | coverage | `policy_arn` — see `tools/estate-gen/overrides.go`. |
| `aws_iam_openid_connect_provider.app` | coverage | `url` — see `tools/estate-gen/overrides.go`. |
| `aws_iam_policy.app` | coverage | `policy` (well-formed JSON) — see `tools/estate-gen/overrides.go`. |
| `aws_iam_server_certificate.app` | coverage | none |
| `aws_iam_user_policy.app` | coverage | `policy` (well-formed JSON) — see `tools/estate-gen/overrides.go`. |
| `aws_iam_user_policy_attachment.app` | coverage | `policy_arn` — see `tools/estate-gen/overrides.go`. |
| `aws_ssoadmin_account_assignment.app` | coverage | `permission_set_arn`, `principal_type`, `target_type`, `principal_id`, `target_id` — see `tools/estate-gen/overrides.go`. |
| `aws_ssoadmin_application.app` | coverage | `application_provider_arn` and `name` — see `tools/estate-gen/overrides.go`. |
| `aws_ssoadmin_application_assignment.app` | coverage | `application_arn`, `principal_type`, `principal_id` — see `tools/estate-gen/overrides.go`. |
| `aws_ssoadmin_instance_access_control_attributes.app` | coverage | `instance_arn` — see `tools/estate-gen/overrides.go`. |
| `aws_ssoadmin_permission_set.app` | coverage | `name` — see `tools/estate-gen/overrides.go`. |

Every override's full `Reasons` text (the exact `terraform validate`/apply
error it exists to fix) is in `tools/estate-gen/overrides.go`'s own
`typeOverrides` map, the same provenance format every earlier cohort's
README uses. A handful of cross-resource references this generator's
generic pass cannot derive on its own — `aws_ssoadmin_permission_set.app`'s
and `aws_ssoadmin_application.app`'s own `arn`, `aws_cognito_user_pool.app`'s
`id`, and `aws_iam_policy.app`'s `arn` — are resolved by hand in
`overrides.go`'s conditional-sibling helper functions
(`ssoadminPermissionSetArnRef`, `ssoadminApplicationArnRef`,
`cognitoUserPoolIDRef`, `iamPolicyArnRef`), the same pattern
`eksClusterNameRef` already established.

## Regenerate with

```
go run ./tools/estate-gen -cohort identity -types aws_cognito_identity_pool,aws_cognito_identity_pool_provider_principal_tag,aws_cognito_identity_pool_roles_attachment,aws_cognito_identity_provider,aws_cognito_resource_server,aws_cognito_user,aws_cognito_user_group,aws_cognito_user_in_group,aws_cognito_user_pool,aws_cognito_user_pool_domain,aws_iam_group_policy,aws_iam_group_policy_attachment,aws_iam_openid_connect_provider,aws_iam_policy,aws_iam_server_certificate,aws_iam_user_policy,aws_iam_user_policy_attachment,aws_ssoadmin_account_assignment,aws_ssoadmin_application,aws_ssoadmin_application_assignment,aws_ssoadmin_instance_access_control_attributes,aws_ssoadmin_permission_set -out /tmp/estate-gen/identity
```

`-types` is explicit because this cohort spans three CFN services
(Cognito, IAM, SSO), and `defaultCohortTypes` only auto-derives a cohort
whose admitted types share exactly one CFN service name — the same reason
`apigateway`, `storage`, `ec2-core` and `route53-cloudfront` all pass
`-types` explicitly too.

Regenerating overwrites `identity.tf` only. `iam.tf` (the supporting
`aws_iam_group`/`aws_iam_user` resources, below) is hand-authored and is
not touched by the generator — the same "Supporting, not coverage" pattern
`live/e2e/estates/messaging/iam.tf`'s `aws_iam_role.messaging` already
uses. A regeneration also loses the hand-wired `group`/`user` references
this README's "Verifying by hand" section applied on top of the generic
output (`aws_iam_group.identity.name` / `aws_iam_user.identity.name` in
place of the generic pass's disconnected `"placeholder"` literals) — reapply
them the same way after regenerating, the same maintenance cost
`messaging.tf`'s own hand-wired `role_arn` already carries.

## Files

| File | Contents |
|---|---|
| `versions.tf` | `terraform`/`provider "aws"` blocks, identical in shape to `live/e2e/estate/versions.tf`. |
| `locals.tf` | `estate_tag` — `"identity-cohort"`, distinct from every other cohort's own tag. |
| `identity.tf` | The 22 ratified types. |
| `iam.tf` | Supporting, not coverage: `aws_iam_group.identity` and `aws_iam_user.identity` give `aws_iam_group_policy(_attachment)` and `aws_iam_user_policy(_attachment)` a real group and a real user to attach to — `aws_iam_group` and `aws_iam_user` are already covered elsewhere (`aws_iam_group` by the ECS/EKS batch, `aws_iam_user` by the IAM/ECR batch), not repeated as coverage rows here. |

## Gating

`go test ./internal/live/lint/... ./internal/live/identity/...` picks this
directory up through `internal/live/flocitest.FixtureDirs` (#48's union
pin) — `TestAdmissionTableCoversEstate` and `TestTableCoversFixtureTypes`
require `admittedTypesV0` and `DefaultTable` to cover exactly the union of
`live/e2e/estate/` and every `estates/*` cohort, this one included.

`live/e2e/run.sh` and the gated `internal/live/discovery` floci integration
test still stand up only the demo estate — wiring cohort estates into the
gated apply-against-floci tier is separate follow-on work, the same note
every earlier cohort's own README already makes.

## Verifying by hand

```
docker run -d --rm -p 4611:4566 --name tofu-identity-cohort-verify floci/floci:latest
export AWS_ENDPOINT_URL=http://localhost:4611 AWS_ACCESS_KEY_ID=test \
       AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

terraform init
terraform validate
terraform apply -auto-approve

terraform destroy -auto-approve
docker rm -f tofu-identity-cohort-verify
```

`terraform validate` passes against the real provider release (6.58.0)
this fixture pins — confirming every argument name above is real, not just
registry-plausible.

An `apply` against `floci/floci:latest` was run by hand during
ratification (not wired into any automated tier — see "Gating" above),
against a fixture whose `identity.tf` was hand-adjusted on top of
`estate-gen`'s own output the same way "Regenerate with" describes (the
`aws_iam_group`/`aws_iam_user` wiring in `iam.tf`, plus real
`aws_cognito_user_pool.app`/`aws_cognito_user_group.app`/`aws_cognito_user.app`
sibling references in place of the generic pass's disconnected
placeholders). `_localstack/health` on this floci image reports neither
`cognito-identity` nor `sso`/`ssoadmin`/`identitystore` among its running
services at all — `cognito-idp` (the User Pool surface) and `iam` are both
listed, but incompletely implemented underneath the health check's own
optimistic "running" label, the same split every earlier batch's own
"Verifying by hand" section has found. Ten of this batch's 22 ratified
types create and destroy cleanly:

- `aws_cognito_resource_server.app`, `aws_cognito_user.app`,
  `aws_cognito_user_group.app`, `aws_cognito_user_in_group.app` and
  `aws_cognito_user_pool.app` (the plain User Pool family, once each
  type's argument was pointed at a real sibling pool/group/user instead of
  a synthesized literal).
- `aws_iam_group_policy.app`, `aws_iam_group_policy_attachment.app`,
  `aws_iam_policy.app`, `aws_iam_user_policy.app` and
  `aws_iam_user_policy_attachment.app` (plain IAM policy attachment,
  against the real `aws_iam_group.identity`/`aws_iam_user.identity`
  support resources and the real `aws_iam_policy.app`'s own minted ARN).

Twelve fail to create, every one on `UnsupportedOperation` or
`UnknownOperationException` — the operation is recognized as belonging to
a real AWS API, but floci's own implementation refuses it, not a fixture
defect:

- **Cognito Identity (the whole service) is absent**:
  `aws_cognito_identity_pool.app` (`CreateIdentityPool`:
  `UnknownOperationException: Unknown operation:
  AWSCognitoIdentityService.CreateIdentityPool`) and
  `aws_cognito_identity_pool_roles_attachment.app`
  (`SetIdentityPoolRoles`, same `UnknownOperationException` shape).
  `aws_cognito_identity_pool_provider_principal_tag.app` depends on the
  roles attachment's own `identity_pool_id` and is never attempted as a
  result — Terraform skips a resource whose dependency failed rather than
  erroring it independently.
- **Cognito federation and domain surfaces are unimplemented**:
  `aws_cognito_identity_provider.app` (`CreateIdentityProvider`:
  `UnsupportedOperation`) and `aws_cognito_user_pool_domain.app`
  (`CreateUserPoolDomain`: `UnsupportedOperation`) both fail against a
  real, successfully-created `aws_cognito_user_pool.app` — the User Pool
  itself works; these two specific operations on it do not.
- **IAM's certificate and OIDC surfaces are unimplemented**:
  `aws_iam_server_certificate.app` (`UploadServerCertificate`:
  `UnsupportedOperation`) and `aws_iam_openid_connect_provider.app`
  (`CreateOpenIDConnectProvider`: `UnsupportedOperation`).
- **SSO Admin (the whole service) is absent**:
  `aws_ssoadmin_instance_access_control_attributes.app`
  (`CreateInstanceAccessControlAttributeConfiguration`:
  `UnknownOperationException: Unknown operation:
  SWBExternalService.CreateInstanceAccessControlAttributeConfiguration`).
  `aws_ssoadmin_application.app`, `aws_ssoadmin_permission_set.app`,
  `aws_ssoadmin_account_assignment.app` and
  `aws_ssoadmin_application_assignment.app` all reference this resource's
  own `instance_arn` (directly or transitively) and are never attempted as
  a result, the same dependency-skip shape the identity pool family has
  above.

None of this is evidence against any of the twelve types' admission:
identity and enumeration are properties of the provider and the registry,
not of one emulator's completeness, the same standard every earlier
cohort's own "Verifying by hand" section states. It is, however, a
materially wider gap than earlier batches found — the messaging batch's
CloudWatch gap and the storage batch's EFS/FSx enumeration gap were each
partial; here, two entire AWS services (Cognito Identity, SSO Admin) have
no floci implementation at all, on top of two specific unimplemented
operations each in Cognito User Pools and IAM.

A floci gap this batch found beyond the failed-apply list above:
`aws_iam_openid_connect_provider` and `aws_ssoadmin_application` are both
ratified on identity grounds (a real, documented ARN import) that hold
independent of any emulator, but neither is confirmed reachable by this
fork's own marker-discovery mechanism — the OIDC provider because the
pinned provider ships it no native list resource in v6.59.0
(`live/survey-full.json`: `list_resource=false`, the same shape
`aws_efs_file_system` has in the storage batch, whose Cloud Control
fallback route is "proven at the discovery package's own test tier but not
yet reachable from a real run"), and the SSO Admin application because the
provider's own docs name no dedicated enumeration data source for it at
all. Both admit correctly regardless — see "Marker path" above — with the
enumeration gap recorded here rather than blocking, the same standard the
storage batch's own README already sets.

## Supporting IAM resources, relocated from the hand-written iam.tf

The comment below traveled with a hand-written iam.tf that #108 criterion 4
folded into the generator (the supporting resources are now emitted by
estate-gen itself - see GENERATED.md's provenance table). Kept verbatim as
ratification evidence:

> Supporting, not coverage: aws_iam_group.identity and aws_iam_user.identity
> exist only so aws_iam_group_policy.app/aws_iam_group_policy_attachment.app
> and aws_iam_user_policy.app/aws_iam_user_policy_attachment.app have a real
> group and a real user to attach to, the same "Supporting, not coverage"
> pattern live/e2e/estates/messaging/iam.tf's aws_iam_role.messaging already
> uses by hand (estate-gen's own generic pass has no cross-type alias for
> "group" or "user" the way it does for "role" - see
> tools/estate-gen/gen.go's iamRoleRefExpr comment). Both are themselves
> client-named-shaped exactly the way live/e2e/estate/'s own aws_iam_role.app
> is, and both are already covered there or by the IAM/ECR batch
> (aws_iam_group, aws_iam_user) - not repeated as coverage rows here.

## Per-resource evidence comments, relocated from the pre-fold files

These comment blocks traveled with the hand-maintained .tf files before
#108 criterion 4 made this cohort generator-emitted. The regeneration
replaces them with generated provenance headers; the originals are kept
here verbatim as ratification evidence.

From `identity.tf`:

> Coverage: generated by estate-gen from hashicorp/aws 6.58.0 and the identity table (internal/live/identity/table.go).
> overrides: schema requires "policy" as a plain string, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON policy"); the generic string placeholder is not JSON - the group-policy sibling of aws_s3_bucket_policy's own override above

From `identity.tf`:

> Coverage: generated by estate-gen from hashicorp/aws 6.58.0 and the identity table (internal/live/identity/table.go).
> overrides: schema requires "policy" as a plain string, but the provider validates it is well-formed JSON; the generic string placeholder is not JSON - the user-policy sibling of aws_iam_group_policy's own override above
