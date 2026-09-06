# compute-platforms cohort

Cohort: `compute-platforms`. Ratified by: the fifth registry-backed
ratification batch against #40's strategy and #44's row generator,
following issue #65's recipe over a batch its own suggested list did not
name: AWS Batch, the EMR remainder, App Runner, Elastic Beanstalk, Amplify
and Lightsail. Mechanism: #48 (see `live/e2e/estates/example/README.md` for
the proof-of-mechanism cohort, and `live/e2e/estates/route53-cloudfront/README.md`
for the batch this one follows most closely in shape and rigor).

This cohort exercises every type this batch ratified into
`internal/live/lint/admission.go` and `internal/live/identity/table.go`: 26
types across six CFN-adjacent services (Batch, EMR/EMRContainers/
EMRServerless, AppRunner, ElasticBeanstalk, Amplify, Lightsail), rejecting
or deliberately scoping out about twenty more — see "Rejected and out of
scope" below. Because no single CFN service name matches the made-up cohort
label "compute-platforms", this cohort was generated with an explicit
`-types` list rather than `tools/estate-gen`'s default per-service
derivation (see "Regenerate" below).

Two rows needed reclassification and one needed a wrong guess corrected —
all three caught by reading the provider's own docs rather than trusting
the registry or row-gen's own bucket. `internal/live/identity/table.go`'s
own batch-banner comment tells the full story for each; the short version:

- **`aws_batch_job_definition`**: row-gen proposed client-named on an "arn"
  argument, but "arn" is Computed-only in the provider's schema (it embeds
  a revision number Batch mints on every new revision) — not a settable
  argument at all. Ratified below as server-assigned instead.
- **`aws_amplify_app`**: row-gen proposed server-assigned via the
  registry's "Arn" field, but the provider's own Import section documents
  import by the App ID alone, a distinct exported attribute from "arn".
  Ratified below with the App ID as the identity.
- **`aws_elastic_beanstalk_environment`**: row-gen classified this
  evidence-only (its "environment_name" guess was unconfident), but the
  provider's own Import section shows import by the environment's own
  opaque id instead — the CFN registry entry does not even carry an
  EnvironmentId in its own read-only properties, a registry gap the
  provider's docs fill in directly. Ratified below as server-assigned.

## Coverage map

### Marker path (server-assigned, taggable)

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| `aws_batch_compute_environment.app` | AWS Batch mints the compute environment's own ARN at create time; the provider's Identity Schema requires `arn`. |
| `aws_batch_job_definition.app` | AWS Batch mints a new ARN, embedding a revision number, on every job definition revision; no argument reconstructs it. Reclassified from row-gen's client-named "arn" proposal — see above. |
| `aws_batch_job_queue.app` | AWS Batch mints the job queue's own ARN at create time; the provider's Identity Schema requires `arn`. |
| `aws_batch_scheduling_policy.app` | AWS Batch mints the scheduling policy's own ARN at create time; the `name` argument is client-chosen but is not the import identity. |
| `aws_emr_cluster.app` | EMR mints the cluster's own id (`j-…`) at create time. The registry's own `AWS::EMR::Cluster` entry carries `handlers.create/read/update/delete/list` all `false` — a Cloud Control stub — but this fork's marker discovery does not depend on Cloud Control's list handler; `live/survey-full.json` (taggable, "recoverable by tag-filtered list") and the provider's own Import section (`terraform import aws_emr_cluster.cluster j-123456ABCDEF`) confirm the identity independently. |
| `aws_emr_studio.app` | EMR mints the studio's own id (`es-…`) at create time; none of the create-only arguments (`auth_mode`, `service_role`, `vpc_id`, ...) reconstructs it. |
| `aws_emrcontainers_virtual_cluster.app` | EMRContainers mints the virtual cluster's own id at create time. |
| `aws_emrserverless_application.app` | EMRServerless mints the application's own id at create time. |
| `aws_apprunner_auto_scaling_configuration_version.app` | App Runner mints the auto scaling configuration version's own ARN at create time; the provider's Identity Schema requires `arn`. |
| `aws_apprunner_observability_configuration.app` | App Runner mints the observability configuration's own ARN at create time; the provider's Identity Schema requires `arn`. |
| `aws_apprunner_service.app` | App Runner mints the service's own ARN at create time; the `service_name` argument is client-chosen but is not the import identity. |
| `aws_apprunner_vpc_connector.app` | App Runner mints the VPC connector's own ARN at create time; the provider's Identity Schema requires `arn`. |
| `aws_apprunner_vpc_ingress_connection.app` | App Runner mints the VPC ingress connection's own ARN at create time; the provider's Identity Schema requires `arn`. |
| `aws_elastic_beanstalk_environment.app` | Elastic Beanstalk mints the environment's own id (`e-…`) at create time. Reclassified from row-gen's wrong evidence-only guess — see above. |
| `aws_amplify_app.app` | Amplify mints the app's own id (App ID, e.g. `d2ypk4k47z8u6`) at create time; the `name` argument is client-chosen but is not the import identity. Reclassified from row-gen's server-assigned "Arn" proposal to the App ID the provider actually documents importing by — see above. |

### Client-named path

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| `aws_elastic_beanstalk_application.app` | Identity is the `name` argument, already in config — row-gen's own proposal, confirmed against the provider's Import section verbatim. |
| `aws_emr_security_configuration.app` | Identity is the `name` argument, already in config — row-gen's own proposal, confirmed against the provider's Import section verbatim. |
| `aws_lightsail_bucket.app` | Identity is the `name` argument — row-gen's own proposal (import-grammar sourced), confirmed against the provider's Import section. |
| `aws_lightsail_container_service.app` | Identity is the `name` argument — row-gen's own proposal (import-grammar sourced), confirmed against the provider's Import section. |
| `aws_lightsail_distribution.app` | Identity is the `name` argument — row-gen's own proposal (import-grammar sourced), confirmed against the provider's Import section. |
| `aws_lightsail_instance.app` | Identity is the `name` argument. row-gen guessed `instance_name` (evidence-only, unconfident, from the CFN property's snake-cased name); the provider's Argument Reference names the real argument plain `name`. |
| `aws_lightsail_database.app` | Identity is the `relational_database_name` argument. row-gen's own guess (evidence-only, unconfident) happened to be right this time — confirmed against the provider's Argument Reference. |
| `aws_lightsail_lb.app` | Identity is the `name` argument. row-gen guessed `load_balancer_name` (evidence-only, unconfident); the provider's Argument Reference names the real argument plain `name`. |
| `aws_lightsail_certificate.app` | Identity is the `name` argument. row-gen guessed `certificate_name` (evidence-only, unconfident); the provider's Argument Reference names the real argument plain `name`. |
| `aws_lightsail_disk.app` | Identity is the `name` argument. row-gen guessed `disk_name` (evidence-only, unconfident); the provider's Argument Reference names the real argument plain `name`. |
| `aws_lightsail_static_ip.app` | Identity is the `name` argument. row-gen guessed `static_ip_name` (evidence-only, unconfident); the provider's Argument Reference names the real argument plain `name`. |

### Parent-derived path

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| `aws_amplify_branch.app` | Composite `app_id/branch_name`, slash-separated per the provider's own documented import command (`terraform import aws_amplify_branch.master d2ypk4k47z8u6/master`) — `app_id` through the just-ratified `aws_amplify_app` marker, `branch_name` a client-chosen argument. row-gen classified this evidence-only (its own `applyImportGrammarDemotions` check fired on the composite id); independent verification supplies the separator, the same hand-separator-from-docs move the Route53/CloudFront batch made repeatedly. |
| `aws_lightsail_lb_certificate.app` | Composite `lb_name,name`, comma-separated per the provider's own documented import command (`terraform import aws_lightsail_lb_certificate.example example-load-balancer,example-load-balancer-certificate`) — both plain configuration arguments (the certificate's own real argument is `name`, not the registry's `CertificateName`), no marker or tag dependency either half. row-gen classified this needs-hand-separator; independent verification supplies the separator and corrects the second argument's name. |

## Rejected and out of scope

Several proposals this batch's independent verification looked at do not
appear above, for two different reasons: genuinely unrecoverable
(**rejected**), or recoverable but outside this batch's own named scope
(**out of scope**, left for a future batch rather than added on
inspiration):

**Rejected — unrecoverable:**

- **`aws_emr_instance_fleet`, `aws_emr_instance_group`** — row-gen proposed
  both server-assigned via the registry's opaque `Id`. The mapping only
  reaches them at all because `live/mapping.json` aliases them onto
  `AWS::EMR::InstanceFleetConfig` and `AWS::EMR::InstanceGroupConfig` (the
  TF and CFN names do not match directly — issue #65's own "the sweeps
  aliased instance fleets/groups" flag). `live/survey-full.json`'s signal
  rejects both anyway: untaggable, no native list resource, "no admission
  path recovers it" — they are child objects of a cluster with no
  individual tagging or listing surface of their own, the same shape
  `live/tag-verbs.json`'s EMR `AddTags` entry shows (`ClusterId` plus
  `ResourceId`, not a single scalar this fork's tag-filtered marker
  discovery can key on). The provider's own Import section confirms the
  same gap from the other direction: both import by
  `CLUSTERID/FLEETID` or `CLUSTERID/GROUPID`, a composite whose second half
  names a specific live instance discovery has no way to find without the
  tag or list surface neither type has.
- **`aws_lightsail_domain`** — row-gen's `domain_name` guess is in fact
  correct (confirmed against the provider's Argument Reference, the sole
  required argument), but the provider ships no Import section for this
  resource at all (confirmed absent from both `live/import-grammar.json`
  and the provider's own docs page), and `live/survey-full.json`'s signal
  agrees separately: untaggable, no native list resource. Two independent
  rejections, and also simply not one of the categories ("instances,
  databases, buckets, LBs + certificates") this batch's own Lightsail scope
  named.

**Out of scope — recoverable, deliberately not added:**

- **`aws_emr_studio_session_mapping`** — needs-hand-separator per row-gen,
  and the provider's Import section does supply a real separator
  (`STUDIOID:IDENTITYTYPE:IDENTITYID`, all three plain configuration
  arguments) the same way the Route53/CloudFront batch hand-verified
  several composites. This batch's own scope for the EMR remainder is
  "only what row-gen proposes" (a narrower mandate than Lightsail's, which
  was explicitly scoped to expand on row-gen's wrong guesses), so it is
  left out here as a deliberate boundary, not a recoverability failure.
- **`aws_emr_managed_scaling_policy`** — folded by row-gen as a
  property-child of `AWS::EMR::Cluster` (evidence-only). Its own Import
  section ("using the EMR Cluster identifier") would make it a clean
  named-singleton-child of `aws_emr_cluster`, the same shape as
  `aws_route53_hosted_zone_dnssec`, once in scope — same boundary as the
  session mapping above.
- **`aws_elastic_beanstalk_application_version`,
  `aws_elastic_beanstalk_configuration_template`** — both
  needs-hand-separator per row-gen. This batch's own scope for Elastic
  Beanstalk names only "applications, environments", and independent
  verification does not make either an easy hand-add regardless: the
  provider ships no Import section at all for
  `aws_elastic_beanstalk_application_version`, and
  `aws_elastic_beanstalk_configuration_template`'s
  `live/survey-full.json` signal is untaggable with no native list
  resource.
- **`aws_amplify_domain_association`** — evidence-only per row-gen; import
  docs show the same composite shape as `aws_amplify_branch` above
  (`APPID/DOMAINNAME`), independently verifiable the same way. This
  batch's own scope for Amplify names only "apps/branches", so it stays
  out.
- **`aws_amplify_webhook`** — never reaches row-gen at all:
  `live/mapping.json` records it `cfn-unmodeled` ("searched AWS::Amplify:
  only App, Branch and Domain exist; no Webhook type") and notes by name
  that it is not `AWS::CodePipeline::Webhook`, a different service's
  unrelated same-named concept — the false-positive risk issue #65's own
  recipe flagged in advance for this batch. Outside the registry-backed
  pipeline entirely; nothing to ratify or reject.
- **`aws_lightsail_bucket_resource_access`,
  `aws_lightsail_container_service_deployment_version`,
  `aws_lightsail_disk_attachment`, `aws_lightsail_domain_entry`,
  `aws_lightsail_lb_attachment`, `aws_lightsail_lb_certificate_attachment`,
  `aws_lightsail_lb_https_redirection_policy`,
  `aws_lightsail_lb_stickiness_policy`, `aws_lightsail_bucket_access_key`**
  — property-child folds of the Lightsail types ratified above. Several
  now have every parent they need (`aws_lightsail_lb_attachment`'s
  `LBNAME,INSTANCENAME` composite, for one), but adding parent-derived rows
  for a whole second tier of child types is a bigger step than this
  batch's named Lightsail scope ("instances, databases, buckets, LBs +
  certificates") asked for. Left for a future batch.

## Untaggable types

Three of the 26 rows above carry no `tags` argument in the AWS provider:
`aws_emr_security_configuration` (confirmed against `live/survey-full.json`
and the registry's own `create_only_properties`, which name no `Tags`
field the way a taggable CFN entry does) and the two Lightsail
parent-derived composites, `aws_lightsail_lb_certificate` and
`aws_lightsail_static_ip` (confirmed the same way, and directly against the
provider's own Argument Reference for each — neither lists `tags` among
its optional arguments, unlike `aws_lightsail_disk`, `aws_lightsail_instance`
and the rest of this cohort's client-named Lightsail rows, which do). None
of the three needs the marker path anyway — `aws_emr_security_configuration`
is client-named and the two Lightsail rows are parent-derived composites,
so their admission does not depend on carrying a tag at all, the same
shape `aws_route53_record` and this cohort's own `aws_amplify_branch` are.
All three **are** added to `live/LIMITATIONS.md`'s "Untaggable types cannot
be removed by the sweep" entry and to
`internal/live/stamp/stamp_test.go`'s `untaggableAdmittedTypes` pin.

## Files

| File | Contents |
|---|---|
| `versions.tf` | `terraform`/`provider "aws"` blocks, identical in shape to `live/e2e/estate/versions.tf`. |
| `locals.tf` | `estate_tag` — `"compute-platforms-cohort"`, distinct from every other cohort's own tag. |
| `compute-platforms.tf` | All 28 resource blocks — 26 ratified types plus generic cross-references: several types' own `name` argument points at `aws_elastic_beanstalk_application.app.name` rather than a second literal string (`tools/estate-gen`'s generic pass picks one already-admitted same-argument-name resource to reference; cosmetic only, `terraform validate` does not care). |

### Overrides

`tools/estate-gen/overrides.go` carries thirteen entries for this cohort —
a provider-side requirement `terraform validate` catches that the wire
schema alone does not name (an enum member no `configschema.Attribute`
marks, a one-of-block requirement the schema calls two independently
Optional blocks, a string length or character-set constraint, an
AZ-within-region plan-time check). Every entry cites the exact `terraform
validate` error it exists to fix; the command below regenerates
`compute-platforms.tf` with every override applied, and each resource
block there carries its own `# overrides: ...` comment naming which one
(or `# overrides: none` for the fifteen rows the generic required-only
pass alone rendered validate-clean).

## Regenerate

No single CFN service name matches "compute-platforms" (it spans Batch,
EMR/EMRContainers/EMRServerless, AppRunner, ElasticBeanstalk, Amplify and
Lightsail), so `tools/estate-gen`'s default per-service type derivation
does not apply here; regenerate with the full `-types` list instead:

```
go run ./tools/estate-gen -cohort compute-platforms -out /tmp/estate-gen/compute-platforms \
  -types aws_batch_compute_environment,aws_batch_job_definition,aws_batch_job_queue,aws_batch_scheduling_policy,\
aws_emr_cluster,aws_emr_security_configuration,aws_emr_studio,aws_emrcontainers_virtual_cluster,aws_emrserverless_application,\
aws_apprunner_auto_scaling_configuration_version,aws_apprunner_observability_configuration,aws_apprunner_service,\
aws_apprunner_vpc_connector,aws_apprunner_vpc_ingress_connection,\
aws_elastic_beanstalk_application,aws_elastic_beanstalk_environment,\
aws_amplify_app,aws_amplify_branch,\
aws_lightsail_bucket,aws_lightsail_certificate,aws_lightsail_container_service,aws_lightsail_database,aws_lightsail_disk,\
aws_lightsail_distribution,aws_lightsail_instance,aws_lightsail_lb,aws_lightsail_lb_certificate,aws_lightsail_static_ip
```

## Gating

Nothing here runs against a live or emulated cloud automatically. `go test
./internal/live/lint/... ./internal/live/identity/...` picks this directory
up through `internal/live/flocitest.FixtureDirs` (#48's union pin) and
checks it by static HCL parse — `TestAdmissionTableCoversEstate` and
`TestTableCoversFixtureTypes` require `admittedTypesV0` and `DefaultTable`
to cover exactly the union of `live/e2e/estate/` and every `estates/*`
cohort, this one included.

`terraform validate` passes against the real provider release (6.58.0) this
fixture pins.

## Verifying by hand

```
docker run -d --rm -p 4630:4566 --name tofu-compute-platforms-cohort-verify \
  ghcr.io/lex00/floci@sha256:4753246c0260a22af1056c65993f4d73b0a907729a9580b9baba5d628b6dad34

export AWS_ENDPOINT_URL=http://localhost:4630 AWS_ACCESS_KEY_ID=test \
       AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

terraform init
terraform validate
terraform apply -auto-approve

terraform destroy -auto-approve
docker rm -f tofu-compute-platforms-cohort-verify
```

An `apply` against the pinned `lex00/floci` image was run by hand during
ratification (not wired into any automated tier — see "Gating" above), and
found the emulator's actual coverage narrower than its health check
advertises, the same split every earlier batch found — none of it is
evidence against any row's admission above, since identity and enumeration
are properties of the provider and the registry, not of one emulator's
completeness:

- `aws_batch_compute_environment.app`, `aws_emr_security_configuration.app`,
  `aws_lightsail_disk.app`, `aws_lightsail_instance.app` and
  `aws_lightsail_static_ip.app` created (and read back) cleanly.
- `aws_elastic_beanstalk_application.app` accepted its create call
  (confirmed present in state afterward) but then failed the very next
  step, the provider's post-create tags read
  (`ListTagsForResource` returning `UnsupportedOperation: Operation
  ListTagsForResource is not supported`) — the same "advertises the
  service, not the operation" gap the messaging batch found for
  CloudWatch. Because several other rows in this cohort's estate
  generically cross-reference this resource's own `name` output (see
  "Files" above), that one failed read-back step blocked Terraform's
  dependency graph from even attempting
  `aws_batch_job_definition.app`, `aws_batch_job_queue.app`,
  `aws_batch_scheduling_policy.app`, `aws_emr_cluster.app`,
  `aws_emr_studio.app`, `aws_emrcontainers_virtual_cluster.app`,
  `aws_emrserverless_application.app`,
  `aws_apprunner_vpc_ingress_connection.app`,
  `aws_elastic_beanstalk_environment.app`, `aws_amplify_app.app` and
  `aws_lightsail_lb_certificate.app` in the same run — an artifact of the
  estate's generic same-argument-name cross-referencing, not evidence
  about any of those eleven rows' own admission.
- `aws_apprunner_auto_scaling_configuration_version.app`,
  `aws_apprunner_observability_configuration.app`,
  `aws_apprunner_service.app` and `aws_apprunner_vpc_connector.app` all
  failed outright with `404 UnknownOperationException` — the App Runner
  create operations are not implemented in this floci image at all, not
  merely unadvertised.
- `aws_lightsail_bucket.app`, `aws_lightsail_certificate.app`,
  `aws_lightsail_container_service.app`, `aws_lightsail_database.app`,
  `aws_lightsail_distribution.app` and `aws_lightsail_lb.app` all failed
  with an explicit `400 UnsupportedOperation: Operation ... is recognized
  by Amazon Lightsail but is not implemented in Floci` — the emulator's
  own error message naming the gap directly, distinct from the bare
  `UnknownOperationException` App Runner returned.
- `aws_amplify_branch.app` failed with a deserialization error
  (`invalid character '<' looking for beginning of value`) — the emulator
  returned an HTML error body where the provider expected JSON, a floci
  response-shape bug, not a configuration error; `terraform validate` and
  the plan both accepted the resource cleanly.

`choudoufu#26` is the umbrella tracking issue.
