# Where things are stored

| What | Where it lives | Who writes it | Losing it costs |
|---|---|---|---|
| Ownership markers | On the resource: two tags on AWS, one label on Kubernetes | The apply | The resource goes invisible and the next plan proposes a duplicate |
| Records | The record store, one record per managed instance | choudoufu | For most resources, a slower or noisier plan. For a record-backed one, the resource |
| Receipts | Ordinary resources you declare | You | Nothing structural. It is your data, in your configuration |
| The cache | `.terraform/choudoufu-cache.tfstate`, on the machine that ran | choudoufu | A read |

Only the markers say what you own. `live/MARKERS.md` is their spec, and the
surface other tooling can rely on. [Records](https://intentius.io/choudoufu/docs/model/values/)
says what a record is and why there are two kinds, and
[the cache](https://intentius.io/choudoufu/docs/model/cache/) has its own page. This page is
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
| `s3` | A bucket you already own | `bucket` (required), `key_prefix`, `region`, `allow_insecure` |
| `kubernetes` | Secrets in a namespace you already own | `namespace` (required), and the connection arguments of stock's `kubernetes` backend |

A `live` block that declares no `record_store` gets the local one, the way
stock implies a local state file.

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
[secrets](https://intentius.io/choudoufu/docs/use/secrets/). Gitignore the directory, or
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
([claim 35](smoke/claims/one-bucket-many-estates.md),
[claim 36](smoke/claims/objects-carry-the-estate-tag.md)).
Nothing is found by tag: objects are found by listing a known prefix.

### Requests

| When | What is sent |
|---|---|
| Opening the store, every run | A conditional `PutObject` of the sentinel, which writes only the first time and is skipped by a role that cannot write. After the first run it is answered `412`, and one `GetObject` of the sentinel follows. Then one `ListObjectsV2` |
| Reading the estate, every run | `ceil(N/1000)` `ListObjectsV2`, then a `GetObject` per key including the sentinel, eight in flight unless `TOFU_LIVE_RECORD_READ_PARALLELISM` says otherwise. The read is complete or the run fails ([claim 31](smoke/claims/a-bulk-read-is-complete-or-it-fails.md)) |
| The hint and the outputs, every run | One `GetObject` each |
| An apply, per record that changed | A `GetObject`, then a conditional `PutObject` or `DeleteObject` |

A create is `If-None-Match: *`, and an update or a delete carries `If-Match`
with the version the writer read. Nothing is locked.
[Two runs at once](https://intentius.io/choudoufu/docs/model/concurrency/) has the races.
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
records is the namespace. Give each estate its own, and bind the estate's
role to Secrets in that namespace alone. Writes carry the estate label, so
[the admission policy](https://intentius.io/choudoufu/kubernetes/gate/) fences them the way
it fences every other object of the estate.

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
[Receipts](https://intentius.io/choudoufu/docs/use/record-an-effect/) and `live/RECEIPTS.md` have
the pattern and the lint rules. An SSM parameter at
`/tofu-receipts/<estate>/<effect>` is one supported home for a receipt, and
nothing requires it.
