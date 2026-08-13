# dynamodb-elasticache cohort

Cohort: `dynamodb-elasticache`. Ratified by: the fourth registry-backed
ratification batch against #40's strategy and #44's row generator (issue
#65) — DynamoDB periphery beyond the already-admitted `aws_dynamodb_table`,
and ElastiCache. Mechanism: #48 (see `live/e2e/estates/example/README.md`
for the proof-of-mechanism cohort, and `live/e2e/estates/messaging/README.md`
for the third registry-backed cohort this one follows).

This cohort exercises every type this batch ratified into
`internal/live/lint/admission.go` and `internal/live/identity/table.go`.

## Coverage map

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| Client-named path, untaggable | `aws_dynamodb_global_table.app` | Identity is the `name` argument, already in config. Never a pastable row-gen proposal — v6.58.0 ships no identity schema for this type, so row-gen could only GUESS the argument name; confirmed directly against the provider's real Argument Reference and Import section. |
| Parent-derived path, untaggable | `aws_dynamodb_resource_policy.app` | Identity is the `resource_arn` argument — the parent table's own ARN. Never a row-gen proposal at all (folded onto `AWS::DynamoDB::Table` with no CFN type of its own); confirmed independently against the provider's real identity schema (`required_for_import=[resource_arn]`). Same named-singleton-child shape as `aws_sns_topic_policy` and `aws_sqs_queue_policy` from the messaging batch. |
| Marker path | `aws_elasticache_cluster.app` | Identity is the `cluster_id` argument, already in config — proposed correctly by row-gen, argument confirmed against `live/import-grammar.json` and the provider's real Argument Reference. |
| Marker path | `aws_elasticache_parameter_group.app` | Identity is the `name` argument, already in config. Row-gen's registry rule read this server-assigned-shaped and printed evidence-only; issue #55's import-grammar demotion caught the mismatch, and the real Argument Reference confirms `name` is Required and `id` equals it verbatim. |
| Marker path | `aws_elasticache_replication_group.app` | Identity is the `replication_group_id` argument, already in config — proposed correctly by row-gen, argument confirmed. |
| Marker path | `aws_elasticache_serverless_cache.app` | Identity is the `name` argument, already in config — proposed correctly by row-gen, argument confirmed. |
| Marker path | `aws_elasticache_subnet_group.app` | Identity is the `name` argument, already in config — proposed correctly by row-gen, argument confirmed. |
| Marker path | `aws_elasticache_user.app` | Identity is the `user_id` argument, already in config — proposed correctly by row-gen, argument confirmed. |
| Marker path | `aws_elasticache_user_group.app` | Identity is the `user_group_id` argument, already in config — proposed correctly by row-gen, argument confirmed. |

## Rejected, and deferred

`tools/row-gen`'s DynamoDB section proposed or surfaced 6 types (5 beyond
the already-admitted `aws_dynamodb_table`); its ElastiCache section proposed
or surfaced 9. Two are ratified above out of DynamoDB's five, seven out of
ElastiCache's nine.

Rejected outright — not a row-gen misclassification, the identity genuinely
cannot be recovered:

- `aws_elasticache_global_replication_group` — row-gen proposed
  server-assigned via the registry's `GlobalReplicationGroupId`, and the
  provider agrees with the shape, unlike this batch's two corrections above.
  Its real Argument Reference has no `global_replication_group_id` argument
  at all: the two Required arguments are
  `global_replication_group_id_suffix` and `primary_replication_group_id`,
  and `global_replication_group_id` is a separate, computed attribute — AWS
  prepends its own region-derived code to the configured suffix (the
  documented import example, `okuqm-global-replication-group-1`, is not a
  string any configuration sets). `live/survey-full.json`'s own automated
  pass reaches "moves to Ops" independently (untaggable, no native list
  resource, no identity schema in v6.58.0), and unlike
  `aws_ecr_registry_policy` and its two account-singleton siblings in the
  IAM/ECR batch, this type is not a one-per-account singleton either: many
  global replication groups can exist per account, so there is no
  deterministic fallback identity to read without a list. No admission path
  recovers it.

Deferred as composite import IDs this batch does not hand-write — the same
restraint the Lambda and messaging batches already state, now applied to
row-gen's evidence-only and needs-hand-separator output rather than a wrong
server-assigned guess. Every separator and argument order below is
confirmed (not guessed) against `live/import-grammar.json`'s scraped Import
sections; a future batch can lift these four rows directly:

- `aws_dynamodb_global_secondary_index` — `table_name` and `index_name`,
  both Required and both named in the provider's own identity schema,
  joined by a comma. Parent-derived off `aws_dynamodb_table`.
- `aws_dynamodb_kinesis_streaming_destination` — `table_name` and
  `stream_arn`, both Required, joined by a comma. Docs-tier evidence only
  (no identity schema in v6.58.0).
- `aws_elasticache_user_group_association` — `user_group_id` and `user_id`,
  both Required, joined by a comma. Parent-derived off
  `aws_elasticache_user_group` and `aws_elasticache_user`, both ratified
  above.
- `aws_dynamodb_contributor_insights` — `table_name` (Required) and
  `index_name` (Optional), joined into
  `name:TABLE_NAME/index:INDEX_NAME/ACCOUNT`. Left out for a second reason
  beyond the separator: `index_name` is optional, and expressing "this
  literal segment only when an argument is set" is a component this
  table's vocabulary does not have — the same gap that kept the messaging
  batch's `aws_cloudwatch_event_rule` out for its optional
  `event_bus_name`.

DynamoDB has no separate "backup" resource type in the provider at all —
point-in-time recovery is an argument on `aws_dynamodb_table` itself, not a
standalone managed resource — so there was nothing here for a backup row to
be, despite issue #65's own recipe naming it as a thing to look for.

## Untaggable types

Two types in the table above, `aws_dynamodb_global_table` and
`aws_dynamodb_resource_policy`, carry no `tags` argument in the AWS
provider — the same shape as `aws_lambda_layer_version` and the three ECR
registry singletons from earlier batches. They are **not** added to
`live/LIMITATIONS.md`'s "Untaggable types cannot be removed by the sweep"
entry by hand: as of `9ebb8e3` (issue #54), that entry is rendered
mechanically by `go run ./tools/survey-gen -render` from
`identity.AdmittedTypes()` intersected with `live/survey-full.json`'s
taggability signal, with no curated-68 boundary left to carve around — the
split `internal/live/stamp/stamp_test.go` used to carry
(`untaggableOutsideCuratedSurvey`) folded back into one list once the doc's
own derivation could see past the curated 68. Both types are named in
`internal/live/stamp/stamp_test.go`'s `untaggableAdmittedTypes` pin, and
`live/LIMITATIONS.md`'s entry picks them up the next time the render runs.

## Files

| File | Contents |
|---|---|
| `versions.tf` | `terraform`/`provider "aws"` blocks, identical in shape to `live/e2e/estate/versions.tf`. |
| `locals.tf` | `estate_tag` — `"dynamodb-elasticache-cohort"`, distinct from every other cohort's own tag. |
| `dynamodb-elasticache.tf` | The nine ratified types. No supporting resources: none of the nine references another admitted type as a required argument. |

## Gating

Nothing here runs against a live or emulated cloud yet. `go test
./internal/live/lint/... ./internal/live/identity/...` picks this directory
up through `internal/live/flocitest.FixtureDirs` (#48's union pin) and
checks it by static HCL parse — `TestAdmissionTableCoversEstate` and
`TestTableCoversFixtureTypes` require `admittedTypesV0` and `DefaultTable`
to cover exactly the union of `live/e2e/estate/` and every `estates/*`
cohort, this one included.

`live/e2e/run.sh` and the gated `internal/live/discovery` floci integration
test still stand up only the demo estate. Wiring cohort estates into the
gated apply-against-floci tier is separate follow-on work, the same note
every other cohort's README already makes.

## Verifying by hand

```
docker run -d --rm -p 4630:4566 --name tofu-dynamodb-elasticache-cohort-verify \
       ghcr.io/lex00/floci@sha256:4753246c0260a22af1056c65993f4d73b0a907729a9580b9baba5d628b6dad34
export AWS_ENDPOINT_URL=http://localhost:4630 AWS_ACCESS_KEY_ID=test \
       AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

terraform init
terraform validate
terraform apply -auto-approve

terraform destroy -auto-approve
docker rm -f tofu-dynamodb-elasticache-cohort-verify
```

`terraform validate` passes against the real provider release (6.58.0) this
fixture pins — confirming every argument name above is real, not just
registry-plausible. Three of the nine resources needed a `typeOverrides`
entry in `tools/estate-gen/overrides.go` to reach that state (see each
resource's `# overrides:` comment in `dynamodb-elasticache.tf`):
`aws_dynamodb_resource_policy` (a syntactically valid ARN and JSON policy
body in place of estate-gen's generic string placeholders),
`aws_elasticache_cluster` (the `engine`/`node_type`/`num_cache_nodes`/
`parameter_group_name` combination the provider requires in practice, with
`engine` set to `memcached` rather than `redis` — see below) and
`aws_elasticache_replication_group` (`node_type`, required once no
`global_replication_group_id` is set). `aws_elasticache_cluster.app`'s and
`aws_elasticache_replication_group.app`'s own identity arguments were also
shortened by hand in the same overrides: this cohort's own name,
`dynamodb-elasticache`, makes estate-gen's generic
`tofu-<cohort>-cohort-<type>` placeholder 54 and 58 characters
respectively, over the provider's 50- and 40-character limits for
`cluster_id` and `replication_group_id`.

An `apply` against the pinned floci image was run by hand during
ratification (not wired into any automated tier — see "Gating" above) and
found the emulator's actual coverage much narrower than its health check
advertises — narrower, in fact, than any earlier batch's cohort found:

- `aws_dynamodb_global_table.app` and `aws_dynamodb_resource_policy.app`
  both fail immediately: floci returns `UnknownOperationException:
  Operation CreateGlobalTable is not supported` and `Operation
  PutResourcePolicy is not supported` respectively. DynamoDB's own service
  presence (`aws_dynamodb_table`, already admitted) works; these two
  periphery operations do not exist in floci's DynamoDB implementation at
  all.
- `aws_elasticache_parameter_group.app`, `aws_elasticache_serverless_cache.app`,
  `aws_elasticache_subnet_group.app` and `aws_elasticache_user_group.app`
  all fail immediately the same way: `UnsupportedOperation` on
  `CreateCacheParameterGroup`, `CreateServerlessCache`,
  `CreateCacheSubnetGroup` and `CreateUserGroup` respectively — the
  operations are not implemented, only the service's presence is, the same
  `choudoufu#26`-territory gap the Lambda and messaging batches already
  found for their own services.
- `aws_elasticache_user.app` is the one type that creates cleanly —
  `CreateUser` succeeds — but the provider's own post-create read fails:
  `ListTagsForResource` returns `UnsupportedOperation`, so `apply` never
  completes even though the user itself now exists in floci (confirmed on
  a second run, which failed differently: `UserAlreadyExistsFault`).
- `aws_elasticache_cluster.app` and `aws_elasticache_replication_group.app`
  are the two types floci actually attempts to provision rather than
  reject outright — both accept the create call and enter a genuine
  "creating" wait rather than returning an immediate error. floci's own
  container logs (`docker logs`) show why: `CreateReplicationGroup`
  launches a real container to emulate the cache node, over the Docker
  socket the floci container expects to reach, and that provisioning
  fails here with `SocketException: No such file or directory` connecting
  to the socket before rolling back. This is `lex00/floci#28`'s own
  Docker-socket requirement — the same gap the RDS batch's
  `aws_db_instance` ran into — not a new finding: neither this
  hand-verification harness nor `live/e2e/run.sh`'s gated tier mounts the
  host's Docker socket into the floci container. `aws_elasticache_cluster.app`
  was still in its own multi-minute "Still creating" wait, consistent with
  the same code path, when this run was interrupted rather than left to
  time out on the same failure. Neither result is evidence against either
  type's admission: identity and enumeration are properties of the
  provider and the registry, not of one emulator's completeness, and both
  types are the two of the nine floci comes closest to actually serving.

None of the nine types in this cohort create and destroy cleanly against
floci in this environment — the widest emulator gap any registry-ratified
batch has found so far. Every rejection above is still an identity
question, verified against the provider's own documentation independent of
floci; the emulator's incompleteness is `choudoufu#26` and
`lex00/floci#28` territory, tracked separately from this batch's admission
decisions.
