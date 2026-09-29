# reference-k8s-cert-manager

The kubernetes lane's CRD estate (#1174), and the first estate to use the
declared cold-deploy pre-apply #1173 ruled on.

## What the configuration is

| | |
|---|---|
| source | `https://github.com/cert-manager/cert-manager/releases/download/v1.21.2/cert-manager.yaml` |
| tag | v1.21.2, published 2026-09-11 |
| artifact | 1,034,400 bytes, sha256 `e03b668ec8675214af6b0a671699d088f2601fa3878e0dbe1b41d3feafd1879f` |
| licence | Apache-2.0; the bundle's own header is restated at the top of `root/cert-manager.tf`, which `tfk8s` does not carry across |
| converter | `tfk8s` v0.1.10, then `order.py` |
| objects | 47 from the bundle + 3 written here = 50, over 13 kinds |
| CRDs | `Certificate`, `CertificateRequest`, `Issuer`, `ClusterIssuer`, `Challenge`, `Order`; `ClusterIssuer` is the only cluster-scoped custom kind |
| images | `quay.io/jetstack/cert-manager-{controller,webhook,cainjector}:v1.21.2`, public, ~51 MB |

`convert.sh` refetches, re-verifies the size and sha256, re-converts and
re-orders. `root/` is committed so a run is offline and reproducible.

`reference-` rather than `corpus-`: cert-manager ships YAML. The Terraform
root is ours, and `live/corpus-manifest.json`'s own header says a corpus row
only means something when nobody here wrote the configuration.

### The two things the converted root needed by hand

1. **The ordering `tfk8s` drops.** It emits no `depends_on` anywhere, so the
   bundle races its own Namespace - 46 of 47 objects apply and the 47th
   fails `NotFound namespaces [cert-manager]`. `order.py` gives the 11
   objects whose own `metadata.namespace` is `cert-manager` a `depends_on`
   on the Namespace block, and nothing else. This is a property of the
   converted root and is deliberately not solved with the pre-apply.
2. **A provider block**, which the bundle has none of. `run.sh` writes it,
   with the `live` block on choudoufu's side only.

## The declared pre-apply

`live/gauntlet/estates.json` carries the 47 bundle addresses as `pre_apply`
with the reason. `run.sh` runs the un-targeted plan first as a control and
requires it to fail, then calls `gauntlet_pre_apply` with both sides in one
call, then waits - bounded, loudly - for the `failurePolicy: Fail`
validating webhook to actually admit an Issuer, and only then applies the
rest. The `cold_deploy` verdict says how many addresses were pre-applied
and where they are declared; the run's own `GAUNTLET pre_apply=` line
carries the list, and the runner checks the two against each other address
by address. See #1173 and `live/GAUNTLET.md`, "The cold-deploy pre-apply".

## Ratification: the run this estate landed on

`bash live/e2e/reference-k8s-cert-manager/run.sh`, 2026-09-16, at
`e600369c41` plus the two script fixes above it, on kind `v1.36.1`
(`kindest/node:v1.36.1`), `hashicorp/kubernetes` 3.2.1, stock Terraform
v1.15.8, darwin/arm64. Two clusters, both created and deleted by the run.
12 stages reported, 963 seconds.

| stage | verdict | s |
|---|---|---|
| cold_deploy | pass | 119 |
| migrate | pass | 48 |
| test_plan | pass | 31 |
| test_apply | pass | 32 |
| drift_reconverge | pass | 80 |
| plan_approval | pass | 154 |
| day2_rename | pass | 80 |
| day2_remove | pass | 182 |
| day2_count | **fail** | 128 |
| day2_teardown | pass | 125 |
| greenfield | **fail** | 12 |
| strict | pass | 12 |

`day2_replace` does not apply on this substrate and is recorded `n/a` by
the runner, not by the script.

`day2_crash` used to be `n/a` here for the same reason, and is not any
more (#1110): the stage reads on kind, as an apply of several objects
killed between one object's create and the next, with the next plan
required to propose exactly the remainder. This script did not run it
until #1237, so the cell read `not_run` while the row read clear - the
stage's tier-1 gating (#999) keeps an unmeasured cell neutral, which is
exactly why it was worth filing rather than leaving. Section 10 runs it
now; see "Thirteen of thirteen" below.

It lands red on two stages, with an issue naming each, under the phase's
rule that a lane which is a gap list beats a lane which is clear on estates
that dodge the hard cases.

## Re-measured with #1178 fixed: 12 of 12

`bash live/e2e/reference-k8s-cert-manager/run.sh`, 2026-09-17, with
`TOFU_BIN` a binary built from main `891ffc346d` plus #1178's fix and its
unit tests - and nothing else. What this branch adds on top of what that
binary ran is the seed's for_each memoization, which returns the same values
its unit test pins, and this text. Same substrate as the landing run above:
kind `v1.36.1` (`kindest/node:v1.36.1`), `hashicorp/kubernetes` 3.2.1, stock
Terraform v1.15.8, darwin/arm64, two clusters created and deleted by the
run. Exit 0.

| stage | verdict | s |
|---|---|---|
| cold_deploy | pass | 117 |
| migrate | pass | 49 |
| test_plan | pass | 31 |
| test_apply | pass | 50 |
| drift_reconverge | pass | 99 |
| plan_approval | pass | 171 |
| day2_rename | pass | 99 |
| day2_remove | pass | 217 |
| day2_count | pass | 325 |
| day2_teardown | pass | 144 |
| greenfield | pass | 229 |
| strict | pass | 56 |

`day2_count` is #1178's own proof, and its verdict line is quoted in that
issue. `greenfield` is green off the back of #1176, which was already on
main at this branch's base; this run measured it but is not its evidence.
`plan_approval` passed at ratification too - it records #1177's answer
rather than requiring stock parity - and it still recorded that answer
here, because #1177's fix landed on main after the binary above was built.


## Thirteen of thirteen, with day2_crash wired (#1237)

`go run ./tools/gauntlet run reference-k8s-cert-manager`, 2026-09-17, with
`TOFU_BIN` a binary built from this branch. Same substrate as above: kind
`v1.36.1`, `hashicorp/kubernetes` 3.2.1, stock Terraform v1.15.8,
darwin/arm64, two clusters created and deleted by the run. Exit 0.

| stage | verdict | s |
|---|---|---|
| cold_deploy | pass | 129 |
| migrate | pass | 48 |
| test_plan | pass | 31 |
| test_apply | pass | 50 |
| drift_reconverge | pass | 98 |
| plan_approval | pass | 221 |
| day2_rename | pass | 98 |
| day2_remove | pass | 217 |
| day2_count | pass | 325 |
| **day2_crash** | **pass** | **291** |
| day2_teardown | pass | 88 |
| greenfield | pass | 261 |
| strict | pass | 56 |

`day2_crash` interrupts an apply creating two `cert-manager.io/v1` Issuers
with the engine's own SIGTERM the instant the first create commits, under
`-parallelism=1`. Every write in that graph, the plan-time server-side dry
run included, goes through the two `failurePolicy: Fail` webhooks this
estate installs. The recovery plan is exactly the remainder and proposes
nothing at all for the object the crash created, which it binds by the
label plus namespace and name - the same binding path #1178 broke for a
counted manifest, reached from a half-finished apply rather than a replan.

The record store contributes nothing here and the stage asserts that by
value rather than printing a count: no record file exists for
`kubernetes_manifest.crash_first` and the file count does not move
(1 -> 1), because a `kubernetes_manifest` declaring no `field_manager`
records nothing (#1188). `greenfield` takes the matching reading - delete
the whole record store and the cache, and the plan is still `No changes.`

## What day2_crash found

- **#1262** - a `kubernetes_manifest` whose `manifest` argument contains a
  reference to another resource never binds to its own object: the next
  plan proposes CREATING it again and the server-side dry run is refused
  with `already exists`. Isolated on one cluster, one root, one variable:
  a plain Issuer replans clean, the same Issuer with a literal annotation
  replans clean, the same Issuer whose annotation reads another manifest's
  name does not. The plan says the object came back "without a
  `manifest.metadata.labels` map" while kubectl shows it carrying
  `tofu-estate`, so the configured seed for the read drops the manifest
  and the marker inside it - #1178's mechanism one case further out, with
  no crash needed to produce it. The crash pair took its graph edge from
  `depends_on` while that stood, and the 291-second pass above was
  measured in that shape. PR #1461 fixed the projection against a fake
  provider and asked for this pair as the cluster proof, so the pair is
  back on the data edge: `crash_second`'s manifest reads
  `kubernetes_manifest.crash_first.manifest.metadata.name` and there is
  no `depends_on` between the two. The empty replan after the recovery is
  the assertion the fix has not yet had on a cluster.

## What it found

- **#1176** - `-target` does not narrow #1097's missing-CRD refusal, so the
  pre-apply stock performs is refused for choudoufu. `cold_deploy` passes
  only because both of its sides are stock; `greenfield`, which is
  choudoufu's own, cannot start.
- **#1177** - a change to `metadata.labels` or `metadata.annotations` on a
  `kubernetes_manifest` is invisible to the plan and the apply writes
  nothing, silently. Stock plans one in-place update for the same edit.
  `plan_approval` measures this on the way in and records both answers.
- **#1178** - a counted `kubernetes_manifest` never binds to the objects it
  created: the next plan proposes creating them again and the sweep
  simultaneously calls them orphans. Fixed 2026-09-17, and the cause turned
  out not to be `count` as such: the projection built its configured seed
  with the bare module-level evaluator, which refuses `count.index`, so an
  expanded manifest's whole `manifest` argument was dropped from the prior
  state and the estate marker inside it went with it. `for_each` was
  affected identically. The counted resource still lives in `run.sh`
  rather than `root/` because scaling it 2 -> 1 -> 2 is the stage's
  measurement and the rest of the estate counts a root of 50.

## What it proved

- 50 of 50 instances bound by `live-import`, zero skipped, including all
  three custom kinds;
- an empty plan with no state file, and an empty plan after a no-op apply;
- exactly one create proposed after one custom resource was deleted out of
  band, matching stock's own plan on the oracle cluster;
- zero churn for a `moved` block over a cluster-scoped custom kind;
- one destroy per removed block, at the sweep's synthetic orphan address,
  for a namespaced custom kind;
- **the controller-copy exclusion**: removing the Certificate destroys
  exactly the Certificate, and the Secret its controller created -
  `example-com-tls`, labelled `controller.cert-manager.io/fao` and carrying
  no estate label - is still on the cluster afterwards and was never
  proposed;
- teardown in one apply, all six CRDs gone, no object left carrying the
  estate label.

## Notes for whoever runs it next

- Every object is a server-side-applied `kubernetes_manifest`, so an
  out-of-band `kubectl patch` makes kubectl a field manager and the
  reconverging apply then fails with a field-manager conflict - on stock
  too. `drift_reconverge` deletes instead, which involves no field manager.
- `run.sh`'s `BREAK_PREAPPLY=1` is the control for the pre-apply itself: it
  applies the whole root in one pass and requires that to succeed, which it
  must not.
