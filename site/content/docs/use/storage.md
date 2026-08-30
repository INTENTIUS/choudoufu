---
title: "Where things are stored"
weight: 8
---

# Where things are stored

choudoufu writes in three places. Two can both end up as SSM parameters, which
is why they get confused. They do different jobs and have different owners.

| What | Where it lives | Who reads it | Losing it costs |
|---|---|---|---|
| Ownership markers | Two tags on the resource itself | choudoufu, and you, with any cloud tool | The resource goes invisible and the next plan proposes a duplicate |
| Micro-state records | A local directory beside the module unless you declare a `record_store` on SSM or S3 | choudoufu, and anyone with read access to wherever you put it | Churn, since the effect re-runs or its value regenerates |
| Receipts | Ordinary resources *you* declare, by convention SSM parameters | You, your reviewers, your incident responder | Nothing structural. It is your data, in your configuration |

The first is the product. The second is plumbing that is there by default and
that you point somewhere else when a team needs to share it. The third you
write yourself, and choudoufu only lints it.

## Ownership markers

Two tags, `tofu-estate` and `tofu-address`, written onto each resource as it is
created. Every plan rebuilds prior state by reading them back off the live
system.

Only these are authoritative about *what you own*, and they live on your
resources in your account rather than anywhere choudoufu keeps.
`live/MARKERS.md` is the normative spec and the surface external tooling can
rely on.

Nothing else on this page is required. An estate of ordinary cloud resources
needs markers and nothing more.

## The record store

Some resources have no cloud twin. Nothing in AWS knows a `null_resource` ran
a script, a `time_static` captured a timestamp, or a `random_pet` generated a
name, so no marker can recover them.

Those persist as **micro-state**, one small record each. Every estate has a
store for them: a `live` block that names no `record_store` gets a local one,
a `.tofu-records` directory beside the module, the way stock OpenTofu implies
a local state file. Nothing to turn on.

Gitignore that directory before the first apply. Nothing generates the line for
you, and [what the store may contain](#what-the-store-may-contain-and-who-can-read-it)
below is why it matters.

```
# .gitignore
.tofu-records/
```

This repository carries exactly that line for its own runs, at `.gitignore`.
The store creates its directories `0700` and its files `0600`, which keeps
other users on the machine out and does nothing whatsoever about `git add`.

Declare a `record_store` when you want the records somewhere a team shares,
or somewhere that survives the working copy.

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "ssm" {}
```

The same block goes inside `live` for the in-`terraform` form. The label picks
the backend.

| Backend | Where it writes | Arguments |
|---|---|---|
| `local` | A directory beside the module, `.tofu-records` by default | `path` |
| `ssm` | SSM Parameter Store, under a prefix derived from the estate name | `key_prefix`, `region` |
| `s3` | An S3 bucket you already own | `bucket` (required), `key_prefix`, `region` |

Three things to know first, and then the one that decides where the store
should live.

**You are not meant to read it.** The payload is a self-describing ctyjson
envelope for this fork's own code. Not an operator-facing artifact, and its
format is not a contract.

**Writes are conditional.** A record is written only if it still carries the
version the writer read. A losing writer gets a named failure, not a blocking
wait and not a silent overwrite.

**Losing a record is churn, not a lost estate.** The effect re-runs or its
value regenerates, and anything reading it plans as a change. It cannot cost
you a resource, because identity arguments must be statically evaluable, so a
record-backed value can never name one.

### What the store may contain, and who can read it

Read this before picking a backend. It is the thing that decides who ends up
able to read your estate's generated values.

**The record store may hold any value the state file would have held,
including secrets, unless you set `strict { secrets = "refuse" }`.** The
default is `strict { secrets = "store" }`, which keeps what a stock state file
keeps, so `random_password`, `random_bytes` and the `tls_*` types are admitted
and their generated values are recorded in clear.

That much is the ordinary bargain of a state-bearing tool. A
`terraform.tfstate` has always held the same values in the same form, and
nothing here makes the exposure larger. What is different is where the store
sits and who already holds a key to that place.

| Backend | Where the value lands | Who can read it |
|---|---|---|
| `local` | A file under `.tofu-records`, mode `0600` inside a `0700` directory | Anyone who can read the working copy. Nothing gitignores it for you, so a commit publishes it to everyone with the repository |
| `ssm` | A Parameter Store parameter, `Type: String`, no KMS key | Anyone holding `ssm:GetParameter` on the path. The payload is base64-encoded, which is an encoding rather than a protection, and no decryption step stands in the way |
| `s3` | An object in your bucket, written with no `ServerSideEncryption` argument, so the bucket's own default encryption is what applies | Anyone holding `s3:GetObject` on the prefix |

`local` puts the values in a working copy that is yours to protect, and the
protection is a `.gitignore` line. `ssm` and `s3` put them in a live AWS
account, under that account's access controls rather than yours, and
[`choudoufu destroy` leaves records behind]({{< relref "/docs/use/setup#nothing-cleans-the-record-store-up" >}}),
so the residue outlives the estate that wrote it.

`strict { secrets = "refuse" }` is the other setting, and it is the principle
this design exists for: those types are refused rather than recorded, so
nothing the run keeps holds key material. It is a stronger answer than
encrypting the store, because there is nothing in the store to decrypt. The
[`strict` block]({{< relref "/docs/use/reference" >}}) covers both settings and
the environment pin that stops a configuration relaxing this on its own.

## Receipts

A receipt records whether an external effect ran, and with what input.

It is not choudoufu storage. It is an ordinary resource you declare, by
convention an SSM parameter at `/tofu-receipts/<estate>/<effect>` holding a
hash. It goes through the ordinary plan and apply cycle, and its diff appearing
in a plan tells a reviewer or a CI gate that this apply will trigger something
outside the resources being managed.

choudoufu does not write receipts. It lints them, enforcing that the value is a
hash or constant and never a `SecureString`, that nothing references a
receipt's attributes, and that inputs name secrets by pointer rather than by
value.

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

`terraform_data` is for the graph, ordering an apply, feeding
`replace_triggered_by`, or standing in for a resource that does nothing.
Receipts are for external effects. Keep them apart.

## Choosing a record store backend

`local` for a single operator or a demo, where a directory beside the module is
fine and nothing else needs to read it. Gitignore `.tofu-records/`.

`ssm` when more than one machine runs the estate. No infrastructure to set up,
since Parameter Store already exists in the account. Scope `ssm:GetParameter`
on the prefix the way you would scope read access to a state file.

`s3` to keep records in a bucket you already operate, with your own versioning
and lifecycle rules. You create and configure the bucket. choudoufu only reads
and writes keys in it. It has to exist before the first plan, not the first
apply, and it cannot be a bucket the same estate declares. Its default
encryption and its bucket policy are the ones that apply, since the write sets
neither.

The `ssm` store writes `Type: String` parameters and does not choose a KMS key.
That default is deliberate and the reasoning is written down, along with what
`SecureString` would buy and cost, in
[the record-store parameter type ruling](https://github.com/INTENTIUS/choudoufu/blob/main/rfc/20260830-ssm-record-parameter-type-ruling.md).

[What you set up by hand]({{< relref "/docs/use/setup" >}}) has the failure
mode for each backend, and what a `destroy` leaves behind in the store.
