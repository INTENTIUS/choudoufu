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

### Re-run of 2026-08-12

The method above was re-run a day later, this time under the fork's own
binary (`choudoufu providers schema -json`) against
`registry.opentofu.org/hashicorp/aws` v6.58.0. It reproduced the
provider-wide figures below exactly: 1,691 resource types, 842 taggable
(49.8%), 468 `resource_identity_schemas`. Same provider release, then, and
the per-type rows the re-run pinned carry the `schema` derivation tier in
the Source column.

One note the re-run did not observe: the schema JSON that `choudoufu
providers schema -json` emits has no list-resource section, so the raw
signal of 61-of-68 native list resources could not be rechecked and is left
as the original pass recorded it.

The re-run's payoff is that `resource_identity_schemas` answers the
admission question directly. Each entry lists the attributes the provider
needs to import the resource and flags which are `optional_for_import`. In
the current release `account_id` and `region` are almost always optional
(the provider fills them from its own configuration), so a type whose only
required identity attribute is `name`, `bucket`, or a pair of client-chosen
strings is client-named under the strict test, and a type whose required
attribute is a bare `arn`, `url`, or server-assigned `id` is not. That test
is what the flag list after the table applies.

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

Three columns carry fixed vocabularies so the table can be parsed rather
than read. Issue #25 proposes generating this table from the provider's
schemas; until that lands, these are the tokens a generator or a wiring
lane should match on, and nothing outside them appears in those columns.
Prose lives in the identity column only.

`Path` is one of `client-named`, `marker`, `parent-derived`, `list +
content match`, `moves to Ops`, and is the path the fork implements where
the two differ from the survey's own classing (see the wrinkles below).

`Status` says what stands between the row and working code. Exactly one
token per row.

| Status | Meaning | Rows |
|---|---|---|
| `wired` | in the fork's admission table (`internal/stateless/lint/admission.go`) and identity table (`internal/stateless/identity/table.go`) today | 16 |
| `ready` | admissible under the rule with no identity mechanism the fork lacks; wiring it is ordinary work (admission entry, identity entry, a list client where the marker path needs one) | 45 |
| `needs-account-derived` | classification holds, but the import identity embeds the account or region, so wiring is blocked until an identity builder can substitute those components (see the flag table) | 4 |
| `ops` | excluded by the rule, forwarded to the lifecycle layer | 3 |
| `blocked-emulator` | admissible, but the e2e emulator cannot serve it, so the row cannot be proven live | 0 |
| `unknown` | path not determined | 0 |

No row carries `blocked-emulator` in this pass. Per-type emulator coverage
was not surveyed here; the token exists so that a wiring lane hitting a gap
like floci-gaps #10 (`stateless/e2e/README.md`) has somewhere to record it
without inventing a vocabulary.

`Source` is two tokens, provenance then derivation tier. Provenance is
`survey note` for the 36 types the original note named, whether as a
worked example or by way of the code that was wired from it, and `roster
fit` for the 32 the note counted but did not name, reconstructed as the
footnote below describes. Derivation tier is `schema` where the identity
argument comes from `resource_identity_schemas` in the 2026-08-12 re-run
(61 rows) and `docs` where v6.58.0 ships no identity schema for the type
and the provider's documented import grammar was read instead (7 rows:
`aws_iam_group`, `aws_key_pair`, `aws_db_parameter_group`,
`aws_ecs_cluster`, `aws_efs_file_system`, `aws_db_instance`,
`aws_cloudfront_origin_access_control`). Provenance and tier are
independent: a `roster fit` row's type was inferred, but its path and
identity argument were derived like every other row's.

| Type | Path | Status | Identity argument or derivation | Source |
|---|---|---|---|---|
| aws_vpc | marker | wired | server-assigned VPC ID (vpc-...), recovered by tag-filtered list | survey note; schema |
| aws_subnet | marker | wired | server-assigned subnet ID (subnet-...) | survey note; schema |
| aws_security_group | marker | wired | server-assigned group ID (sg-...); the group name is not its import identity | survey note; schema |
| aws_route_table | marker | wired | server-assigned route table ID (rtb-...) | survey note; schema |
| aws_internet_gateway | marker | wired | server-assigned gateway ID (igw-...) | survey note; schema |
| aws_eip | list + content match | wired | server-assigned allocation ID (eipalloc-...); fungible set bound by the tofu-slot marker | survey note; schema |
| aws_route | parent-derived | wired | route_table_id + one of destination_cidr_block / destination_ipv6_cidr_block / destination_prefix_list_id | survey note; schema |
| aws_route_table_association | parent-derived | wired | subnet_id (or gateway_id) + route_table_id | survey note; schema |
| aws_iam_role_policy_attachment | parent-derived | wired | role + policy_arn, both client-named, so the composite is concrete in any realistic config | survey note; schema |
| aws_s3_bucket | client-named | wired | bucket | survey note; schema |
| aws_s3_bucket_policy | client-named | wired | bucket (a named singleton child, one per bucket) | survey note; schema |
| aws_iam_role | client-named | wired | name | survey note; schema |
| aws_cloudwatch_log_group | client-named | wired | name | survey note; schema |
| aws_ssm_parameter | client-named | wired | name | survey note; schema |
| aws_dynamodb_table | client-named | wired | name | survey note; schema |
| aws_ecs_cluster | client-named | wired | name; the provider sets id to the cluster ARN, so only name carries the import ID | roster fit; docs |
| aws_iam_user | client-named | ready | name | survey note; schema |
| aws_iam_policy | client-named | needs-account-derived | name + path, but the required import attribute is the policy ARN (see flag F3) | survey note; schema |
| aws_lambda_function | client-named | ready | function_name | survey note; schema |
| aws_lambda_permission | client-named | ready | function_name + statement_id, optionally qualifier | survey note; schema |
| aws_eks_cluster | client-named | ready | name | survey note; schema |
| aws_route53_record | client-named | ready | zone_id + name + type, plus set_identifier for weighted and latency sets (see flag F5) | survey note; schema |
| aws_sqs_queue | client-named | needs-account-derived | name, but the required import attribute is the queue URL (see flag F1) | survey note; schema |
| aws_sns_topic | client-named | needs-account-derived | name, but the required import attribute is the topic ARN (see flag F2) | survey note; schema |
| aws_instance | marker | ready | server-assigned instance ID (i-...) | survey note; schema |
| aws_kms_key | marker | ready | server-assigned key ID (a UUID); the alias is a separate resource | survey note; schema |
| aws_cloudfront_distribution | marker | ready | server-assigned distribution ID | survey note; schema |
| aws_db_instance | marker | ready | taggable, recovered by tag-filtered list; no identity schema shipped, and `identifier` is also the documented import ID (see the wrinkles below) | survey note; docs |
| aws_route53_zone | marker | ready | server-assigned hosted zone ID (Z...) | survey note; schema |
| aws_lb_listener | marker | ready | server-assigned listener ARN | survey note; schema |
| aws_lb_target_group_attachment | parent-derived | ready | target_group_arn + target_id, optionally port and availability_zone | survey note; schema |
| aws_sns_topic_subscription | parent-derived | ready | subscription ARN: the parent topic ARN plus a server-assigned UUID suffix (see flag F6) | survey note; schema |
| aws_ecs_task_definition | parent-derived | ready | family + revision, the revision assigned server-side per registration | survey note; schema |
| aws_cloudfront_origin_access_control | list + content match | ready | server-assigned OAC ID; no identity schema shipped | survey note; docs |
| aws_iam_access_key | moves to Ops | ops | server-assigned access key ID (AKIA...), and the secret half is unreadable after create | survey note; schema |
| aws_secretsmanager_secret_version | moves to Ops | ops | secret_id + server-assigned version_id (a UUID) | survey note; schema |
| aws_acm_certificate_validation | moves to Ops | ops | certificate_arn, recording only that the wait finished | survey note; schema |
| aws_s3_bucket_versioning | client-named | ready | bucket (named singleton child) | roster fit; schema |
| aws_s3_bucket_public_access_block | client-named | ready | bucket | roster fit; schema |
| aws_s3_bucket_server_side_encryption_configuration | client-named | ready | bucket | roster fit; schema |
| aws_s3_bucket_lifecycle_configuration | client-named | ready | bucket | roster fit; schema |
| aws_iam_instance_profile | client-named | ready | name | roster fit; schema |
| aws_iam_role_policy | client-named | ready | role + name, both client-named, so concrete wherever the role is | roster fit; schema |
| aws_iam_group | client-named | ready | name; no identity schema shipped, import ID documented as the group name | roster fit; docs |
| aws_autoscaling_group | client-named | ready | name; tags are `tag` blocks rather than a `tags` argument, so the marker path is not open to it | roster fit; schema |
| aws_key_pair | client-named | ready | key_name; no identity schema shipped, import ID documented as key_name | roster fit; docs |
| aws_cloudwatch_metric_alarm | client-named | ready | alarm_name | roster fit; schema |
| aws_cloudwatch_event_rule | client-named | ready | name; import ID is event_bus_name/name, the bus defaulting to `default` when omitted | roster fit; schema |
| aws_db_subnet_group | client-named | ready | name | roster fit; schema |
| aws_db_parameter_group | client-named | ready | name; no identity schema shipped, import ID documented as the group name | roster fit; docs |
| aws_kms_alias | client-named | ready | name, the full `alias/...` string | roster fit; schema |
| aws_ecs_service | client-named | ready | cluster + name, the cluster itself client-named | roster fit; schema |
| aws_ecr_repository | client-named | ready | name | roster fit; schema |
| aws_ecr_lifecycle_policy | client-named | ready | repository (one policy per repository) | roster fit; schema |
| aws_eks_node_group | client-named | ready | cluster_name + node_group_name | roster fit; schema |
| aws_ssm_document | client-named | ready | name | roster fit; schema |
| aws_secretsmanager_secret | client-named | needs-account-derived | name in config, but the required import attribute is the secret ARN, which carries a server-generated suffix (see flag F4) | roster fit; schema |
| aws_vpc_security_group_ingress_rule | marker | ready | server-assigned rule ID (sgr-...), taggable, one resource per rule | roster fit; schema |
| aws_vpc_security_group_egress_rule | marker | ready | server-assigned rule ID (sgr-...), taggable | roster fit; schema |
| aws_launch_template | marker | ready | server-assigned template ID (lt-...); `name` is client-chosen but the identity schema requires the ID | roster fit; schema |
| aws_nat_gateway | marker | ready | server-assigned gateway ID (nat-...) | roster fit; schema |
| aws_lb | marker | ready | server-assigned load balancer ARN | roster fit; schema |
| aws_lb_target_group | marker | ready | server-assigned target group ARN | roster fit; schema |
| aws_acm_certificate | marker | ready | server-assigned certificate ARN | roster fit; schema |
| aws_api_gateway_rest_api | marker | ready | server-assigned REST API ID | roster fit; schema |
| aws_sfn_state_machine | marker | ready | server-assigned state machine ARN | roster fit; schema |
| aws_efs_file_system | marker | ready | server-assigned file system ID (fs-...); no identity schema shipped, `creation_token` is client-chosen but is not the import ID | roster fit; docs |
| aws_ebs_volume | marker | ready | server-assigned volume ID (vol-...) | roster fit; schema |

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

A third wrinkle surfaced in the re-run and points the other way.
`aws_db_instance` sits under marker, but its documented import ID is
`identifier`, a client-chosen string, so the strongest-path rule would put
it under client-named. The provider ships no identity schema for it, which
is probably why the original pass fell back to taggability. The row stays
where the survey put it, since moving it would break the summary counts,
but a wiring batch that reaches RDS should expect to admit it by name.

### How the roster was reconstructed

The survey note kept per-path counts and per-path examples, not the 68-row
roster. Thirty-six rows carry `survey note` provenance: the types the note
named as examples, plus the fourteen that were wired from it before this
pass. The other thirty-two are inference to fit the counts, one of which
(`aws_ecs_cluster`) has since been wired, which is why fifteen of the
sixteen `wired` rows are `survey note` and one is `roster fit`. In survey
terms the sourced rows are 15 client-named, 12 marker,
and complete rosters for parent-derived (5), list-plus-content (1) and
moves-to-Ops (3), which leaves exactly 21 client-named and 11 marker to
find; the roster was then filled from the most-used types of the curated
services, preferring the collectives the note gestures at without naming
(four S3 child types beyond the bucket, three further IAM types, the
per-rule security group resources) and covering every curated service at
least once. Each chosen type's path and identity argument is then real,
read off the live schema, and it is only the choice of type that is
inferred. A future schema re-run with the original roster in hand may swap
individual rows.

Two secondary signals say how good the fit is, and they say "close, not
exact". The recorded raw signals for the 68 are 49 taggable and 64 with
identity schemas. This reconstruction yields 47 and 61. The gap is small
and in the direction you would expect from a roster picked for service
coverage rather than for those statistics: the S3 children and the IAM
composites are untagged, and five of the picks ship no identity schema
where the original roster can have held at most two such rows among its
unnamed ones. Types that would close it, if a later pass wants them, are
`aws_rds_cluster`, `aws_network_interface`, `aws_vpc_endpoint`,
`aws_apigatewayv2_api` and `aws_lb_listener_rule`, all taggable with
identity schemas; they were passed over only because the hinted collectives
had the stronger claim on the slots.

### Strict client-named test: six flags

The re-run applied a stricter test than the original survey to every
client-named and parent-derived row: can the import identity be built from
config arguments alone, with no call to AWS and no knowledge of the
account? Six rows fail it. They keep their survey path, because the summary
counts are the survey's result and not something this file should quietly
restyle, but the wiring lanes need to see them before they pick up those
types.

F1 through F4 need an account-derived component mechanism, some
account/region pair the identity builder can substitute into a template,
and carry status `needs-account-derived` in the table. F5 and F6 need
parent resolution instead, which the fork already has, so they stay
`ready`; they are listed here because the parent component is easy to miss
when a row's identity reads like a plain name.

| Flag | Type | Status | What breaks | What wiring needs |
|---|---|---|---|---|
| F1 | aws_sqs_queue | needs-account-derived | required import attribute is `url`, `https://sqs.REGION.amazonaws.com/ACCOUNT/NAME` | account and region components |
| F2 | aws_sns_topic | needs-account-derived | required import attribute is `arn`, `arn:aws:sns:REGION:ACCOUNT:NAME` | account and region components |
| F3 | aws_iam_policy | needs-account-derived | required import attribute is `arn`, `arn:aws:iam::ACCOUNT:policy/PATH+NAME` | account component; region is empty for IAM |
| F4 | aws_secretsmanager_secret | needs-account-derived | required import attribute is `arn`, and Secrets Manager appends a six-character server-generated suffix to the name | account and region are not enough; this one needs a name-to-ARN lookup at admission, or a marker |
| F5 | aws_route53_record | ready | identity is `zone_id` + `name` + `type`; `zone_id` is the parent zone's server-assigned Z-ID | parent resolution through the aws_route53_zone marker |
| F6 | aws_sns_topic_subscription | ready | identity is the subscription ARN, the parent topic ARN plus a server-assigned UUID | parent resolution plus a list-and-match on protocol and endpoint |

F1 and F2 were found by the #19 wiring slice and are recorded here so the
next slice does not rediscover them; F3 through F6 are new in the
2026-08-12 re-run. F4 is the one worth arguing about: an account-derived
component will not build that ARN, so `aws_secretsmanager_secret` is either
a marker (it is taggable) or a client-named type that needs a lookup, and
the choice should be made deliberately rather than inherited from this
table.

## The three the rule excludes

Exactly three of the 68 fail the admission rule, and they fail it
permanently: they are out by the rule itself, not by v0 scoping. This is a
different kind of "not admitted" than the surveyed types that are merely
not wired yet (49 of them at the 16 types wired today, the `ready` and
`needs-account-derived` rows above), and `stateless/LIMITATIONS.md`'s
`unadmitted-type` entry draws the same distinction.

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
