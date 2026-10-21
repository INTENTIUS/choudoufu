# Where things are stored

| What | Where it lives | Who writes it | Losing it costs |
|---|---|---|---|
| Ownership markers | On the resource: two tags on AWS, one label on Kubernetes | The apply | The resource goes invisible and the next plan proposes a duplicate |
| Records | The record store, one record per managed instance | choudoufu | For most resources, a slower or noisier plan. For a record-backed one, the resource |
| Receipts | Ordinary resources you declare | You | Nothing structural. It is your data, in your configuration |
| The cache | `.terraform/choudoufu-cache.tfstate`, on the machine that ran | choudoufu | A read |

Only the markers say what you own. `live/MARKERS.md` is their spec, and the
surface other tooling can rely on. [Records](https://intentius.io/choudoufu/how-it-works/values/)
says what a record is and why there are two kinds, and
[The cache](#the-cache) below covers the cache. This page is
where each thing physically is.

## The record store

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "s3" {
  bucket = "my-records-bucket"
}
```

The same block goes inside `live` for the in-`terraform` form. The label picks
the backend.

| Backend | Where it writes | Arguments |
|---|---|---|
| `local` | A directory beside the module, `.tofu-records` by default | `path` |
| `s3` | A bucket you already own | `bucket` (required), `bucket_owner`, `key_prefix`, `region`, `allow_insecure` |
| `kubernetes` | Secrets in a namespace you already own | `namespace` (defaults to `tofu-records-<estate>`), `key_prefix`, and the connection arguments of stock's `kubernetes` backend |
| `azurerm` | Blobs in an Azure Storage container you already own | `storage_account_name`, `container_name`, `resource_group_name` (all required), `key_prefix`, `allow_insecure`, and the connection arguments of stock's `azurerm` backend |
| `gcs` | A Google Cloud Storage bucket you already own, one per estate by default | `bucket` (required), `key_prefix`, `allow_insecure`, and the connection and encryption arguments of stock's `gcs` backend |

A `live` block that declares no `record_store` gets the local one, the way
stock implies a local state file.

All four give the same guarantees; a run cannot tell them apart. Each record
is its own object, tagged or labelled with the estate's name the way a
managed resource is, so one S3 bucket or cluster serves any number of estates
and your IAM or RBAC decides who reads which. A GCS bucket cannot fence its
listing, so it is one per estate by default; see
[One bucket per estate](#one-bucket-per-estate).

Use a bucket or a cluster for anything more than one operator shares. The
local store is for one person or a demo. It is also what a CI runner gets if
the estate declares nothing, and there it is empty on every run: an ordinary
resource still binds by its marker, and a record-backed resource is proposed
for create again.

An estate that runs only on Kubernetes has no reason to reach for a bucket:
its shared store is the cluster it already has, and
[Kubernetes](https://intentius.io/choudoufu/kubernetes/) says which of its resources stop
working without a record.

### Opening a store

A store proves itself before a plan trusts it. The first run writes
`.store-sentinel` and reads it back through the same listing a plan uses. A
store that accepts the write and does not list it is refused by name. It
never reads as an empty estate, which would have the next plan propose
rebuilding everything.

A role that may read the store and not write it can plan. Once the sentinel
exists, a run that cannot write it reads it back and carries on. A store with
no sentinel, opened by a role that cannot write one, is refused by name.

A store that refused stops every command: a bucket that fails
[its three settings](https://intentius.io/choudoufu/docs/use/bucket/), a listing
that does not return what was just written, a KMS key that refused the run.
For `plan`, `apply` and `live-import`, a store that could not be reached
stops the run too.

`live-plan` and `live-mv` go on without records and say so in a warning
titled `The record store was not read`. A record-backed resource is known
only by its record, so it may appear as something to create when it already
exists. A `kubernetes_manifest` needs its record to see that the
configuration dropped a label, so without it the output can read
"No changes" while the live object keeps the label.

## The local store

One file per record under `.tofu-records`, created at the first run, with
directories `0700` and files `0600`. That keeps other users on the machine
out and does nothing about `git add`, and records hold
[secrets](SECRETS.md). Gitignore the directory, or
whatever `path` names, before the first run.

```
# .gitignore
.tofu-records/
```

A write takes a `<file>.lock` sidecar for the length of one file operation,
which is how a plain directory gets a conditional write. A lock older than
thirty seconds is broken by the next writer, so a killed run cannot wedge
the store.

## The bucket

One bucket serves any number of estates. choudoufu never creates it and never
configures it. [What you set up by hand](https://intentius.io/choudoufu/docs/use/setup/) has
the creating, [the three settings](https://intentius.io/choudoufu/docs/use/bucket/)
has what it must have, and [IAM](https://intentius.io/choudoufu/docs/use/bucket/) has the
policy for an estate's role.

### Layout

An estate writes under three prefixes and nowhere else.

| Prefix | What is there | How many objects |
|---|---|---|
| `tofu-records/<estate>/` | One object per managed instance, at `<type>/<encoded address>`, plus `.store-sentinel` | As many as the estate has instances, plus one |
| `tofu-hints/<estate>/` | `guided`, where guided discovery last found things | One |
| `tofu-outputs/<estate>/` | The value each root output settled on at the last apply, so a plan can render a change as a change | One per root output, never a `sensitive` one |

Every prefix ends in `/`. S3 matches a prefix as a plain string, so
`tofu-records/prod` is also a prefix of `tofu-records/prod-eu/...`. With the
delimiter, an estate called `prod` and one called `prod-eu` share no keys, no
listing and no bulk read
([claim 28](smoke/claims/a-name-prefix-shares-no-keys.md)).

A `key_prefix` override moves the first of the three. It may not begin with
any reserved root, so a record cannot land where a hint, an output or a
receipt lives.

### Tags

Every object is written with `tofu-estate`, and a record also with
`tofu-address`, in the same request as the object, so it never exists
untagged. The tags are for authorization and provenance. The published policy
requires the tag on a write, denies a read of an object tagged as another
estate's, and denies relabelling one
([claim 28](smoke/claims/a-name-prefix-shares-no-keys.md#on-aws),
`one-bucket-many-estates`).
Nothing is found by tag: objects are found by listing a known prefix.

### Requests

| When | What is sent |
|---|---|
| Opening the store, every run | A conditional `PutObject` of the sentinel, which writes only the first time and is skipped by a role that cannot write. After the first run it is answered `412`, and one `GetObject` of the sentinel follows. Then one `ListObjectsV2` |
| Reading the estate, every run | `ceil(N/1000)` `ListObjectsV2`, then a `GetObject` per key including the sentinel, eight in flight unless `TOFU_LIVE_RECORD_READ_PARALLELISM` says otherwise. The read is complete or the run fails ([claim 29](smoke/claims/a-bulk-read-is-complete-or-it-fails.md)) |
| The hint and the outputs, every run | One `GetObject` each |
| An apply, per record that changed | A `GetObject`, then a conditional `PutObject` or `DeleteObject`. For a record-backed or record-carried instance, and for any destroyed one, it is sent when that instance's apply step returns (#1944); for every other record, after the whole apply |
| The end of an apply, per record already written when its instance returned | Nothing, unless something that write leaves out changed: a record-carried instance whose residue the final pass classifies costs a second `GetObject` and conditional `PutObject` |

A create is `If-None-Match: *`, and an update or a delete carries `If-Match`
with the version the writer read. The final pass of an apply that already
wrote a record when its instance returned expects the version that write
produced, not the plan's. Nothing is locked. A record the apply
would write back exactly as it read it is not written at all, so two applies
that change different resources of one estate both land (#1938).

The conditional write referees only what is in a record, and an ordinary
cloud resource's record holds its identity, the arguments the provider never
reads back, taint, deposed objects and tombstones. An attribute the provider
reads back, such as a queue's visibility timeout or an instance's type, is not
in it. Two applies that change such an attribute leave the record's bytes as
they were, so neither writes it and no condition is checked. Two plain
`apply` runs are last-writer-wins at the cloud API: both succeed, and the
cloud keeps whichever call came last. `apply <planfile>` re-reads the live
system, plans again, and refuses with exit status 3 when one of the plan's own
changes differs from the approved plan in its before- or after-values (#878).
A plan approved against a value another apply has since replaced is refused by
name, and the cloud keeps the other apply's value. Two saved-plan applies that
both re-read before either reaches the cloud are not covered (#1504).
[Claim 15's](smoke/claims/apply-what-was-approved.md#on-aws)
`apply-what-was-approved`, steps 8-14, races two saved plans on one SQS
queue's visibility timeout.
[CONCURRENCY.md](CONCURRENCY.md) has the races.
Every read is scoped to one estate, so adding an estate to the bucket slows
no other.

### Deleted records and versions

The bucket is versioned. A deleted record becomes a delete marker with the
record underneath as a noncurrent version, until the bucket's lifecycle rule
expires it. That window is the recovery path for a record deleted by mistake,
and the only one a record-backed resource has.
[Recover an estate](https://intentius.io/choudoufu/docs/use/recover-an-estate/) uses it.

`choudoufu destroy` destroys the resources and deletes the records of the
record-backed ones. It leaves a few small objects under the estate's prefixes: the
sentinel, the hint, the outputs, and a tombstone per destroyed instance.
Removing them is yours to do, and `examples/record-store-bucket`'s `just down`
refuses to delete a bucket that still holds any.

## The cluster

`record_store "kubernetes"` keeps the same records as Secrets, for an estate
that has no AWS account to put a bucket in.

Each record is one Secret in the namespace you name, labelled
`tofu-estate=<estate>` and with the record's key in an annotation. A write is
a create, or an update or delete carrying the `resourceVersion` the writer
read, so the API server decides a race in one step and nothing is held. There
is no Lease. A record larger than a Secret may hold is refused by name.

RBAC cannot condition on a label, so what keeps one estate out of another's
records is the namespace. Each estate gets its own by default,
`tofu-records-<estate>`; bind the estate's role to Secrets in that namespace
alone. Writes carry the estate label, so
[the admission policy](https://intentius.io/choudoufu/kubernetes/gate/) fences them the way
it fences every other object of the estate.

The store does not create the namespace, and a namespace that is not there is
refused by name with the `kubectl` line that creates it. Creating one and
granting an identity Secrets in it are two halves of the same cluster-admin
act, and a list in a namespace that does not exist answers empty, which would
otherwise read as an estate with no records.

A read says the same. A `get` of a Secret in a namespace that is gone is a 404
naming the Secret, not the namespace, so every read that would answer "nothing
here" asks whether the namespace is still there, and one that is missing or
being deleted is refused rather than returned as an empty estate. An identity
that may not `get namespaces` cannot be asked that; for those runs the signal
is the sentinel, and a listing that does not carry it is refused when the store
is opened.

The listing carries no label selector, because a selector cannot find a record
by the label it is missing. A record Secret that lost `tofu-estate` or
`app.kubernetes.io/managed-by`, one whose name is not the hash of the key it
claims, and two that claim one key are each refused by name with the `kubectl`
line that settles them, rather than left out of the listing.

A Secret under the wrong name is moved by copying it to the right one, and the
refusal names both the Secret it found and the name that record's key hashes
to. With `<namespace>`, `<misnamed>` and `<hashed-name>` read off it:

```
kubectl -n <namespace> get secret <misnamed> -o json \
  | jq 'del(.metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp)
        | .metadata.name = "<hashed-name>"' \
  | kubectl create -f -
kubectl -n <namespace> delete secret <misnamed>
```

That needs `jq`, which is why the refusal names this file instead of carrying
the pipeline.

A read of one key has its own refusal. The Secret that key hashes to can carry
a different key in its `choudoufu.intentius.io/record-key` annotation, which is
what hand-editing that annotation gets, and answering with that other record
would be worse than refusing.

Anyone who can `get secrets` in the records namespace reads every recorded
value, the same bargain `s3:GetObject` on the bucket makes.

### What the store checks about the cluster

Four things, asked on an estate's first contact with the store and again
before every run that writes a record: an apply, a `live-mv` that is not a
dry run, a `live-import -approve`. Never on an ordinary plan. They are the
cluster's version of the bucket's three settings.

| Assertion | What it asks | Asked with |
| --- | --- | --- |
| `namespace_access` | the records namespace is there, and this identity may do to Secrets in it what this run will ask | one `SelfSubjectAccessReview` per verb, never an attempted write |
| `read_isolation` | no other estate's records are readable, in another namespace or in this one | a cluster-wide review, then one per other namespace it can see, and a list of this namespace's records by their `managed-by` label |
| `encryption_at_rest` | the API server runs with `--encryption-provider-config` | the API server's own static Pod, where that Pod is visible; on EKS, GKE and AKS the provider's API, through a `control_plane` block (#1524) |
| `estate_boundary` | `estate-boundary.yaml`'s policy and its binding are installed, observed, denying and in force over the record Secrets, and this identity is granted its estate | a get on each, compared against the shipped file, and one review of `use` on `estates.choudoufu.intentius.io/<estate>` |

A fifth finding comes from the block itself. `insecure = true` turns off
verification of the API server's certificate. Anything on the path can then
answer as the API server, and it receives this identity's credential and every
record. The finding is named `tls_verification`. It is known from the block
before any request and refuses like a property that was read and is wrong.
Remove `insecure = true` and set `cluster_ca_certificate`. A block that does
not set `insecure = true` has no such finding.

`choudoufu live-cluster` asks the same four and prints them, with no plan and
nothing written. Run in a configuration directory it uses that live block's
namespace; `-namespace=<name>` checks any other, on the same cluster the
block's connection names, and the report says which API server answered. It
exits non-zero unless all four hold, except on a warning: a finding a run
proceeds past is counted and exits 0. `-plan-identity` asks what a plan job's
identity needs rather than what an apply needs.

A run refuses on a property that was READ and is wrong, and warns on one it
could not read. The two are different and the difference decides whether
anyone can act: a cluster whose API server carries no encryption configuration
is a fact somebody can change, while a Role scoped to one namespace cannot see
kube-system's Pods, cannot get a `ValidatingAdmissionPolicy`, and cannot list
the namespaces the other estates keep records in. Refusing on the second would
put `allow_insecure` into every correctly scoped CI job on its first day. So a
run says it by name on every run and never calls it a pass, and `live-cluster`,
run by someone holding those reads, is what answers it.

`encryption_at_rest` is never more than half readable. The flag is on the API
server's Pod where that Pod is visible, and the EncryptionConfiguration it
names is a file on the control plane: a configuration whose first provider for
secrets is `identity` sets the flag and encrypts nothing. So a missing flag
refuses, a present one is NOT CHECKED, and the finding carries the `cat` line
an operator runs on the node to finish it.

On a managed control plane there is no such Pod, and the setting is the
provider's: EKS's `encryptionConfig` (and its envelope-encryption default from
Kubernetes 1.28), GKE's `databaseEncryption`, AKS's
`securityProfile.azureKeyVaultKms`. A `control_plane "eks" | "gke" | "aks"`
block in the record_store block names the cluster, and the check reads that
one setting from the provider's API, believing the answer only for the
cluster whose endpoint is the host the store reaches (#1524). An EKS
connection through `aws eks get-token --cluster-name` is recognised without
the block. `examples/record-store-cluster/CONTRACT.md` has the table, and
`live/managed-k8s/harness.sh` is the run against a real managed cluster.

`allow_insecure` takes these four names and `tls_verification` the way it
takes the bucket's three.
A waiver reaches only what it names, silences a refusal or a warning, and says
what it costs on every run for as long as it is configured.

`read_isolation` is the one assertion about the run rather than the cluster,
so it has a floor and a refusal. An identity that may read Secrets cluster-wide
on a cluster holding no other estate's records has exposed nothing yet, and
warns. One that can read a records namespace belonging to another estate is
refused, naming it. A cluster-admin applying the second estate on a cluster is
refused, which is the arrangement this store exists to make unnecessary: bind
each estate to a Role in its own records namespace.

Two estates given the same `namespace` are refused the same way, naming the
other estate. A records namespace is whatever the block says it is, so the
other estates are not found by the `tofu-records-` prefix: every namespace this
identity can see is reviewed, and a readable one that is not named like a
records namespace is settled by looking for record Secrets in it. An identity
that cannot list namespaces cannot ask any of this, and that is NOT CHECKED
rather than a pass.

### What a plan job needs

`get` and `list` on Secrets in the records namespace, and nothing else.
Measured on kind: such an identity plans to `No changes.` and no record
Secret's `resourceVersion` moves. Claim 29 step 9 is that measurement.

A plan does send one write. The provisioning sentinel (issue #693) is written
with a conditional create on every open, and for a plan identity the API server
refuses it. That refusal is carried past when the sentinel is already there,
because an earlier writing run proved the store's write, read and list paths
and nothing about this run being unable to repeat the proof makes the store
less sound. So the order matters: an estate has to be applied once under an
identity that may write before a plan-only identity can plan it. Until then
the plan is refused by name, because a store with no sentinel and an identity
that cannot provision one reads exactly like an empty estate.

Because a plan identity never creates the sentinel, its runs are never a first
contact, and the four assertions above never run on one. `choudoufu
live-cluster -plan-identity` is how that identity asks them on purpose.

## The container

`record_store "azurerm"` keeps the same records as blobs in one Azure
Storage container, laid out under the bucket's three prefixes, each ending
in `/`. It takes stock's `azurerm` backend arguments and its `ARM_*`
environment variables, and authenticates through stock's own code.

A create is Put Blob with `If-None-Match: *`, and an update or a delete
carries `If-Match` with the ETag the writer read. Nothing is leased. The
estate goes on every blob as the blob index tag `tofu-estate`, in the same
request (`x-ms-tags`). A blob index tag value allows fewer characters than
an AWS tag (letters, digits, space and `+ - . / : = _`, with no `@`), so a
record whose escaped address holds an `@` is written with `tofu-estate`
alone, never with an altered address.

The storage account has to have blob versioning on, a lifecycle rule that
deletes previous versions and never current ones, and anonymous access
disallowed. Those are read through Azure Resource Manager, which needs an
Azure AD credential and `resource_group_name`.
[`examples/record-store-azurerm/CONTRACT.md`](../examples/record-store-azurerm/CONTRACT.md)
has the three settings, their waivers, and the read fence:
[`live/azure/record-store-abac.condition`](azure/record-store-abac.condition),
a role assignment condition on the blob index tag. It binds Azure AD
principals only, so an account key or a SAS token goes around it.

## The GCS bucket

`record_store "gcs"` keeps the same records as objects in a Google Cloud
Storage bucket. Writes, layout and settings follow the S3 bucket. Who can see
whose keys follows [the cluster](#the-cluster), and so does the default: one
bucket per estate.

```hcl
record_store "gcs" {
  bucket = "prod-records"
}
```

The connection arguments are stock's `gcs` backend's, with stock's
precedence: `access_token`, then `credentials`, then the `GOOGLE_*`
variables, then Application Default Credentials, with
`impersonate_service_account` and its delegates over whichever applies.
`storage_custom_endpoint`, `universe_domain`, `encryption_key` (a
customer-supplied key) and `kms_encryption_key` are stock's too. Where in the
bucket the records go is `key_prefix`, as on every store. Stock's `prefix` is
refused by name.

### Layout and writes

The prefixes are the bucket's: `tofu-records/<estate>/`,
`tofu-hints/<estate>/` and `tofu-outputs/<estate>/`, each with its trailing
`/`.

A version is the object's generation. A create sends `ifGenerationMatch=0`,
and an update or a delete sends the generation the writer read. GCS answers a
failed precondition with `412`, which the store reports as a version
conflict. Nothing is locked.

Every object carries `tofu-estate`, and a record also `tofu-address`, as
custom metadata on the write. GCS IAM cannot condition on object metadata, so
the tags are provenance here.

The sentinel is proved through the listing, and the bulk read is one listing
and a read per object, complete or failed.

A Cloud KMS key that refuses the run is a refusal, by name, on every command,
as on S3. It is never read as an identity that may read and not write. GCS
uses the key as the project's Cloud Storage service agent, so the fix is
usually a grant to that agent rather than to the estate's identity.

### One bucket per estate

GCS checks `storage.objects.list` against the bucket, not against an object
name. No IAM condition narrows a listing to one estate's prefix, so anything
that may list its own keys lists every key in the bucket. That is the
cluster's position, where RBAC cannot condition on a label, and the answer is
the same: give each estate its own bucket.

Several estates may still share one. What they share is the listing: each
estate's identity sees the others' keys, which are type names and encoded
addresses. The records' contents stay behind each estate's grant. This is the
gap claim 28 states for GCP. On S3, `s3:prefix` fences the listing and one
bucket serves many estates; on GCS, nothing does.

`list_isolation` says so on every run that writes a record. It finds other
estates by their sentinels, wherever their `key_prefix` put them, and warns
naming them. It never refuses. `allow_insecure = ["list_isolation"]` accepts
the shared listing and silences it.

### What the store checks about the bucket

Five things, when the S3 bucket's three are checked: an estate's first
contact, every run that writes a record, and `choudoufu live-bucket` in the
estate's directory.

| Assertion | What it asks | Read with |
|---|---|---|
| `object_versioning` | Object Versioning is enabled | `storage.buckets.get` |
| `noncurrent_lifecycle` | a `Delete` rule removes noncurrent versions under every prefix, and none can match a live object | `storage.buckets.get` |
| `public_access_prevention` | it is `enforced` | `storage.buckets.get` |
| `uniform_bucket_level_access` | it is enabled; IAM conditions need it | `storage.buckets.get` |
| `list_isolation` | no other estate's keys are in the bucket | `storage.objects.list` |

A rule that deletes live objects cannot be waived. A denied
`storage.buckets.get` refuses like a failure; a denied listing is
`not_checked`.
[examples/record-store-gcs/CONTRACT.md](../examples/record-store-gcs/CONTRACT.md)
has what counts.

### The grant

An IAM condition on a bucket can match the object name, so an estate's
identity can be held to its own prefixes for reads and writes:

```
resource.name.startsWith("projects/_/buckets/<bucket>/objects/tofu-records/<estate>/")
```

`examples/record-store-gcs/iam/render-grant.sh <estate> <bucket> <member>`
prints two bindings: `roles/storage.objectUser` conditioned on the estate's
three prefixes, and `roles/storage.legacyBucketReader` on the bucket for the
settings read and the listing. Conditions need uniform bucket-level access
and IAM policy version 3. In a bucket of its own the condition changes
nothing an estate can reach; it matters in a shared one. Reading another
estate's outputs is a binding on that estate's `tofu-outputs/` prefix, which
the script does not render.

### What has not been measured

Nothing in this section has run against real GCS. The store and its checks
are tested against a fake of the JSON API
(`internal/live/staterecord/gcs_test.go`), and reference-gcp's
`record_store_gcs` gauntlet stage runs the store against floci-gcp's Cloud
Storage, an emulator and not GCS. On the emulator it reaches part of the
first item below (a create-only write's `412` when two applies race) and the
fourth (the sentinel listing); floci-gcp does not answer a missing bucket the
way GCS does (lex00/floci-gcp#32), so the second cannot be measured there.
These are ahead of measurement against GCS:

- that GCS answers a stale `ifGenerationMatch` on a write and a delete with
  `412`, and an update of a deleted object with `412` or `404`;
- that a missing bucket's `404` says "bucket does not exist", which is how
  the store tells it from a missing record;
- that a Cloud KMS refusal's message names Cloud KMS, which is how the store
  recognises one;
- that `matchGlob` finds every sentinel in the bucket;
- that the grant allows every request a run makes, refuses another estate's
  objects, and still answers `404` rather than `403` for a record that does
  not exist.

## The cache

Every live-block run keeps an ordinary state file as a cache:
`choudoufu-cache.tfstate`, under the `.terraform` directory stock already
gitignores. It is written at the end of each run and read at the start of the
next, and it is never consulted for ownership. Identity lives on the
resources, and the cache only remembers attributes.

The cache is a file on the machine that ran the plan: not in the record
store, seen by no other machine, shared between no two people or CI jobs.
The backend is the markers and the record store. Two runs never write the
same cache, so it has no bearing on concurrency. A fresh checkout, a new
laptop or a thrown-away CI runner plans correctly with no cache at all, and
restoring one from a CI cache buys nothing on a default plan.

Ruled 2026-09-26: this is by ruling, not accident. The cache stays
local, disposable, per working copy, never consulted for ownership. A
shared cache is not wanted; if it ever is, that is a separate feature with
its own name, not a setting here.

The file is written unencrypted and nothing scrubs it: every sensitive
attribute and root output is in it, in clear. Treat an applied working
directory like one holding `terraform.tfstate`, especially in CI, where an
artifact step sweeping up `.terraform` carries the values along. An estate
that sets `strict { secrets = "refuse" }` gets no cache at all, written or
read, unless `CHOUDOUFU_STATE_CACHE` names a path on purpose.
[SECRETS.md](SECRETS.md#the-cache-file-holds-them-too) has the rest.

On a default plan the cache buys nothing, on purpose: the read pass is drift
detection, and no cache freshness excuses skipping it. It pays out on the one
opt-in path, `-refresh=false`, where a vouched-for instance is served from
the cache with no wire read. Vouching means the marker was verified by this
run's sweep, or the record store attests ownership while this run's own
listing proves the instance exists.
[Claim 3](smoke/claims/staleness-costs-reads.md) measures the saving.

### Knobs

`CHOUDOUFU_STATE_CACHE` names a different path for the file, or the
literal `off` disables it. `reads = "selective" | "full"` in the live
block (or `CHOUDOUFU_READS` per run) governs whether `-refresh=false`
may serve from it. Neither knob changes what a default plan does.

## Receipts

A receipt records whether an external effect ran, and with what input. It is
an ordinary resource you declare, holding a hash, so it goes through plan and
apply and its diff tells a reviewer that this apply triggers something
outside the resources being managed.

choudoufu lints receipts and does not write them. A receipt stays out of the
record store on purpose: its job is to be readable with a plain cloud CLI by someone with read-only
access and no `choudoufu` binary, and a record is tool-internal JSON in a store few people may read. A
`key_prefix` starting with `tofu-receipts` is a configuration error, and
`terraform_data`'s `triggers_replace` is not a substitute.
[RECEIPTS.md](RECEIPTS.md) has
the pattern and the lint rules. An SSM parameter at
`/tofu-receipts/<estate>/<effect>` is one supported home for a receipt, and
nothing requires it.
