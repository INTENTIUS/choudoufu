# messaging cohort

Cohort: `messaging`. Ratified by: the second registry-backed ratification
batch against #40's strategy and #44's row generator — SQS, SNS beyond the
already-admitted `aws_sns_topic`, CloudWatch, and EventBridge/Events.
Mechanism: #48 (see `live/e2e/estates/example/README.md` for the
proof-of-mechanism cohort, and `live/e2e/estates/lambda/README.md` for the
first registry-backed cohort this one follows).

This cohort exercises every type this batch ratified into
`internal/live/lint/admission.go` and `internal/live/identity/table.go`.

## Coverage map

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| Client-named path | `aws_cloudwatch_composite_alarm.app` | Identity is the `alarm_name` argument, already in config — row-gen proposed this correctly the first time (no CFN/TF mismatch), confirmed against the provider's documented import command and its Attribute Reference (`id` equals `alarm_name`). |
| Client-named path, untaggable | `aws_cloudwatch_dashboard.app` | Identity is the `dashboard_name` argument, already in config — proposed correctly. Carries no `tags` argument — see "Untaggable types" below. |
| Client-named path | `aws_cloudwatch_metric_stream.app` | Identity is the `name` argument, already in config — proposed correctly. |
| Parent-derived path | `aws_sns_topic_policy.app` | Row-gen proposed server-assigned via the registry's opaque, undocumented `Id`; rejected as a proposal. The provider's real, documented import ID is the topic's own ARN, which this resource's sole reference argument, `arn`, already carries — the same named-singleton-child shape as `aws_s3_bucket_policy`. |
| Account-derived path | `aws_sqs_queue.app` | Row-gen proposed server-assigned via the registry's `QueueUrl`/`Arn` — right in spirit, but the real shape is account-derived, the same as `aws_sns_topic`: the queue URL is the `name` argument wrapped in the run's region and account. Ratified despite a floci gap — see "Verifying by hand" below. |
| Parent-derived path | `aws_sqs_queue_policy.app` | Row-gen proposed server-assigned via the registry's opaque, undocumented `Id`; rejected as a proposal. The provider's real, documented import ID is the queue's own `url`, which this resource's sole reference argument, `queue_url`, already carries — the same shape as `aws_sns_topic_policy.app` above. |

## Rejected, and deferred

`tools/row-gen`'s SQS/SNS/CloudWatch/Events/Logs service sections proposed
nine types in this batch's scope (beyond the already-admitted
`aws_cloudwatch_log_group` and `aws_sns_topic`, which this batch does not
repeat). Six are ratified above. Two are rejected on independent
verification against the provider's documented import behavior, not
against the registry:

- `aws_cloudwatch_alarm_mute_rule` — row-gen proposed server-assigned via
  the registry's `Arn` (read-only; `Name` is `createOnlyProperties` only).
  The provider disagrees: both its documented import command and its
  identity schema's sole `required_for_import` attribute are the rule's
  own `name` argument, already in configuration — a name-composed ID the
  registry's own read-only field does not name. Same failure shape as the
  Lambda batch's `aws_lambda_alias`.
- `aws_cloudwatch_event_rule` — row-gen proposed server-assigned via the
  registry's `Arn`, `AWS::Events::Rule`'s sole `readOnlyProperty`. The
  provider disagrees: the documented import ID is
  `event_bus_name/rule_name`, a composite of two configured arguments
  (`event_bus_name` silently defaulting to the account's default bus when
  omitted) — `live/SURVEY.md`'s own curated-68 row already named this
  exact grammar. Wiring it needs a component this table's vocabulary does
  not have yet (a literal fallback for an omitted argument, not just a
  separator), so it stays a "needs hand separator" case rather than a
  guess this batch writes blind, the same stance as the Lambda batch's two
  rejections.

One more is deferred for a reason that has nothing to do with its identity:

- `aws_sns_topic_subscription` — row-gen proposed server-assigned via the
  registry's `Arn`, and the provider agrees: SNS mints the subscription's
  own ARN (the topic ARN plus a UUID) only once the subscription confirms,
  and `live/SURVEY.md`'s own curated-68 row already reaches the same
  "ready" verdict by hand. Verified live, too — a manual apply against
  floci (below) created and destroyed it cleanly. It is still left out of
  `admittedTypesV0` and `DefaultTable`: the type carries no `tags`
  argument, and it is one of `live/survey.json`'s curated 68 rows, so
  admitting it obligates `live/LIMITATIONS.md`'s "Untaggable types cannot
  be removed by the sweep" entry — `tools/survey-gen/limitations_test.go`
  derives that entry's roster mechanically from the survey intersected
  with the admission table, with no escape hatch the way
  `internal/live/stamp/stamp_test.go`'s `untaggableOutsideCuratedSurvey`
  list gives types outside the curated 68. This batch's mandate is to
  leave `live/LIMITATIONS.md` and the curated-68 apparatus alone (issue
  #54 is where extending that apparatus to the full registry roster
  belongs), so the honest move is to defer this one type rather than
  either break that rule or ratify it with a doc left silently
  inconsistent. Nothing about the identity classification is in question;
  a future batch that also touches `live/LIMITATIONS.md`'s derivation can
  pick it straight up.

Most of the Logs and Events resource family carries no `cfn_type` in
`live/mapping.json` at all — `aws_cloudwatch_log_stream`,
`aws_cloudwatch_log_metric_filter`, `aws_cloudwatch_log_subscription_filter`,
`aws_cloudwatch_event_bus`, `aws_cloudwatch_event_target` and the rest — so
`tools/row-gen` proposes nothing for them; they are simply outside this
batch's scope, not evidence-only or needs-hand-separator findings.

## Untaggable types

One type in the table above, `aws_cloudwatch_dashboard`, carries no `tags`
argument in the AWS provider — the same shape as the twelve untaggable
types `live/e2e/estate/README.md` names and the Lambda batch's
`aws_lambda_layer_version`. It is **not** added to `live/LIMITATIONS.md`'s
"Untaggable types cannot be removed by the sweep" entry, on purpose, for
the same reason `live/e2e/estates/lambda/README.md` already documents at
length: `tools/survey-gen/limitations_test.go`'s
`TestLimitationsDocAgainstSurvey` derives that entry's roster from
`live/survey.json`, the curated 68-type roster, intersected with the
admission table — not from `live/survey-full.json`, the registry-backed
roster this batch's evidence came from. `aws_cloudwatch_dashboard` is
outside the curated 68, so it never enters that test's `derived` set, and
it instead follows the Lambda batch's `untaggableOutsideCuratedSurvey`
split in `internal/live/stamp/stamp_test.go`. Documenting it correctly in
`live/LIMITATIONS.md` needs the pin test extended to the full registry
roster first (issue #54). Left as follow-up.

`aws_sns_topic_subscription` would have hit this same untaggable shape
from *inside* the curated 68, where that escape hatch does not exist — see
"Rejected, and deferred" above for why that difference is exactly what
kept it out of this batch rather than in it.

## Files

| File | Contents |
|---|---|
| `versions.tf` | `terraform`/`provider "aws"` blocks, identical in shape to `live/e2e/estate/versions.tf`. |
| `locals.tf` | `estate_tag` — `"messaging-cohort"`, distinct from the demo estate's `"stateless-e2e"` and the lambda cohort's `"lambda-cohort"`. |
| `iam.tf` | `aws_iam_role.messaging`, supporting infrastructure for the metric stream's role — not a coverage row; `aws_iam_role` is already covered by `live/e2e/estate/`. |
| `messaging.tf` | The six ratified types, plus `aws_sns_topic.app`, supporting infrastructure for `aws_sns_topic_policy.app` — not a coverage row; `aws_sns_topic` is already covered by `internal/live/lint/admission.go`'s original account-derived section. |

## Gating

Nothing here runs against a live or emulated cloud yet. `go test
./internal/live/lint/... ./internal/live/identity/...` picks this directory
up through `internal/live/flocitest.FixtureDirs` (#48's union pin) and
checks it by static HCL parse — `TestAdmissionTableCoversEstate` and
`TestTableCoversFixtureTypes` require `admittedTypesV0` and `DefaultTable`
to cover exactly the union of `live/e2e/estate/` and every `estates/*`
cohort, this one included.

`live/e2e/run.sh` and the gated `internal/live/discovery` floci integration
test still stand up only the demo estate (confirmed by inspection: neither
mentions `estates/` or "cohort"). Wiring cohort estates into the gated
apply-against-floci tier is separate follow-on work, the same note
`estates/example/README.md` and `estates/lambda/README.md` already make.

## Verifying by hand

```
docker run -d --rm -p 4610:4566 --name tofu-messaging-cohort-verify floci/floci:latest
export AWS_ENDPOINT_URL=http://localhost:4610 AWS_ACCESS_KEY_ID=test \
       AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

terraform init
terraform validate
terraform apply -auto-approve

terraform destroy -auto-approve
docker rm -f tofu-messaging-cohort-verify
```

`terraform validate` passes against the real provider release (6.58.0) this
fixture pins — confirming every argument name above is real, not just
registry-plausible. An `apply` against `floci/floci:latest` was run by hand
during ratification (not wired into any automated tier — see "Gating"
above), against a temporary fixture that also included an
`aws_sns_topic_subscription.app` block (the deferred type above, added only
for this manual check and not part of the committed fixture), and found the
emulator's actual coverage narrower than its health check advertises, the
same split the Lambda batch found:

- `aws_sns_topic.app` (supporting), `aws_sns_topic_policy.app`,
  `aws_sns_topic_subscription.app` (the deferred type), `aws_sqs_queue.app`,
  `aws_sqs_queue_policy.app` and the supporting `aws_iam_role.messaging`
  create and destroy cleanly.
- `aws_cloudwatch_composite_alarm.app`, `aws_cloudwatch_dashboard.app` and
  `aws_cloudwatch_metric_stream.app` all fail to create: floci's
  `/_localstack/health` reports `monitoring: running`, but
  `PutCompositeAlarm`, `PutDashboard` and `PutMetricStream` each return
  `UnsupportedOperation: Operation ... is not supported by CloudWatch
  JSON.` — the operations are not actually implemented, only the service's
  presence is. This is a floci gap (`choudoufu#26` territory), not
  evidence against any of the three types' admission: identity and
  enumeration are properties of the provider and the registry, not of one
  emulator's completeness.

`aws_sqs_queue.app`'s own apply is the emulator caveat this batch's
ratification explicitly accepted rather than skipped over: floci minted
the queue's `id` as
`http://localhost:4566/000000000000/tofu-messaging-cohort-queue` — its own
container endpoint — rather than the canonical
`https://sqs.us-east-1.amazonaws.com/000000000000/tofu-messaging-cohort-queue`
form real AWS returns. A plain `apply` never notices, because Terraform
just stores whatever `id` the create call handed back and reads through
that same string on every later refresh; nothing at this layer ever
re-derives or re-parses it. The gap bites only in the path this fork's own
stateless marker discovery takes: a context-less run reconstructs the
canonical URL to hand the provider's importer (`internal/live/identity`'s
`aws_sqs_queue` entry expresses that exact template), the AWS provider's
own importer accepts only the `amazonaws.com` form, and floci's URL fails
that parse. `choudoufu#26` tracks the emulator gap; the identity itself is
sound, on paper and by this apply, which is exactly why this batch
ratifies the type rather than skipping it.
