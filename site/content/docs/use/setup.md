---
title: "What you set up by hand"
weight: 2
---

# What you set up by hand

An evaluation asks this early: before any of this works, what has to exist
that choudoufu will not create?

The short answer is credentials, a region, and - on one of the three record
store backends - a bucket. Everything else is a line of configuration, a tag
write you run on purpose, or something the first run creates for you.

Every row below was stood up from an empty directory against the pinned
emulator rather than read off the source, and each failure mode is the text
that actually printed. [How this was checked](#how-this-was-checked) says what
that covers and, more usefully, what it does not.

## The short answer

| Piece | What it is | Who creates it |
|---|---|---|
| Credentials and a region | The ordinary AWS SDK chain | You, before the first plan |
| Provider configuration | A `provider "aws"` block, or nothing | Optional |
| Estate declaration | `estate.chdf.hcl`, or `live { estate = "..." }` | A config edit |
| Record store, `local` | `.tofu-records` beside the module | The first apply |
| Record store, `ssm` | Parameter Store paths | The first apply. Nothing pre-created |
| Record store, `s3` | A bucket you already own | **You, before the first plan** |
| Markers on resources choudoufu creates | Two tags, stamped on create | The apply |
| Markers on resources that already exist | Two tags | A tag write you run |
| IAM policy | Provider permissions plus this fork's own | **You** |

Two of those rows are genuinely out of band on every path: credentials and
IAM. A third, the `s3` bucket, applies only if you choose that backend.

## Before the first plan

### Credentials and region

choudoufu configures the AWS provider the way any OpenTofu run does, through
the SDK's own chain: environment variables, then `~/.aws/config` and
`~/.aws/credentials`, then instance metadata. Nothing about a `live` block
changes where credentials come from.

One consequence is worth stating out loud, because it bit while this page was
being written. A configuration with no `provider "aws"` block still runs:
`choudoufu init` resolves `hashicorp/aws` from the resource type prefix, and
the provider then takes its region and credentials from whatever the ambient
environment supplies. A plan typed straight out of
[Start a new estate]({{< relref "/docs/use/start" >}}) therefore goes wherever
your default profile points, and a marker-mode plan reads far more of an
account than a stock plan does (see
[what the first plan reads](#what-the-first-plan-reads-on-an-account-that-is-not-empty)).
Set `AWS_PROFILE` deliberately, or configure the provider at the account you
mean, before the first plan rather than after.

When something is missing, the failure arrives under a live-markers heading
rather than a provider one, because discovery needs a configured provider
before the plan graph is walked.

| Missing | What prints |
|---|---|
| Region | `Error: Provider unavailable for marker discovery` … `cannot configure provider …: invalid AWS Region: .` |
| Credentials | `Error: Provider unavailable for marker discovery` … `cannot configure provider …: No valid credential sources found` |

### A pinned provider version

Not required, but the first plan says something about it if you skip it.
Admission evidence - which types are accepted, what their import IDs look
like, how identity resolves - is measured against one provider version.
Resolving a different one prints:

```
Warning: Provider version does not match the admission evidence version
```

The version it was measured against is the `provider_version` field in
`live/survey.json`. Pinning `required_providers` to that version silences the
warning; anything else is a caution rather than a refusal, and the plan
continues.

## The estate declaration

One file or one block turns marker mode on.

```hcl
# estate.chdf.hcl
estate = "my-estate"
```

Until this exists, the binary behaves as stock OpenTofu: no discovery pass, no
markers stamped, no record directory created. That is verifiable rather than
promised - a configuration with no declaration plans and applies through the
ordinary state-file path and leaves nothing behind.

Two refusals guard the edges, both at `init`, before any command runs.

| Mistake | What prints |
|---|---|
| A `backend` or `cloud` block alongside it | `Error: Both a backend and a live configuration are present`, at the offending block's own line |
| Both the sidecar and a `live` block | `Error: Both a live sidecar file and a live block are present`, naming both |

[Start a new estate]({{< relref "/docs/use/start" >}}) covers the two forms and
why the sidecar is the leading one.

### Deleting the state file is not enforced

[Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) tells you to
keep the state file until the migration is done, and makes deleting it an
optional last step. Nothing checks that you did either one.

A leftover `terraform.tfstate` is ignored rather than refused. A plan run
beside one proposes creating every resource the file names, exactly as if the
file were not there. Prior state now comes from markers, and the markers are
not on those resources yet. The file's presence is not the
hazard. Believing it still counts for something is.

That harmlessness is why the ordering is safe to get right: **`choudoufu
live-import` reads that state file**, and it is the command's only input. If
your estate uses `count` or `for_each`, that command is the path you want, and
deleting the state file first throws its input away. Run `live-import` before
the deletion.

## The record store

Every estate has one, and declaring no `record_store` gets you a local one.
[Where things are stored]({{< relref "/docs/use/storage" >}}) covers what it
holds and how to choose a backend. This page answers only the setup question:
what has to exist before the first plan.

| Backend | What must exist first | Created by |
|---|---|---|
| `local` | Nothing | The first apply, as `.tofu-records` beside the module |
| `ssm` | **Nothing** | The first apply, as Parameter Store paths |
| `s3` | The bucket | You |

### `ssm` needs no bootstrap, and that is the point

Stock Terraform's S3 backend has the chicken-and-egg every team hits once: the
bucket that holds state has to exist before the backend can create anything,
so it gets its own bootstrap configuration with its own state, and that
configuration's state has to live somewhere too.

`record_store "ssm" {}` has no equivalent step. Parameter Store is ambient in
every AWS account, with nothing to provision, so the first apply writes its
parameters into an account that has never used the service.

Stood up against an account with zero parameters, an apply of three resources
produced four parameters and needed no preparation of any kind:

```
/tofu-records/<estate>/tofu-hints/<estate>/guided
/tofu-records/<estate>/tofu-records/<estate>/null_resource/<encoded address>
/tofu-records/<estate>/tofu-records/<estate>/random_pet/<encoded address>
/tofu-records/<estate>/tofu-records/<estate>/aws_vpc/<encoded address>
```

No path had to be created, and no KMS key or parameter tier was chosen. The
parameters come back as `Type: String` with a null `KeyId`.

Two bounds on that, since the emulator is not a quota authority. Real
Parameter Store applies a default per-account parameter limit and a Standard
tier value ceiling, and an estate large enough to reach either would meet a
real bootstrap decision that this run cannot show you. Neither was exercised.

### `s3` is the one backend with a prerequisite

You create and configure the bucket. choudoufu reads and writes keys in it and
never creates it.

If the bucket is absent, the **plan** fails, not just the apply:

```
Error: Cannot list the record store

Listing the record store to find untaggable resources whose configuration
block was removed failed: staterecord: s3: listing "tofu-records/<estate>":
operation error S3: ListObjectsV2, https response error StatusCode: 404,
… NoSuchBucket: The specified bucket does not exist..
```

Failing at plan time is the good outcome. Nothing partial happens first.

### The `s3` bootstrap cycle is refused, but not diagnosed

Declaring the record store's own bucket inside the estate that uses it does
not work, and stock's chicken-and-egg comes back in full: the plan aborts
before it can propose creating the bucket, so the estate can never stand up
its own store.

The failure is loud but unexplained: the error is the same `NoSuchBucket`
text as a typo'd bucket name, with nothing naming the cycle. If you see that
error and the bucket is one your own configuration declares, this is why.

Create the bucket outside the estate, the way a stock bootstrap configuration
would, or use `ssm`, which has no such cycle.

### The store holds secrets by default

One thing to know before choosing a backend, because it decides who ends up
able to read your estate's generated values: the default is `strict { secrets
= "store" }`, which keeps what a stock state file keeps, in clear.
[Where things are stored]({{< relref "/docs/use/storage#what-the-store-may-contain-and-who-can-read-it" >}})
has the per-backend version of who can read it, and what `strict { secrets =
"refuse" }` changes.

## Markers are a command, not configuration

For a resource choudoufu creates, there is no step: the create stamps
`tofu-estate` and `tofu-address` inline, as part of the same API call, and no
separate tagging permission comes into it.

For a resource that already exists, adoption is a tag write you run on
purpose. `choudoufu plan` prints an `Adoptable` section with the command
already built, carrying the region and endpoint the plan itself used:

```
aws_vpc.solo <- aws_vpc vpc-12909d4c
    matched on: cidr_block=10.70.0.0/16
    adopt with: aws ec2 create-tags --resources 'vpc-12909d4c' --tags …
```

That covers what the plan can recognise. It does not cover a `count` or
`for_each` instance, which content matching never offers, and it is not the
path to reach for on an estate that still has its state file.
[Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) has the whole
loop and the blind spot demonstrated, and it covers `choudoufu live-import`.
That command takes its addresses from the state file and so pays nothing for
expansion.

## Permissions

The actions are catalogued in
[Reference]({{< relref "/docs/use/reference#permissions-a-run-needs" >}}),
per stage and per record store backend. Two things about them are easier to
measure than to read.

**A plan writes nothing.** Every AWS call a plan made against a two-resource
estate with an `ssm` record store was a read - not one create, put, delete or
tag action appeared, on the record store or anywhere else. A prospect can run
a plan against a real account with a read-only role and see the real answer
before granting any write permission. The record store is included in that:
against a Parameter Store with zero parameters, a first plan created none.

**A plan reads widely.** The estate-wide sweep is what finds resources whose
configuration block was deleted, and its width comes from the admission
table rather than from the size of your estate. That same two-resource plan
issued one `tag:GetResources` and 435 `cloudformation:ListResources` calls.
A read-only role scoped to the services your estate declares will not cover
it, and the sweep degrades to warnings rather than failing when it cannot
list a type.

For the marker write itself, a create needs no separate permission, since the
tags ride the create call. Adopting an existing resource needs that service's
own tagging action - `ec2:CreateTags` for the EC2 family, and the
[per-service verb]({{< relref "/docs/use/reference#marker-stamping" >}}) for
everything else.

## What the first plan reads on an account that is not empty

Worth setting expectations on, because the first plan is where an evaluation
forms its impression.

The estate-wide sweep walks every admitted type, not only the ones you
declared. Against a fresh emulator account holding nothing but AWS-managed
defaults, a two-resource estate's first plan scanned 761 types. The output
ran to 1105 lines and carried 43 warnings. `Plan: 2 to add` sat at line 601
with several hundred lines of sweep reporting after it.

Most of that volume is the emulator's, not your account's: a type the sweep
cannot list produces a warning, and the emulator does not implement many of
the services it is asked about. A real account will not reproduce the count.
It will reproduce the shape - an `Incomplete sweep` warning for each type the
sweep could not read, and a `Not swept for removal` section listing them.

Those warnings are part of the answer, not noise. An empty removal list is a
statement about the types that were swept and about nothing else, which is
what the section says. The plan is deliberate about not claiming more than it
measured.

If your account already holds resources this configuration should manage, read
[Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) before
applying anything. Nothing binds a live resource to your configuration until
its markers are on it, so applying against unmarked resources creates a second
copy beside them.

## How this was checked

Every claim above came from standing estates up from empty directories against
`ghcr.io/lex00/floci`, the emulator pinned in `live/floci-image`, with the AWS
provider pinned to `live/survey.json`'s measured version. The account was a
freshly started container each time, holding what an AWS account holds before
anyone touches it: one default VPC with its three subnets, one default
security group and one route table.

What that does not cover, and where the real answer has to come from
elsewhere:

- **IAM enforcement** went untested. The emulator authorizes everything, so
  what was measured is which calls each stage makes, not which denial an
  under-scoped policy produces. The permission tables in
  [Reference]({{< relref "/docs/use/reference#permissions-a-run-needs" >}}) are
  the authority on the actions; nothing here tested a policy that refuses one.
- **Parameter Store quotas and tiers** went unexercised. No estate here
  approached the per-account parameter limit or the Standard tier value
  ceiling.
- **Scale** stayed small. These estates were two to five resources.
  Migration cost is linear in resources stamped;
  [Migrate an existing estate]({{< relref "/docs/use/migrate#moving-a-large-estate-in-one-go" >}})
  carries the measured rate and its bounds.
- **The `s3` backend's own durability settings** were not exercised.
  Versioning and lifecycle rules on the record bucket are yours to configure.
- **A real account** settles anything the emulator answers differently from
  AWS.
  [`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md)
  names the questions an emulator-backed run cannot answer at any scale.

## Next

- The [Start a new estate]({{< relref "/docs/use/start" >}}) page covers an
  estate with nothing in it yet.
- The [Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) page
  covers an account that already holds the resources.
- The refusals can be found before anything stands up with
  [How to check a configuration before migrating]({{< relref "/docs/use/check-a-config" >}}).
- The per-backend trade-offs behind the record store choice are in
  [Where things are stored]({{< relref "/docs/use/storage" >}}).
