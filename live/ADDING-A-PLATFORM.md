# Platforms: the contract and adding one

A platform here is a provider family the live backend runs an estate on.
Two meet the contract today, AWS and Kubernetes, and neither is the
template for the other: each answers the same requirements in its own way.
Azure is the third, built on the `azure` branch and not yet measured: every
Azure claim cell is `open` and its lane has not run. This page lists the
requirements with real paths for all three, and marks which parts are fixed
and which are a per-platform choice needing a ruling.

It is written for the GCP epic ([#1119](https://github.com/INTENTIUS/choudoufu/issues/1119),
ruling [#1121](https://github.com/INTENTIUS/choudoufu/issues/1121)) and the
Azure epic ([#1120](https://github.com/INTENTIUS/choudoufu/issues/1120),
ruling [#1122](https://github.com/INTENTIUS/choudoufu/issues/1122)).

The contract is already written down, and this page cites it rather than
restating it:

| Where | What it holds |
|---|---|
| `internal/live/substrate` ([#1118](https://github.com/INTENTIUS/choudoufu/issues/1118)) | The `Substrate` interface: one value per family (`substrate.AWS` in `aws.go`, `substrate.Kubernetes` in `kubernetes.go`, `substrate.Azure` in `azure.go`), registered in `substrate.All` |
| `live/SUBSTRATES.md` | The capability matrix, generated from `substrate.All` by `tools/substrates-gen`; fence and harness cells are hand-kept in `fenceByFamily` and `harnessByFamily` in `tools/substrates-gen/build.go` |
| `live/MARKERS.md` | Marker keys, the Kubernetes label section, the grants |
| `live/IDENTITY.md` | Where identity comes from, and why untaggable is not unidentifiable |
| `live/STORAGE.md` | The record stores, opening checks and conditional writes |
| `examples/record-store-bucket/CONTRACT.md`, `examples/record-store-cluster/CONTRACT.md`, `examples/record-store-azurerm/CONTRACT.md` | What a bucket, a cluster and a storage account must have before a store trusts them |

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
- Agents file issues on their own platform's floci fork and do nothing
  else there: no branches, commits, PRs, image publishes or re-pins. That
  work is the maintainer's. See "Emulator policy" below.
- The safety rule from `HANDOFF.md` holds on every platform: never write a
  wrong marker; drop to the record rung, never refuse the estate.

## The contract, requirement by requirement

Each subsection is one requirement, and the first one comes before all
the others. "Fixed" is what every platform must do the same way. "Per
platform" is what AWS and Kubernetes chose differently, so a new platform
needs a ruling on it. The Azure column is what the `azure` branch built
under #1122's ruling; where an answer is a fact about ARM, it comes from
Azure's docs and the provider source and awaits measurement on floci-az.

### 1. Stock provider compatibility

Every platform is fully compatible with the stock provider for that
platform, as published at the pin: `hashicorp/aws`, `hashicorp/kubernetes`,
`hashicorp/google` and `google-beta`, `hashicorp/azurerm`, `azure/azapi`.

- No provider fork, patch or wrapper.
- The marker goes in only through arguments the stock provider already
  accepts, on the create call it already makes.
- Apart from that argument, the plan is what stock would show. No resources
  are synthesized.
- The exit holds: remove the `live` block, keep the cache, and stock
  OpenTofu with the same provider carries on.
- Every configuration stock accepts either works or is refused by name with
  a lint rule.

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| Marker argument | the provider's own `tags` map | the provider's own `metadata.labels` (and `metadata.annotations` for the address) | the provider's own `tags` (azurerm's argument; azapi's top-level `tags`, or `body.tags` where the configuration keeps its tags) |
| Exit | claim 6, `stock-when-you-need-it.sh` | claim 6, `k8s-stock-when-you-need-it.sh`; BREAK runs the leg with the `live` block still on | claim 6 has no Azure scenario; the intended proof is `reference-azure`'s `cold_deploy` and `migrate` stages, not run |
| Refused by name | the AWS lint rules in `internal/live/lint/issue.go` | the same, plus `generate-name` | the same, plus `policy-overwrite-at-key` and `arm-tag-limit` (a warning) |

Fixed: all of it. A carrier choice that needs a second resource in the plan
(the post-create tag-binding option #1121 weighed for GCP) has to be
squared with the no-synthesized-resources rule before it is ruled.

### 2. A family value

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| Value | `substrate.AWS`, `internal/live/substrate/aws.go` | `substrate.Kubernetes`, `internal/live/substrate/kubernetes.go` | `substrate.Azure`, `internal/live/substrate/azure.go` |
| Provider match | `ForProvider("aws")` | `ForProvider("kubernetes")` | `ForProvider("azurerm")` and `ForProvider("azapi")` |

Fixed: every question goes through the family value, chosen by provider
name, never by schema shape (#1742: an azurerm `tags` map looks exactly like
an AWS one). AWS stays last in `All`, because its identity answer claims
every type (`TestTheCatchAllFamilyIsLast`); `All` is
`{Kubernetes, Azure, AWS}`. `thirdfamily_test.go` already
registers a fake third family and shows the dispatch seams it would fall
into. Today `hashicorp/google` falls through to the AWS identity-schema
route (`substrate/identity.go`), which a GCP family replaces, as the Azure
family replaced it for azurerm and azapi.

The family answers every question `substrate.Substrate` asks. Most of the
answers are rendered in the generated matrix,
[`live/SUBSTRATES.md`](SUBSTRATES.md). This table has every question, with
the reason each family gives in `aws.go`, `kubernetes.go` and `azure.go`:

| Question | AWS | Kubernetes | Azure |
|---|---|---|---|
| `Surfaces`, `SurfaceOf`, `MarkersOf`, `CarrierPaths`, `MarkerNoun`, `CarrierPhrase` | `tags` (`tags`, `tags_all`), "tag" | `labels` (`metadata[0].labels`) and `manifest` (`manifest.metadata.labels`), "label" | `arm-tags` (`tags`), keys read case-insensitively, "tag" |
| `Writes` | in-create / tags-only-plan / tagging-api | in-create / labels-only-plan or api-patch / never-needed | in-create / tags-only-plan / arm-tags-api |
| `CarriesAddress` | true: the `tofu-address` tag | true: the address annotation (#1605's ruling, built as PRs #1664, #1678, #1684) | true: the `tofu-address` tag, as on AWS |
| `AddressInMarkers`, `AddressCarrier` | true: the address is a key of the tag map | false: the annotation sits outside the label map | true: the address is a key of the tag map |
| `Sweep`, `NewSweeper` | tagging-index, through the configured provider, no client | label-list, a cluster client built from the provider block | resource-graph: one Azure Resource Graph query per subscription, a client built from the azurerm or azapi provider block (`internal/live/azsweep`) |
| `SweepFindsUnadmitted` | false: the tagging-index universe is the admission table read another way | true: it lists every kind the cluster serves | true, from the design: Resource Graph lists every tagged ARM resource whatever its type; awaits measurement |
| `MarkerOutlivesObject` | true: the tagging index keeps listing a terminated object's tags for a while, so a record tombstone is read | false: the label is a field of the object, so a tombstone has no reader (#1883) | true: Azure documents a lag before a delete reaches Resource Graph, so tombstones are read; from the docs, awaits measurement |
| `CreateCollidesOnKey` | false: out of scope of #1546, since an AWS create of an existing object often succeeds or renames, per type and unmeasured | true for `labels`: the API server answers 409 while an unlabelled object holds the name (#1546); false for `manifest`, where the server-side dry run already answers | false: an ARM create is a PUT that updates an existing object. `CreateOverwritesAtKey` is true instead, and an unmarked object at a declared id is refused by name before any create |
| `MarkerWriter`, `PostCreateNeeded` | tagging-api; needed when the type's CloudFormation counterpart has `tag_on_create` false (`live/registry.json`) | never needed: both surfaces carry the label in the create call | ARM's Tags API (`PATCH .../providers/Microsoft.Resources/tags/default`, Merge); needed for the two azurerm types whose create carries no tags (`internal/live/armtypes`, `azurerm_tags_generated.json`) |
| `ManualMarkFix`, `CreatedObject` | an `aws resourcegroupstaggingapi tag-resources` line naming the ARN | the generic fix; the object's id | an `az tag update --operation Merge` line naming the ARM id; the ARM id |
| `ControllerHeld`, `HoldRecognition` (#1706) | the object's own tags carry a key in `markers.ControllerTagKeys` (ACK, Crossplane) | the Helm release annotation (`kubesweep.HelmReleaseNameAnnotation`) | ARM `managedBy` on the object (`HoldEvidence.ManagedBy`), or an `aks-managed-` tag |
| `SynthesizeIdentity` | the identity-schema route, which claims every type | object metadata, manifest, or a field-granular patch target (#1191) | azurerm: the ARM id template from `live/azurerm-ids.json`, or the identity-schema route, which admits 103 of the 196 types that publish one and hands 72 to the template; `azapi_resource`: `parent_id` + `type` + `name` |
| `NotACarrier` | `markers.NotAMarkerSurface` | a Kubernetes sentence of its own | an Azure sentence of its own |
| `ProviderTypes` | `["aws"]` (PR #2118) | `["kubernetes"]` (PR #2118) | `["azurerm", "azapi"]` |

A new platform fills in the same column. These are the rows the GCP and
Azure drafts got wrong first: `CarriesAddress`, `MarkerOutlivesObject`,
`CreateCollidesOnKey`, `ControllerHeld`, `PostCreateNeeded` and
`SweepFindsUnadmitted`. Each one is a fact about the cloud's API, so
do not copy it from AWS. In a build round, with no emulator runs, take the
answer from the platform's docs and the provider source, say so where the
answer is recorded, and the maintainer measures it on the emulator
afterwards.

### 3. Estate identity rule (admission of types)

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| Source | Ratified rows in `internal/live/identity/table_generated.go`, proposed by `tools/row-gen` from CloudFormation registry evidence (`live/registry.json`, `live/mapping.json`); otherwise the provider's identity schema (`aws.SynthesizeIdentity` returns `FromIdentitySchema`) | A schema-shape rule: `identity.ObjectMetaShape` in `internal/live/identity/metadata.go` resolves `{namespace}/{name}` or `{name}`; `kubernetes_manifest` through `identity/manifest.go`. Four ratified rows carry `NonAWSProvider: true` and must agree with the rule | `live/azurerm-ids.json`, generated by `tools/azurerm-id-gen` from azurerm 5.10.0's id parsers, embedded in `internal/live/substrate/azurerm_ids.go`; the identity-schema route for 103 of the 196 types that publish one, each type's route recorded in the artifact; `azapi_resource` by its own arguments (`internal/live/identity/armid.go`) |
| Landed | rows over many issues | [#1064](https://github.com/INTENTIUS/choudoufu/issues/1064), PR [#1068](https://github.com/INTENTIUS/choudoufu/pull/1068) | #1120 unit 10 (merged to `azure` at `068aa5704e`) and unit 13 (`d8dee32983`) |

Fixed: read from the schema or generated evidence, never a type-name list;
`SynthesizeIdentity` is the family's answer for a type with no row. Per
platform: the source. GCP's open question is Magic Modules rows versus a
`name`/`project`/location rule (#1119 item 1, #1121 measurement 5), not yet
ruled. Azure's is ruled (#1122) and built: azurerm binds by an ARM id template generated
from its own id parsers (`live/azurerm-ids.json`), filled from `name`,
`resource_group_name` and the parents, with the subscription from the
provider block; azapi binds by `type` + `parent_id` + `name`, the manifest
mechanism. Of azurerm 5.10.0's 1,106 types, 843 are admitted; 254 have no
template that could be read and 9 have one a guard withholds, and all 263
are refused by name, never bound by a guess from the schema.

### 4. Marker carrier, written in-create

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| Surface | `markers.SurfaceTags`, found by `markers.TagSurface` / `markers.Taggable` (`markers/markers.go`) | `markers.SurfaceLabels` via `markers.LabelSurface` (`markers/labels.go`); `markers.SurfaceManifest` for `kubernetes_manifest` | `markers.SurfaceARMTags` via `markers.ARMTaggable` (`markers/armtags.go`) |
| Create write | `tags` stamped by `projection.NodeResolver.AdjustConfigValue` (`projection/nodestamp.go`) | the same hook, label arm in `projection/nodestamp_labels.go` | the same hook, ARM arm in `projection/nodestamp_armtags.go` (`armTagTarget`) |
| Adopt write | tags-only plan (`liveimport/tags.go`, `mv/rewrite.go`) | labels-only plan (`liveimport/labels.go`, `mv/label.go`); api-patch for manifests (`kubesweep.LabelPatcher`) | tags-only plan, with the marker keys folded (`liveimport/stamp.go`, `mv/rewrite.go`) |
| Landed | | [#1061](https://github.com/INTENTIUS/choudoufu/issues/1061), PR [#1062](https://github.com/INTENTIUS/choudoufu/pull/1062); live-import PR [#1078](https://github.com/INTENTIUS/choudoufu/pull/1078) | #1120 unit 1 (merged at `979c67ba3e`), live-import and `live-mv` included |

Fixed: `tofu-estate` on every taggable managed object, written by the stamp
so the create carries it, and a `Writes` entry per surface
(`substrate.Write`: `in-create`, `tags-only-plan`, `labels-only-plan`,
`api-patch`). A new surface is a new `markers.Surface` constant in
`markers/surfaces.go`, which `TestEverySurfaceSeamHandlesEverySurface`
(`markers/seams_test.go`) then forces every seam to handle. Per platform:
the carrier and its value grammar (labels cap at 63 characters). GCP is
ruled: labels, in-create, estate-only, tag bindings later (#1121). Azure's
carrier is ruled and built: `tags`, in-create, on azurerm and azapi alike
(#1122), on a new surface, `markers.SurfaceARMTags`.

### 5. Binding a live object back to its address

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| Join key | the `tofu-address` tag, with continuation tags and `tofu-slot` (`live/MARKERS.md`) | the natural key (group, kind, namespace, name); since [#1605](https://github.com/INTENTIUS/choudoufu/issues/1605) also `markers.AddressAnnotation` (`choudoufu.intentius.io/tofu-address`), no continuations, no slot | the `tofu-address` tag with continuations and `tofu-slot`, as on AWS, keys matched case-insensitively |
| Server-assigned ids | recovered by marker discovery (`internal/live/discovery`) | none: `TestNoKubernetesTypeNeedsDiscovery` pins it | none in the ordinary case: every id is built from the block; seven untaggable azurerm types mint a name when it is omitted, held by their record (`substrate/azure_minted.go`) |

Fixed: the address is on the object. `CarriesAddress` is true for every
family, and `TestEveryFamilyCarriesTheAddress` pins it. Kubernetes shipped
first without it (#1016) and gained it under #1605. Per platform: where the
address sits (`AddressInMarkers`), and whether slots and continuations
exist. GCP is ruled: a second label, `tofu-address`, holding a fixed-length
encoding of the address (#1121, 2026-10-19). Azure keeps AWS's: the
`tofu-address` tag with continuations and slots, keys matched
case-insensitively.

### 6. Types that are identifiable but cannot carry a marker

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| Answer | the declaration-carried tier, counted in `live/SUBSTRATES.md` and classified by survey path in `live/RECOVERY.md` (client-named, parent-derived, account-derived, unique-name) | no untaggable whole-object types; the field-granular types (#1191) carry the server-side-apply field manager `choudoufu:<estate>` as their marker (`kubesweep/fieldgranular.go`) | declaration-carried for an untaggable type whose id the block supplies (489 azurerm types; 2,366 azapi ARM types at their latest version), counted in `live/READINESS.md`'s Azure section; a removed block is found through its record (`substrate/azure_record.go`) |
| Veto | `identity.MarkerlessTypes` (`identity/markerless_generated.go`) refuses untaggable types whose identity is server-minted | none needed | none: no admitted Azure type has a server-minted id outright |
| Fence gap | stated per service in `live/MARKERS.md`, "What this grant cannot reach" | none for whole objects | stated in `live/MARKERS.md`, "What the Azure grant cannot reach": the group's RBAC covers untaggable children, Policy does not; types outside any group are counted per tier in `live/READINESS.md` |

The rule for a new platform, in order: look first for a carrier the stock
provider already writes; classify the rest by survey path; veto the
server-minted ones; state the fence gap. This tier is large on both new
platforms: GCP's network skeleton has no labels (#1119 item 3). On Azure,
719 of azurerm 5.10.0's 1,106 types have no ARM tags map (387 have one),
and azapi 2.13.0 knows 3,397 ARM types, of which 1,031 take tags at their
latest API version and 84 change between versions (#2119,
`live/READINESS.md`). The proving claims are inventoried in
[#2119](https://github.com/INTENTIUS/choudoufu/issues/2119) and built
last.

### 7. Not-found recogniser

`internal/live/absent` (`absent.Matches`) decides whether a provider's
import error means "no such object" or "could not answer". It is textual,
because only Summary and Detail cross the plugin wire. It holds AWS phrasings
(`ResourceNotFoundException` and the provider's own), the
`hashicorp/kubernetes` one, added in PR
[#1068](https://github.com/INTENTIUS/choudoufu/pull/1068), and azurerm's
two renderings of a 404 plus ARM's not-found codes (#1120). azapi needs
none: it answers a missing object with a null import. Fixed: a new
provider's not-found text is added here with a test, or its imports read as
failures.

### 8. Sweep and orphans

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| Kind | `substrate.SweepTaggingIndex`: the Resource Groups Tagging API and the other legs in `discovery/tagging.go`, `nativesweep.go`, `cloudcontrol.go`, through the configured provider | `substrate.SweepLabelList`: one label-selected list per served kind per namespace, `discovery/kubernetes.go` over the `internal/live/kubesweep` client from `NewSweeper` | `substrate.SweepResourceGraph`: `discovery/resourcegraph.go` over the `internal/live/azsweep` client |
| Finds a type with no row | no | yes (`SweepFindsUnadmitted`) | yes, from the design |
| Marker outlives the object | yes, so tombstones are read | no (`MarkerOutlivesObject`) | yes, from Azure's docs |
| Controller-held | ACK and Crossplane tags (`markers.ControllerTagKeys`) | the Helm release annotation; objects with `ownerReferences` are excluded by the sweep in `kubesweep` | ARM `managedBy`, and tags with the `aks-managed-` prefix |
| Landed | | [#1065](https://github.com/INTENTIUS/choudoufu/issues/1065), PR [#1070](https://github.com/INTENTIUS/choudoufu/pull/1070) | #1120 unit 11 (merged at `d4ff02eee3`) |

Fixed: an undeclared object carrying the estate's marker shows up in a plan;
a sweep failure is a refusal registered in `discovery/refusals.go`. Per
platform: the call shape and the consistency lag. GCP's is Cloud Asset
Inventory, and sweep equivalence waits on lex00/floci-gcp#7 (#1121). Azure's
is Resource Graph, one query per subscription, which floci-az does not
serve yet (`live/FLOCI.md`).

### 9. Record store

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| Backend | `record_store "s3"`, `staterecord/s3.go` | `record_store "kubernetes"`, `staterecord/kubernetes.go`, opened by `projection/kubernetes_store.go` | `record_store "azurerm"`, `staterecord/azureblob.go`, opened by `projection/azurerm_store.go` |
| Conditional write | `If-None-Match: *` and `If-Match` on the object | `resourceVersion` on a Secret; no lock, no Lease | `If-None-Match: *` and `If-Match` on the blob's ETag; no lease |
| Opening checks | three bucket settings (`staterecord/bucketcontract.go`, `examples/record-store-bucket/CONTRACT.md`) | four cluster assertions (`staterecord/kubernetescontract.go`, `examples/record-store-cluster/CONTRACT.md`) | three storage-account settings read through ARM (`staterecord/azurecontract.go`, `examples/record-store-azurerm/CONTRACT.md`) |
| Landed | [#1332](https://github.com/INTENTIUS/choudoufu/issues/1332) | [#1392](https://github.com/INTENTIUS/choudoufu/issues/1392), PR [#1435](https://github.com/INTENTIUS/choudoufu/pull/1435) | #1120 (merged at `e1fb9be94e`) |

Fixed: `staterecord.Store` (`Get`, `PutIfVersion`, `PutIfAbsent`) with a
conditional write, the sentinel check on opening (`live/STORAGE.md`,
"Opening a store"), a bulk read that is complete or fails, and a
`staterecord.ContractChecker`. The block label is validated in
`internal/configs/live.go`. Per platform: the backend and its checks.
Kubernetes had a lane (PR #1074, 2026-09-12) a week before its own store
(PR #1435, 2026-09-20), so a native store is not the first unit. Azure's
store needs an ARM credential beside the blob one, because its three
opening checks are storage-account properties.

### 10. The fence

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| Mechanism | IAM conditions on `aws:ResourceTag`, reads and writes, per principal (`live/MARKERS.md`, "Granting an estate") | one `ValidatingAdmissionPolicy`, writes only, per principal through the authorizer: `live/kubernetes/estate-boundary.yaml` and `estate-grant.yaml`, held by `live/kubernetes_gate_test.go` | resource-group RBAC per principal, which alone governs deletes, plus Azure Policy `deny` on writes lacking or changing `tofu-estate`, with an opt-in `denyAction` delete lock (`deleteLock`, off by default, ruled on #1122 2026-10-20): `live/azure/estate-grant.json` and `estate-boundary.json`, held by `live/azure_fence_test.go` |
| Landed | | [#1066](https://github.com/INTENTIUS/choudoufu/issues/1066), PR [#1071](https://github.com/INTENTIUS/choudoufu/pull/1071) | #1120 unit 8 (merged at `154b238fd2`) |

Fixed: the fence is never code here; it is a template a person installs,
and the claims table says per platform whether it is proven or restated.
Per platform: everything else, and the headline must say what the fence does
not cover (Kubernetes's is write-only). GCP is ruled project-scoped for now
(#1121). Azure RBAC cannot condition on tags; the fence is resource groups
(ruled 2026-10-19, #1122), and it fences no reads. `live/azure/GATE.md`
has the rest.

### 11. Lint and refusals

Fixed: a construct the platform cannot honour is refused by name, with a
lint rule and a fixture. Kubernetes added one, `generate-name`
(`internal/live/lint/generate_name.go`), because a server-minted name leaves
no join key; AWS's rules (`count-index`, `markerless-type` and the rest) live
beside it in `internal/live/lint/issue.go`. Azure added two:
`policy-overwrite-at-key`, because an ARM create overwrites an unmarked
object, and `arm-tag-limit`, a warning for a resource with no room for its
markers. The registries a rule joins are
listed below.

### 12. Emulator and harness

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| Harness | floci, pinned by digest in `live/floci-image`; `stack_up` in `live/smoke/lib.sh`; coverage probed in `live/floci-capabilities.json`; traps in `live/FLOCI.md` | kind, a real API server, pinned in `live/kind-node-image`; `cluster_up` / `cluster_down` in `live/smoke/lib.sh` | floci-az, pinned by digest in `live/floci-az-image`; `azure_up` / `azure_down` in `live/smoke/lib.sh`; gaps in `live/FLOCI.md` |
| Landed | | [#1057](https://github.com/INTENTIUS/choudoufu/issues/1057), PR [#1058](https://github.com/INTENTIUS/choudoufu/pull/1058) | #1120 unit 7 (merged at `9e365b8025`) |

Fixed: a pinned image and a per-run lifecycle in `lib.sh`. GCP and Azure
run on floci forks (policy below).

Azure's harness is lex00/floci-az, pinned by digest in `live/floci-az-image`;
`azure_up` / `azure_down` in `live/smoke/lib.sh` start it, export the
provider environment, and remove it. What it serves is in `live/FLOCI.md`.

### 13. Claims with a BREAK control

The Kubernetes bar on #1119: bind, mark on create, sweep and a governed
fence, each a claim with a BREAK control, then a lane.

| Claim | AWS scenario | Kubernetes scenario | Azure draft, not run | BREAK |
|---|---|---|---|---|
| 7, identity is a tag | `carve-by-retag.sh` and others | `k8s-greenfield.sh` | `az-identity-is-a-tag` | strips the label; the replan must restore it |
| 1, no silent orphans | `no-silent-orphans.sh` | `k8s-no-silent-orphans.sh` | `az-no-silent-orphans` | strips the orphan's label; the replan must leave it |
| 13, the tag is the boundary | `the-tag-is-the-boundary.sh` | `k8s-the-label-is-the-boundary.sh` | `az-the-tag-is-the-boundary` | removes the policy; the denied write must go through |
| 29, the store refuses what it can't trust | `a-wrong-bucket-is-refused.sh` | `k8s-records-in-the-cluster.sh` | `az-records-in-a-storage-account` | removes the fences |

Fixed (`live/smoke_claims_test.go`): the platform's name joins
`provider_order` in `live/smoke/claims.json`; every claim gets a cell for it
(proven, restated, unproven, open or n/a, with a note unless proven); a
claim's `substrate` list equals its cells that are not n/a; a scenario's
header reads `# CLAIM <n> (<provider>) - <title>`; and each claim page under
`live/smoke/claims/` gets an `## On <Provider>` section naming every proof.
A scenario may start as a demo in `smokeDemoScenarios` before it can assert
a marker, as `k8s-greenfield` did.

### 14. Gauntlet lane

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| Lanes | `terraform-popular`, `opentofu-native`, `reference`, `published-deployment` | `kubernetes` (`gauntlet.LaneKubernetes`), its own bar | `azure`, its own bar (`lanes.azure`) |
| Substrate | `SubstrateFloci`, `SubstrateFlociEKS` | `SubstrateKind`, `gauntlet_kind_up` in `live/e2e/lib/gauntlet.sh` | floci-az, `gauntlet_floci_az_up` in `live/e2e/lib/gauntlet.sh` |
| Landed | | [#1067](https://github.com/INTENTIUS/choudoufu/issues/1067), PR [#1074](https://github.com/INTENTIUS/choudoufu/pull/1074) (`reference-k8s`), PR [#1089](https://github.com/INTENTIUS/choudoufu/pull/1089) (first published estate) | #1120 unit 15 (merged at `ca71415d3e`): `reference-azure`, not run |

Fixed: estates in `live/gauntlet/estates.json`, a lane in
`tools/gauntlet/manifest.go`'s `KnownLanes`, and a per-stage substrate
reading in `tools/gauntlet/stages.go`, so a stage whose oracle is
AWS-shaped is marked per substrate rather than silently skipped. A
hand-written reference estate comes first, then published roots.

### 15. Docs pages

| | AWS | Kubernetes | Azure |
|---|---|---|---|
| `live/` | the root `live/*.md` docs, substrate-neutral | `live/kubernetes/` ADOPT, COMPATIBILITY, FAULTS, GATE, OPERATE, PROOF | `live/azure/` ADOPT, COMPATIBILITY, FAULTS, GATE, OPERATE, PROOF, beside the fence templates |
| Site hub | `site/src/content/docs/aws/` | `site/src/content/docs/kubernetes/` | `site/src/content/docs/azure/` |

Fixed: under the #1602 ruling root docs stay substrate-neutral with a
section per substrate where they differ, and `live/<platform>/` keeps only
what is platform-only; `live/compatibility_outline_test.go` holds
`live/kubernetes/COMPATIBILITY.md` and `live/azure/COMPATIBILITY.md` to the
root outline, and a new platform's copy joins the same list. A site hub is an `index.md` plus the five slot pages
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

And where Azure stands against both, from the `azure` branch:

- Every Azure claim cell reads `open`; the four proofs are drafts, and
  `reference-azure` has not run.
- The Azure fence covers writes only, like Kubernetes's, and through
  Policy rather than per principal.
- Azure's sweep can find a type with no row, like Kubernetes's.
- Azure has slots and continuation markers, like AWS.
- An unmarked object at a declared id is refused by name, where AWS
  plans a second resource and Kubernetes gets a 409.

## Build order

The order Kubernetes landed in works as a build order. It is not the
definition of the contract.

| Step | Ruled or existing first | Kubernetes did | Azure did (#1120) | Closed by |
|---|---|---|---|---|
| 1. Ruling | the research issue | #1016, ruled 2026-09-11 | #1122, ruled 2026-10-19 | the maintainer's comment |
| 2. Harness | ruling | PR #1058: `cluster_up`, `estate-k8s`, `k8s-greenfield` as a demo | unit 7: floci-az pin, `azure_up` | a scenario with a verdict per step |
| 3. Carrier | harness | PR #1062: `LabelSurface`, label stamp | unit 1: `SurfaceARMTags`, the ARM stamp; draft `az-identity-is-a-tag`, not run | claim 7, BREAK strips the label |
| 4. Admission | carrier | PR #1068: `ObjectMetaShape`, `generate-name`, `absent` | units 10 and 13: `azurerm-ids.json`, azapi's natural key, `absent` | identity golden values, the lint fixture |
| 5. Sweep | admission | PR #1070: `kubesweep`, discovery's Kubernetes leg | unit 11: `azsweep`, the Resource Graph leg; draft `az-no-silent-orphans`, not run | claim 1, BREAK strips the label |
| 6. Fence | sweep, fence ruling | PR #1071: `estate-boundary.yaml` | unit 8: `estate-grant.json`, `estate-boundary.json`; draft, not run | claim 13, BREAK removes the policy |
| 7. Lane | the claims above | PR #1074, PR #1089 | unit 15: `reference-azure`, not run | lane reads clear |
| 8. Migration | carrier | PR #1078: label carrier in `live-import` | unit 1: the ARM tags in `live-import` | lane migrate stage |
| 9. Record store | optional | PR #1435 | `record_store "azurerm"`; draft `az-records-in-a-storage-account`, not run | claim 29, BREAK removes the fences |

Kubernetes got its family value after the fact (#1118) and its address
annotation later (#1605); a new platform should start with the family value
and rule where the address lives together with the carrier.

Azure did that: the family value and the address carrier came in unit 1,
with the carrier ruling. It also built every step before any could run,
because the emulator lacked the sweep, the fence and the store's settings
(`live/FLOCI.md`). Only the ruling and admission are closed, admission by
`internal/live/check/testdata/azure-synthesis-golden.txt` and the two lint
fixtures; every step a claim or the lane closes is still open.

Known differences for the next two:

- GCP: labels for the estate and an encoded `tofu-address` label, no
  labels on the network skeleton, fence project-scoped for now, `google`
  and `google-beta` share types (#1119 items 3 and 6, #1121).
- Azure, built through step 9 and run through none: tags on 387 of
  azurerm 5.10.0's 1,106 types and on 1,031 of the 3,397 ARM types azapi
  2.13.0 knows (84 depend on the API version), the fence is resource
  groups, azapi beside azurerm as one family, case-insensitive tag keys
  and ARM ids; carrier and identity rule ruled on #1122 (2026-10-19).

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
| A claim cell | see requirement 13 |
| A site hub | see requirement 15 |
| A tool under `tools/` | a `.gitignore` entry (`TestEveryToolHasAGitignoreEntry`) |
| An artifact measured on an emulator | its image field in `live/flociimage_test.go` |

## Emulator policy

Each platform has its own floci fork: lex00/floci for AWS, lex00/floci-gcp
for GCP, lex00/floci-az for Azure.

- Agents only file issues, and only on the fork for their platform.
- Agents never do work on a floci fork: no branches, commits, PRs, image
  publishes or re-pins. The maintainer does that work.
- Before filing, check upstream floci-io to see whether the gap is already
  known.
- The issue's text is shown to the maintainer before it is filed.
- Code in choudoufu that needs a missing emulator capability waits for the
  fork. Do not route around it.
- The image a harness runs is pinned by digest, as `live/floci-image` pins
  AWS's (`ghcr.io/lex00/floci@sha256:...`). The surfaces each fork must
  serve are listed in lex00/floci-gcp#2 and lex00/floci-az#2.
