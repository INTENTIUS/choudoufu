---
title: "Secrets in the record store"
weight: 9
---

# Secrets in the record store

**Anyone who holds `s3:GetObject` on an estate's prefix can read every value
that estate recorded, secrets included, in clear.** S3 decrypts on read for
any caller IAM allows, so encryption at rest does not change this. With a
customer managed key the reader also needs `kms:Decrypt` on the key, and that
is the only configuration in which a second thing stands in the way.

That is the default, and it is the same bargain a state file makes. This page
is about who ends up holding that read, and the two ways out of it.

## What is recorded

Only record-backed resources have records: the ones with no cloud twin to
carry a marker, such as `random_password`, `random_pet`, `time_static`,
`tls_private_key`, `null_resource` and `terraform_data`. An estate made only
of ordinary taggable cloud resources records nothing secret, because it
records no values at all.

For the ones that are recorded, the default is `strict { secrets = "store" }`,
which keeps what a stock `terraform.tfstate` keeps: a `random_password`'s
result, a `tls_private_key`'s key material and the rest, as the provider
returned them. The record also carries the provider's `private` blob and the
list of which attributes were sensitive.

Root output values are recorded too, under `tofu-outputs/<estate>/`, with one
exception that matters here: **an output marked `sensitive` is never
written.** It renders as `(sensitive value)` in a plan the way it always did.

## Who holds the read

| Who | Why they can read it |
|---|---|
| The estate's own role | It has to. [The published policy]({{< relref "/docs/use/iam" >}}) scopes it to the estate's prefixes and denies it objects tagged as another estate's |
| Anyone with `s3:GetObject` on `tofu-records/<estate>/*` | A broad `s3:GetObject` on the bucket or the account, a read-only audit role, a leaked read-only credential |
| Anyone who can assume either | Including CI, for the pipeline's role |
| Whoever recovers a deleted record | Recovery reads the record |

The middle row is the one to look for. A read-only role is usually thought of
as harmless, and on this bucket it reads private keys. The public-access block
the store [asserts]({{< relref "/docs/use/bucket-contract" >}}) stops the
bucket being published. It does nothing about a principal inside the account.

Noncurrent versions are readable the same way, to a caller with
`s3:GetObjectVersion`, for as long as the lifecycle rule keeps them. A secret
that was rotated is still in the bucket until then.

## The two ways out

**`strict { secrets = "refuse" }`, available today.** Resource types that
generate or hold secret material are refused at lint time and never recorded,
so nothing the run keeps holds key material. This is stronger than any
encryption of the store, because there is nothing in the store to decrypt.
The cost is that those types cannot be in the estate: generate the secret
somewhere that is built to hold one, and pass a reference.
[Reference]({{< relref "/docs/use/reference" >}}) covers the setting and the
environment pin that stops a configuration relaxing it.

**Secret values in SSM, planned and not built.** The design is for records to
stay in the bucket while `sensitive_attributes` and `private` alone go to
Parameter Store as `SecureString` under a KMS key, the record carrying a
reference. [#1244](https://github.com/INTENTIUS/choudoufu/issues/1244)
section 3 has it. No code implements it, no configuration selects it, and a
page that tells you to turn it on is wrong. It would mean depending on two
services, which is a cost to weigh when it exists.

This is separate from Parameter Store as a *record store*, which is retired:
`record_store "ssm"` is refused. What is planned keeps records in the bucket.

## What a customer managed key adds

A second gate, and a revocation that does not go through IAM.
[Encryption at rest]({{< relref "/docs/use/encryption" >}}) has the
arrangement. It narrows the middle row of the table above to principals the
key policy also names. It does not help against the estate's own role being
misused, since that role has to hold both.

## The local store

`record_store "local"`, and the implied store when none is declared, write the
same values to `.tofu-records` beside the module, files `0600` inside
directories `0700`. Nothing gitignores that directory for you. A commit
publishes it to everyone with the repository.
