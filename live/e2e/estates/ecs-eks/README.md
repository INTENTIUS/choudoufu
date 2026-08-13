# ecs-eks cohort

Cohort: `ecs-eks`. Ratified by: the fourth registry-backed ratification
batch against #40's strategy and #44's row generator (issue #65) — ECS and
EKS. Mechanism: #48 (see `live/e2e/estates/example/README.md` for the
proof-of-mechanism cohort, and `live/e2e/estates/messaging/README.md` for
the most recent registry-backed cohort this one follows).

This cohort exercises every type this batch ratified into
`internal/live/lint/admission.go` and `internal/live/identity/table.go`.
It does not repeat `aws_ecs_cluster`, already covered by `live/e2e/estate/`,
or `aws_iam_group`, which this same batch ratifies but lands in the
`iam-ecr` cohort instead — see that cohort's own README.

Regenerate the generator's own output with:

```
go run ./tools/estate-gen -cohort ecs-eks \
  -types aws_ecs_cluster_capacity_providers,aws_ecs_daemon,aws_eks_access_entry,aws_eks_access_policy_association,aws_eks_addon,aws_eks_capability,aws_eks_cluster,aws_eks_fargate_profile,aws_eks_node_group \
  -out live/e2e/estates/ecs-eks
```

`-types` is explicit because `defaultCohortTypes` derives a cohort's roster
from a single `live/mapping.json` CFN service name, and this cohort spans
two ("ECS" and "EKS"). Regenerating overwrites the two hand adjustments
below (`ecs-eks.tf`'s `aws_ecs_cluster_capacity_providers.app.cluster_name`
and the supporting `aws_ecs_cluster.ecs-eks` block in `supporting.tf`) —
see "Hand adjustments beyond overrides.go".

## Coverage map

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| Client-named path, docs tier | `aws_ecs_cluster_capacity_providers.app` | Identity is the `cluster_name` argument, already in config. v6.58.0 ships this type with no identity schema at all; the evidence is the documented import command (`terraform import ... my-cluster`) and the Attribute Reference ("id - Same as cluster_name"), the same docs-tier shape `aws_ecs_cluster` itself already carries. Untaggable — a named singleton child of the cluster, the same shape as `aws_s3_bucket_policy`. |
| Marker path | `aws_ecs_daemon.app` | ECS mints the daemon's ARN at create time (`arn:aws:ecs:REGION:ACCOUNT:daemon/CLUSTER/NAME`); the `name` argument is client-chosen but is not the import identity. Taggable, and v6.58.0 ships a native list resource for it — the marker path's two requirements, both met. |
| Client-named path | `aws_eks_access_entry.app` | Identity is `cluster_name` + `principal_arn`, colon-joined, both required arguments per the provider's own Identity Schema — confirmed against `live/import-grammar.json`'s separator and the documented import command. |
| Client-named path, untaggable | `aws_eks_access_policy_association.app` | Identity is `cluster_name` + `principal_arn` + `policy_arn`, octothorp-joined, all three required. Carries no `tags` argument at all — see "Untaggable types" below. |
| Client-named path | `aws_eks_addon.app` | Identity is `cluster_name` + `addon_name`, colon-joined, both required. The Attribute Reference documents `id` explicitly as the same colon-joined pair. |
| Client-named path | `aws_eks_capability.app` | Identity is `cluster_name` + `capability_name`, comma-joined, both required. A newer EKS resource (GitOps capabilities: ArgoCD, ACK, KRO) outside `live/SURVEY.md`'s curated 68, the same standing as `aws_lambda_layer_version` before it. |
| Client-named path | `aws_eks_cluster.app` | Identity is the `name` argument, already in config — the provider's own Attribute Reference says `id` equals it. `live/SURVEY.md`'s curated-68 row already reached "client-named" by hand; its status moves from `blocked-emulator` to `wired` in this batch (the identity was always sound; the floci gap is documented, not blocking — see "Verifying by hand"). |
| Client-named path | `aws_eks_fargate_profile.app` | Identity is `cluster_name` + `fargate_profile_name`, colon-joined, both required, `id` documented as the same pair. |
| Client-named path (name-generation idiom) | `aws_eks_node_group.app` | Identity is `cluster_name` + `node_group_name`, colon-joined. `node_group_name` is Optional+Computed — Terraform assigns a random name when omitted, the same idiom `aws_iam_role`'s own name/name_prefix pair already carries — so `live/SURVEY.md`'s curated-68 row classes it client-named by hand rather than by the strict schema rule, and this entry follows that judgment (see `tools/survey-gen/admission_evidence_test.go`'s `admissionEvidenceExceptions` entry). |

## Rejected

`tools/row-gen`'s ECS and EKS service sections proposed nine types each
(eighteen total) in this batch's scope. Nine are ratified above (plus
`aws_ecs_cluster`, already admitted before this batch). Eight are rejected,
on independent verification against the provider's documented Argument
Reference, Attribute Reference and Import section — not against the
registry's own classification:

- `aws_ecs_capacity_provider` — row-gen proposed client-named via the
  registry's createOnlyProperties `Name`. The provider disagrees: its own
  Identity Schema requires the server-assigned `arn`
  (`arn:aws:ecs:REGION:ACCOUNT:capacity-provider/NAME`), not `name` — the
  same registry-says-client-named-but-the-provider-disagrees shape the
  Lambda and IAM/ECR batches' own rejections established. Even granting
  server-assigned status, v6.58.0 ships this type with no native list
  resource, the same gap that keeps `aws_efs_file_system` out of the marker
  cohort in `internal/live/lint/admission.go`: nothing enumerates it.
- `aws_ecs_daemon_task_definition` — the same family+server-assigned-revision
  shape as `aws_ecs_task_definition` below (ECS's new daemon-scheduling
  sibling of the ordinary task definition, added in the same provider
  release). Rejected for the same reason.
- `aws_ecs_express_gateway_service` — v6.58.0 ships this type with no
  identity schema at all, its `service_name` argument is Optional and
  Terraform-generated when omitted, and row-gen's own enumeration story
  calls it flatly "not listable". Three independent reasons, any one of
  which alone would keep a type out of the four admission paths.
- `aws_ecs_service` — `live/SURVEY.md`'s curated-68 row calls this type
  client-named ("cluster + name, the cluster itself client-named"), and its
  identity schema does require exactly those two names. But the resource's
  own Argument Reference documents `cluster` as "(Optional) ARN of an ECS
  cluster" — accepting an ARN — while the identity schema's `cluster` field
  is documented as "The name of the cluster". The type's own Example Usage
  sets `cluster = aws_ecs_cluster.foo.id`, and this table's own
  `aws_ecs_cluster` entry records that `id` *is* the cluster's ARN, not its
  name — the idiomatic form of this exact argument would silently build a
  wrong identity rather than fail visibly. Needs a config-signal check this
  batch does not attempt, the same non-goal boundary the messaging batch's
  `aws_cloudwatch_event_rule` rejection drew.
- `aws_ecs_task_definition` — `live/SURVEY.md`'s own curated-68 row records
  this type's shape as "family + revision, the revision assigned server-side
  per registration" and groups it among the rows its wrinkles section admits
  neither derivation nor a marker recovers. `revision` is not a configuration
  argument — the Attribute Reference exports it read-only, incrementing on
  every new registration of the same family — which rules out client-naming,
  and, less obviously, rules out the marker path too: ECS does not vary a
  task definition's tags by revision, so a tag-filtered list returns every
  revision of a family under one identical tag set with nothing left to
  tell them apart. A shape outside the four admission paths, honestly;
  rejected rather than forced into either one.
- `aws_ecs_task_set` — a three-part primary identifier
  (`ecs-svc/DEPLOYMENTID,SERVICEARN,CLUSTERARN`) whose `DEPLOYMENTID`
  segment is server-assigned with no configuration argument or previously
  admitted parent's identity attribute supplying it — unlike
  `aws_route53_record`'s `zone_id`, which comes from an already-resolved
  parent. Compounding it, both `cluster` and `service` are documented as
  "Short name or ARN", the same argument-accepts-either-shape ambiguity that
  rejected `aws_ecs_service` above, twice over in one type.
- `aws_eks_identity_provider_config` — `live/import-grammar.json`'s own
  separator (`:`) resolves cleanly, the same shape as `aws_eks_addon` above.
  But `identity_provider_config_name` is not a top-level argument of this
  resource: the provider's Argument Reference nests it inside the required
  `oidc` block. Every `Component` this table has ever built reads a
  top-level resource argument only
  (`internal/live/identity/resolve.go`'s `identityArgs` builds its schema
  from top-level `hcl.AttributeSchema` entries alone) — this table's
  vocabulary cannot honestly express an identity sourced from inside a
  nested block without inventing that capability, which is a mechanism
  change, not a ratification.
- `aws_eks_pod_identity_association` — the identity requires `cluster_name`
  plus `association_id`, which is not a configuration argument at all — the
  provider mints it, documented "The ID of the association", read-only.
  Server-assigned, so this needs the marker path; the type is taggable, but
  v6.58.0 ships it with no native list resource, the same
  `aws_ecs_capacity_provider` gap above: nothing enumerates it.

## A note on floci, not on any of the rejections above

EKS cluster creation is unsupported by the pinned floci image
(lex00/floci#27, still open, reprobed during this batch's own verification
below), so nothing in this cohort that names a `cluster_name` argument could
be apply-verified live. Per issue #65's own recipe ("apply against the
pinned floci image where it serves the types, gaps documented in the cohort
README, not blocking"), that gap is documented rather than treated as a
reason to leave `aws_eks_cluster` and its five EKS dependents unratified —
the same standard the messaging batch applied to `aws_sqs_queue`'s own open
floci gap.

## Untaggable types

One type in the table above, `aws_eks_access_policy_association`, carries no
`tags` argument in the AWS provider — the same shape as
`aws_cloudwatch_dashboard` before it. `live/LIMITATIONS.md`'s "Untaggable
types cannot be removed by the sweep" entry names it directly:
`tools/survey-gen/untaggable_render.go` (issue #54) derives that entry's
roster from `live/survey-full.json`, the registry-backed roster, intersected
with the admission table — not from the curated 68 the way this derivation
used to be scoped, which is exactly the doc-consistency gate that kept
`aws_iam_group` and `aws_sns_topic_subscription` deferred from earlier
batches. `aws_ecs_cluster_capacity_providers` is the same shape, docs tier
rather than registry tier.

## Files

| File | Contents |
|---|---|
| `versions.tf` | `terraform`/`provider "aws"` blocks, identical in shape to `live/e2e/estate/versions.tf`. |
| `locals.tf` | `estate_tag` — `"ecs-eks-cohort"`, distinct from every other cohort's own tag. |
| `ecs-eks.tf` | The nine ratified types. |
| `supporting.tf` | `aws_iam_role.ecs-eks` (estate-gen's own addition, for the four `*_role_arn` arguments) plus `aws_ecs_cluster.ecs-eks` (hand-added — see below) — neither is a coverage row; `aws_iam_role` and `aws_ecs_cluster` are already covered elsewhere. |

## Hand adjustments beyond overrides.go

Most of this cohort's generic-pass corrections live in
`tools/estate-gen/overrides.go`'s own `typeOverrides` table, in the tool's
own extensibility mechanism: six EKS types' `cluster_name` argument, which
the generator's same-name parent search matched against
`aws_ecs_cluster_capacity_providers` (an unrelated ECS type that also
self-identifies by an argument spelled `cluster_name`) rather than
`aws_eks_cluster` — none of the six has a single-component, self-named
identity of its own for the search to find `aws_eks_cluster` by; three
required-ARN and one required-enum argument the generic pass's placeholder
strings do not satisfy; and one scaling-config minimum the generic pass's
zero value undershoots. See `eksClusterNameRef` and each type's own
`Reasons` entry in that file for the detail.

Two adjustments could not be expressed as an override, because they add an
entirely new resource block rather than editing the type's own — a
`typeOverride.Apply` closure receives only its own resource's body:

- `aws_ecs_cluster_capacity_providers.app.cluster_name` names a real ECS
  cluster (`PutClusterCapacityProviders` 400s with `ClusterNotFoundException`
  against one that does not exist, found only by running this cohort
  against floci — see "Verifying by hand"), and `aws_ecs_cluster` is not one
  of this cohort's own requested types, since it is already covered by
  `live/e2e/estate/`. Hand-pointed at the supporting `aws_ecs_cluster`
  resource below instead of a bare string.
- `supporting.tf`'s `aws_ecs_cluster.ecs-eks` — added by hand for exactly
  that reference, the same "supporting, not coverage" shape
  `live/e2e/estates/iam-ecr/iam.tf`'s `aws_iam_role.support` already is.

Regenerating this cohort with `go run ./tools/estate-gen -cohort ecs-eks
-types ... -out live/e2e/estates/ecs-eks` reverts both; re-apply them by
hand afterward, or leave `aws_ecs_cluster_capacity_providers.app` pointed at
a bare string and expect the floci gap above to reproduce on the next live
verification.

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
every cohort README before this one already makes.

## Verifying by hand

```
docker run -d --rm -p 4611:4566 --name tofu-ecs-eks-cohort-verify floci/floci:latest
export AWS_ENDPOINT_URL=http://localhost:4611 AWS_ACCESS_KEY_ID=test \
       AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

terraform init
terraform validate
terraform apply -auto-approve

terraform destroy -auto-approve
docker rm -f tofu-ecs-eks-cohort-verify
```

`terraform validate` passes against the real provider release (6.58.0) this
fixture pins — confirming every argument name above is real, not just
registry-plausible. An `apply` against `floci/floci:latest` was run by hand
during ratification (not wired into any automated tier — see "Gating"
above), and found:

- `aws_ecs_cluster.ecs-eks` (supporting), `aws_iam_role.ecs-eks`
  (supporting) and `aws_ecs_cluster_capacity_providers.app` create and
  destroy cleanly — floci's `/_localstack/health` reports `ecs: running`,
  and `PutClusterCapacityProviders` actually is implemented, once a real
  cluster exists for it to name.
- `aws_ecs_daemon.app` fails to create: `CreateDaemon` returns
  `UnsupportedOperation: Operation CreateDaemon is not supported.` The
  operation is not implemented at all, only ECS's presence is (the same
  split the messaging batch found for `PutCompositeAlarm`, `PutDashboard`
  and `PutMetricStream`) — a floci gap (`choudoufu#26` territory), not
  evidence against the type's admission: identity and enumeration are
  properties of the provider and the registry, not of one emulator's
  completeness.
- `aws_eks_cluster.app` fails: the create call is accepted, but the cluster
  transitions to `FAILED` rather than `ACTIVE`, and the provider's
  availability waiter surfaces that as an error
  (`waiting for EKS Cluster ... create: unexpected state 'FAILED'`) — the
  live confirmation of `lex00/floci#27`, "floci cannot create EKS
  clusters", still open as of this batch. Every type below it in this
  cohort names a `cluster_name` argument, so none of
  `aws_eks_access_entry.app`, `aws_eks_access_policy_association.app`,
  `aws_eks_addon.app`, `aws_eks_capability.app`,
  `aws_eks_fargate_profile.app` or `aws_eks_node_group.app` could be
  create-verified either, for the same reason one level up rather than five
  separate ones.

Nothing above is evidence against any of the nine types' admission: every
identity in the Coverage map is independently confirmed against the
provider's own documented Argument Reference, Attribute Reference and
Import section, fetched from the provider's own `website/docs/r/` source at
the pinned v6.58.0 tag. The floci gaps are `choudoufu#26` and
`lex00/floci#27` territory, tracked there rather than here.
