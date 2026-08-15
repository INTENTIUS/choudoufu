# AWS admission survey

Surveyed 2026-08-11 against the `hashicorp/aws` provider's current schema,
read via `terraform providers schema -json` under Terraform 1.15.8.

This file is the durable artifact for the survey the other docs cite:
`live/LIMITATIONS.md`'s `unadmitted-type` entry and the comment on
`internal/live/lint/admission.go`. (`live/FAQ.md` cited it too, as "65 of
the top 68". That file was absorbed into the docs site by issue #79 and its
coverage claim, already stale, was not carried over.) The raw signals and the mechanical
path classification behind the per-type table are regenerated from the
provider schemas by `go run ./tools/survey-gen`, which writes
`live/survey.json`.

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
   and whether the provider publishes a resource identity schema for it.

The admission rule itself is documented in the `internal/live` package
documentation. `live/LIMITATIONS.md`'s `unadmitted-type` entry is the
operator-facing statement of it.

### Re-run of 2026-08-12

The method above was re-run a day later, this time under the fork binary
(`choudoufu providers schema -json`) against
`registry.opentofu.org/hashicorp/aws` v6.58.0. It reproduced the
provider-wide figures below exactly: 1,691 resource types, 842 taggable
(49.8%), 468 `resource_identity_schemas`. Same provider release, then, and
the per-type rows the re-run pinned carry the `schema` derivation tier in
the Source column.

One note the re-run did not observe: the schema JSON that `choudoufu
providers schema -json` emits has no list-resource section, so the raw
signal of native list resources could not be rechecked by that route.
`tools/survey-gen` (issue #25) later closed it by reading one
`GetProviderSchema` response in process, where the list section does
appear, and corrected the figure from the original pass's 61 to 58. The ten
roster types v6.58.0 has no list resource for are
`aws_acm_certificate_validation`, `aws_cloudfront_origin_access_control`,
`aws_db_instance`, `aws_db_parameter_group`, `aws_ecr_lifecycle_policy`,
`aws_ecs_cluster`, `aws_efs_file_system`, `aws_iam_group`,
`aws_iam_instance_profile` and `aws_key_pair`. `aws_db_instance` being
among them matters: the classification note below already says that row
should wire by `identifier` instead of by marker, and an unlistable type
cannot be discovered by marker at all.

The re-run's payoff is that `resource_identity_schemas` answers the
admission question directly. Each entry lists the attributes the provider
needs to import the resource and flags which are `optional_for_import`. In
the current release `account_id` and `region` are almost always optional
(the provider fills them from its configuration), so a type whose only
required identity attribute is `name`, `bucket`, or a pair of client-chosen
strings is client-named under the strict test, and a type whose required
attribute is a bare `arn`, `url`, or server-assigned `id` is not. That test
is what the flag list after the table applies.

## Summary

The table between the markers is rendered by `go run ./tools/survey-gen
-render`: a tally of the per-type table's Path column, counting
`account-derived` under client-named and applying the two exception rows
the prose below records (`aws_iam_role_policy_attachment` and `aws_eip`
are counted by the survey's classing, while the table shows the fork's
path).

<!-- survey-gen:begin summary -->
| Path | Count |
|---|---|
| Client-named identity | 36 |
| Marker (tags) | 23 |
| Parent-derived | 5 |
| List + content match | 1 |
| Moves to Ops (excluded by the rule) | 3 |
| Residue needing a store | 0 |
<!-- survey-gen:end summary -->

65 of 68 (96%) admitted in the resource model. Nothing in the top set
requires memory, so the residual-store row is empty, which is the result the
admission rule was designed to force. The three exceptions, two credentials
and one waiter, are the cases the design predicted, and are documented below
with their forwarding addresses.

## Raw signals

The sentence between the markers is rendered by `go run ./tools/survey-gen
-render` from `live/survey.json`'s counts, so these figures are the
committed roster's, read off the provider's schemas in process. The
original pass hand-recorded 49 taggable and 64 with identity schemas. The
reconstruction footnote below keeps that record and the delta.

<!-- survey-gen:begin raw-signals -->
On the 68 curated types: 47 are taggable, 58 have native list resources, 61
have provider identity schemas.
<!-- survey-gen:end raw-signals -->

Provider-wide, two substrate findings. The provider now publishes
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

Three columns carry fixed vocabularies so the table can be machine-parsed.
Issue #25 proposes generating this table from the provider's
schemas. Until that lands, these are the tokens a generator or a wiring
lane should match on, and nothing outside them appears in those columns.
Prose lives in the identity column only.

`Path` is one of `client-named`, `marker`, `parent-derived`, `list +
content match`, `account-derived`, `moves to Ops`, and is the path the fork
implements where the two differ from the survey's classing (see the
classification notes below).

`account-derived` is the newest token, a refinement of `client-named` that
the summary still counts under it: the name is in the configuration, and
the provider's import identity is that name wrapped in the account and
region of the cloud the run is against. The fork substitutes those two
values through `internal/live/identity`'s `CloudContext`, and a run
that has neither falls back to the marker path. Only the rows the fork
actually wires that way carry the token, which is also what
`tools/survey-gen`'s classifier reads: it looks the type up in the identity
table and asks whether any component names a cloud value, because no schema
distinguishes an ARN that wraps a client-chosen name from one carrying a
server-generated suffix (flag F4 is that case).

`Status` says what stands between the row and working code. Exactly one
token per row.

| Status | Meaning | Rows |
|---|---|---|
| `wired` | in the fork's admission table (`internal/live/lint/admission.go`) and identity table (`internal/live/identity/table.go`) today | <!-- survey-gen:begin wired-count -->878<!-- survey-gen:end wired-count --> |
| `ready` | admissible under the rule with no identity mechanism the fork lacks; wiring it is ordinary work (admission entry, identity entry, a list client where the marker path needs one) | 1 |
| `needs-account-derived` | classification holds, but the import identity embeds the account or region, so wiring is blocked until an identity builder can substitute those components | 0 |
| `ops` | excluded by the rule, forwarded to the lifecycle layer | 3 |
| `blocked-emulator` | admissible, but the e2e emulator cannot serve it, so the row cannot be proven live | 3 |
| `unknown` | path not determined | 0 |

The `wired` count above is the admission table's global size
(`identity.AdmittedTypes`, rendered from the table itself), so it covers
more than the rows below. Most of it is rows below,
classified and wired the way every batch before #40 was, or later
reclassified from `blocked-emulator` by a registry-ratified batch. The
rest comes from the registry-ratified batches (#40, #44, #65). The
first (Lambda) contributed `aws_lambda_capacity_provider`,
`aws_lambda_code_signing_config`, `aws_lambda_event_source_mapping` and
`aws_lambda_layer_version`, plus `aws_lambda_function`, already a row
below and reclassified `wired` in that batch. The second (IAM and ECR,
issue #26) contributed `aws_ecr_registry_policy`,
`aws_ecr_registry_scanning_configuration`,
`aws_ecr_replication_configuration` and `aws_iam_service_linked_role`,
plus `aws_ecr_repository`, `aws_iam_instance_profile` and `aws_iam_user`,
all three already rows below and reclassified `wired` in that batch. The
third (messaging: SQS, SNS beyond `aws_sns_topic`, CloudWatch) contributed
`aws_cloudwatch_composite_alarm`, `aws_cloudwatch_dashboard`,
`aws_cloudwatch_metric_stream`, `aws_sns_topic_policy` and
`aws_sqs_queue_policy`, plus `aws_sqs_queue`, already a row below
(`blocked-emulator`) and reclassified `wired` in that batch despite the
emulator gap that row's note still names (see
`live/e2e/estates/messaging/README.md`). The fourth (EC2 core: instances,
EBS, ENI, issue #65) contributed `aws_placement_group`, `aws_ec2_fleet`,
`aws_ec2_capacity_reservation`, `aws_ec2_host`, `aws_network_interface`,
`aws_network_interface_attachment`, `aws_network_interface_permission`,
`aws_eip_association`, `aws_volume_attachment`, `aws_spot_fleet_request`
and `aws_ebs_snapshot_block_public_access`, plus two rows already below,
`aws_instance` (`blocked-emulator`) and `aws_key_pair` (`ready`), both
reclassified `wired` in that batch (see
`live/e2e/estates/ec2-core/README.md`). Most registry-ratified types have
no row in this table at all: they are outside the curated 68 this survey
measures, reached through `live/registry.json` and `tools/row-gen` (#44),
outside this survey's provider-schema path. The messaging batch also
proposed `aws_sns_topic_subscription`, whose row below stays `ready`: it
classifies cleanly but is deferred for a `live/LIMITATIONS.md` reason
unrelated to its identity (see the same README). Extending this
roster and `live/survey.json` to the full registry-backed universe was
#54's follow-on work. A future batch's roster growth
shows up only in the rendered count above and in
`internal/live/lint/admission.go`, never as a hand edit here.

The fifth (Route53 remainder and CloudFront, #65) reclassified two rows
already below from `blocked-emulator` to `wired`: `aws_cloudfront_distribution`
(the pinned floci image now creates and reads a distribution back cleanly,
since lex00/floci#29's fix landed in it, closing the gap the earlier
`blocked-emulator` note named) and `aws_cloudfront_origin_access_control`
(floci creates and lists origin access controls cleanly, and the row was
never blocked on identity, only on the emulator). Unlike the messaging batch's
`aws_sns_topic_subscription`, this batch's untaggable curated-68 type,
`aws_cloudfront_origin_access_control` itself, did not need a deferral:
issue #54 landed between the two batches, and `live/LIMITATIONS.md`'s
untaggable-admitted span now derives from `live/survey-full.json` across the
whole registry-backed roster instead of the curated 68 intersected
with the admission table, so admitting an untaggable curated-68 type needs
only the same mechanical doc fixup as any other untaggable type. See
`live/e2e/estates/route53-cloudfront/README.md`.

The `blocked-emulator` rows were found by the #19 and #20 wiring lanes, by
probing each candidate end to end through the provider against the harness's
`floci/floci:latest` image before wiring. CLI round-trips are not enough,
since `aws_instance`'s probe looked fine from the CLI and still died in the
provider's create waiter. Each such row names what failed in its identity
column, with the issue tracking the gap. Issue choudoufu#26 is the
collection point: when the harness adopts an image carrying the floci fixes,
these rows rejoin their wiring lanes.
`needs-account-derived` is empty as of the #20/#21 wiring lane, which built
the mechanism it was waiting for: `internal/live/identity`'s
`CloudContext` substitutes an account and a region into an identity
template, and a run with neither classifies the instance as needing
discovery instead of failing. F1 and F2 are wired on it, F3 is blocked by
the emulator alone, and F4 turned out not to be an
account-derivation problem at all. The token stays in the vocabulary
because the next provider survey may find rows that need it again.

`blocked-emulator` was empty in the first pass, held twenty rows at its
2026-08-13 peak, and holds three now after the ratification batches and
the 2026-08-14 status reconciliation,
all of them found by wiring lanes probing each candidate against floci.
Each names its gap and its tracking issue in
the identity column, and choudoufu#26 is the umbrella. Six of the gaps
(Lambda, EKS, RDS, EC2 run-instances, SQS queue URLs, the CloudFront
lifecycle) were fixed upstream on 2026-08-12 (lex00/floci#26, #27, #28,
#29, #32 and #34 all closed that evening), but no pullable harness image
carries the fixes yet, so their rows stay blocked until the republished
image lands and a probe proves them live. These rows say nothing about real
AWS, which is why the token is kept separate from `ready`.

`Source` is two tokens, provenance then derivation tier. Provenance is
`survey note` for the 36 types the original note named, whether as a
worked example or by way of the code that was wired from it, and `roster
fit` for the 32 the note counted but did not name, reconstructed as the
footnote below describes. Derivation tier is `schema` where the identity
argument comes from `resource_identity_schemas` in the 2026-08-12 re-run
(61 rows) and `docs` where v6.58.0 has no identity schema for the type
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
| aws_iam_user | client-named | wired | name; registry-ratified (#40, #44, choudoufu#26), outside this survey's provider-schema path; floci's iam:GetUser now returns Tags on the pinned image, so the earlier blocked-emulator note (floci's iam:GetUser omits Tags, the GetRole gap family) no longer holds | survey note, registry; schema |
| aws_lambda_function | client-named | wired | function_name; registry-ratified (#40, #44), outside this survey's provider-schema path; floci's current image creates and destroys it cleanly, so the earlier blocked-emulator note (lex00/floci#26) no longer holds | survey note, registry; schema |
| aws_lambda_permission | client-named | wired | function_name + statement_id, optionally qualifier (the optional-qualifier import shape stays out of the 2-component row); ratified 2026-08-15 (#175) | survey note; schema |
| aws_eks_cluster | client-named | wired | name; blocked: floci cannot create EKS clusters (lex00/floci#27) Row corrected 2026-08-14: the type is in the admission and identity tables, so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | survey note; schema |
| aws_route53_record | client-named | wired | zone_id + name + type, plus set_identifier for weighted and latency sets; the fork wires it as a composite through the aws_route53_zone marker, since the Z-ID is the zone's server-assigned identity (see the classification notes below) | survey note; schema |
| aws_kms_key | marker | wired | server-assigned key ID (a UUID); the alias is a separate resource | survey note; schema |
| aws_iam_policy | client-named | wired | name + path, but the required import attribute is the policy ARN; the account-derived mechanism builds it, and floci's iam:GetPolicy omits Tags so the row cannot be proven live (choudoufu#26) Row corrected 2026-08-14: the type is in the admission and identity tables, so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | survey note; schema |
| aws_sqs_queue | account-derived | wired | name, wrapped in the run's region and account as https://sqs.REGION.amazonaws.com/ACCOUNT/NAME; registry-ratified (#40, #44) despite the gap this row was blocked on: floci still reports a queue's URL under its own endpoint instead of the amazonaws.com form the provider's importer parses, so the marker path a context-less run takes still cannot complete (choudoufu#26), but a plain apply against floci creates and destroys the type cleanly regardless (see live/e2e/estates/messaging/README.md) | survey note, registry; schema |
| aws_sns_topic | account-derived | wired | name, wrapped in the run's region and account as arn:aws:sns:REGION:ACCOUNT:NAME | survey note; schema |
| aws_instance | marker | wired | server-assigned instance ID (i-...); registry-ratified (#40, #44, issue #65) despite the gap this row was blocked on: lex00/floci#32 ("floci jumps a new instance straight to `terminated`") closed upstream 2026-08-12, and a manual apply against the pinned harness image during the EC2 core batch's ratification created and destroyed the type cleanly with no waiter hang; a same-evening reprobe recorded the published image still terminating, but a 2026-08-14 run-instances probe against the same pinned digest reached `running` and stayed there, so the clean behavior is the one that reproduces (choudoufu#26; see live/e2e/estates/ec2-core/README.md) | survey note; schema |
| aws_cloudfront_distribution | marker | wired | server-assigned distribution ID; registry-ratified (#40, #44, #65), outside this survey's provider-schema path; the pinned floci image now creates and reads a distribution back cleanly (lex00/floci#29's lifecycle fix landed in the image this checkout pins), so the earlier blocked-emulator note no longer holds; floci's resourcegroupstagging still covers no CloudFront, an open question for the marker-discovery wiring lane; admission is unaffected | survey note, registry; schema |
| aws_db_instance | marker | wired | taggable, but v6.58.0 has neither an identity schema nor a list resource for it, and `identifier` is the documented import ID, so it wires client-named when unblocked (see the classification notes below); RDS needs the Docker socket mounted into the emulator (lex00/floci#28, choudoufu#26) Row corrected 2026-08-14: the type is in the admission and identity tables, so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | survey note; docs |
| aws_route53_zone | marker | wired | server-assigned hosted zone ID (Z...); the identity schema names zone_id instead of id | survey note; schema |
| aws_lb_listener | marker | wired | server-assigned listener ARN | survey note; schema |
| aws_lb_target_group_attachment | parent-derived | wired | target_group_arn + target_id + port, comma-joined; availability_zone is accepted and never read back, so an attachment that sets it re-plans forever | survey note; schema |
| aws_sns_topic_subscription | parent-derived | wired | subscription ARN: the parent topic ARN plus a server-assigned UUID suffix, which neither derivation nor a marker recovers Row corrected 2026-08-14: the type is in the admission and identity tables, so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | survey note; schema |
| aws_secretsmanager_secret | client-named | wired | name in config, but the required import attribute is the secret ARN, whose six-character server-generated suffix no account/region template reconstructs; deferred, and ready by the marker path since the type is taggable Row corrected 2026-08-14: the type is in the admission and identity tables, so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | roster fit; schema |
| aws_ecs_task_definition | parent-derived | wired | family + revision, the revision assigned server-side per registration Row corrected 2026-08-14: the type is in the admission and identity tables, so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | survey note; schema |
| aws_cloudfront_origin_access_control | list + content match | wired | server-assigned OAC ID, recovered by listing and matching on the required, AWS-enforced-unique "name" argument, since the type carries no tags; registry-ratified (#40, #44, #65), outside this survey's provider-schema path; the pinned floci image creates and lists OACs cleanly, so the earlier blocked-emulator note no longer holds | survey note, registry; docs |
| aws_iam_access_key | moves to Ops | ops | server-assigned access key ID (AKIA...), and the secret half is unreadable after create. The REMAINDER batch briefly admitted it against this rule; #125 ruled for the exclusion and the admission was removed 2026-08-14 | survey note; schema |
| aws_secretsmanager_secret_version | moves to Ops | ops | secret_id + server-assigned version_id (a UUID) | survey note; schema |
| aws_acm_certificate_validation | moves to Ops | ops | certificate_arn, recording only that the wait finished | survey note; schema |
| aws_s3_bucket_versioning | client-named | wired | bucket (named singleton child) | roster fit; schema |
| aws_s3_bucket_public_access_block | client-named | wired | bucket | roster fit; schema |
| aws_s3_bucket_server_side_encryption_configuration | client-named | wired | bucket | roster fit; schema |
| aws_s3_bucket_lifecycle_configuration | client-named | wired | bucket | roster fit; schema |
| aws_iam_instance_profile | client-named | wired | name; registry-ratified (#40, #44, choudoufu#26), outside this survey's provider-schema path; floci's iam:GetInstanceProfile now returns Tags on the pinned image (probed live during ratification), so the earlier blocked-emulator note (probed 2026-08-12) no longer holds | roster fit, registry; schema |
| aws_iam_role_policy | client-named | wired | role + name, both client-named, so concrete wherever the role is | roster fit; schema |
| aws_iam_group | client-named | wired | name; no identity schema in v6.58.0, import ID documented as the group name Row corrected 2026-08-14: the type is in the admission and identity tables, so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | roster fit; docs |
| aws_autoscaling_group | client-named | wired | name; tags come as `tag` blocks instead of a `tags` argument, so the marker path is not open to it Row corrected 2026-08-14: the type is in the admission and identity tables, so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | roster fit; schema |
| aws_key_pair | client-named | wired | key_name; no identity schema in v6.58.0, import ID documented as key_name; registry-ratified (#40, #44, issue #65; see live/e2e/estates/ec2-core/README.md) | roster fit; docs |
| aws_cloudwatch_metric_alarm | client-named | wired | alarm_name | roster fit; schema |
| aws_cloudwatch_event_rule | client-named | wired | name; import ID is event_bus_name/name, the bus defaulting to `default` when omitted - the Component.Default fallback the 2026-08-15 reversal built (#175) | roster fit; schema |
| aws_db_subnet_group | client-named | wired | name; blocked: floci's rds:ListTagsForResource serves no tags back, so the written marker never reads back (probed 2026-08-12; choudoufu#26) Row corrected 2026-08-14: the type is in the admission and identity tables, so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | roster fit; schema |
| aws_db_parameter_group | client-named | wired | name; no identity schema in v6.58.0, import ID documented as the group name; blocked: floci's rds:ListTagsForResource serves no tags back (probed 2026-08-12; choudoufu#26) Row corrected 2026-08-14: the type is in the admission and identity tables, so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | roster fit; docs |
| aws_kms_alias | client-named | wired | name, the full `alias/...` string | roster fit; schema |
| aws_ecs_service | client-named | wired | cluster + name, the cluster itself client-named and defaulting server-side when omitted; the 2022-era rejection cited an ecs-svc/DEPLOYMENTID import shape the 6.59.0 page no longer documents, so the veto reversed 2026-08-15 (#175) | roster fit; schema |
| aws_ecr_repository | client-named | wired | name; registry-ratified (#40, #44, choudoufu#26), outside this survey's provider-schema path; floci's ecr:CreateRepository no longer needs a Docker daemon on the pinned image, so the earlier blocked-emulator note no longer holds | roster fit, registry; schema |
| aws_ecr_lifecycle_policy | client-named | wired | repository (one policy per repository); blocked: needs a live parent repository, which floci cannot create (choudoufu#26) Row corrected 2026-08-14: the type is in the admission and identity tables, so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | roster fit; schema |
| aws_eks_node_group | client-named | wired | cluster_name + node_group_name; blocked: needs a live parent cluster, which floci cannot create (lex00/floci#27) node_group_name is Optional+Computed (Terraform assigns a random name when omitted), the name-generation idiom aws_iam_role's own row already carries, ratified by hand the same way. Row corrected 2026-08-14: the type is in the admission and identity tables (registry-ratified batches), so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | roster fit; schema |
| aws_ssm_document | client-named | blocked-emulator | name; blocked: floci answers ssm:CreateDocument with UnsupportedOperation (probed 2026-08-12; choudoufu#26) | roster fit; schema |
| aws_vpc_security_group_ingress_rule | marker | wired | server-assigned rule ID (sgr-...), taggable, one resource per rule | roster fit; schema |
| aws_vpc_security_group_egress_rule | marker | wired | server-assigned rule ID (sgr-...), taggable | roster fit; schema |
| aws_launch_template | marker | wired | server-assigned template ID (lt-...); `name` is client-chosen but the identity schema requires the ID | roster fit; schema |
| aws_nat_gateway | marker | wired | server-assigned gateway ID (nat-...); blocked: the provider reads subnet_id out of the NatGatewayAddresses list, which floci returns empty, so an imported gateway loses its subnet and every plan proposes replacement (probed 2026-08-12; choudoufu#26) Row corrected 2026-08-14: the type is in the admission and identity tables (registry-ratified batches), so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | roster fit; schema |
| aws_lb | marker | wired | server-assigned load balancer ARN | roster fit; schema |
| aws_lb_target_group | marker | wired | server-assigned target group ARN | roster fit; schema |
| aws_acm_certificate | marker | wired | server-assigned certificate ARN | roster fit; schema |
| aws_api_gateway_rest_api | marker | wired | server-assigned REST API ID; blocked: floci serves no status for a created REST API, so the provider's availability waiter dies waiting for AVAILABLE (probed 2026-08-12; choudoufu#26) Row corrected 2026-08-14: the type is in the admission and identity tables (registry-ratified batches), so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | roster fit; schema |
| aws_sfn_state_machine | marker | wired | server-assigned state machine ARN | roster fit; schema |
| aws_efs_file_system | marker | wired | server-assigned file system ID (fs-...); no identity schema in v6.58.0, `creation_token` is client-chosen but is not the import ID; v6.58.0 also has no list resource for it, so the marker path cannot enumerate it until the provider adds one (the aws_db_instance situation again, found by the #20 third-slice lane) Row corrected 2026-08-14: the type is in the admission and identity tables (registry-ratified batches), so the status is wired regardless of what the emulator note describes - found by TestRosterStatusAgreesWithAdmission, the #91-class drift check (#100). | roster fit; docs |
| aws_ebs_volume | marker | wired | server-assigned volume ID (vol-...) | roster fit; schema |

Some classifications differ between the survey and the wired code. The
differences are recorded here. The survey's five parent-derived types are
enumerated in full above (route, route_table_association,
lb_target_group_attachment, sns_topic_subscription, ecs_task_definition)
and `aws_iam_role_policy_attachment` is not among them. Both of its
components are client-named strings, so the survey presumably counted it
under client-named, while `admission.go` groups it structurally as
parent-derived. Likewise `aws_eip` is taggable, so the survey's
strongest-path classification would be marker, while the fork wires it
through list-plus-content as a fungible set with a tofu-slot marker. And
`aws_route53_record` keeps its client-named row (its name and type are
client-chosen) while the wired code composes its import ID through the
`aws_route53_zone` marker, because the third component is the zone's
server-assigned Z-ID (flag F5). The table above shows each wired type
under the path the fork actually implements, except where a row's note
says otherwise.

A third difference surfaced in the re-run and points the other way.
`aws_db_instance` sits under marker, but its documented import ID is
`identifier`, a client-chosen string, so the strongest-path rule would put
it under client-named. The provider has no identity schema for it, which
is probably why the original pass fell back to taggability. The row stays
where the survey put it, since moving it would break the summary counts,
but a wiring batch that reaches RDS should expect to admit it by name. The
generator later added a second reason to expect that: v6.58.0 has no list
resource for the type either, so the marker path could not enumerate it at
all. The row is `wired` today (the 2026-08-14 reconciliation), and proving
it live still waits on floci serving RDS (Docker socket, lex00/floci#28),
which is now an emulator-proof gap rather than an admission one.

A fourth is the `account-derived` token itself. `aws_sns_topic` and
`aws_sqs_queue` are both client-named in the survey's classing and stay
counted that way in the summary, while the table shows each under the path
the fork implements, the same treatment `aws_eip` gets, now that the
messaging batch wired the queue's identity table entry the same
account/region-template way as the topic's.

### How the roster was reconstructed

The survey note kept per-path counts and per-path examples, but not the
68-row roster. Thirty-six rows carry `survey note` provenance: the types the note
named as examples, plus the fourteen that were wired from it before this
pass. The other thirty-two are inference to fit the counts, sixteen of which
have since been wired by the #19, #20 and #21 lanes. Of the sixty-one
`wired` rows today, thirty-two source from `survey note` and twenty-nine
from `roster fit` (seven of those also cite the registry). In survey
terms the sourced rows are 15 client-named, 12 marker,
and complete rosters for parent-derived (5), list-plus-content (1) and
moves-to-Ops (3), which leaves exactly 21 client-named and 11 marker to
find. The roster was then filled from the most-used types of the curated
services, preferring the collectives the note gestures at without naming
(four S3 child types beyond the bucket, three further IAM types, the
per-rule security group resources) and covering every curated service at
least once. Each chosen type's path and identity argument is then real,
read off the live schema, and it is only the choice of type that is
inferred. A future schema re-run with the original roster in hand may swap
individual rows.

Two secondary signals measure the fit, and it is close but not exact. The
recorded raw signals for the 68 are 49 taggable and 64 with
identity schemas. This reconstruction yields 47 and 61. The gap is small
and in the direction you would expect from a roster picked for service
coverage over those statistics: the S3 children and the IAM
composites are untagged, and five of the picks have no identity schema
where the original roster can have held at most two such rows among its
unnamed ones. Types that would close it, if a later pass wants them, are
`aws_rds_cluster`, `aws_network_interface`, `aws_vpc_endpoint`,
`aws_apigatewayv2_api` and `aws_lb_listener_rule`, all taggable with
identity schemas. They were passed over only because the hinted collectives
had the stronger claim on the slots.

### Strict client-named test

The 2026-08-12 re-run applied a stricter test than the original survey to
every client-named and parent-derived row: can the import identity be built
from config arguments alone, with no call to AWS and no knowledge of the
account? Six rows fail it. They keep their survey path, because the summary
counts are the survey's result and this file does not restate them, and
they carried status `needs-account-derived` in the per-type table until
the wiring lanes below picked them up. The Status table's zero for that
row is the after-state.

Which component each one needs is recorded on the wiring lanes that picked
them up: issue #19 for the client-named rows, issues #20 and #21 for the
rest. Five of the six are settled and the per-type rows above carry the
outcome, so only a summary of the finding is repeated here. Two further
rows fail the strict test on a parent component instead of the account:
`aws_route53_record` (the zone's Z-ID) and `aws_sns_topic_subscription`
(the topic ARN). Both need parent resolution, which the fork already has,
and the record is wired through the `aws_route53_zone` marker today.

The account-derived mechanism `internal/live/identity`'s
`CloudContext` provides is exact for the two rows whose identity is a
configured name wrapped in an account and a region, and both are wired
today. `aws_sns_topic`'s ARN is canonical from every angle. The queue's URL
is canonical too, and the emulator's is not: floci reports
`http://localhost:4566/ACCOUNT/NAME`, the AWS provider's importer parses
only the `amazonaws.com` form, and marker discovery hands the import the
one string it will refuse. No run in this fork can yet supply a
`CloudContext`, so a context-less run still reaches a queue through its
marker instead of the account-derived template, and that marker path is
where the emulator gap bites (choudoufu#26). A plain apply against
floci creates and destroys the type cleanly regardless, which is what let
the messaging batch wire `aws_sqs_queue` anyway. Real AWS has no such gap.

The mechanism did not close `aws_iam_policy` either, and there too the
obstacle is the emulator: the template expresses
an IAM ARN's empty region segment perfectly well, and floci's
`iam:GetPolicy` omits `Tags` the way its `iam:GetRole` does. It was never
going to close `aws_secretsmanager_secret`, whose ARN carries a suffix
generated per secret. That row is deferred to the marker path it is already
taggable for. The two parent-component rows are unchanged:
`aws_route53_record` resolves through its zone, and
`aws_sns_topic_subscription` needs more than parent resolution, since the
UUID in its ARN has no source in configuration and the type takes no tags,
which leaves only a list-plus-content match on protocol and endpoint.

## What the classifier does not settle

`tools/survey-gen/survey_gen_test.go`'s `pathExceptions` table names every
row above where the schema-derived classifier and this file's hand-written
Path column disagree for a documented reason. It is the measured size of
the opentofu#2854 gap: the AWS provider has not yet published identity
schemas precise enough for the strict client-named rule to prove some of
these types by schema alone, so the hand row's judgment stands in. Five
cohorts:

| Cohort | Count |
|---|---|
| Name-prefix idiom (Optional+Computed identifying argument) | 12 |
| Account-derived import identity, not yet wired | 2 |
| Docs tier (no identity schema in v6.58.0) | 5 |
| Fork-wiring wrinkle (deliberate, permanent) | 4 |
| Parent component the survey keeps client-named | 1 |
| **Total** | **24** |

24 is the number to watch: it should shrink release by release as the
provider adds the identity schemas opentofu#2854 tracks, and
`TestExceptionCohortCounts` fails the moment `pathExceptions` moves without
this table following it. The per-type detail (which attribute, which
flag) stays in the test, and each row there also carries a `choudoufu#NN`
tracking reference or an explicit `permanent` marker, so a reader can tell
which exceptions should shrink the count and which are fork design that
will not (`TestExceptionTracking`).

## The three the rule excludes

Exactly three of the 68 fail the admission rule, and they fail it
permanently: the rule itself excludes them, and no later scope change
readmits them. This is a
different kind of "not admitted" than the surveyed types that are merely
not wired yet (4 of them at the 61 types wired today: the `ready` and
`blocked-emulator` rows above), and `live/LIMITATIONS.md`'s
`unadmitted-type` entry draws the same distinction.

`aws_iam_access_key` and `aws_secretsmanager_secret_version` are
credentials: identity born server-side alongside a secret that can never be
read again. The access key is the clearest case of true residue. Two active
keys for a user are content-identical, but a third-party system holds one
of them, so set semantics do not apply, and no marker, derivation, or list
can say which is which. The forwarding address is the lifecycle layer: an
Op creates the key pair or secret version and writes the secret to the
secret store, with the key ID riding along in the same entry.
Configuration references it by ARN or pointer, never by value. The secret
store was already in the architecture, so no new store appears. This is
the same forwarding `random_password` already has in
`live/LIMITATIONS.md`, and OpenTofu's ephemeral resources and
write-only attributes cover parts of the credential story natively.

`aws_acm_certificate_validation` is a waiter in resource form:
its entire job is to block until DNS validation completes, and its state
entry records only that the wait finished once. Waiting belongs to the
lifecycle layer's sequencing, the same forwarding address `time_sleep`
has in `live/LIMITATIONS.md`.

The survey's main result is that the excluded set is credentials plus a
waiter and the residue row is zero: after four admission
paths and credentials-to-Ops, nothing in the AWS top set needs a store.
