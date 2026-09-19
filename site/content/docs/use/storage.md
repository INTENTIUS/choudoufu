---
title: "Where things are stored"
weight: 8
---

# Where things are stored

choudoufu writes in three places. They do different jobs and have different
owners.

| What | Where it lives | Who reads it | Losing it costs |
|---|---|---|---|
| Ownership markers | Two tags on the resource itself | choudoufu, and you, with any cloud tool | The resource goes invisible and the next plan proposes a duplicate |
| Micro-state records | A local directory beside the module, or an S3 bucket you name with `record_store "s3"` | choudoufu, and anyone with read access to wherever you put it | Churn, since the effect re-runs or its value regenerates. For a value other resources are named after, more than churn |
| Receipts | Ordinary resources *you* declare, by convention SSM parameters | You, your reviewers, your incident responder | Nothing structural. It is your data, in your configuration |

The first is the product. The second is plumbing that is there by default and
that you point at a bucket when a team needs to share it. The third you write
yourself, and choudoufu only lints it.

There is a fourth file, the [cache]({{< relref "/docs/model/cache" >}}), and it
is not on this list on purpose. It is on the client, it is never consulted for
ownership, and losing it costs a read.

## Ownership markers

Two tags, `tofu-estate` and `tofu-address`, written onto each resource as it is
created. Prior state is a projection of them, allowed to go stale, and it
is cached: every live-mode run writes `choudoufu-cache.tfstate` under the
data dir (`.terraform`, or `TF_DATA_DIR`), and the next plan serves an
instance from that cache only when the estate sweep has verified its
marker in the same run. The cache is never consulted for ownership, so
losing it - or its being arbitrarily stale - costs one slower run and
nothing else, which a guard proves by requiring a fresh, a stale and a
missing cache to plan byte-identically
([#685](https://github.com/INTENTIUS/choudoufu/issues/685)).
`CHOUDOUFU_STATE_CACHE` overrides the path; the literal value `off`
disables persistence.

Only these are authoritative about *what you own*, and they live on your
resources in your account rather than anywhere choudoufu keeps.
`live/MARKERS.md` is the normative spec and the surface external tooling can
rely on.

An estate of ordinary cloud resources needs markers and nothing more; the
rest of this page is optional.

## The record store

Some resources have no cloud twin. Nothing in AWS knows a `null_resource` ran
a script, a `time_static` captured a timestamp, or a `random_pet` generated a
name, so no marker can recover them.

Those persist as **micro-state**, one small record each. Every estate has a
store for them: a `live` block that names no `record_store` gets a local one,
a `.tofu-records` directory beside the module, the way stock OpenTofu implies
a local state file.

Gitignore that directory before the first apply. No tool generates the line
for you, and [Secrets]({{< relref "/docs/use/secrets" >}}) is why it matters.

```
# .gitignore
.tofu-records/
```

The store creates its directories `0700` and its files `0600`, which keeps
other users on the machine out and does nothing whatsoever about `git add`.

Declare a bucket when you want the records somewhere a team shares, or
somewhere that survives the working copy.

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
| `s3` | An S3 bucket you already own | `bucket` (required), `key_prefix`, `region`, `allow_insecure` |

`record_store "ssm"` was a third and is retired. A configuration that still
declares it is refused with the reasons and this replacement. That is about
Parameter Store as a place for records. Receipts, below, are ordinary SSM
parameters you declare and are untouched.

## The bucket

One bucket serves any number of estates. choudoufu never creates it and never
configures it. [What you set up by hand]({{< relref "/docs/use/setup" >}}) has
the creating, and
[the three settings]({{< relref "/docs/use/bucket-contract" >}}) has what it
must have.

### Layout

An estate writes under three prefixes and nowhere else.

| Prefix | What is there | How many objects |
|---|---|---|
| `tofu-records/<estate>/` | One object per record-backed resource instance, at `<type>/<encoded address>`, plus `.store-sentinel` | The record-backed slice of the estate, which is usually a small fraction of it |
| `tofu-hints/<estate>/` | `guided`, the hint guided discovery uses to look where resources were last found | One |
| `tofu-outputs/<estate>/` | The value each root output settled on at the last apply, so a plan can render a change as a change | One per root output, never a `sensitive` one |

Every prefix ends in `/`, and that character is doing real work. S3 matches a
prefix as a plain string, so `tofu-records/prod` is also a prefix of
`tofu-records/prod-eu/...`. With the delimiter, an estate called `prod` and
one called `prod-eu` share no keys, no listing and no bulk read
([claim 28]({{< relref "/docs/claims/a-name-prefix-shares-no-keys" >}})). The
[IAM policy]({{< relref "/docs/use/iam" >}}) carries the same delimiter, and
for a list, a write and a delete it is the whole defence between estates.

A `key_prefix` override moves the first of the three. It may not begin with
any of the reserved roots, so a record can never land where a hint, an output
or a receipt lives.

An ordinary taggable cloud resource has no object here at all. Its identity is
its two tags.

### What is in an object

A record is a JSON envelope for this fork's own code. It carries up to four
independently optional facts about one instance: the resource's value (its
attributes, the provider's `private` blob, and which attributes were
sensitive), an import identity, argument values a provider's read never gives
back, and whether a create-time provisioner ran. A `kind` field inside the
envelope, and never the key's spelling, decides whether a reader may treat the
object as something it can propose to destroy.

You are not meant to read it, and its format is not a contract. The sentinel
is the exception: its payload is a sentence saying what it is for.

### The tags on every object

Every object is written with the estate's own marker, in the same request as
the object itself, so there is no moment at which it exists untagged.

| Tag | On | Value |
|---|---|---|
| `tofu-estate` | Every object | The estate's name |
| `tofu-address` | Records | The resource instance's address, in the marker form every managed resource carries |

They are for authorization and provenance. The published IAM policy requires
the tag on a write and denies a read of an object tagged as another estate's,
so reaching a neighbour's records takes a wrong prefix *and* a wrong tag
([claim 35]({{< relref "/docs/claims/one-bucket-many-estates" >}})). They are
not how anything is found: objects are found by listing a known prefix, and
the Resource Groups Tagging API does not index S3 objects.
[Claim 36]({{< relref "/docs/claims/objects-carry-the-estate-tag" >}}) reads
every object's tags back and shows the tag is load-bearing.

### How it is read and written

**One listing, then parallel reads.** A run reads its whole records namespace
up front: one paginated `ListObjectsV2`, then a `GetObject` per key, eight at
a time unless `TOFU_LIVE_RECORD_READ_PARALLELISM` says otherwise. The result
is complete or the run fails. A read that errors partway never reaches the
plan as a smaller estate
([claim 31]({{< relref "/docs/claims/a-bulk-read-is-complete-or-it-fails" >}})).
Every read is scoped to one estate, so adding an estate to the bucket slows no
other.

**Every write is conditional.** A create is `PutObject` with
`If-None-Match: *`, and an update or a delete carries `If-Match` with the
version the writer read. A losing writer gets a named conflict and changes
nothing. [Two runs at once]({{< relref "/docs/model/concurrency" >}}) has the
cases. Nothing is locked.

**A store proves itself before a plan trusts it.** At first use it writes
`.store-sentinel` and reads it back through the same listing a plan uses. A
store that cannot answer refuses by name. It never reads as an empty estate,
which would have the next plan propose rebuilding everything.

**Losing a record cannot produce a wrong marker.** An identity-bearing
argument is evaluated over `var`, `local`, `path`, `terraform` and `tofu`
alone, so a record's value is never folded into a marker. Where an identity
cannot be rendered the instance is omitted with a named reason and the plan
proposes a create; nothing is bound to the wrong object.

**It can still cost you more than churn.** A record-backed value may be a
*component* of another resource's identity - `name =
"svc-${random_pet.suffix.id}"` is the ordinary shape - and losing that record
regenerates the pet, so everything named after it is proposed for create under
a name no live object has. [Recover an
estate]({{< relref "/docs/use/recover-an-estate" >}}) has what this looks like
in a plan and what to do about it.

### Deleted records, versions, and what cleans up

A destroyed instance's record is deleted. In a versioned bucket that writes a
delete marker, and the record stays underneath as a noncurrent version until
the bucket's lifecycle rule expires it. That window is the recovery path for a
record destroyed by mistake, and it is the only one a record-backed resource
has. [The three settings]({{< relref "/docs/use/bucket-contract" >}}) covers
choosing it.

`choudoufu destroy` destroys the resources and deletes their records. It does
not remove the sentinel or the hint, and it cannot remove noncurrent versions.
The lifecycle rule takes care of the versions. The rest is a few small objects
under the estate's prefixes, and removing them is yours to do.
`examples/record-store-bucket`'s `just down` refuses to delete a bucket that
still holds any version of a record.

### Who can read it

Anyone with `s3:GetObject` on the prefix, secrets included.
[Secrets]({{< relref "/docs/use/secrets" >}}) starts there.

## Receipts

A receipt records whether an external effect ran, and with what input.

It is not choudoufu storage but an ordinary resource you declare, by
convention an SSM parameter at `/tofu-receipts/<estate>/<effect>` holding a
hash. A receipt goes through the ordinary plan and apply cycle, and its diff
appearing in a plan tells a reviewer or a CI gate that this apply will
trigger something outside the resources being managed.

choudoufu does not write receipts but lints them: the value must be a hash
or constant and never a `SecureString`; nothing may reference a receipt's
attributes; and inputs must name secrets by pointer rather than by value.

`live/RECEIPTS.md` has the pattern and the reasoning behind each guard.

## Why receipts are not record-store entries

Enforced rather than advised. A `key_prefix` whose first segment is
`tofu-receipts` is a configuration error, so a record can never land in the
receipts namespace.

Visibility is why. A receipt is AWS-native so its value stays readable with a
plain `aws ssm get-parameter`, by someone with read-only IAM and no `choudoufu`
binary. A record-store payload is tool-internal by
design. Moving a receipt onto it would trade `aws ssm get-parameter` for
choudoufu's internal JSON envelope, strictly worse for the one artifact whose
job is being legible to someone not running the tool.

**The tempting mistake**, now `terraform_data` is record-backed, is using its
`triggers_replace` as a pseudo-receipt. Do not. It hides the fingerprint in the
tool's own store instead of a declared resource, and collapses a receipt into
"did an input change", with no existence flavour, no hash flavour, and no
naming convention the lint rules recognise.

`terraform_data` is for the graph - ordering an apply, feeding
`replace_triggered_by`, standing in for a resource that does nothing.
Receipts are for external effects. Keep them apart.

## Choosing a record store backend

**A bucket is how an estate is meant to be run.** An S3 record write is a real
compare-and-swap the server enforces, which is what makes "a losing writer
gets a named failure" true for every write. It has no object-count ceiling, it
is shared, and it sits under IAM.

`local` for a single operator or a demo, where a directory beside the module
is fine and nothing else needs to read it. Gitignore `.tofu-records/`. It is
also what a CI runner gets if the estate declares nothing, and there it is
empty on every run: correct, since every instance falls back to its marker
tags, and no use for a record-backed resource.

[What you set up by hand]({{< relref "/docs/use/setup" >}}) has what a bucket
needs to exist before the first plan, and the failure mode when it does not.
