# AWS admission survey

Surveyed 2026-08-11 against the `hashicorp/aws` provider's current schema,
read via `terraform providers schema -json` under Terraform 1.15.8.

This file is the durable artifact for the survey the other docs cite:
`live/FAQ.md`'s "65 of the top 68", `live/LIMITATIONS.md`'s
`unadmitted-type` entry, and the comment on
`internal/live/lint/admission.go`. The raw signals and the mechanical
path classification behind the per-type table are regenerated from the
provider's own schemas by `go run ./tools/survey-gen`, which writes
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
   and whether the provider ships a resource identity schema for it.

The admission rule itself is documented in the `internal/live` package
documentation; `live/LIMITATIONS.md`'s `unadmitted-type` entry is the
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
signal of native list resources could not be rechecked by that route.
`tools/survey-gen` (issue #25) later closed it by reading one
`GetProviderSchema` response in process, where the list section does
appear, and corrected the figure from the original pass's 61 to 58. The ten
roster types v6.58.0 ships no list resource for are
`aws_acm_certificate_validation`, `aws_cloudfront_origin_access_control`,
`aws_db_instance`, `aws_db_parameter_group`, `aws_ecr_lifecycle_policy`,
`aws_ecs_cluster`, `aws_efs_file_system`, `aws_iam_group`,
`aws_iam_instance_profile` and `aws_key_pair`. `aws_db_instance` being
among them is worth keeping in view: the wrinkle below already says that
row should wire by `identifier` rather than by marker, and an unlistable
type could not be marker-discovered even if it wanted to be.

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

The table between the markers is rendered by `go run ./tools/survey-gen
-render`: a tally of the per-type table's own Path column, counting
`account-derived` under client-named and applying the two wrinkle rows the
prose below records (`aws_iam_role_policy_attachment` and `aws_eip` count
under the survey's classing, not the table's).

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
admission rule was designed to force. The three exceptions are exactly the
shapes the design predicted, two credentials and one waiter, and are
documented below with their forwarding addresses.

## Raw signals

The sentence between the markers is rendered by `go run ./tools/survey-gen
-render` from `live/survey.json`'s counts, so these figures are the
committed roster's, read off the provider's schemas in process. The
original pass hand-recorded 49 taggable and 64 with identity schemas; the
reconstruction footnote below keeps that record and the delta.

<!-- survey-gen:begin raw-signals -->
On the 68 curated types: 47 are taggable, 58 have native list resources, 61
have provider identity schemas.
<!-- survey-gen:end raw-signals -->

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
content match`, `account-derived`, `moves to Ops`, and is the path the fork
implements where the two differ from the survey's own classing (see the
wrinkles below).

`account-derived` is the newest token and is a refinement of `client-named`
rather than a sixth admission path: the name is in the configuration, and
the provider's import identity is that name wrapped in the account and
region of the cloud the run is against. The fork substitutes those two
values through `internal/live/identity`'s `CloudContext`, and a run
that has neither falls back to the marker path. Only the rows the fork
actually wires that way carry the token, which is also what
`tools/survey-gen`'s classifier reads: it looks the type up in the identity
table and asks whether any component names a cloud value, because no schema
distinguishes an ARN that wraps a client-chosen name from one carrying a
server-generated suffix (flag F4 is exactly that case).

`Status` says what stands between the row and working code. Exactly one
token per row.

| Status | Meaning | Rows |
|---|---|---|
| `wired` | in the fork's admission table (`internal/live/lint/admission.go`) and identity table (`internal/live/identity/table.go`) today | 31 |
| `ready` | admissible under the rule with no identity mechanism the fork lacks; wiring it is ordinary work (admission entry, identity entry, a list client where the marker path needs one) | 16 |
| `needs-account-derived` | classification holds, but the import identity embeds the account or region, so wiring is blocked until an identity builder can substitute those components | 0 |
| `ops` | excluded by the rule, forwarded to the lifecycle layer | 3 |
| `blocked-emulator` | admissible, but the e2e emulator cannot serve it, so the row cannot be proven live | 18 |
| `unknown` | path not determined | 0 |

The `blocked-emulator` rows were found by the #19 and #20 wiring lanes, by
probing each candidate end to end through the provider against the harness's
`floci/floci:latest` image before wiring (CLI round-trips are not enough;
`aws_instance`'s probe looked fine from the CLI and still died in the
provider's create waiter). Each such row names what failed in its identity
column, with the issue tracking the gap. Issue choudoufu#26 is the
collection point: when the harness adopts an image carrying the floci fixes,
these rows rejoin their wiring lanes.
`needs-account-derived` is empty as of the #20/#21 wiring lane, which built
the mechanism it was waiting for: `internal/live/identity`'s
`CloudContext` substitutes an account and a region into an identity
template, and a run with neither classifies the instance as needing
discovery rather than failing. F2 is wired on it; F1 and F3 are blocked by
the emulator rather than by the mechanism, in different places; and F4
turned out not to be an account-derivation problem at all. The token stays
in the vocabulary because the next provider survey may find rows that need
it again.

`blocked-emulator` was empty in the first pass and holds six rows now, all
of them found by wiring lanes probing against floci rather than by a survey
of emulator coverage: `aws_instance`, `aws_db_instance`, the CloudFront
pair, `aws_iam_policy` and `aws_sqs_queue`. Each names its gap and its
tracking issue in the identity column; choudoufu#26 is the umbrella.
Nothing about these rows is a claim about real AWS, which is the point of
keeping the token separate from `ready`.

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
| aws_iam_user | client-named | blocked-emulator | name; blocked: floci's iam:GetUser omits Tags, the GetRole gap family, so ownership can never read back (choudoufu#26) | survey note; schema |
| aws_lambda_function | client-named | blocked-emulator | function_name; blocked: floci cannot create Lambda functions (lex00/floci#26) | survey note; schema |
| aws_lambda_permission | client-named | blocked-emulator | function_name + statement_id, optionally qualifier; blocked: needs a live parent function, which floci cannot create (lex00/floci#26) | survey note; schema |
| aws_eks_cluster | client-named | blocked-emulator | name; blocked: floci cannot create EKS clusters (lex00/floci#27) | survey note; schema |
| aws_route53_record | client-named | wired | zone_id + name + type, plus set_identifier for weighted and latency sets; the fork wires it as a composite through the aws_route53_zone marker, since the Z-ID is the zone's server-assigned identity (see the wrinkles below) | survey note; schema |
| aws_kms_key | marker | wired | server-assigned key ID (a UUID); the alias is a separate resource | survey note; schema |
| aws_iam_policy | client-named | blocked-emulator | name + path, but the required import attribute is the policy ARN; the account-derived mechanism builds it, and floci's iam:GetPolicy omits Tags so the row cannot be proven live (choudoufu#26) | survey note; schema |
| aws_sqs_queue | client-named | blocked-emulator | name, and the required import attribute is the queue URL; the account-derived template builds it, but floci reports a queue's URL as its own endpoint and the provider's importer parses only the amazonaws.com form, so the marker path cannot complete (choudoufu#26) | survey note; schema |
| aws_sns_topic | account-derived | wired | name, wrapped in the run's region and account as arn:aws:sns:REGION:ACCOUNT:NAME | survey note; schema |
| aws_instance | marker | blocked-emulator | server-assigned instance ID (i-...); floci jumps a new instance straight to `terminated` and the provider's create waits for `running` (lex00/floci#32, choudoufu#26) | survey note; schema |
| aws_cloudfront_distribution | marker | blocked-emulator | server-assigned distribution ID; floci serves no usable CloudFront distribution lifecycle (choudoufu#26) | survey note; schema |
| aws_db_instance | marker | blocked-emulator | taggable, but v6.58.0 ships neither an identity schema nor a list resource for it, and `identifier` is the documented import ID, so it wires client-named when unblocked (see the wrinkles below); RDS needs the Docker socket mounted into the emulator (lex00/floci#28, choudoufu#26) | survey note; docs |
| aws_route53_zone | marker | wired | server-assigned hosted zone ID (Z...); the identity schema names zone_id rather than id | survey note; schema |
| aws_lb_listener | marker | wired | server-assigned listener ARN | survey note; schema |
| aws_lb_target_group_attachment | parent-derived | wired | target_group_arn + target_id + port, comma-joined; availability_zone is accepted and never read back, so an attachment that sets it re-plans forever | survey note; schema |
| aws_sns_topic_subscription | parent-derived | ready | subscription ARN: the parent topic ARN plus a server-assigned UUID suffix, which neither derivation nor a marker recovers | survey note; schema |
| aws_secretsmanager_secret | client-named | ready | name in config, but the required import attribute is the secret ARN, whose six-character server-generated suffix no account/region template reconstructs; deferred, and ready by the marker path since the type is taggable | roster fit; schema |
| aws_ecs_task_definition | parent-derived | ready | family + revision, the revision assigned server-side per registration | survey note; schema |
| aws_cloudfront_origin_access_control | list + content match | blocked-emulator | server-assigned OAC ID; no identity schema and no list resource shipped, and floci serves no CloudFront (choudoufu#26) | survey note; docs |
| aws_iam_access_key | moves to Ops | ops | server-assigned access key ID (AKIA...), and the secret half is unreadable after create | survey note; schema |
| aws_secretsmanager_secret_version | moves to Ops | ops | secret_id + server-assigned version_id (a UUID) | survey note; schema |
| aws_acm_certificate_validation | moves to Ops | ops | certificate_arn, recording only that the wait finished | survey note; schema |
| aws_s3_bucket_versioning | client-named | wired | bucket (named singleton child) | roster fit; schema |
| aws_s3_bucket_public_access_block | client-named | wired | bucket | roster fit; schema |
| aws_s3_bucket_server_side_encryption_configuration | client-named | wired | bucket | roster fit; schema |
| aws_s3_bucket_lifecycle_configuration | client-named | wired | bucket | roster fit; schema |
| aws_iam_instance_profile | client-named | blocked-emulator | name; blocked: floci's iam:GetInstanceProfile omits Tags, the GetRole gap family, so the marker never reads back (probed 2026-08-12; choudoufu#26) | roster fit; schema |
| aws_iam_role_policy | client-named | wired | role + name, both client-named, so concrete wherever the role is | roster fit; schema |
| aws_iam_group | client-named | ready | name; no identity schema shipped, import ID documented as the group name | roster fit; docs |
| aws_autoscaling_group | client-named | ready | name; tags are `tag` blocks rather than a `tags` argument, so the marker path is not open to it | roster fit; schema |
| aws_key_pair | client-named | ready | key_name; no identity schema shipped, import ID documented as key_name | roster fit; docs |
| aws_cloudwatch_metric_alarm | client-named | wired | alarm_name | roster fit; schema |
| aws_cloudwatch_event_rule | client-named | ready | name; import ID is event_bus_name/name, the bus defaulting to `default` when omitted | roster fit; schema |
| aws_db_subnet_group | client-named | blocked-emulator | name; blocked: floci's rds:ListTagsForResource serves no tags back, so the written marker never reads back (probed 2026-08-12; choudoufu#26) | roster fit; schema |
| aws_db_parameter_group | client-named | blocked-emulator | name; no identity schema shipped, import ID documented as the group name; blocked: floci's rds:ListTagsForResource serves no tags back (probed 2026-08-12; choudoufu#26) | roster fit; docs |
| aws_kms_alias | client-named | wired | name, the full `alias/...` string | roster fit; schema |
| aws_ecs_service | client-named | blocked-emulator | cluster + name, the cluster itself client-named; blocked: floci's ecs:CreateService demands a task definition even for an EXTERNAL deployment controller, which real ECS does not (probed 2026-08-12; choudoufu#26) | roster fit; schema |
| aws_ecr_repository | client-named | blocked-emulator | name; blocked: floci's ecr:CreateRepository dies starting the backing registry container (choudoufu#26) | roster fit; schema |
| aws_ecr_lifecycle_policy | client-named | blocked-emulator | repository (one policy per repository); blocked: needs a live parent repository, which floci cannot create (choudoufu#26) | roster fit; schema |
| aws_eks_node_group | client-named | blocked-emulator | cluster_name + node_group_name; blocked: needs a live parent cluster, which floci cannot create (lex00/floci#27) | roster fit; schema |
| aws_ssm_document | client-named | blocked-emulator | name; blocked: floci answers ssm:CreateDocument with UnsupportedOperation (probed 2026-08-12; choudoufu#26) | roster fit; schema |
| aws_vpc_security_group_ingress_rule | marker | ready | server-assigned rule ID (sgr-...), taggable, one resource per rule | roster fit; schema |
| aws_vpc_security_group_egress_rule | marker | ready | server-assigned rule ID (sgr-...), taggable | roster fit; schema |
| aws_launch_template | marker | ready | server-assigned template ID (lt-...); `name` is client-chosen but the identity schema requires the ID | roster fit; schema |
| aws_nat_gateway | marker | ready | server-assigned gateway ID (nat-...) | roster fit; schema |
| aws_lb | marker | wired | server-assigned load balancer ARN | roster fit; schema |
| aws_lb_target_group | marker | wired | server-assigned target group ARN | roster fit; schema |
| aws_acm_certificate | marker | ready | server-assigned certificate ARN | roster fit; schema |
| aws_api_gateway_rest_api | marker | ready | server-assigned REST API ID | roster fit; schema |
| aws_sfn_state_machine | marker | ready | server-assigned state machine ARN | roster fit; schema |
| aws_efs_file_system | marker | ready | server-assigned file system ID (fs-...); no identity schema shipped, `creation_token` is client-chosen but is not the import ID | roster fit; docs |
| aws_ebs_volume | marker | ready | server-assigned volume ID (vol-...) | roster fit; schema |

Classification wrinkles between the survey and the wired code, recorded
rather than smoothed over. The survey's five parent-derived types are
enumerated in full above (route, route_table_association,
lb_target_group_attachment, sns_topic_subscription, ecs_task_definition)
and `aws_iam_role_policy_attachment` is not among them; both of its
components are client-named strings, so the survey presumably counted it
under client-named, while `admission.go` groups it structurally as
parent-derived. Likewise `aws_eip` is taggable, so the survey's
strongest-path classification would be marker, while the fork wires it
through list-plus-content as a fungible set with a tofu-slot marker. And
`aws_route53_record` keeps its client-named row — its name and type are
client-chosen — while the wired code composes its import ID through the
`aws_route53_zone` marker, because the third component is the zone's
server-assigned Z-ID (flag F5). The table above shows each wired type
under the path the fork actually implements, except where a row's own note
says otherwise.

A third wrinkle surfaced in the re-run and points the other way.
`aws_db_instance` sits under marker, but its documented import ID is
`identifier`, a client-chosen string, so the strongest-path rule would put
it under client-named. The provider ships no identity schema for it, which
is probably why the original pass fell back to taggability. The row stays
where the survey put it, since moving it would break the summary counts,
but a wiring batch that reaches RDS should expect to admit it by name. The
generator later added a second reason to expect that: v6.58.0 ships no list
resource for the type either, so the marker path could not enumerate it at
all. The row is `blocked-emulator` today (floci needs the Docker socket
mounted to serve RDS, lex00/floci#28), and when that unblocks it wires
client-named by `identifier`.

A fourth is the `account-derived` token itself. `aws_sns_topic` is
client-named in the survey's own classing and stays counted that way in the
summary, while the table shows it under the path the fork implements - the
same treatment `aws_eip` gets. `aws_sqs_queue` is the same shape and keeps
`client-named`, because the fork implements nothing for it yet.

### How the roster was reconstructed

The survey note kept per-path counts and per-path examples, not the 68-row
roster. Thirty-six rows carry `survey note` provenance: the types the note
named as examples, plus the fourteen that were wired from it before this
pass. The other thirty-two are inference to fit the counts, three of which
(`aws_ecs_cluster`, `aws_lb`, `aws_lb_target_group`) have since been wired,
which is why twenty of the twenty-three `wired` rows are `survey note` and
three are `roster fit`. In survey
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

### Strict client-named test

The 2026-08-12 re-run applied a stricter test than the original survey to
every client-named and parent-derived row: can the import identity be built
from config arguments alone, with no call to AWS and no knowledge of the
account? Six rows fail it. They keep their survey path, because the summary
counts are the survey's result and not something this file should quietly
restyle, and they carry status `needs-account-derived` in the per-type
table above where the failure blocks wiring.

Which component each one needs, and the open question about
`aws_secretsmanager_secret`, are recorded on the wiring lanes that will
pick them up: issue #19 for the client-named rows, issue #21 for
`aws_sns_topic_subscription`. Two further rows fail the strict test on a
parent component rather than the account — `aws_route53_record` (the
zone's Z-ID) and `aws_sns_topic_subscription` (the topic ARN) — and need
parent resolution, which the fork already has; the record is wired through
the `aws_route53_zone` marker today.
Which component each one needs is recorded on the wiring lanes that picked
them up: issue #19 for the client-named rows, issues #20 and #21 for the
rest. Five of the six are settled and the per-type rows above carry the
outcome, so only the shape of the finding is worth repeating here.

The account-derived mechanism `internal/live/identity`'s
`CloudContext` provides is exact for the two rows whose identity is a
configured name wrapped in an account and a region, and only one of them is
wired. `aws_sns_topic` is: its ARN is canonical from every angle. The
queue's URL is canonical too, and the emulator's is not - floci reports
`http://localhost:4566/ACCOUNT/NAME`, the AWS provider's importer parses
only the `amazonaws.com` form, and marker discovery hands the import the
one string it will refuse. Since no run in this fork can supply a
`CloudContext` yet, marker discovery is the path a queue actually takes, so
`aws_sqs_queue` is `blocked-emulator` rather than wired. Real AWS has no
such gap.

The mechanism did not close `aws_iam_policy` either, and there too the
emulator is the obstacle rather than the mechanism: the template expresses
an IAM ARN's empty region segment perfectly well, and floci's
`iam:GetPolicy` omits `Tags` the way its `iam:GetRole` does. It was never
going to close `aws_secretsmanager_secret`, whose ARN carries a suffix
minted per secret; that row is deferred to the marker path it is already
taggable for. The two parent-component rows are unchanged:
`aws_route53_record` resolves through its zone, and
`aws_sns_topic_subscription` needs more than parent resolution, since the
UUID in its ARN has no source in configuration and the type takes no tags,
which leaves only a list-plus-content match on protocol and endpoint.

## The three the rule excludes

Exactly three of the 68 fail the admission rule, and they fail it
permanently: they are out by the rule itself, not by v0 scoping. This is a
different kind of "not admitted" than the surveyed types that are merely
not wired yet (34 of them at the 31 types wired today, the `ready` and
`blocked-emulator` rows above), and `live/LIMITATIONS.md`'s
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
`live/LIMITATIONS.md`, and OpenTofu's own ephemeral resources and
write-only attributes cover parts of the credential story natively.

`aws_acm_certificate_validation` is a waiter pretending to be a resource:
its entire job is to block until DNS validation completes, and its state
entry records nothing but "the wait finished once." Waiting belongs to the
lifecycle layer's sequencing, the same forwarding address `time_sleep`
has in `live/LIMITATIONS.md`.

That the excluded set is exactly credentials plus a waiter, with the
residue row at zero, is the survey's main result: after four admission
paths and credentials-to-Ops, nothing in the AWS top set needs a store.
