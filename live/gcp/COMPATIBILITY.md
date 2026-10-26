# Compatibility

**Layout.** `live/COMPATIBILITY.md` is the substrate-neutral reference;
this page covers the same sections for GCP only, and keeps only what is
GCP-specific (ruled on
[#1602](https://github.com/INTENTIUS/choudoufu/issues/1602)). A section
with nothing GCP-specific to add says so in one line.

```
choudoufu live-check
```

No project needed. It names each block a GCP root cannot plan, and why.

## Your provider

`hashicorp/google` and `google-beta` 8.6.0, stock and unmodified
([#1121](https://github.com/INTENTIUS/choudoufu/issues/1121)). The marker
goes in through the provider's own `labels` argument, in the create call
it already makes, and nothing else in the plan differs from what stock
would show. No resource is synthesized. Delete `estate.chdf.hcl` (or the
`live` block), keep the cache file, and stock OpenTofu with the same provider carries on.
Everything stock accepts either works or is refused by name.

Both provider names are one family (`substrate.Google`): a type spelled
through `google-beta` is judged exactly as the same type through `google`.

## Your resource types

A type carries its marker as labels when it has a settable top-level
`labels` map with the computed `effective_labels` map beside it: 297 of
hashicorp/google 8.6.0's types. That is read from the schema
(`markers.GoogleLabelSurface`), not from a list of names. The marker is
three labels, `tofu-estate`, an encoded `tofu-address` and, on a fungible
`count` set, `tofu-slot` (`live/MARKERS.md`, "GCP: labels").

### Readiness tiers

| Class | Tier | What recovers it |
|---|---|---|
| Labelled types | marker-carried | the labels, read back from `effective_labels` |
| The network skeleton: networks, subnetworks, firewalls, routes, routers, service accounts, custom roles and the other types with no `labels` map | declaration-carried | the configuration's `name`, `project` and location, recomputed on every plan |
| Instance templates | declaration-carried | the configured name; their `labels` belong to the VMs, below. A template named by `name_prefix` gets its name at create and is held by its record instead |
| Unlabelled types whose identity the server mints | record-carried | the estate's record, written at create (the located-record route, as on AWS) |

The skeleton's tier has a sweep gap. The sweep finds an estate's resources
by the estate label, and a skeleton object has none, so an undeclared one
is never found. Its removal is proposed only by a deleted block's record.
Lose the record store and the block together and the object is an orphan
nothing finds. No stock argument can carry a marker there instead:
`description` is ForceNew on networks, subnetworks and routes, and is the
user's own on the rest. One bucket per estate keeps the records
(`live/STORAGE.md`).

### Refused

| What | Why | Fix |
|---|---|---|
| `tofu-estate`, `tofu-address` or `tofu-slot` written by hand into an instance template's `labels` | GCP copies those labels onto every VM a managed instance group makes, and each VM would read as an orphan of the estate ("Ownership marker conflict") | Remove the key; the template carries no marker of its own |
| An estate name over 63 characters on a labelled type | a label value holds at most 63 ("Ownership marker is not a legal label value") | A shorter estate name |
| An unlabelled type whose identity the server mints, in a root with no `live` block | no marker, no configured name, and no record store to hold the identity (`markerless-type`). Under a `live` block, which always has a record store, it is held by its record | Add the `live` block |
| A resource whose `labels`, the provider's `default_labels` and the markers come to more than 64 labels | GCP holds at most 64, and stock would have created it (`gcp-label-count`) | Drop labels until two or three fit |

Controller-held objects are never swept and never adopted: a Compute
Engine instance or disk carrying a label GKE, Dataproc or Batch reserves
for what it creates (`goog-gke-node`, `goog-gke-volume`,
`goog-k8s-cluster-name`, `goog-k8s-node-pool-name`,
`goog-dataproc-cluster-uuid`, `goog-dataproc-cluster-name`,
`batch-job-uid`), and a Cloud Run service whose `goog-managed-by` label is
non-empty. Exact keys, never a `goog-` prefix, because the provider writes
`goog-terraform-provisioned` on everything it creates.

## How your configuration is written

### Expansion

No GCP-specific case. `count` and `for_each` expand as
`live/COMPATIBILITY.md` describes.

### `for_each` keys

The `tofu-address` label is a fixed-length hash of the address as
OpenTofu prints it, so a key is never escaped into it and never makes the
label longer. There are no continuation labels.

### Identity arguments

No GCP stops beyond `live/COMPATIBILITY.md`'s table. A labelled type binds
by its label, so its identity argument matters only before the first
create. A skeleton type binds by its configured `name`, `project` and
location, so those have to resolve before a provider runs, the ordinary
rule.

## Your modules

No GCP-specific case. The address label carries the full module path,
hashed, so a module-nested address is no longer a label than a root one.

### Crossing a module boundary

A move across a module boundary is an ordinary rename. `live-mv` rewrites
a labelled resource's `tofu-address` label (and, with `-from-estate`, its
`tofu-estate` label) in one labels-only plan and apply, after a Cloud Asset
search shows no other resource already carries the new value; it refuses
by name when that search cannot be made. Renaming the block and planning
works too: the records follow the address, and the plan rewrites the
`tofu-address` label as an in-place change. A `moved` block works the same
way.

## Your accounts and regions

The project is the unit, because the fence is the project
(`live/MARKERS.md`, "Granting a GCP estate"). One estate per project. Two
estates sharing one project are not fenced from each other per principal:
each principal's binding reaches the other estate's resources. Lint warns
when one estate's google resources resolve to more than one project
(`gcp-multi-project`).

The sweep runs one Cloud Asset Inventory search per project the estate's
resources name, so an estate that does span projects is still swept
whole, as long as each project has the Cloud Asset API enabled and the
principal holds `cloudasset.assets.searchAllResources` there.

### Mixed estates

A root mixing `aws_*`, `kubernetes_*` and `google_*` resources keeps each
substrate's admission, marker, sweep and fence independent. A GKE
cluster's nodes and disks are controller-held, above, so a
`google_container_cluster` estate never proposes removing them.

## How you run it

The command-level refusals in `live/COMPATIBILITY.md` are unchanged.

The sweep reads an index, and the index lags. A resource created moments
ago may be missing from the search, and a deleted one may still be listed
with its labels for a while. A record tombstone is how the plan tells a
lingering hit from a second claimant, as on AWS. A project the search
cannot cover (the API disabled, the permission missing, a page failing
part way) is the warning "GCP sweep incomplete", a gap in coverage and
never a wrong plan.

A labelled object whose address label matches nothing declared is
reported, never planned for removal ("GCP resource carries this estate's
label and no declared address"): the label is matched, never decoded, so
the plan cannot know which address to destroy it at. A deleted block's
record proposes that removal instead.

### Two creates of one server-assigned object

Most google types are named by their configuration, and the API refuses a
second create of the same name, so two concurrent applies cannot both
create one. A labelled type whose identity the API assigns at create time
can be created twice, and the next plan has to see both objects to name
the collision ([CONCURRENCY.md](../CONCURRENCY.md)). The index can miss the
newer one, so these types are listed through their own service, which reads
the store the create wrote to, and both objects carrying one address label
are a marker collision that refuses the plan. These types are what the
race tests. Each one binds, consistency unmeasured: none of these services
publishes a list consistency model, and no race has been run against them.

| Type | Listed through |
|---|---|
| `google_active_directory_domain` | Managed Microsoft AD `domains.list` (`locations/global`) |
| `google_document_ai_schema` | Document AI `schemas.list` |
| `google_recaptcha_enterprise_key` | reCAPTCHA Enterprise `keys.list` |
| `google_service_directory_namespace` | Service Directory `namespaces.list` |
| `google_transcoder_job` | Transcoder `jobs.list` |
| `google_vertex_ai_dataset` | Vertex AI `datasets.list` |
| `google_vertex_ai_index` | Vertex AI `indexes.list` |
| `google_vertex_ai_index_endpoint` | Vertex AI `indexEndpoints.list` |
| `google_vertex_ai_reasoning_engine` | Vertex AI `reasoningEngines.list` |
| `google_vertex_ai_tensorboard` | Vertex AI `tensorboards.list` |

Each list is made in the project and location every declared instance
names: its block's own `project`, `region` or `location`, else the provider
block's project and, for Vertex AI, its region. The provider's
`*_custom_endpoint` argument for the product, or its environment variable,
is honoured. The principal needs the type's list permission
(`aiplatform.indexEndpoints.list` and so on) and the service enabled. When
a list cannot be made (a location written as another resource's attribute,
a denied or disabled service), the plan warns "GCP type list incomplete"
and binds that type from the index, as every other type is bound.

Two server-assigned labelled types are bound from the index only and are
not what the race tests: `google_dialogflow_cx_intent`, listed under its agent,
which is another resource's attribute at plan time, and
`google_assured_workloads_workload`, listed under an organization rather
than a project.

### Every other type listed through its own service

The same per-type listing covers 143 more labelled types, chosen by the
rule plunge's `pkg/backend/plunge/gcplist` uses (INTENTIUS/plunge#15's
ruling: per-service listers for every covered type, the same in
both repos). A type is covered when `pkg/identity.GoogleAssetImports` gives
its Asset name and its service is one of the 28 in
`internal/live/gcpsweep/servicelist.go`: Compute Engine, Cloud Storage,
Pub/Sub, Cloud Run, Cloud Functions, Secret Manager, Artifact Registry,
Cloud KMS, Cloud DNS, Memorystore for Redis and Memcached, Filestore,
Spanner, AlloyDB, API Gateway, Eventarc, Certificate Manager, CA Service,
Cloud Deploy, Network Services, Network Security, Network Connectivity,
Dataproc, Cloud Workstations, Datastream, Dataproc Metastore, Managed
Service for Apache Kafka and Parameter Manager. The list is the Asset
name's parent collection (`projects/{{project}}/topics` for a topic,
`projects/{{project}}/zones/{{zone}}/disks` for a disk, `b?project=` for
buckets), read to the last page and kept by the estate's label client
side. The 153 types both repos list are in
`internal/live/gcpsweep/testdata/covered-types.txt`, and plunge carries an
identical copy that its own test reads.

Where such a list is made comes from the instance's block: `project`,
`region` and `zone` fall back to the provider block's, as the provider
defaults them, and a self link counts by its last segment. `location` is
the block's own only, since the provider's default for it differs by type.
A parent the path is under (a crypto key's `key_ring`, a map entry's `map`)
is the block's argument of that name and has to be known from
configuration alone. When one of these cannot be read, or the list fails,
the plan warns "GCP type list incomplete" and binds the type from the
index, as above. The principal needs the service enabled and its list
permission for the collection (`pubsub.topics.list`, `compute.disks.list`,
`storage.buckets.list` and so on).

Consistency is claimed for Cloud Storage only, which documents bucket
listing as strongly consistent. Every other service in the table binds,
consistency unmeasured: it publishes no list consistency model, and its
list is kept by the estate's label client side rather than filtered by the
API. Its list reads the service's own store, which is what a create race
on floci-gcp tests.

### Known differences

hashicorp/google reads `labels` and `terraform_labels` non-authoritatively:
a read keeps only the keys its prior state named. A plan built without the
cache mirrors the marker keys out of `effective_labels` into both, and
rebuilds `terraform_labels` from the keys of the provider block's
`default_labels`. Only the keys are needed, so a default label whose value
comes from another resource or a data source is still read. When the key
set itself, or `add_terraform_attribution_label`, is not known statically
(a whole map read from another resource), the plan warns "Provider default
labels not known at plan time" and shows `terraform_labels` as a change
that the apply makes no call for. Write the keys out in the block to
remove it.

## Constructs this page used to refuse, and no longer does

Before [#1119](https://github.com/INTENTIUS/choudoufu/issues/1119) a
`google_*` type planned only through the provider's identity schema and
was never stamped. Nothing else on `live/COMPATIBILITY.md`'s list for this
section is GCP-specific; it applies to a GCP root unchanged.

## Effects do work

No GCP-specific case. `null_resource`, `terraform_data`, `time_*` and the
`random_*` family get the lifecycle `live/COMPATIBILITY.md` describes.

## Two hazards that are now refusals

The tag hazard has a GCP instantiation. `ignore_changes = all`,
`ignore_changes = [labels]`, `ignore_changes = [labels["tofu-estate"]]` and
`ignore_changes = [labels["tofu-address"]]` on a labelled type are refused,
naming the label this mode writes. Ignoring a key of your own,
`labels["owner"]`, stays admitted. The child-side `providers` mapping
hazard is provider-agnostic and applies as written.

## Editors and linters

No GCP-specific case. `live/COMPATIBILITY.md`'s section applies as written.
