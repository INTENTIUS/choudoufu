# AWS admission survey

Surveyed 2026-08-11 against the `hashicorp/aws` provider's current schema,
read via `terraform providers schema -json` under Terraform 1.15.8.

This file is the durable artifact for the survey the other docs cite:
`stateless/FAQ.md`'s "65 of the top 68", `stateless/LIMITATIONS.md`'s
`unadmitted-type` entry, and the comment on
`internal/stateless/lint/admission.go`. Until it was committed, the survey
lived in a maintainer-side research note, and the pre-publication ledger
warned that its figures should be treated as an unverified estimate "until a
survey artifact is committed somewhere durable." The Azure and GCP companion
tables were session scratchpad artifacts and are already lost (see the
cross-cloud section at the end). That loss is why this file exists.

## Method

Stated so the table can be re-derived against any future provider release.

1. Dump the provider schema: `terraform providers schema -json`, run under
   Terraform 1.15.8 against the `hashicorp/aws` release current on
   2026-08-11, in a working directory that requires the provider.
2. Take the curated top set: 68 types drawn from
   VPC/EC2/ELB/S3/IAM/Lambda/CloudWatch/RDS/DynamoDB/KMS/SNS/SQS/ECS/ECR/
   EKS/Route53/CloudFront/ACM/Secrets Manager/SSM/API Gateway/Step
   Functions/EFS/EBS.
3. Classify each type by the strongest admission path that recovers its
   identity with no memory, in the rule's order: client-assigned identity
   (the name is in the config), marker (ownership tags, found by
   tag-filtered list), parent-derived (a composite key over admitted
   parents), list plus content match (bind by content, identical siblings
   as a fungible set). A type that fails all four leaves the resource model.
4. While the schema is open, record the raw signals per type: whether it
   takes a `tags` argument, whether a native list resource exists for it,
   and whether the provider ships a resource identity schema for it.

The admission rule itself is documented in the `internal/stateless` package
documentation; `stateless/LIMITATIONS.md`'s `unadmitted-type` entry is the
operator-facing statement of it.

## Summary

| Path | Count |
|---|---|
| Client-named identity | 36 |
| Marker (tags) | 23 |
| Parent-derived | 5 |
| List + content match | 1 |
| Moves to Ops (excluded by the rule) | 3 |
| Residue needing a store | 0 |

65 of 68 (96%) admitted in the resource model. Nothing in the top set
requires memory, so the residual-store row is empty, which is the result the
admission rule was designed to force. The three exceptions are exactly the
shapes the design predicted, two credentials and one waiter, and are
documented below with their forwarding addresses.

## Raw signals

On the 68 curated types: 49 are taggable, 61 have native list resources, 64
have provider identity schemas.

Provider-wide, two substrate findings worth keeping. The provider now ships
`resource_identity_schemas` for 468 types and growing: a per-type
declaration of exactly what identifies the resource, which is the
admission-rule metadata maintained upstream by the provider itself. And 183
native list resources exist already (the query/search work), including
nearly all high-traffic types.

Global stats across all 1,691 AWS resource types, for trajectory: 49%
taggable, 27% identity-schema (mid-rollout), 10% list (early rollout), 62%
tags-or-identity today. The long tail thins out, but usage concentrates in
the head, and both identity and list coverage are actively expanding
upstream.

## Per-type table

Provenance first. The survey note kept the summary counts and examples per
path, not the full 68-row roster, so this table lists only the rows that
can be honestly sourced today, from two places. Rows marked "wired v0" are
the fourteen types already in the fork's admission table
(`internal/stateless/lint/admission.go`) and identity table
(`internal/stateless/identity/table.go`); their path and identity-argument
columns restate what that code asserts. Rows marked "survey note" are the
types the note names as examples for a path; their identity arguments are
left blank, to be pinned when a wiring batch adds them, because writing
them here from memory is what this file exists to stop.

| Type | Path | Identity argument or derivation | Source |
|---|---|---|---|
| aws_vpc | marker | server-assigned VPC ID (vpc-...), recovered by tag-filtered list | wired v0 |
| aws_subnet | marker | server-assigned subnet ID (subnet-...) | wired v0 |
| aws_security_group | marker | server-assigned group ID (sg-...); the group name is not its import identity | wired v0 |
| aws_route_table | marker | server-assigned route table ID (rtb-...) | wired v0 |
| aws_internet_gateway | marker | server-assigned gateway ID (igw-...) | wired v0 |
| aws_eip | list + content match | server-assigned allocation ID (eipalloc-...); fungible set bound by the tofu-slot marker | wired v0 |
| aws_route | parent-derived | route_table_id + one of destination_cidr_block / destination_ipv6_cidr_block / destination_prefix_list_id | wired v0 |
| aws_route_table_association | parent-derived | subnet_id (or gateway_id) + route_table_id | wired v0 |
| aws_iam_role_policy_attachment | parent-derived | role + policy_arn, both client-named, so the composite is concrete in any realistic config | wired v0 |
| aws_s3_bucket | client-named | bucket | wired v0 |
| aws_s3_bucket_policy | client-named | bucket (a named singleton child, one per bucket) | wired v0 |
| aws_iam_role | client-named | name | wired v0 |
| aws_cloudwatch_log_group | client-named | name | wired v0 |
| aws_ssm_parameter | client-named | name | wired v0 |
| aws_iam_user | client-named | | survey note |
| aws_iam_policy | client-named | | survey note |
| aws_lambda_function | client-named | | survey note |
| aws_lambda_permission | client-named | | survey note |
| aws_dynamodb_table | client-named | | survey note |
| aws_eks_cluster | client-named | | survey note |
| aws_route53_record | client-named | | survey note |
| aws_sqs_queue | client-named | | survey note |
| aws_sns_topic | client-named | | survey note |
| aws_instance | marker | | survey note |
| aws_kms_key | marker | | survey note |
| aws_cloudfront_distribution | marker | | survey note |
| aws_db_instance | marker | | survey note |
| aws_route53_zone | marker | | survey note |
| aws_lb_listener | marker | | survey note |
| aws_lb_target_group_attachment | parent-derived | | survey note |
| aws_sns_topic_subscription | parent-derived | | survey note |
| aws_ecs_task_definition | parent-derived | | survey note |
| aws_cloudfront_origin_access_control | list + content match | | survey note |
| aws_iam_access_key | moves to Ops | | survey note |
| aws_secretsmanager_secret_version | moves to Ops | | survey note |
| aws_acm_certificate_validation | moves to Ops | | survey note |

Two classification wrinkles between the survey and the wired code, recorded
rather than smoothed over. The survey's five parent-derived types are
enumerated in full above (route, route_table_association,
lb_target_group_attachment, sns_topic_subscription, ecs_task_definition)
and `aws_iam_role_policy_attachment` is not among them; both of its
components are client-named strings, so the survey presumably counted it
under client-named, while `admission.go` groups it structurally as
parent-derived. Likewise `aws_eip` is taggable, so the survey's
strongest-path classification would be marker, while the fork wires it
through list-plus-content as a fungible set with a tofu-slot marker. The
table above shows each wired type under the path the fork actually
implements.

### Rows awaiting re-derivation

The table names 36 of the 68 types. The remaining 32 rows were in the
survey's counts but not in its kept examples, and this file does not invent
them. By the summary arithmetic they split roughly 21 client-named and 11
marker (give or take the two wrinkles above); among them are the collectives
the note gestures at without naming: four S3 child types beyond the bucket,
three further IAM types, and the per-rule security group resources. The
parent-derived, list-plus-content, and moves-to-Ops rosters are complete as
listed. Re-deriving the missing rows is mechanical by the method section
above, and any wiring batch that adds one of them should fill in its row
here, identity argument included, at the same time.

## The three the rule excludes

Exactly three of the 68 fail the admission rule, and they fail it
permanently: they are out by the rule itself, not by v0 scoping. This is a
different kind of "not admitted" than the 51 surveyed types that are merely
not wired yet, and `stateless/LIMITATIONS.md`'s `unadmitted-type` entry
draws the same distinction.

`aws_iam_access_key` and `aws_secretsmanager_secret_version` are
credentials: identity born server-side alongside a secret that can never be
read again. The access key is the archetype of the true residue. Two active
keys for a user are content-identical, but a third-party system holds one
of them, so set semantics do not apply, and no marker, derivation, or list
can say which is which. The forwarding address is the lifecycle layer: an
Op creates the key pair or secret version and writes the secret to the
secret store, with the key ID riding along in the same entry;
configuration references it by ARN or pointer, never by value. The secret
store was already in the architecture, so no new store appears. This is
the same forwarding `random_password` already has in
`stateless/LIMITATIONS.md`, and OpenTofu's own ephemeral resources and
write-only attributes cover parts of the credential story natively.

`aws_acm_certificate_validation` is a waiter pretending to be a resource:
its entire job is to block until DNS validation completes, and its state
entry records nothing but "the wait finished once." Waiting belongs to the
lifecycle layer's sequencing, the same forwarding address `time_sleep`
has in `stateless/LIMITATIONS.md`.

That the excluded set is exactly credentials plus a waiter, with the
residue row at zero, is the survey's main result: after four admission
paths and credentials-to-Ops, nothing in the AWS top set needs a store.

## Cross-cloud survey (azurerm 4.81, google 7.44, surveyed 2026-08-11)

Same method, run the same day against the other two majors. Headline: both
need markers less than AWS, for opposite reasons.

| Metric | AWS | Azure | GCP |
|---|---|---|---|
| Total types | 1,691 | 1,141 | 1,342 |
| Taggable/labelable | 49% | 35% | 22% |
| Native identity schema | 27% | 15% | 68% |
| Structurally client-derivable | n/a | ~80% | ~75% |
| Curated top set admitted | 65/68 (96%) | 30/30 (100%) | 29/30 (97%) |

Azure: ARM resource IDs are deterministic name-paths
(`/subscriptions/.../resourceGroups/NAME/...`), so about 80% of all types
are client-derivable from name plus resource group plus parent, five times
what the provider's identity-schema metadata advertises. The rollout is a
documentation lag, not a capability gap; a small ARM-path decomposer
recovers it. GCP: names are client-chosen and the provider's native
identity metadata (68%) already tracks the structural ceiling, no parsing
fallback needed. GCP's split-identity idiom (client short ID plus computed
fully-qualified name) is the ARM trick spread across two fields.

The residue pattern held exactly on all three clouds: credentials and
waiters, nothing else. `google_service_account_key` is architecturally
`aws_iam_access_key` (server-assigned ID, content-identical siblings,
external holder); `google_secret_manager_secret_version` mirrors its AWS
twin; Azure's analog is `container_registry_token_password`. All go to Ops
with the secret store, per the design.

A caution that is also this file's origin story: the Azure and GCP
per-type tables were session artifacts in a scratchpad that no longer
exists. The headline numbers above are everything that survives of them.
Treat the Azure/GCP figures as unverified estimates until someone re-runs
the method against those providers and commits the result beside this one.
The AWS survey was a research-note citation away from the same fate, which
is why it now lives here.
