# Platforms: the contract and adding one

A platform here is a provider family the live backend runs an estate on.
Two meet the contract today, AWS and Kubernetes, and neither is the
template for the other: each answers the same requirements in its own way.
This page lists those requirements with real paths for both, and marks
which parts are fixed and which are a per-platform choice needing a ruling.

It is written for the GCP epic ([#1119](https://github.com/INTENTIUS/choudoufu/issues/1119),
ruling [#1121](https://github.com/INTENTIUS/choudoufu/issues/1121)) and the
Azure epic ([#1120](https://github.com/INTENTIUS/choudoufu/issues/1120),
ruling [#1122](https://github.com/INTENTIUS/choudoufu/issues/1122)).

The contract is already written down, and this page cites it rather than
restating it:

| Where | What it holds |
|---|---|
| `internal/live/substrate` ([#1118](https://github.com/INTENTIUS/choudoufu/issues/1118)) | The `Substrate` interface: one value per family (`substrate.AWS` in `aws.go`, `substrate.Kubernetes` in `kubernetes.go`), registered in `substrate.All` |
| `live/SUBSTRATES.md` | The capability matrix, generated from `substrate.All` by `tools/substrates-gen`; fence and harness cells are hand-kept in `fenceByFamily` and `harnessByFamily` in `tools/substrates-gen/build.go` |
| `live/MARKERS.md` | Marker keys, the Kubernetes label section, the grants |
| `live/IDENTITY.md` | Where identity comes from, and why untaggable is not unidentifiable |
| `live/STORAGE.md` | The record stores, opening checks and conditional writes |
| `examples/record-store-bucket/CONTRACT.md`, `examples/record-store-cluster/CONTRACT.md` | What a bucket and a cluster must have before a store trusts them |

## Rules for agents on a platform epic

- Rulings are the maintainer's. The marker carrier, the identity source and
  the fence are each ruled in one comment before the code that depends on
  them, as on [#1016](https://github.com/INTENTIUS/choudoufu/issues/1016)
  (2026-09-11) and [#1121](https://github.com/INTENTIUS/choudoufu/issues/1121)
  (2026-10-11). An agent writes the research and a recommendation, labelled
  as a recommendation, and stops.
- Only the maintainer runs the gauntlet, a paid run, or a whole-set run
  (`CLAUDE.md`, "Only the maintainer runs the gauntlet"). `gauntlet run` and
  every estate `run.sh` ask for "run" at a terminal and refuse otherwise
  (`tools/gauntlet/manualrun.go`, `live/e2e/lib/gauntlet.sh`). Never fake a
  terminal.
- Claims and test benches need a person at a terminal. Since the 2026-10-11
  ruling GitHub CI is gofmt, the Go unit tier and the ci-pipelines npm test;
  no workflow runs a smoke scenario or a lane. Build the scenario, merge it,
  and tell the maintainer it is ready to run.
- The safety rule from `HANDOFF.md` holds on every platform: never write a
  wrong marker; drop to the record rung, never refuse the estate.

## The contract, requirement by requirement

Each subsection is one requirement. "Fixed" is what every platform must do
the same way. "Per platform" is what AWS and Kubernetes chose differently,
so a new platform needs a ruling on it.

### 1. A family value

| | AWS | Kubernetes |
|---|---|---|
| Value | `substrate.AWS`, `internal/live/substrate/aws.go` | `substrate.Kubernetes`, `internal/live/substrate/kubernetes.go` |
| Provider match | `ForProvider("aws")` | `ForProvider("kubernetes")` |

Fixed: every question goes through the family value, chosen by provider
name, never by schema shape (#1742: an azurerm `tags` map looks exactly like
an AWS one). AWS stays last in `All`, because its identity answer claims
every type (`TestTheCatchAllFamilyIsLast`). `thirdfamily_test.go` already
registers a fake third family and shows the dispatch seams it would fall
into. Today `hashicorp/google` falls through to the AWS identity-schema
route (`substrate/identity.go`), which a GCP family replaces.

### 2. Estate identity rule (admission of types)

| | AWS | Kubernetes |
|---|---|---|
| Source | Ratified rows in `internal/live/identity/table_generated.go`, proposed by `tools/row-gen` from CloudFormation registry evidence (`live/registry.json`, `live/mapping.json`); otherwise the provider's identity schema (`aws.SynthesizeIdentity` returns `FromIdentitySchema`) | A schema-shape rule: `identity.ObjectMetaShape` in `internal/live/identity/metadata.go` resolves `{namespace}/{name}` or `{name}`; `kubernetes_manifest` through `identity/manifest.go`. Four ratified rows carry `NonAWSProvider: true` and must agree with the rule |
| Landed | rows over many issues | [#1064](https://github.com/INTENTIUS/choudoufu/issues/1064), PR [#1068](https://github.com/INTENTIUS/choudoufu/pull/1068) |

Fixed: read from the schema or generated evidence, never a type-name list;
`SynthesizeIdentity` is the family's answer for a type with no row. Per
platform: the source. GCP's open question is Magic Modules rows versus a
`name`/`project`/location rule (#1119 item 1, #1121 measurement 5); Azure's
is azurerm's id parsers versus `resource_group_name` + `name`, plus azapi
(#1122 items 4 and 5). Neither is ruled yet.

### 3. Marker carrier, written in-create

| | AWS | Kubernetes |
|---|---|---|
| Surface | `markers.SurfaceTags`, found by `markers.TagSurface` / `markers.Taggable` (`markers/markers.go`) | `markers.SurfaceLabels` via `markers.LabelSurface` (`markers/labels.go`); `markers.SurfaceManifest` for `kubernetes_manifest` |
| Create write | `tags` stamped by `projection.NodeResolver.AdjustConfigValue` (`projection/nodestamp.go`) | the same hook, label arm in `projection/nodestamp_labels.go` |
| Adopt write | tags-only plan (`liveimport/tags.go`, `mv/rewrite.go`) | labels-only plan (`liveimport/labels.go`, `mv/label.go`); api-patch for manifests (`kubesweep.LabelPatcher`) |
| Landed | | [#1061](https://github.com/INTENTIUS/choudoufu/issues/1061), PR [#1062](https://github.com/INTENTIUS/choudoufu/pull/1062); live-import PR [#1078](https://github.com/INTENTIUS/choudoufu/pull/1078) |

Fixed: `tofu-estate` on every taggable managed object, written by the stamp
so the create carries it, and a `Writes` entry per surface
(`substrate.Write`: `in-create`, `tags-only-plan`, `labels-only-plan`,
`api-patch`). A new surface is a new `markers.Surface` constant in
`markers/surfaces.go`, which `TestEverySurfaceSeamHandlesEverySurface`
(`markers/seams_test.go`) then forces every seam to handle. Per platform:
the carrier and its value grammar (labels cap at 63 characters). GCP is
ruled: labels, in-create, estate-only, tag bindings later (#1121). Azure's
carrier is expected to be `tags` (#1120 item 2) and is not yet ruled.

### 4. Binding a live object back to its address

| | AWS | Kubernetes |
|---|---|---|
| Join key | the `tofu-address` tag, with continuation tags and `tofu-slot` (`live/MARKERS.md`) | the natural key (group, kind, namespace, name); since [#1605](https://github.com/INTENTIUS/choudoufu/issues/1605) also `markers.AddressAnnotation` (`choudoufu.intentius.io/tofu-address`), no continuations, no slot |
| Server-assigned ids | recovered by marker discovery (`internal/live/discovery`) | none: `TestNoKubernetesTypeNeedsDiscovery` pins it |

Fixed: `Substrate.CarriesAddress` answers whether the address is on the
object, and `TestEveryFamilyCarriesTheAddress` checks it. Per platform: where
the address lives, and whether slots and continuations exist. Kubernetes
shipped first with no address on the object (#1016) and added the annotation
later, so the contract permits both. GCP's ruling puts no `tofu-address` in
a label (#1121); where else the address goes is not ruled.

### 5. Not-found recogniser

`internal/live/absent` (`absent.Matches`) decides whether a provider's
import error means "no such object" or "could not answer". It is textual,
because only Summary and Detail cross the plugin wire. It holds AWS phrasings
(`ResourceNotFoundException` and the provider's own) and the
`hashicorp/kubernetes` one, added in PR
[#1068](https://github.com/INTENTIUS/choudoufu/pull/1068). Fixed: a new
provider's not-found text is added here with a test, or its imports read as
failures.

### 6. Sweep and orphans

| | AWS | Kubernetes |
|---|---|---|
| Kind | `substrate.SweepTaggingIndex`: the Resource Groups Tagging API and the other legs in `discovery/tagging.go`, `nativesweep.go`, `cloudcontrol.go`, through the configured provider | `substrate.SweepLabelList`: one label-selected list per served kind per namespace, `discovery/kubernetes.go` over the `internal/live/kubesweep` client from `NewSweeper` |
| Finds a type with no row | no | yes (`SweepFindsUnadmitted`) |
| Marker outlives the object | yes, so tombstones are read | no (`MarkerOutlivesObject`) |
| Controller-made objects | ACK and Crossplane tags (`markers.ControllerTagKeys`) | `ownerReferences`; Helm release annotation |
| Landed | | [#1065](https://github.com/INTENTIUS/choudoufu/issues/1065), PR [#1070](https://github.com/INTENTIUS/choudoufu/pull/1070) |

Fixed: an undeclared object carrying the estate's marker shows up in a plan;
a sweep failure is a refusal registered in `discovery/refusals.go`. Per
platform: the call shape and the consistency lag. GCP's is Cloud Asset
Inventory, and sweep equivalence waits on lex00/floci-gcp#7 (#1121). Azure's
is Resource Graph (#1120 item 3).

### 7. Record store

| | AWS | Kubernetes |
|---|---|---|
| Backend | `record_store "s3"`, `staterecord/s3.go` | `record_store "kubernetes"`, `staterecord/kubernetes.go`, opened by `projection/kubernetes_store.go` |
| Conditional write | `If-None-Match: *` and `If-Match` on the object | `resourceVersion` on a Secret; no lock, no Lease |
| Opening checks | three bucket settings (`staterecord/bucketcontract.go`, `examples/record-store-bucket/CONTRACT.md`) | four cluster assertions (`staterecord/kubernetescontract.go`, `examples/record-store-cluster/CONTRACT.md`) |
| Landed | [#1332](https://github.com/INTENTIUS/choudoufu/issues/1332) | [#1392](https://github.com/INTENTIUS/choudoufu/issues/1392), PR [#1435](https://github.com/INTENTIUS/choudoufu/pull/1435) |

Fixed: `staterecord.Store` (`Get`, `PutIfVersion`, `PutIfAbsent`) with a
conditional write, the sentinel check on opening (`live/STORAGE.md`,
"Opening a store"), a bulk read that is complete or fails, and a
`staterecord.ContractChecker`. The block label is validated in
`internal/configs/live.go`. Per platform: the backend and its checks.
Kubernetes had a lane (PR #1074, 2026-09-12) a week before its own store
(PR #1435, 2026-09-20), so a native store is not the first unit.

### 8. The fence

| | AWS | Kubernetes |
|---|---|---|
| Mechanism | IAM conditions on `aws:ResourceTag`, reads and writes, per principal (`live/MARKERS.md`, "Granting an estate") | one `ValidatingAdmissionPolicy`, writes only, per principal through the authorizer: `live/kubernetes/estate-boundary.yaml` and `estate-grant.yaml`, held by `live/kubernetes_gate_test.go` |
| Landed | | [#1066](https://github.com/INTENTIUS/choudoufu/issues/1066), PR [#1071](https://github.com/INTENTIUS/choudoufu/pull/1071) |

Fixed: the fence is never code here; it is a template a person installs,
and the claims table says per platform whether it is proven or restated.
Per platform: everything else, and the headline must say what the fence does
not cover (Kubernetes's is write-only). GCP is ruled project-scoped for now
(#1121). Azure RBAC cannot condition on tags, so resource groups versus a
restated fence is the open ruling (#1122).

### 9. Lint and refusals

Fixed: a construct the platform cannot honour is refused by name, with a
lint rule and a fixture. Kubernetes added one, `generate-name`
(`internal/live/lint/generate_name.go`), because a server-minted name leaves
no join key; AWS's rules (`count-index`, `markerless-type` and the rest) live
beside it in `internal/live/lint/issue.go`. The registries a rule joins are
listed below.

### 10. Emulator and harness

| | AWS | Kubernetes |
|---|---|---|
| Harness | floci, pinned by digest in `live/floci-image`; `stack_up` in `live/smoke/lib.sh`; coverage probed in `live/floci-capabilities.json`; traps in `live/FLOCI.md` | kind, a real API server, pinned in `live/kind-node-image`; `cluster_up` / `cluster_down` in `live/smoke/lib.sh` |
| Landed | | [#1057](https://github.com/INTENTIUS/choudoufu/issues/1057), PR [#1058](https://github.com/INTENTIUS/choudoufu/pull/1058) |

Fixed: a pinned image and a per-run lifecycle in `lib.sh`. GCP and Azure
run on floci forks (policy below).

### 11. Claims with a BREAK control

The Kubernetes bar on #1119: bind, mark on create, sweep and a governed
fence, each a claim with a BREAK control, then a lane.

| Claim | AWS scenario | Kubernetes scenario | BREAK |
|---|---|---|---|
| 7, identity is a tag | `carve-by-retag.sh` and others | `k8s-greenfield.sh` | strips the label; the replan must restore it |
| 1, no silent orphans | `no-silent-orphans.sh` | `k8s-no-silent-orphans.sh` | strips the orphan's label; the replan must leave it |
| 13, the tag is the boundary | `the-tag-is-the-boundary.sh` | `k8s-the-label-is-the-boundary.sh` | removes the policy; the denied write must go through |
| 29, the store refuses what it can't trust | `a-wrong-bucket-is-refused.sh` | `k8s-records-in-the-cluster.sh` | removes the fences |

Fixed (`live/smoke_claims_test.go`): the platform's name joins
`provider_order` in `live/smoke/claims.json`; every claim gets a cell for it
(proven, restated, unproven, open or n/a, with a note unless proven); a
claim's `substrate` list equals its cells that are not n/a; a scenario's
header reads `# CLAIM <n> (<provider>) - <title>`; and each claim page under
`live/smoke/claims/` gets an `## On <Provider>` section naming every proof.
A scenario may start as a demo in `smokeDemoScenarios` before it can assert
a marker, as `k8s-greenfield` did.

### 12. Gauntlet lane

| | AWS | Kubernetes |
|---|---|---|
| Lanes | `terraform-popular`, `opentofu-native`, `reference`, `published-deployment` | `kubernetes` (`gauntlet.LaneKubernetes`), its own bar |
| Substrate | `SubstrateFloci`, `SubstrateFlociEKS` | `SubstrateKind`, `gauntlet_kind_up` in `live/e2e/lib/gauntlet.sh` |
| Landed | | [#1067](https://github.com/INTENTIUS/choudoufu/issues/1067), PR [#1074](https://github.com/INTENTIUS/choudoufu/pull/1074) (`reference-k8s`), PR [#1089](https://github.com/INTENTIUS/choudoufu/pull/1089) (first published estate) |

Fixed: estates in `live/gauntlet/estates.json`, a lane in
`tools/gauntlet/manifest.go`'s `KnownLanes`, and a per-stage substrate
reading in `tools/gauntlet/stages.go`, so a stage whose oracle is
AWS-shaped is marked per substrate rather than silently skipped. A
hand-written reference estate comes first, then published roots.

### 13. Docs pages

| | AWS | Kubernetes |
|---|---|---|
| `live/` | the root `live/*.md` docs, substrate-neutral | `live/kubernetes/` ADOPT, COMPATIBILITY, FAULTS, GATE, OPERATE, PROOF |
| Site hub | `site/src/content/docs/aws/` | `site/src/content/docs/kubernetes/` |

Fixed: under the #1602 ruling root docs stay substrate-neutral with a
section per substrate where they differ, and `live/<platform>/` keeps only
what is platform-only; `live/compatibility_outline_test.go` holds
`live/kubernetes/COMPATIBILITY.md` to the root outline, and a new platform's
copy should join it. A site hub is an `index.md` plus the five slot pages
(adopt, gate, operate, compatibility, proof), each with a `description:`,
and a sidebar group in `site/astro.config.mjs`
(`TestSiteProviderHubsCarryTheFiveSlots`). The site is reader path only and
`TestSiteStaysShort` (`live/site_size_test.go`) caps it at 10,000 words,
`site/src/data/rooms.ts` included, so a hub is a few hundred words and the
evidence stays in `live/`.

## Gaps between AWS and Kubernetes

Things one platform meets and the other does not, as the tree says today:

- The Kubernetes fence covers writes only; AWS's covers reads too.
- Kubernetes has no per-service capability probe like
  `live/floci-capabilities.json` (`live/SUBSTRATES.md`).
- AWS's sweep cannot find a type with no row; Kubernetes's can.
- Kubernetes has no slots or continuation markers, by ruling.
- Claim 47 (`plan-cost-tracks-the-change`) reads `open` on Kubernetes.

## Build order

The order Kubernetes landed in works as a build order. It is not the
definition of the contract.

| Step | Ruled or existing first | Kubernetes did | Closed by |
|---|---|---|---|
| 1. Ruling | the research issue | #1016, ruled 2026-09-11 | the maintainer's comment |
| 2. Harness | ruling | PR #1058: `cluster_up`, `estate-k8s`, `k8s-greenfield` as a demo | a scenario with a verdict per step |
| 3. Carrier | harness | PR #1062: `LabelSurface`, label stamp | claim 7, BREAK strips the label |
| 4. Admission | carrier | PR #1068: `ObjectMetaShape`, `generate-name`, `absent` | identity golden values, the lint fixture |
| 5. Sweep | admission | PR #1070: `kubesweep`, discovery's Kubernetes leg | claim 1, BREAK strips the label |
| 6. Fence | sweep, fence ruling | PR #1071: `estate-boundary.yaml` | claim 13, BREAK removes the policy |
| 7. Lane | the claims above | PR #1074, PR #1089 | lane reads clear |
| 8. Migration | carrier | PR #1078: label carrier in `live-import` | lane migrate stage |
| 9. Record store | optional | PR #1435 | claim 29, BREAK removes the fences |

Kubernetes got its family value after the fact (#1118) and its address
annotation later (#1605); a new platform should start with the family value
and rule where the address lives together with the carrier.

Known differences for the next two, from the issues only:

- GCP: labels ruled, no labels on the network skeleton, fence
  project-scoped for now, `google` and `google-beta` share types (#1119
  items 3 and 6, #1121).
- Azure: tags nearly universal, no tag condition in RBAC, azapi beside
  azurerm, case-insensitive tag keys; carrier, fence and identity rule not
  yet ruled (#1120, #1122).

## Registries that bite in CI

Each was found by a red CI run.

| Adding | Must also touch |
|---|---|
| A lint rule | the `Rule` constant in `internal/live/lint/issue.go`; its `ruleInfo` entry; a fixture at `live/e2e/limits/<rule>/`; `enforcedLimits` in `internal/live/lint/limits_test.go`; a `### <rule>` section in `live/LIMITATIONS.md` (`TestLimitationsDocCoversDirs`), with the table regenerated by `just limits`; and `lintRuleTargetScope` in `internal/live/lint/targetscope_test.go` (`TestEveryRuleHasATargetScopeVerdict`) |
| A package `internal/live/<pkg>` | `guardedPackages` or `deferredPackages` in `internal/live/marksafe/marksafe_test.go`; a layer, or `notAStage` in `internal/live/check/check_test.go` (`TestLayersClassifyEveryLivePackage`) |
| A fixture directory | the golden (`env -u PWD go test ./internal/live/check/ -run TestIdentityGolden -update`, explaining every moved line); `identityGoldenPinDirs` and its note in `live/identity_golden_pin_test.go`; the figure in `HANDOFF.md`'s "What is enforced" table (`TestHandoffFiguresMatchTheirPins`) |
| A refusal | the stage's registry (for example `discovery/refusals.go`, `projection/refusals.go`); `internal/live/refusalscan` fails on a summary with no entry |
| A marker surface | a `markers.Surface` constant; every seam in `TestEverySurfaceSeamHandlesEverySurface`, or an exemption with a reason |
| A family | `substrate.All` before AWS; `TestEverySubstrateAnswersEveryQuestion`; `fenceByFamily` and `harnessByFamily` in `tools/substrates-gen`; regenerate `live/substrates.json` and `live/SUBSTRATES.md` |
| A claim cell | see requirement 11 |
| A site hub | see requirement 13 |
| A tool under `tools/` | a `.gitignore` entry (`TestEveryToolHasAGitignoreEntry`) |
| An artifact measured on an emulator | its image field in `live/flociimage_test.go` |

## Emulator policy

- One floci fork per platform: lex00/floci (AWS), lex00/floci-gcp and
  lex00/floci-az. Never upstream floci-io (#1119, #1120).
- Before fixing an emulator gap, check whether upstream floci-io already
  fixed it; the forks lag.
- Fixes stay on the fork, are published to GHCR, and are pinned by digest,
  as `live/floci-image` pins AWS's.
- Gaps are filed on the fork as the lane finds them (lex00/floci-gcp#2,
  lex00/floci-az#2 list the surfaces). The maintainer reviews any text
  before it is posted to a floci repository.
