# rds cohort

Cohort: `rds`. Ratified by: the fourth registry-backed ratification batch,
against issue #65's ratification campaign (the recipe #54/#56 already
proved on Lambda, IAM+ECR and messaging) and `tools/row-gen`'s RDS service
section. Mechanism: #48 (see `live/e2e/estates/example/README.md` for the
proof-of-mechanism cohort, and `live/e2e/estates/lambda/README.md` for the
first registry-backed cohort this one follows).

This cohort exercises every type this batch ratified into
`internal/live/lint/admission.go` and `internal/live/identity/table.go`.
`tools/row-gen` proposed 18 types in the RDS service section; 17 are
ratified below, 1 is rejected — see `internal/live/identity/table.go` for
the full per-type evidence, including five corrections where row-gen's own
classification (evidence-only or needs-hand-separator) undersold a
concrete, documented import grammar the provider's own Import section
names outright, the same shape as the messaging batch's
`aws_sns_topic_policy` correction.

## Coverage map

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| Client-named path | `aws_db_event_subscription.app` | Identity is the `name` argument (Optional; Terraform assigns a random one when omitted, the same idiom `aws_s3_bucket`'s `bucket` already has). Confirmed against the documented import command and the Attribute Reference (`id` equals `name`). |
| Client-named path, emulator caveat | `aws_db_instance.app` | Identity is the `identifier` argument — `live/SURVEY.md`'s own recorded "third wrinkle": the original survey filed this type under marker on taggability alone (v6.58.0 ships it no identity schema and no list resource), but its documented import ID is the client-chosen `identifier`, exactly what that file predicted a batch reaching RDS would find. `id` is a distinct provider-minted "RDS DBI resource ID", not `identifier`, so `id` is not claimed as an identity source. Ratified despite a floci gap — see "Verifying by hand" below. |
| Parent-derived path, untaggable | `aws_db_instance_role_association.app` | Row-gen proposed nothing pastable (a fold child of `aws_db_instance` with no registry `primaryIdentifier` of its own); the provider's real, documented import ID is `db_instance_identifier` and `role_arn` comma-joined, both Required arguments already in configuration — the same concrete-composite shape as `aws_iam_role_policy`. Carries no `tags` argument — see "Untaggable types" below. |
| Client-named path | `aws_db_option_group.app` | Identity is the `name` argument (Optional; Terraform assigns a random one when omitted). Confirmed against the documented import command and the Attribute Reference (`id` equals `name`). |
| Client-named path | `aws_db_parameter_group.app` | Identity is the `name` argument. v6.58.0 ships this type no identity schema at all — the same "docs tier" evidence `aws_ecs_cluster` already carries — so the documented import command and Attribute Reference (`id` equals `name`) are the only evidence. |
| Client-named path | `aws_db_proxy.app` | Identity is the `name` argument — unlike every other type in this batch, `name` is a plain Required argument here, no `name_prefix` alternative. Its own `id` is documented as the proxy's ARN, a different value, so `id` is not claimed as an identity source, the same standard of care as `aws_ecs_cluster`'s synthesized id. |
| Parent-derived path, untaggable | `aws_db_proxy_default_target_group.app` | Row-gen filed this evidence-only (the registry's `TargetGroupArn` primaryIdentifier is entirely read-only). The provider's real import ID is `db_proxy_name` — a named-singleton child of `aws_db_proxy`, the same shape as `aws_sns_topic_policy` in the messaging batch, confirmed by its own Attribute Reference (`id` equals "Name of the RDS DB Proxy"). Carries no `tags` argument — see "Untaggable types" below. |
| Parent-derived path | `aws_db_proxy_endpoint.app` | Row-gen filed this evidence-only (a guessed argument name "not backed by a provider identity schema or the carve seed"). The provider's real, documented import ID is `db_proxy_name` and `db_proxy_endpoint_name` slash-joined, both Required arguments already in configuration — the same concrete-composite shape as `aws_iam_role_policy_attachment`. |
| Client-named path | `aws_db_subnet_group.app` | Identity is the `name` argument (Optional+Computed, the `name_prefix` idiom — the provider ships an identity schema for this type, which `live/survey-full.json`'s own mechanical pass reads as `needs-config-signal` for exactly that reason, overridden here by hand the same way `aws_s3_bucket` already is). Confirmed against the documented import command and Attribute Reference (`id` equals `name`). |
| Client-named path | `aws_rds_cluster.app` | Identity is the `cluster_identifier` argument — the same `needs-config-signal` mechanical classification as the subnet group above, for the same reason, overridden the same way. Confirmed against the documented import command and Attribute Reference (`id` equals "RDS Cluster Identifier"). |
| Client-named path | `aws_rds_cluster_instance.app` | Identity is the `identifier` argument — this type maps to the same `AWS::RDS::DBInstance` CFN type as `aws_db_instance` above, but unlike it, its Attribute Reference lists both `identifier` and `id` as "Instance identifier", the same string, so `id` *is* claimed as an identity source here. |
| Client-named path | `aws_rds_cluster_parameter_group.app` | Identity is the `name` argument (Optional; Terraform assigns a random one when omitted). Confirmed against the documented import command and Attribute Reference (`id` equals `name`). |
| Parent-derived path, untaggable | `aws_rds_cluster_role_association.app` | Row-gen proposed nothing pastable (a fold child of `aws_rds_cluster`). The provider's real, documented import ID is `db_cluster_identifier` and `role_arn` comma-joined, both Required arguments already in configuration — the same shape as `aws_db_instance_role_association` above. Carries no `tags` argument — see "Untaggable types" below. |
| Parent-derived/composite path | `aws_rds_custom_db_engine_version.app` | Row-gen refused a pastable row outright ("the composite separator is not registry evidence; a human chooses it" — `primaryIdentifier=[Engine, EngineVersion]`). The provider's real, documented import ID is `engine` and `engine_version` colon-joined, both Required arguments already in configuration — the same concrete-composite shape as `aws_iam_role_policy`'s `ROLENAME:POLICYNAME`. No `id` attribute is exported at all, so this imports by string only, like `aws_route_table_association`. |
| Client-named path | `aws_rds_global_cluster.app` | Row-gen filed this evidence-only, flagging its own guessed argument name (`global_cluster_identifier`) as "not backed by a provider identity schema or the carve seed". The provider's Argument Reference resolves the guess directly: `global_cluster_identifier` is Required, with no Terraform-assigned fallback (unlike every other identifier in this batch), and its Attribute Reference confirms `id` equals it. |
| Marker path (server-assigned) | `aws_rds_integration.app` | The RDS service assigns the integration's own ARN at create time; `integration_name`, `source_arn` and `target_arn` together name what it connects, not the integration resource itself. Confirmed against the documented import command and Attribute Reference, which lists both `arn` and a deprecated `id` alias of the same ARN. |
| Client-named path | `aws_rds_shard_group.app` | Identity is the `db_shard_group_identifier` argument — Required, with no Terraform-assigned fallback. Its Attribute Reference exports no `id` attribute at all (only `arn`, `db_shard_group_resource_id`, `endpoint`), so nothing beyond the argument itself is claimed as an identity source. |

## Rejected, and deferred

`tools/row-gen`'s RDS service section proposed 18 types in this batch's
scope. 17 are ratified above. One is rejected on independent verification
against the provider's documented import behavior, not against the
registry:

- `aws_db_proxy_target` — row-gen filed this evidence-only (a fold child of
  `aws_db_proxy_default_target_group` with no registry `primaryIdentifier`
  of its own). The provider's documented import ID is
  `db_proxy_name/target_group_name/type/resource_identifier`, where
  `db_proxy_name` and `target_group_name` are both configured arguments and
  `resource_identifier` is whichever of `db_instance_identifier` or
  `db_cluster_identifier` a config sets (an alternation this table's
  `idlessAttr` already expresses fine) — but `type` is the literal string
  `RDS_INSTANCE` or `TRACKED_CLUSTER`, chosen by *which* of those two
  optional arguments is set, not a value any argument carries and not a
  fixed separator either. No `Component` in `internal/live/identity`'s
  vocabulary expresses "a literal conditioned on which alternative
  matched", so this stays a needs-hand-separator case rather than a guess
  this batch writes blind — the same stance as the messaging batch's
  `aws_cloudwatch_event_rule` rejection.

Two more row-gen proposals in the RDS section (`aws_db_proxy_endpoint`'s
own guessed argument name, and `aws_rds_global_cluster`'s) turned out to be
correct on inspection of the real Import section — see the coverage map
above rather than a rejection here, since they landed ratified, not out.

## Untaggable types

Three types in the table above carry no `tags` argument in the AWS
provider at all: `aws_db_instance_role_association`,
`aws_db_proxy_default_target_group` and `aws_rds_cluster_role_association`
— the same shape as the twelve untaggable types
`live/e2e/estate/README.md` names and the Lambda batch's
`aws_lambda_layer_version`. None of the three is in `live/SURVEY.md`'s
curated 68, so — the same reasoning `live/e2e/estates/lambda/README.md`
and `live/e2e/estates/messaging/README.md` already give at length — each
follows `internal/live/stamp/stamp_test.go`'s `untaggableAdmittedTypes`
pin directly. Unlike those two batches, this repository's own follow-up to
issue #54 has since landed
(`tools/survey-gen/untaggable_render.go` now derives
`live/LIMITATIONS.md`'s "Untaggable types cannot be removed by the sweep"
entry from `live/survey-full.json`'s full registry roster, not only the
curated 68), so all three types are named in that doc's rendered span with
no split list and no follow-up needed — `go run ./tools/survey-gen
-render` picked them up mechanically once this batch's types were admitted.

## Files

| File | Contents |
|---|---|
| `versions.tf` | `terraform`/`provider "aws"` blocks, identical in shape to `live/e2e/estate/versions.tf`. |
| `locals.tf` | `estate_tag` — `"rds-cohort"`, distinct from every other cohort's own tag. |
| `rds.tf` | All 17 ratified types. None needed the shared `aws_iam_role` support resource `tools/estate-gen`'s `NeedsIAMRole`/`isRoleArg` mechanism wires automatically for a `role` or `*_role_arn` argument: every RDS association type in this batch names its role argument the bare `role_arn`, which that alias does not match, so each gets a literal placeholder ARN instead (see the overrides below) rather than a wired reference to a shared role. |

`tools/estate-gen`'s generic required-only pass could not satisfy several
providers' plan-time validations or intra-cohort parent references on its
own; `tools/estate-gen/overrides.go` carries eleven hand-written
`typeOverride` entries for this batch (`aws_db_event_subscription`,
`aws_db_instance`, `aws_db_instance_role_association`, `aws_db_proxy`,
`aws_db_proxy_default_target_group`, `aws_rds_cluster`,
`aws_rds_cluster_instance`, `aws_rds_cluster_role_association`,
`aws_rds_custom_db_engine_version`, `aws_rds_shard_group` and
`aws_rds_integration` — `aws_db_option_group`, `aws_db_parameter_group`,
`aws_db_proxy_endpoint`, `aws_db_subnet_group`,
`aws_rds_cluster_parameter_group` and `aws_rds_global_cluster` needed
none) — each entry's own `Reasons` field is the `terraform validate`
error, or the intra-cohort parent link the generic pass's `parentRef`
heuristic missed, it exists to fix; the generated file's own per-resource
comment cites the same text. No `.tf` file in this directory was
hand-edited outside that mechanism — every constraint found was
expressible as an override.

## Gating

Nothing here runs against a live or emulated cloud yet. `go test
./internal/live/lint/... ./internal/live/identity/...` picks this
directory up through `internal/live/flocitest.FixtureDirs` (#48's union
pin) and checks it by static HCL parse — `TestAdmissionTableCoversEstate`
and `TestTableCoversFixtureTypes` require `admittedTypesV0` and
`DefaultTable` to cover exactly the union of `live/e2e/estate/` and every
`estates/*` cohort, this one included.

`live/e2e/run.sh` and the gated `internal/live/discovery` floci
integration test still stand up only the demo estate (confirmed by
inspection: neither mentions `estates/` or "cohort"). Wiring cohort
estates into the gated apply-against-floci tier is separate follow-on
work, the same note `estates/example/README.md` and the three earlier
cohorts' READMEs already make.

## Verifying by hand

```
docker run -d --rm -p 4611:4566 --name tofu-rds-cohort-verify \
  ghcr.io/lex00/floci@sha256:4753246c0260a22af1056c65993f4d73b0a907729a9580b9baba5d628b6dad34
export AWS_ENDPOINT_URL=http://localhost:4611 AWS_ACCESS_KEY_ID=test \
       AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

terraform init
terraform validate
terraform apply -auto-approve

terraform destroy -auto-approve
docker rm -f tofu-rds-cohort-verify
```

`terraform validate` passes against the real provider release (6.58.0)
this fixture pins — confirming every argument name above is real, not
just registry-plausible. Ten `validate` failures (invalid ARNs, an
`engine_family`/`engine` enum, an `engine` prefix rule) were found this
way and fixed by the `tools/estate-gen/overrides.go` entries listed above,
the same discipline the Lambda and S3 overrides already established.

An `apply` was run by hand during ratification (against the pinned image
above, `ghcr.io/lex00/floci`'s fork, not `floci/floci:latest` — the same
pin `live/e2e/estates/iam-ecr/README.md` uses), not wired into any
automated tier. The result is a narrower split than the health check
advertises, the same shape the Lambda and messaging batches found:

- `aws_db_parameter_group.app` and `aws_rds_cluster_parameter_group.app`
  create and destroy cleanly.
- `aws_rds_cluster.app` creates cleanly (30s) but interacting with it
  further — updating it in place, or destroying it after
  `aws_rds_cluster_instance.app` fails alongside it — surfaces the
  `skip_final_snapshot`/`final_snapshot_identifier` requirement in ways
  `terraform validate` never catches; `tools/estate-gen/overrides.go` now
  sets `skip_final_snapshot = true` up front for exactly this reason.
- `aws_rds_cluster_instance.app` fails to create against floci with
  "Provider produced inconsistent final plan": this batch's fixture sets
  `engine = aws_rds_cluster.app.engine` ("aurora-mysql"), the same
  argument the provider's own documented example uses, and floci's
  `CreateDBInstance` handler silently coerces the response to `"mysql"` —
  a floci gap (`choudoufu#26` territory: the Aurora engine family is not
  respected), not evidence against the type's identity, which needs no
  cloud read at all (`identifier` alone).
- `aws_db_instance.app` **creates and destroys cleanly** (both directions,
  ~80s each), reporting `status = "available"` and an `endpoint` shaped
  like a Docker bridge address (`172.17.0.2:...`) rather than an
  `amazonaws.com`/RDS-style hostname — the same self-referential-endpoint
  shape the messaging batch found for `aws_sqs_queue`'s URL. This is
  surprising given `live/SURVEY.md`'s and `live/residue.go`'s recorded
  blocker (lex00/floci#28: RDS needs the Docker socket mounted into the
  emulator, which — confirmed by inspection during this batch — neither
  the gated Go harness (`internal/live/flocitest`), the shell e2e harness
  (`live/e2e/run.sh`), nor any cohort README's `docker run` command does).
  The Create/Delete API calls themselves plainly do not require the
  socket against this pinned image. What they do not demonstrate is a
  genuinely functioning database: the synthetic endpoint address is not
  something a client could connect to, so nothing here proves floci#28's
  underlying claim wrong, only that its narrowest slice (the two RDS API
  calls Terraform's own lifecycle needs) does not need it. This batch
  keeps the emulator caveat as recorded — `aws_db_instance` ratifies on
  identity evidence alone, the same stance the messaging batch took for
  `aws_sqs_queue` despite its own, differently-shaped floci gap.
- `aws_db_event_subscription.app`, `aws_db_option_group.app`,
  `aws_db_proxy.app`, `aws_db_instance_role_association.app`,
  `aws_rds_cluster_role_association.app`,
  `aws_rds_custom_db_engine_version.app`, `aws_rds_global_cluster.app`,
  `aws_rds_integration.app` and `aws_rds_shard_group.app` all fail to
  create: floci's `/_localstack/health` reports `rds: running`, but each
  one's create call (`CreateEventSubscription`, `CreateOptionGroup`,
  `CreateDBProxy`, `AddRoleToDBInstance`, `AddRoleToDBCluster`,
  `CreateCustomDBEngineVersion`, `CreateGlobalCluster`,
  `CreateIntegration`, `CreateDBShardGroup`) returns `UnsupportedOperation:
  Operation ... is not supported.` — the operations are not actually
  implemented, only the service's presence is. This is a floci gap
  (`choudoufu#26` territory), not evidence against any of the nine types'
  admission: identity and enumeration are properties of the provider and
  the registry, not of one emulator's completeness.
- `aws_db_subnet_group.app` fails to create for a reason that has nothing
  to do with floci: this fixture's `subnet_ids` is a placeholder string,
  not a real subnet, and RDS (real or emulated) rejects a subnet group
  built from a subnet that does not exist. Not a coverage finding, an
  artifact of this cohort standing alone with no `aws_subnet` of its own —
  same as `live/e2e/estate/`'s own real VPC resources this cohort does
  not duplicate.
- `aws_db_proxy_default_target_group.app` and `aws_db_proxy_endpoint.app`
  are never reached: both depend on `aws_db_proxy.app`, which fails to
  create as noted above.

No state or container from this verification run is committed; the
commands above are reproducible from a clean checkout.
