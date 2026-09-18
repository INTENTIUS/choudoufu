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
for you, and [what the store may contain](#what-the-store-may-contain-and-who-can-read-it)
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
| `ssm` | SSM Parameter Store, under a prefix derived from the estate name | `key_prefix`, `region`, `tier` |
| `s3` | An S3 bucket you already own | `bucket` (required), `key_prefix`, `region` |

Three things to know first, and then the one that decides where the store
should live.

**You are not meant to read it**: the payload is a self-describing ctyjson
envelope for this fork's own code. Not an operator-facing artifact, and its
format is not a contract.

**Writes are conditional.** A record is written only if it still carries the
version the writer read. A losing writer gets a named failure rather than a
blocking wait or a silent overwrite.

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

### What the store may contain, and who can read it

Read this before picking a backend. It is the thing that decides who ends up
able to read your estate's generated values.

**The record store may hold any value the state file would have held,
including secrets, unless you set `strict { secrets = "refuse" }`.** The
default is `strict { secrets = "store" }`, which keeps what a stock state file
keeps: `random_password`, `random_bytes` and the `tls_*` types are admitted
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
account, under that account's access controls rather than yours, and the
residue outlives the estate that wrote it, which is the next section.

### How many records a backend can hold

`local` and `s3` have no practical ceiling: a local store is bounded by the
filesystem, and an S3 bucket has no object-count limit.

`ssm` does, and it is a hard one. SSM Parameter Store allows **10,000 standard
parameters per account per region** - Service Quotas `L-C3B871CB`, listed
`Adjustable: False`, so a support request cannot raise it. One record is one
parameter, and the ceiling counts every parameter in the account and region,
including ones choudoufu never wrote. An estate of ten thousand resources does
not fit, and no amount of retrying changes that.

A plan checks this before it writes anything. An estate with more records than
the tier allows is refused by name, with the count, the ceiling and the tier in
the message. An estate above nine tenths of the ceiling gets a warning instead,
because whether it fits depends on what else is already in the account, and the
plan does not spend a full `DescribeParameters` sweep finding that out.

The `tier` argument is how you raise it.

```hcl
record_store "ssm" {
  tier = "intelligent_tiering"
}
```

| `tier` | Records | Value size | Cost |
|---|---|---|---|
| unset (the default) | Whatever the account's own default-tier configuration allows, 10,000 unless it has been changed | 4KB | None |
| `"standard"` | 10,000 | 4KB | None |
| `"advanced"` | 100,000 | 8KB | Billed per parameter per month |
| `"intelligent_tiering"` | 100,000 | 8KB where needed | Billed only for the parameters past 10,000 |

Unset is not the same as `"standard"`. It sends no tier at all, so the
account's own default-tier setting decides - which is what every run before
this argument existed did. Setting `"standard"` pins the tier against an
account default of advanced or intelligent tiering.

Two things to know before choosing `"advanced"`: it bills every parameter,
including the first one, and an advanced parameter cannot be reverted to a
standard one, because the revert would truncate an 8KB value to 4KB.
`"intelligent_tiering"` reaches the same ceiling and charges only for what
crosses 10,000, which is usually the one you want.

An estate too large for even the advanced tier has no SSM answer. Use `s3`.

### Nothing cleans the store up

`choudoufu destroy` destroys the resources and leaves records behind. A
two-resource estate destroyed down to nothing left its guided hint and one
resource's record still in the store.

On `local` that is a directory to delete. On `ssm` and `s3` it is residue in a
live account, and removing it is yours to do.

`strict { secrets = "refuse" }` is the other setting, and it is the principle
this design exists for: those types are refused rather than recorded, so
nothing the run keeps holds key material. That is a stronger answer than
encrypting the store, because there is nothing in the store to decrypt. The
[`strict` block]({{< relref "/docs/use/reference" >}}) covers both settings and
the environment pin that stops a configuration relaxing this on its own.

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

**`s3` is how an estate is meant to be run**, and the argument is consistency
rather than capacity. An S3 record write is a real compare-and-swap the server
enforces, through `If-Match` and `If-None-Match` on the object's ETag, which is
what makes "a losing writer gets a named failure" true for every write rather
than only for the first one.

You create and configure the bucket. choudoufu only reads and writes keys in
it. It has to exist before the first plan, not the first apply, and it cannot
be a bucket the same estate declares. Its default encryption and its bucket
policy are the ones that apply, since the write sets neither. Keep versioning
on: it is what turns a deleted record store from an incident into an undo, and
[Recover an estate]({{< relref "/docs/use/recover-an-estate" >}}) explains what
the alternative costs.

`local` for a single operator or a demo, where a directory beside the module is
fine and nothing else needs to read it. Gitignore `.tofu-records/`.

**`ssm` is on its way out as a record store.** It still works and is still
documented here, and [#1244](https://github.com/INTENTIUS/choudoufu/issues/1244)
carries the decision and the migration question for estates already on it. The
reason is worth stating plainly, because it is not the one people expect.

Parameter Store's 10,000-parameter standard quota is **account-wide and shared
with everything else in the account** - your application configuration, your
pipelines, anything else that writes a parameter - and it is listed
`Adjustable: False`. So the failure is not "a large estate runs out". An
account already near the limit cannot host even a small estate, on day one.

And choudoufu cannot see that coming. The capacity check compares the estate's
record count against the full quota, as though the whole budget belonged to
this tool. A 500-record estate going into an account that already holds 9,800
parameters clears the refusal, clears the warning band, and then fails partway
through with a half-written store against live resources. Reading the real
remaining budget would cost a full `DescribeParameters` sweep on every plan and
be stale the moment it returned. A store whose capacity belongs to the customer
and cannot be measured is the wrong place for the record of what an estate owns.

Keeping *secrets* in Parameter Store is a different question from keeping
*records* there, and retiring the second does not retire the first. §3 of
[#1244](https://github.com/INTENTIUS/choudoufu/issues/1244) proposes records in
S3 with `sensitive_attributes` and `private` alone written to SSM as
`SecureString` under a KMS key, the S3 record carrying a reference rather than
the value. That is a proposal. Nothing implements it today.

The `ssm` store writes `Type: String` parameters and does not choose a KMS key.
That default is deliberate and the reasoning is written down, along with what
`SecureString` would buy and cost, in
[the record-store parameter type ruling](https://github.com/INTENTIUS/choudoufu/issues/600).

[What you set up by hand]({{< relref "/docs/use/setup" >}}) has what each
backend needs to exist before the first plan, and the failure mode when it
does not.
