# The gauntlet board

An estate is a real OpenTofu or Terraform configuration someone else wrote,
pinned by commit and run through every stage below beside stock OpenTofu. It
is clear when every headline stage passes. The board is re-measured when the
maintainer runs it, not on every change, so a row is as current as its commit
and date. [GAUNTLET.md](GAUNTLET.md) defines the stages and the artifact; the
[claims](CLAIMS.md) are the proof surface for people deciding whether to use
choudoufu.

Everything between the generated markers is rendered from
`live/gauntlet-board.json` and `live/gauntlet.json` by
`go run ./tools/gauntlet render`. Edit the text around the markers, never
inside them.

<!-- board-gen:begin banner -->
Estates below were last measured against different emulator pins: 23 against `ghcr.io/lex00/floci@sha256:6c3d5c2dac72209e6f2e45c08902e783480d4d34f48d25daba8da94b9f882072`, 2 against `ghcr.io/lex00/floci@sha256:ff46eb8d1b73a9727948d1a194a660972e44cc64164a486548da7cb468a09144` (last_run.date ranges from 2026-10-02T19:19:40Z to 2026-10-21T16:24:51Z across these rows, not one shared measurement). The current pin is `ghcr.io/lex00/floci@sha256:5ec92ae8046445d62c0009590dd09f04560886e5a5ef31248cb3fcc09b2961ec`; a row not measured against it is stale evidence, not a failure - `go run ./tools/gauntlet next` surfaces it as work.

The engine is built on OpenTofu `1.13.0` (`version/VERSION`). Of the 46 estates measured so far, 46 recorded running on it.

Estates were last measured against different hashicorp/aws versions: 20 against `6.63.0` (current pin), 1 against `5.100.0`, 1 against `6.59.0`. The current pin is `6.63.0`; a row not measured against it is stale evidence, not a failure - `go run ./tools/gauntlet next` surfaces it as work. Estates were last measured against different hashicorp/kubernetes versions: 17 against `3.2.1` (current pin), 3 against `3.3.0`. The current pin is `3.2.1`; a row not measured against it is stale evidence, not a failure - `go run ./tools/gauntlet next` surfaces it as work.

**44 of 46 rows below were measured before their own estate directory, or the shared protocol library they source, last changed.** Their verdicts describe an earlier version of the crossing script: a stage one of those edits broke still reads `pass` here until the estate is re-run, and a stage one of them fixed still reads `fail` (#1264). For 20 of them nothing in their own directory moved: what changed is `live/e2e/lib`, the protocol library every crossing script sources (#1292). The rows are corpus-alb-complete, corpus-autoscaling-complete, corpus-cloud-platform-components, corpus-dynamodb-table-basic, corpus-ec2-instance-complete, corpus-ecs-fargate, corpus-eks-basic, corpus-evoteum-modules, corpus-giantswarm-crossplane, corpus-govuk-cluster-access, corpus-govuk-cluster-services, corpus-hongbomiao-labelbox, corpus-iam-policy, corpus-k8s-metrics-server, corpus-lambda-simple, corpus-leynos-monitoring, corpus-mastino-dns, corpus-overture-tiles, corpus-quickpizza, corpus-rds-complete-postgres, corpus-s3-bucket-complete, corpus-security-group-complete, corpus-simpleinfra-dns, corpus-sqs-basic, corpus-sumaform-aws, corpus-vpc-complete, corpus-xancloud-iac, reference-ec2-vpc, reference-eks, reference-k8s, reference-k8s-cert-manager, reference-k8s-helm-argo-cd, reference-k8s-helm-cloudnative-pg, reference-k8s-helm-external-secrets, reference-k8s-helm-ingress-nginx, reference-k8s-helm-keda, reference-k8s-helm-kyverno, reference-k8s-helm-metallb, reference-k8s-helm-template, reference-k8s-platform-app, reference-k8s-shared-objects, reference-k8s-workloads, reference-k8s-yaml-rollouts, terralith-scale.

<!-- board-gen:end banner -->

## The bars

The two AWS bars count every estate that runs on the emulator. The third is
the `kubernetes` lane, whose estates run against a real API server on a kind
cluster and count toward neither AWS bar.

<!-- board-gen:begin bars -->
| Bar | Clear | Verified on the current pins | Carried from an earlier pin |
|---|---|---|---|
| Core estates | 23 of 23 | 0 | 23 |
| All estates | 24 of 25 | 0 | 24 |
| kubernetes lane | 19 of 19 | 0 | 19 |

Per active stage, over the same bars:

| Stage | Core estates | All estates | kubernetes lane |
|---|---|---|---|
| Cold deploy | 23 pass, 0 fail, 0 not run | 25 pass, 0 fail, 0 not run | 19 pass, 0 fail, 0 not run |
| Migrate | 23 pass, 0 fail, 0 not run | 24 pass, 1 fail, 0 not run | 19 pass, 0 fail, 0 not run |
| Replan from nothing | 23 pass, 0 fail, 0 not run | 24 pass, 0 fail, 1 not run | 19 pass, 0 fail, 0 not run |
| No-op apply | 23 pass, 0 fail, 0 not run | 24 pass, 0 fail, 1 not run | 19 pass, 0 fail, 0 not run |
| Drift and reconverge | 23 pass, 0 fail, 0 not run | 24 pass, 0 fail, 1 not run | 19 pass, 0 fail, 0 not run |
| Rename | 23 pass, 0 fail, 0 not run | 24 pass, 0 fail, 1 not run | 19 pass, 0 fail, 0 not run |
| Remove a block | 23 pass, 0 fail, 0 not run | 24 pass, 0 fail, 1 not run | 19 pass, 0 fail, 0 not run |
| Change count | 23 pass, 0 fail, 0 not run | 24 pass, 0 fail, 1 not run | 19 pass, 0 fail, 0 not run |
| Replace with create_before_destroy | 23 pass, 0 fail, 0 not run | 24 pass, 0 fail, 1 not run | 19 pass, 0 fail, 0 not run |
| Crash mid-apply | 1 pass, 0 fail, 22 not run | 1 pass, 0 fail, 24 not run | 19 pass, 0 fail, 0 not run |
| Teardown | 0 pass, 0 fail, 23 not run | 0 pass, 0 fail, 25 not run | 19 pass, 0 fail, 0 not run |
| Plan, review, apply | 23 pass, 0 fail, 0 not run | 24 pass, 0 fail, 1 not run | 19 pass, 0 fail, 0 not run |
| Greenfield apply | 23 pass, 0 fail, 0 not run | 24 pass, 0 fail, 1 not run | 19 pass, 0 fail, 0 not run |
| Strict profile | 1 pass, 0 fail, 22 not run | 1 pass, 0 fail, 24 not run | 19 pass, 0 fail, 0 not run |
| Plan with no local state | 0 pass, 0 fail, 23 not run | 0 pass, 0 fail, 25 not run | 18 pass, 0 fail, 1 not run |

Behaviors proven: 1 of 15 stages have their representative-set fixtures passing against the tier-1 behavior matrix. It is a development-loop signal, not a coverage claim.
<!-- board-gen:end bars -->

## The stages

Planned stages show the target and count toward nothing until activated.

<!-- board-gen:begin stages -->
| # | Stage | Status | Headline | What a pass proves |
|---|---|---|---|---|
| 1 | Cold deploy | active | yes | The estate is real and buildable: the stock binary applies the unmodified configuration against the emulator, with no live block and no choudoufu involved. |
| 2 | Migrate | active | yes | `choudoufu live-import -approve` against the stock state file binds every instance: each state entry becomes a marker on the resource, a record, or an identity derived from the declaration, and the summary line reports zero skipped. |
| 3 | Replan from nothing | active | yes | With the state file deleted, `choudoufu live-plan` is empty, and a representative set of rendered identities equals what the AWS CLI reports for the same objects. |
| 4 | No-op apply | active | yes | Applying the empty plan changes nothing: the estate's tagged-object count before and after is identical. |
| 5 | Drift and reconverge | active | yes | One live object is mutated out of band through the AWS CLI; the next plan proposes fixing exactly that object and nothing else, and apply reconverges it. |
| 6 | Rename | active | yes | Renaming a resource through a `moved` block and through `choudoufu live-mv` both produce zero churn: no destroy, no create, the marker rewritten in place. |
| 7 | Remove a block | active | yes | Deleting a resource block destroys the object under the default policy, in an order the cloud accepts, including blocks for untaggable children whose parents stay. |
| 8 | Change count | active | yes | Scaling a `count` block down and back up destroys and creates only the instances stock would, and every surviving instance keeps its identity. |
| 9 | Replace with create_before_destroy | active | yes | A forced replacement under `create_before_destroy` creates the new object, destroys the old one, and the next plan is empty with no marker collision. |
| 10 | Crash mid-apply | active | yes (tier-1 gated) | A replace interrupted after the create and before the destroy is recovered by the next plan without a human: the old object is destroyed, the new one is bound. |
| 11 | Teardown | active | yes (tier-1 gated) | `choudoufu apply -destroy` removes every object the estate owns in one apply, in an order the cloud accepts, and leaves nothing marked. |
| 12 | Plan, review, apply | active | yes | `plan -out` followed by `apply <planfile>` applies when the world has not moved and refuses, naming the mismatch, when it has. |
| 13 | Greenfield apply | active | yes | Applying the same configuration from an empty account with choudoufu directly, no migration, produces the same objects stock's cold deploy produced, plus markers. |
| 14 | Strict profile | active | no | With every strict toggle on, the estate is refused for exactly the things the toggles name (secrets stored, markers unrepaired, and so on) with the documented message, and for nothing else. |
| 15 | Plan with no local state | active | no | After the estate is applied and its plan is empty, deleting BOTH the local record store and the state cache - a fresh clone or a new machine, with only the account left - still yields a plan that finds every declared object by its own marker or a stamped parent: nothing created, destroyed or replaced. |
<!-- board-gen:end stages -->

## The estates

Each estate links to its crossing script's directory. "script changed" means
the estate's own files, or the shared protocol library it sources, moved
after the run the row records.

<!-- board-gen:begin estates -->
P pass, **F** fail, - not run, n/a does not apply on this substrate, s stale (carried from an earlier run than the one the row records). Columns are the stage numbers above.

| Estate | Set | Lane | Clear | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 | 13 | 14 | 15 | Last run |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| <a id="corpus-alb-complete"></a>[corpus-alb-complete](e2e/corpus-alb-complete/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-autoscaling-complete"></a>[corpus-autoscaling-complete](e2e/corpus-autoscaling-complete/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-dynamodb-table-basic"></a>[corpus-dynamodb-table-basic](e2e/corpus-dynamodb-table-basic/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-ec2-instance-complete"></a>[corpus-ec2-instance-complete](e2e/corpus-ec2-instance-complete/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-ecs-fargate"></a>[corpus-ecs-fargate](e2e/corpus-ecs-fargate/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-eks-basic"></a>[corpus-eks-basic](e2e/corpus-eks-basic/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-evoteum-modules"></a>[corpus-evoteum-modules](e2e/corpus-evoteum-modules/) | core | opentofu-native | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-giantswarm-crossplane"></a>[corpus-giantswarm-crossplane](e2e/corpus-giantswarm-crossplane/) | core | opentofu-native | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-hongbomiao-labelbox"></a>[corpus-hongbomiao-labelbox](e2e/corpus-hongbomiao-labelbox/) | core | opentofu-native | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `2f02038dd1` 2026-10-12 |
| <a id="corpus-iam-policy"></a>[corpus-iam-policy](e2e/corpus-iam-policy/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-lambda-simple"></a>[corpus-lambda-simple](e2e/corpus-lambda-simple/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-leynos-monitoring"></a>[corpus-leynos-monitoring](e2e/corpus-leynos-monitoring/) | core | opentofu-native | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-overture-tiles"></a>[corpus-overture-tiles](e2e/corpus-overture-tiles/) | core | opentofu-native | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-rds-complete-postgres"></a>[corpus-rds-complete-postgres](e2e/corpus-rds-complete-postgres/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-s3-bucket-complete"></a>[corpus-s3-bucket-complete](e2e/corpus-s3-bucket-complete/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-security-group-complete"></a>[corpus-security-group-complete](e2e/corpus-security-group-complete/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-simpleinfra-dns"></a>[corpus-simpleinfra-dns](e2e/corpus-simpleinfra-dns/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-sqs-basic"></a>[corpus-sqs-basic](e2e/corpus-sqs-basic/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-sumaform-aws"></a>[corpus-sumaform-aws](e2e/corpus-sumaform-aws/) | core | opentofu-native | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-vpc-complete"></a>[corpus-vpc-complete](e2e/corpus-vpc-complete/) | core | terraform-popular | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-xancloud-iac"></a>[corpus-xancloud-iac](e2e/corpus-xancloud-iac/) | core | opentofu-native | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="reference-ec2-vpc"></a>[reference-ec2-vpc](e2e/reference-ec2-vpc/) | core | reference | yes, script changed | P | P | P | P | P | P | P | P | P | P | - | P | P | P | - | `f707182267` 2026-10-02 |
| <a id="terralith-scale"></a>[terralith-scale](e2e/terralith-scale/) | core | reference | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `debf8ccd05` 2026-10-03 |
| <a id="corpus-azapi-role-assignment"></a>[corpus-azapi-role-assignment](e2e/corpus-azapi-role-assignment/) | growing | azure | yes | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `4b34e44e3e` 2026-10-21 |
| <a id="corpus-cloud-platform-components"></a>[corpus-cloud-platform-components](e2e/corpus-cloud-platform-components/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="corpus-govuk-cluster-access"></a>[corpus-govuk-cluster-access](e2e/corpus-govuk-cluster-access/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="corpus-govuk-cluster-services"></a>[corpus-govuk-cluster-services](e2e/corpus-govuk-cluster-services/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | - | `2f02038dd1` 2026-10-12 |
| <a id="corpus-k8s-metrics-server"></a>[corpus-k8s-metrics-server](e2e/corpus-k8s-metrics-server/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="corpus-mastino-dns"></a>[corpus-mastino-dns](e2e/corpus-mastino-dns/) | growing | published-deployment | yes, script changed | P | P | P | P | P | P | P | P | P | - | - | P | P | - | - | `f707182267` 2026-10-02 |
| <a id="corpus-quickpizza"></a>[corpus-quickpizza](e2e/corpus-quickpizza/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="reference-azure"></a>[reference-azure](e2e/reference-azure/) | growing | azure | yes | P | P | P | P | P | P | P | P | P | P | P | P | P | - | **F** | `4b34e44e3e` 2026-10-21 |
| <a id="reference-eks"></a>[reference-eks](e2e/reference-eks/) | growing | reference | no, script changed | P | **F** | - | - | - | - | - | - | - | - | - | - | - | - | - | `2f02038dd1` 2026-10-12 |
| <a id="reference-k8s"></a>[reference-k8s](e2e/reference-k8s/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="reference-k8s-cert-manager"></a>[reference-k8s-cert-manager](e2e/reference-k8s-cert-manager/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="reference-k8s-helm-argo-cd"></a>[reference-k8s-helm-argo-cd](e2e/reference-k8s-helm-argo-cd/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="reference-k8s-helm-cloudnative-pg"></a>[reference-k8s-helm-cloudnative-pg](e2e/reference-k8s-helm-cloudnative-pg/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="reference-k8s-helm-external-secrets"></a>[reference-k8s-helm-external-secrets](e2e/reference-k8s-helm-external-secrets/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `56fcd26c6c` 2026-10-13 |
| <a id="reference-k8s-helm-ingress-nginx"></a>[reference-k8s-helm-ingress-nginx](e2e/reference-k8s-helm-ingress-nginx/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="reference-k8s-helm-keda"></a>[reference-k8s-helm-keda](e2e/reference-k8s-helm-keda/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="reference-k8s-helm-kyverno"></a>[reference-k8s-helm-kyverno](e2e/reference-k8s-helm-kyverno/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `56fcd26c6c` 2026-10-13 |
| <a id="reference-k8s-helm-metallb"></a>[reference-k8s-helm-metallb](e2e/reference-k8s-helm-metallb/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="reference-k8s-helm-template"></a>[reference-k8s-helm-template](e2e/reference-k8s-helm-template/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="reference-k8s-platform-app"></a>[reference-k8s-platform-app](e2e/reference-k8s-platform-app/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="reference-k8s-shared-objects"></a>[reference-k8s-shared-objects](e2e/reference-k8s-shared-objects/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
| <a id="reference-k8s-workloads"></a>[reference-k8s-workloads](e2e/reference-k8s-workloads/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `56fcd26c6c` 2026-10-13 |
| <a id="reference-k8s-yaml-rollouts"></a>[reference-k8s-yaml-rollouts](e2e/reference-k8s-yaml-rollouts/) | growing | kubernetes | yes, script changed | P | P | P | P | P | P | P | P | P | P | P | P | P | P | P | `2f02038dd1` 2026-10-12 |
<!-- board-gen:end estates -->

## Run time

<!-- board-gen:begin runtime -->
46 of 46 estates have a recorded run duration, totaling 13h40m11.7s, but not from one sweep: 8h32m40.9s across 18 estate(s) at commit `2f02038dd1`; 13m29.6s across 2 estate(s) at commit `4b34e44e3e`; 1h34m40.7s across 3 estate(s) at commit `56fcd26c6c`; 10m16.9s across 1 estate(s) at commit `debf8ccd05`; 3h9m3.6s across 22 estate(s) at commit `f707182267`. This total spans different commits, not a single board run, and excludes 0 estate(s) with no recorded duration yet.

| Estate | Total | Per stage |
|---|---|---|
| corpus-alb-complete | 10m55.1s | cold_deploy 1m49s, migrate 1m21s, test_plan 9s, test_apply 11s, drift_reconverge 51s, day2_rename 38s, day2_remove 38s, day2_count 1m47s, day2_replace 49s, plan_approval 51s, greenfield 1m51s |
| corpus-autoscaling-complete | 10m18.4s | cold_deploy 1m52s, migrate 1m16s, test_plan 10s, test_apply 11s, drift_reconverge 21s, day2_rename 39s, day2_remove 33s, day2_count 1m58s, day2_replace 35s, plan_approval 51s, greenfield 1m50s |
| corpus-dynamodb-table-basic | 4m54.6s | cold_deploy 31s, migrate 1m9s, test_plan 5s, test_apply 6s, drift_reconverge 11s, day2_rename 21s, day2_remove 13s, day2_count 52s, day2_replace 21s, plan_approval 27s, greenfield 38s |
| corpus-ec2-instance-complete | 9m43.2s | cold_deploy 1m15s, migrate 39s, test_plan 12s, test_apply 11s, drift_reconverge 15s, day2_rename 29s, day2_remove 39s, day2_count 3m13s, day2_replace 1m2s, plan_approval 39s, greenfield 1m9s |
| corpus-ecs-fargate | 12m59s | cold_deploy 1m37s, migrate 1m10s, test_plan 58s, test_apply 14s, drift_reconverge 25s, day2_rename 1m16s, day2_remove 34s, day2_count 2m2s, day2_replace 36s, plan_approval 1m7s, greenfield 3m0s |
| corpus-eks-basic | 9m41.4s | cold_deploy 1m36s, migrate 39s, test_plan 9s, test_apply 13s, drift_reconverge 24s, day2_rename 40s, day2_remove 33s, day2_count 1m58s, day2_replace 33s, plan_approval 58s, greenfield 1m55s |
| corpus-evoteum-modules | 3m2.1s | cold_deploy 33s, migrate 24s, test_plan 5s, test_apply 3s, drift_reconverge 8s, day2_rename 14s, day2_remove 10s, day2_count 19s, day2_replace 16s, plan_approval 17s, greenfield 33s |
| corpus-giantswarm-crossplane | 3m38.6s | cold_deploy 35s, migrate 16s, test_plan 5s, test_apply 12s, drift_reconverge 7s, day2_rename 16s, day2_remove 9s, day2_count 1m9s, day2_replace 10s, plan_approval 19s, greenfield 21s |
| corpus-hongbomiao-labelbox | 6m17s | cold_deploy 2m26s, migrate 38s, test_plan 6s, test_apply 8s, drift_reconverge 8s, day2_rename 12s, day2_remove 9s, day2_count 26s, day2_replace 10s, plan_approval 17s, greenfield 1m36s |
| corpus-iam-policy | 3m59.9s | cold_deploy 25s, migrate 13s, test_plan 6s, test_apply 13s, drift_reconverge 10s, day2_rename 17s, day2_remove 13s, day2_count 1m0s, day2_replace 14s, plan_approval 24s, greenfield 44s |
| corpus-lambda-simple | 4m30.5s | cold_deploy 34s, migrate 20s, test_plan 4s, test_apply 9s, drift_reconverge 24s, day2_rename 35s, day2_remove 16s, day2_count 48s, day2_replace 23s, plan_approval 24s, greenfield 33s |
| corpus-leynos-monitoring | 2m35.4s | cold_deploy 34s, migrate 26s, test_plan 4s, test_apply 3s, drift_reconverge 7s, day2_rename 8s, day2_remove 8s, day2_count 23s, day2_replace 8s, plan_approval 15s, greenfield 18s |
| corpus-overture-tiles | 9m4s | cold_deploy 1m12s, migrate 59s, test_plan 18s, test_apply 17s, drift_reconverge 14s, day2_rename 1m24s, day2_remove 57s, day2_count 1m8s, day2_replace 12s, plan_approval 22s, greenfield 2m1s |
| corpus-rds-complete-postgres | 14m20.1s | cold_deploy 1m49s, migrate 30s, test_plan 21s, test_apply 10s, drift_reconverge 18s, day2_rename 34s, day2_remove 1m42s, day2_count 1m41s, day2_replace 3m5s, plan_approval 46s, greenfield 3m23s |
| corpus-s3-bucket-complete | 9m27.2s | cold_deploy 1m38s, migrate 1m6s, test_plan 6s, test_apply 13s, drift_reconverge 25s, day2_rename 29s, day2_remove 26s, day2_count 1m11s, day2_replace 26s, plan_approval 44s, greenfield 2m42s |
| corpus-security-group-complete | 4m53.8s | cold_deploy 33s, migrate 44s, test_plan 9s, test_apply 8s, drift_reconverge 13s, day2_rename 27s, day2_remove 20s, day2_count 53s, day2_replace 20s, plan_approval 36s, greenfield 30s |
| corpus-simpleinfra-dns | 12m44s | cold_deploy 1m32s, migrate 1m20s, test_plan 15s, test_apply 39s, drift_reconverge 32s, day2_rename 40s, day2_remove 36s, day2_count 1m15s, day2_replace 1m18s, plan_approval 1m6s, greenfield 3m30s |
| corpus-sqs-basic | 12m15s | cold_deploy 1m22s, migrate 2m22s, test_plan 4s, test_apply 5s, drift_reconverge 8s, day2_rename 14s, day2_remove 54s, day2_count 2m9s, day2_replace 1m19s, plan_approval 21s, greenfield 3m16s |
| corpus-sumaform-aws | 18m21.1s | cold_deploy 1m33s, migrate 4m0s, test_plan 27s, test_apply 26s, drift_reconverge 55s, day2_rename 1m37s, day2_remove 39s, day2_count 4m42s, day2_replace 1m53s, plan_approval 2m8s, greenfield 3m12s |
| corpus-vpc-complete | 6m39.7s | cold_deploy 40s, migrate 1m32s, test_plan 8s, test_apply 7s, drift_reconverge 19s, day2_rename 24s, day2_remove 29s, day2_count 1m58s, day2_replace 28s, plan_approval 33s, greenfield 58s |
| corpus-xancloud-iac | 5m6.8s | cold_deploy 45s, migrate 38s, test_plan 8s, test_apply 7s, drift_reconverge 17s, day2_rename 22s, day2_remove 26s, day2_count 45s, day2_replace 16s, plan_approval 31s, greenfield 51s |
| reference-ec2-vpc | 5m57s | cold_deploy 1m49s, migrate 41s, test_plan 4s, test_apply 5s, drift_reconverge 11s, day2_rename 19s, day2_remove 13s, day2_count 39s, day2_replace 35s, day2_crash 46s, plan_approval 25s, greenfield 10s, strict - |
| terralith-scale | 10m16.9s | cold_deploy 2m28s, migrate 56s, test_plan 12s, test_apply 18s, drift_reconverge 1m11s, day2_rename 31s, day2_remove 22s, day2_count 45s, day2_replace 35s, plan_approval 43s, greenfield 2m15s, strict - |
| corpus-azapi-role-assignment | 3m16.3s | cold_deploy 1m25s, migrate 7s, test_plan 1s, test_apply 2s, drift_reconverge 5s, day2_rename 6s, day2_remove 5s, day2_count 13s, day2_replace 14s, day2_crash 15s, day2_teardown 2s, plan_approval 6s, greenfield 28s, strict 3s, no_local_state 4s |
| corpus-cloud-platform-components | 17m17.8s | cold_deploy 2m15s, migrate 19s, test_plan 17s, test_apply 18s, drift_reconverge 31s, day2_rename 28s, day2_remove 41s, day2_count 1m42s, day2_replace 2m21s, day2_crash 4m6s, day2_teardown 29s, plan_approval 40s, greenfield 2m6s, strict 20s, no_local_state 43s |
| corpus-govuk-cluster-access | 14m2.8s | cold_deploy 1m52s, migrate 15s, test_plan 13s, test_apply 18s, drift_reconverge 45s, day2_rename 35s, day2_remove 35s, day2_count 1m9s, day2_replace 58s, day2_crash 4m11s, day2_teardown 26s, plan_approval 35s, greenfield 1m10s, strict 19s, no_local_state 35s |
| corpus-govuk-cluster-services | 13m21.9s | cold_deploy 2m11s, migrate 14s, test_plan 16s, test_apply 18s, drift_reconverge 40s, day2_rename 25s, day2_remove 37s, day2_count 1m14s, day2_replace 1m2s, day2_crash 4m24s, day2_teardown 25s, plan_approval 37s, greenfield 38s, strict 19s, no_local_state 1s |
| corpus-k8s-metrics-server | 13m43.4s | cold_deploy 2m33s, migrate 11s, test_plan 13s, test_apply 14s, drift_reconverge 45s, day2_rename 23s, day2_remove 34s, day2_count 1m8s, day2_replace 58s, day2_crash 3m56s, day2_teardown 15s, plan_approval 34s, greenfield 1m11s, strict 13s, no_local_state 34s |
| corpus-mastino-dns | 13m56.7s | cold_deploy 2m20s, migrate 54s, test_plan 9s, test_apply 21s, drift_reconverge 42s, day2_rename 41s, day2_remove 50s, day2_count 1m29s, day2_replace 1m3s, plan_approval 1m2s, greenfield 4m26s |
| corpus-quickpizza | 17m2.2s | cold_deploy 4m3s, migrate 8s, test_plan 12s, test_apply 12s, drift_reconverge 25s, day2_rename 25s, day2_remove 36s, day2_count 1m15s, day2_replace 1m20s, day2_crash 4m7s, day2_teardown 39s, plan_approval 35s, greenfield 1m52s, strict 28s, no_local_state 38s |
| reference-azure | 10m13.3s | cold_deploy 2m4s, migrate 17s, test_plan 1s, test_apply 3s, drift_reconverge 25s, day2_rename 5s, day2_remove 25s, day2_count 31s, day2_replace 1m52s, day2_crash 3m2s, day2_teardown 32s, plan_approval 5s, greenfield 48s, no_local_state 3s |
| reference-eks | 1m12.3s | cold_deploy 50s, migrate 22s |
| reference-k8s | 12m50.9s | cold_deploy 2m2s, migrate 9s, test_plan 13s, test_apply 17s, drift_reconverge 27s, day2_rename 23s, day2_remove 34s, day2_count 57s, day2_replace 57s, day2_crash 3m48s, day2_teardown 26s, plan_approval 35s, greenfield 1m10s, strict 18s, no_local_state 34s |
| reference-k8s-cert-manager | 48m17.4s | cold_deploy 2m54s, migrate 1m4s, test_plan 33s, test_apply 53s, drift_reconverge 1m41s, day2_rename 1m42s, day2_remove 3m41s, day2_count 5m31s, day2_replace 5m4s, day2_crash 12m30s, day2_teardown 1m29s, plan_approval 3m48s, greenfield 4m37s, strict 56s, no_local_state 1m54s |
| reference-k8s-helm-argo-cd | 1h1m1.3s | cold_deploy 3m53s, migrate 1m11s, test_plan 41s, test_apply 1m3s, drift_reconverge 2m22s, day2_rename 3m11s, day2_remove 3m5s, day2_count 8m44s, day2_replace 5m9s, day2_crash 16m6s, day2_teardown 3m10s, plan_approval 2m44s, greenfield 5m52s, strict 1m1s, no_local_state 2m47s |
| reference-k8s-helm-cloudnative-pg | 32m39.7s | cold_deploy 3m0s, migrate 32s, test_plan 25s, test_apply 35s, drift_reconverge 1m11s, day2_rename 1m40s, day2_remove 1m32s, day2_count 3m46s, day2_replace 3m28s, day2_crash 8m37s, day2_teardown 57s, plan_approval 1m29s, greenfield 3m28s, strict 36s, no_local_state 1m22s |
| reference-k8s-helm-external-secrets | 30m19.3s | cold_deploy 2m25s, migrate 30s, test_plan 22s, test_apply 34s, drift_reconverge 1m9s, day2_rename 2m11s, day2_remove 1m59s, day2_count 3m39s, day2_replace 3m27s, day2_crash 7m51s, day2_teardown 52s, plan_approval 1m31s, greenfield 2m10s, strict 29s, no_local_state 1m9s |
| reference-k8s-helm-ingress-nginx | 17m52.3s | cold_deploy 1m37s, migrate 10s, test_plan 13s, test_apply 17s, drift_reconverge 31s, day2_rename 48s, day2_remove 1m32s, day2_count 2m35s, day2_replace 1m19s, day2_crash 4m45s, day2_teardown 52s, plan_approval 44s, greenfield 1m10s, strict 30s, no_local_state 47s |
| reference-k8s-helm-keda | 34m9.7s | cold_deploy 3m53s, migrate 2m42s, test_plan 26s, test_apply 37s, drift_reconverge 1m14s, day2_rename 1m41s, day2_remove 1m36s, day2_count 3m40s, day2_replace 3m10s, day2_crash 7m58s, day2_teardown 54s, plan_approval 1m41s, greenfield 2m29s, strict 41s, no_local_state 1m27s |
| reference-k8s-helm-kyverno | 59m46.5s | cold_deploy 4m34s, migrate 1m1s, test_plan 52s, test_apply 1m29s, drift_reconverge 3m25s, day2_rename 3m5s, day2_remove 2m53s, day2_count 8m37s, day2_replace 5m23s, day2_crash 15m11s, day2_teardown 1m46s, plan_approval 2m44s, greenfield 5m27s, strict 58s, no_local_state 2m20s |
| reference-k8s-helm-metallb | 43m51.3s | cold_deploy 3m9s, migrate 56s, test_plan 31s, test_apply 48s, drift_reconverge 1m34s, day2_rename 2m11s, day2_remove 3m3s, day2_count 5m17s, day2_replace 4m44s, day2_crash 11m50s, day2_teardown 1m18s, plan_approval 2m9s, greenfield 3m41s, strict 49s, no_local_state 1m49s |
| reference-k8s-helm-template | 1h49m28.4s | cold_deploy 7m3s, migrate 2m32s, test_plan 1m14s, test_apply 2m3s, drift_reconverge 4m13s, day2_rename 5m30s, day2_remove 5m21s, day2_count 13m29s, day2_replace 13m19s, day2_crash 32m25s, day2_teardown 3m40s, plan_approval 5m5s, greenfield 7m1s, strict 2m3s, no_local_state 4m29s |
| reference-k8s-platform-app | 17m2.7s | cold_deploy 1m37s, migrate 48s, test_plan 35s, test_apply 32s, drift_reconverge 26s, day2_rename 27s, day2_remove 38s, day2_count 1m9s, day2_replace 1m8s, day2_crash 5m1s, day2_teardown 1m3s, plan_approval 37s, greenfield 1m26s, strict 47s, no_local_state 47s |
| reference-k8s-shared-objects | 17m33.3s | cold_deploy 1m32s, migrate 9s, test_plan 23s, test_apply 24s, drift_reconverge 58s, day2_rename 47s, day2_remove 47s, day2_count 1m10s, day2_replace 59s, day2_crash 3m55s, day2_teardown 1m26s, plan_approval 2m58s, greenfield 49s, strict 30s, no_local_state 45s |
| reference-k8s-workloads | 4m34.9s | cold_deploy 1m58s, migrate 7s, test_plan 2s, test_apply 4s, drift_reconverge 4s, day2_rename 3s, day2_remove 8s, day2_count 8s, day2_replace 11s, day2_crash 25s, day2_teardown 29s, plan_approval 3s, greenfield 24s, strict 15s, no_local_state 2s |
| reference-k8s-yaml-rollouts | 34m56.5s | cold_deploy 4m36s, migrate 37s, test_plan 44s, test_apply 43s, drift_reconverge 2m8s, day2_rename 3m23s, day2_remove 1m36s, day2_count 4m17s, day2_replace 2m34s, day2_crash 7m18s, day2_teardown 46s, plan_approval 2m35s, greenfield 1m55s, strict 31s, no_local_state 1m13s |
<!-- board-gen:end runtime -->

## Live-AWS certification

A real-AWS run is evidence about one run on one date and never counts toward
either bar.

<!-- board-gen:begin livecert -->
| Estate | Target | Region | Clear | Commit | Date | Ceiling |
|---|---|---|---|---|---|---|
| reference-ec2-vpc | aws | us-east-2 | yes | `4d72968cdb` | 2026-09-08 | $5.00 |
| terralith-scale | aws | us-east-2 | no | `8bbef274d6` | 2026-09-11 | $100.00 |
<!-- board-gen:end livecert -->
