# iam-ecr cohort

Cohort: `iam-ecr`. Ratified by: the second registry-backed ratification
batch against #40's strategy and #44's row generator, closing issue #26's
two named types. Mechanism: #48 (see `live/e2e/estates/example/README.md`
for the proof-of-mechanism cohort this one follows, and
`live/e2e/estates/lambda/README.md` for the first registry-ratified batch
this one repeats the shape of).

This cohort exercises every type `tools/row-gen`'s IAM and ECR batch
proposed and this batch ratified into `internal/live/lint/admission.go` and
`internal/live/identity/table.go`, plus one addition (`aws_iam_group`): the
ECS/EKS batch (issue #65) ratified its own deferral here rather than
opening a second cohort for one already-settled type — see "Issue #26" and
"Rejected, and deliberately absent" below for the history, and
`live/e2e/estates/ecs-eks/README.md` for that batch's own cohort.

## Coverage map

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| Client-named path | `aws_ecr_repository.app` | Identity is the `name` argument, already in config — confirmed against the provider's own identity schema (`live/survey-full.json`). Issue #26's first named type. |
| Marker path, untaggable | `aws_ecr_registry_policy.app` | A singleton per AWS account; identity is the account's own ECR registry ID, which pre-exists the resource and is never supplied by a configuration argument. |
| Marker path, untaggable | `aws_ecr_registry_scanning_configuration.app` | Same singleton-per-account shape as the registry policy. |
| Marker path, untaggable | `aws_ecr_replication_configuration.app` | Same singleton-per-account shape as the registry policy. |
| Client-named path, untaggable | `aws_iam_group.app` | Identity is the `name` argument, already in config. No identity schema shipped in v6.58.0; the evidence is the documented import command, which sets `id` to the group name verbatim. IAM has no `TagGroup` API, so this type carries no `tags` argument at all — see "Untaggable types" below. Ratified by the ECS/EKS batch (issue #65); see "Issue #26" for why this batch itself deferred it. |
| Client-named path | `aws_iam_instance_profile.app` | Identity is the `name` argument, already in config — confirmed against the provider's own identity schema. |
| Marker path | `aws_iam_service_linked_role.app` | IAM computes the role's name from `aws_service_name` using its own internal per-service convention, not a string transform of any configured argument. |
| Client-named path | `aws_iam_user.app` | Identity is the `name` argument, already in config — confirmed against the provider's own identity schema. Issue #26's second named type. |

`aws_iam_role.support` is supporting infrastructure for the instance
profile's role, not a coverage row — see "Supporting, not coverage" in
`iam.tf`; `aws_iam_role` is already covered by `live/e2e/estate/` and
`live/e2e/estates/lambda/`.

## Issue #26

Both types issue #26 named — `aws_ecr_repository` and `aws_iam_user` — are
ratified in this batch and confirmed live against the pinned floci image
(see "Verifying by hand" below): both create, tag, and destroy cleanly, and
a refresh-only plan afterward reports no drift. The earlier
blocked-emulator notes in `live/SURVEY.md` no longer hold for either row.

## Rejected, and deliberately absent

`tools/row-gen` proposed 13 pastable rows across the two services (plus
evidence-only and needs-hand-separator rows this batch never touches, and
`aws_iam_role`, its eighth IAM proposal, already wired via this table's own
pre-#40 slice rather than via the registry). 7 are ratified above. 5 more
were proposed and independently rejected on verification against the
provider's own documented import behavior, not against the registry:

- `aws_iam_policy` — the registry's opaque `Id` (read-only) led row-gen to
  classify it server-assigned. The provider's documented import ID is the
  policy's ARN, and the ARN embeds the `name` and `path` arguments already
  in configuration (`name` is optional — Terraform assigns a random one
  when omitted — but when set, it is literally the ARN's final path
  segment). `live/SURVEY.md` already carries this type as client-named,
  account-derived, the same `CloudContext` mechanism `aws_sns_topic` uses;
  wiring it that way is follow-on work this batch does not attempt.
- `aws_iam_saml_provider` — same failure shape: the registry's `Arn` reads
  as server-assigned, but `name` is a *required* argument with no
  generated fallback, and it is literally the ARN's final path segment
  (`arn:aws:iam::ACCOUNT:saml-provider/NAME`).
- `aws_iam_virtual_mfa_device` — same failure shape again: the registry's
  `SerialNumber` reads as server-assigned, but the provider's own docs say
  the serial number *is* the ARN
  (`arn:aws:iam::ACCOUNT:mfa/NAME`), and `NAME` is the required
  `virtual_mfa_device_name` argument verbatim. The type also mints a
  secret, `base_32_string_seed`, that can never be read back after
  create — a second, independent reason it needs care beyond this batch's
  scope even had its identity checked out.
- `aws_iam_access_key` — the classification (server-assigned via the
  registry's opaque `Id`) is not disputed, but this type is one of the
  three `live/SURVEY.md`'s "The three the rule excludes" names
  permanently: an access key is a credential born server-side alongside a
  secret that can never be read again, forwarded to the lifecycle layer by
  the fork's own architecture rather than modeled as an ordinary resource.
  Admitting it here would reverse that standing decision.

One more was correctly classified but was deferred rather than wired, by
this batch:

- `aws_iam_group` — row-gen correctly proposed client-named via `name`,
  confirmed against the provider's documented import (id is the group
  name verbatim). `live/survey.json` — the curated 68
  `live/SURVEY.md` measures — already carries this type, and its own
  signal says untaggable (IAM has no `TagGroup` API). Admitting it would
  have moved it into `tools/survey-gen/limitations_test.go`'s
  `TestLimitationsDocAgainstSurvey` derived set (admitted ∩ curated-68 ∩
  untaggable), which required `live/LIMITATIONS.md`'s "Untaggable types
  cannot be removed by the sweep" entry to name it. Unlike
  `aws_lambda_layer_version`, which sidesteps that doc by being outside
  the curated 68 entirely, `aws_iam_group` could not dodge it that way — it
  is squarely inside the 68. Extending the doc's derivation past the
  curated 68 was issue #54's own scope, not this batch's, so `aws_iam_group`
  was left for a batch prepared to move that doc.

  **Update, issue #65:** #54 has since landed —
  `tools/survey-gen/untaggable_render.go`'s "untaggable-admitted" render now
  derives `live/LIMITATIONS.md`'s entry from `live/survey-full.json`
  (the registry-backed roster) intersected with the admission table, with
  no curated-68 boundary left to carve around. The ECS/EKS batch ratifies
  this deferral (see the Coverage map above); nothing about the identity
  evidence changed from what this section already established.

## Untaggable types

Four types above — `aws_ecr_registry_policy`,
`aws_ecr_registry_scanning_configuration`,
`aws_ecr_replication_configuration` and `aws_iam_group` — carry no `tags`
argument in the AWS provider, the same shape as `aws_lambda_layer_version`
in the first registry-ratified batch. All four are named in
`live/LIMITATIONS.md`'s "Untaggable types cannot be removed by the sweep"
entry: `tools/survey-gen/untaggable_render.go` (issue #54) derives that
entry's roster from `live/survey-full.json`, the 1,691-type registry-backed
roster, intersected with the admission table — not from `live/survey.json`,
the curated 68-type roster, the way this derivation used to be scoped. That
generalization is exactly what let the ECS/EKS batch (issue #65) ratify
`aws_iam_group`'s own deferral above: the first three ECR types entered the
doc as soon as #54 landed, and `aws_iam_group` joins them here rather than
in a cohort of its own.

## Files

| File | Contents |
|---|---|
| `versions.tf` | `terraform`/`provider "aws"` blocks, identical in shape to `live/e2e/estate/versions.tf`. |
| `locals.tf` | `estate_tag` — `"iam-ecr-cohort"`, distinct from the demo estate's `"stateless-e2e"` and the lambda cohort's `"lambda-cohort"`. |
| `iam.tf` | The four ratified IAM types (three from this cohort's own batch, plus `aws_iam_group` from the ECS/EKS batch), plus `aws_iam_role.support` — supporting infrastructure, not a coverage row. |
| `ecr.tf` | The four ratified ECR types. |

## Gating

Nothing here runs against a live or emulated cloud yet as part of the
automated test tiers. `go test ./internal/live/lint/... ./internal/live/identity/...`
picks this directory up through `internal/live/flocitest.FixtureDirs`
(#48's union pin) and checks it by static HCL parse —
`TestAdmissionTableCoversEstate` and `TestTableCoversFixtureTypes` require
`admittedTypesV0` and `DefaultTable` to cover exactly the union of
`live/e2e/estate/` and every `estates/*` cohort, this one included.

`live/e2e/run.sh` and the gated `internal/live/discovery` floci
integration test still stand up only the demo estate. Wiring cohort
estates into the gated apply-against-floci tier is separate follow-on
work, the same note `estates/example/README.md` and
`estates/lambda/README.md` already make.

## Verifying by hand

```
docker run -d --rm -p 4610:4566 --name tofu-iam-ecr-cohort-verify \
  ghcr.io/lex00/floci@sha256:4753246c0260a22af1056c65993f4d73b0a907729a9580b9baba5d628b6dad34
export AWS_ENDPOINT_URL=http://localhost:4610 AWS_ACCESS_KEY_ID=test \
       AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

terraform init
terraform validate
terraform apply -auto-approve

terraform destroy -auto-approve
docker rm -f tofu-iam-ecr-cohort-verify
```

`terraform validate` passes against the real provider release (6.58.0)
this fixture pins. An `apply` against the pinned floci image
(`internal/live/flocitest.defaultImage`, the fork carrying the IAM-tag and
ECR fixes issue #26 tracks) was run by hand during ratification, not wired
into any automated tier — see "Gating" above:

- `aws_ecr_repository.app`, `aws_iam_user.app`, `aws_iam_instance_profile.app`
  and the supporting `aws_iam_role.support` all create cleanly, and a
  subsequent `terraform plan -refresh-only` reports no drift — tags round-
  trip through Get/List for all four, confirming the fixes lex00/floci#22
  and #24 (`iam:GetRole`/`GetUser`/`GetPolicy` now return `Tags`) and
  #23/#25 (`ecr:CreateRepository` no longer needs a Docker daemon)
  actually reach the two types #26 named, plus the instance profile as a
  bonus: floci's `iam:GetInstanceProfile` also returns `Tags` now, even
  though the flocitest.go pin comment only names Role/User/Policy —
  `live/SURVEY.md`'s `aws_iam_instance_profile` row is updated to `wired`
  alongside the two named types.
- `aws_ecr_registry_policy.app`, `aws_ecr_registry_scanning_configuration.app`,
  `aws_ecr_replication_configuration.app` and `aws_iam_service_linked_role.app`
  all fail to create: floci's `/_localstack/health` reports `ecr: running`
  and `iam: running`, but `PutRegistryPolicy`, `PutRegistryScanningConfiguration`,
  `PutReplicationConfiguration` and `CreateServiceLinkedRole` all return
  `UnsupportedOperation` — the operations are not implemented, only the
  services' presence is. This is a floci gap in the same family as the
  first batch's capacity-provider and code-signing-config findings, not
  evidence against any of the four types' admission: identity and
  enumeration are properties of the provider and the registry, not of one
  emulator's completeness.

**Update, issue #65:** `aws_iam_group.app` was verified separately, when
this batch ratified the deferral above — `terraform apply -target
aws_iam_group.app` against `floci/floci:latest` creates the group cleanly
(`id = tofu-iam-ecr-cohort-group`) and `terraform destroy -target
aws_iam_group.app` removes it, with no drift on refresh in between. No
floci gap on this row.
