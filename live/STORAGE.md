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
| `kubernetes` | Secrets in a namespace you already own | `namespace` (defaults to `tofu-records-<estate>`), `key_prefix`, and the connection arguments of stock's `kubernetes` backend |

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

Anyone who can `get secrets` in the records namespace reads every recorded
value, the same bargain `s3:GetObject` on the bucket makes.

### What the store checks about the cluster

Four things, asked once on an estate's first contact with the store and again
before every apply, never on an ordinary plan. They are the cluster's version
of the bucket's three settings.

| Assertion | What it asks | Asked with |
| --- | --- | --- |
| `namespace_access` | the records namespace is there, and this identity may do to Secrets in it what this run will ask | one `SelfSubjectAccessReview` per verb, never an attempted write |
| `read_isolation` | no other estate's records are readable, in another namespace or in this one | a cluster-wide review, then one per other namespace it can see, and a list of this namespace's records by their `managed-by` label |
| `encryption_at_rest` | the API server runs with `--encryption-provider-config` | the API server's own static Pod, where that Pod is visible |
| `estate_boundary` | `estate-boundary.yaml`'s policy and its binding are installed, observed, denying and in force over the record Secrets, and this identity is granted its estate | a get on each, compared against the shipped file, and one review of `use` on `estates.choudoufu.intentius.io/<estate>` |

`choudoufu live-cluster` asks the same four and prints them, with no plan and
nothing written. Run in a configuration directory it uses that live block's
namespace; `-namespace=<name>` checks any other. It exits non-zero unless all
four hold, and `-plan-identity` asks what a plan job's identity needs rather
than what an apply needs.

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

`allow_insecure` takes these four names the way it takes the bucket's three.
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
Secret's `resourceVersion` moves. Claim 39 step 9 is that measurement.

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
