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
| Micro-state records | A local directory beside the module unless you declare a `record_store` on SSM or S3 | choudoufu only | Churn, since the effect re-runs or its value regenerates |
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

Four things to know first.

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

**Secrets never go here.** `random_password`, `random_bytes` and every `tls_*`
are refused rather than recorded. Their output is key material, and a record
holding a secret would be exactly the thing this design exists to avoid.

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
fine and nothing else needs to read it.

`ssm` when more than one machine runs the estate. No infrastructure to set up,
since Parameter Store already exists in the account.

`s3` to keep records in a bucket you already operate, with your own versioning
and lifecycle rules. You create and configure the bucket. choudoufu only reads
and writes keys in it.
