---
title: "Secrets in the record store"
weight: 9
---

# Secrets in the record store

Anyone who can read an estate's records can read every value that estate
recorded, secrets included, in clear. On a bucket that is anyone holding
`s3:GetObject` on the estate's prefix. On a cluster it is anyone who can
`get` Secrets in the records namespace. On the local store it is anyone who
can read the directory.

Encryption at rest does not change this. S3 decrypts on read for any caller
IAM allows, and the API server does the same for any caller RBAC allows. With
a customer managed key the reader of a bucket also needs `kms:Decrypt` on the
key, and that is the only configuration in which a second thing stands in the
way.

That is the default, and it is the same bargain a state file makes. This page
is about who ends up holding that read, what the settings do and do not
change, and the other place the same values are kept: a cache file on every
machine that applies.

## What is recorded

Every managed instance has a record, and secret material reaches one by two
routes. The default, `strict { secrets = "store" }`, keeps what a stock
`terraform.tfstate` keeps on both.

A record-backed resource is recorded whole. `random_password`,
`tls_private_key` and the rest have no live object to carry a marker, so the
record is the only copy of the value: a generated password, a private key, as
the provider returned them, with the provider's `private` blob and the list
of which attributes were sensitive.

An ordinary resource is recorded in part. Its record holds what a
read of the live resource cannot give back, and that includes arguments the
API never returns. A database's master password is the usual one. So an estate
with no `random_*` or `tls_*` in it can still have secrets in its store.

A write-only argument is never recorded, under either setting.

Root output values are recorded too, except that an output marked
`sensitive` is never written. It renders as `(sensitive value)` in a plan the way it always did.

## Who holds the read

| Who | Why they can read it |
|---|---|
| The estate's own role | It has to. [The published policy]({{< relref "/docs/use/iam" >}}) scopes it to the estate's prefixes and denies it objects tagged as another estate's |
| Anyone with `s3:GetObject` on `tofu-records/<estate>/*`, or `get` on Secrets in the records namespace | A broad read grant on the bucket, the account or the cluster, a read-only audit role, a leaked read-only credential |
| Anyone who can assume either | Including CI, for the pipeline's role |
| Whoever recovers a deleted record | Recovery reads the record |

The middle row is the one to look for. A read-only role is usually thought of
as harmless, and on a record store it reads private keys. The public-access block
the store [asserts]({{< relref "/docs/use/bucket-contract" >}}) stops the
bucket being published. It does nothing about a principal inside the account,
and nothing about a bucket policy that names another specific account, since
a named account is not "public".

Noncurrent versions are readable the same way, to a caller with
`s3:GetObjectVersion`, for as long as the lifecycle rule keeps them. A secret
that was rotated is still in the bucket until then.

## The cache file holds them too

Every apply also writes `.terraform/choudoufu-cache.tfstate` on the machine
that ran it. It is a stock-format state file, written unencrypted, and it
holds what a state file holds: every attribute of every resource, sensitive
ones included, and every root output, sensitive ones included. Neither
`secrets` setting changes that. `refuse` governs what goes into the record
store and has no effect on this file.

So the values this page is about are in two places: the bucket, and the
working directory of each laptop and CI runner that applied. `.terraform` is
gitignored by convention, which keeps the file out of a commit and does
nothing about who can read the disk or what a CI system caches between jobs.
`CHOUDOUFU_STATE_CACHE=off` stops the file being written, at the cost of
[what the cache buys]({{< relref "/docs/model/cache" >}}).

## What `strict { secrets = "refuse" }` does, exactly

It is two refusals.

- Seven resource types whose provider schema marks an attribute sensitive are
  refused at lint and never recorded: `random_password`, `random_bytes`,
  `tls_private_key`, `tls_cert_request`, `tls_self_signed_cert`,
  `tls_locally_signed_cert` and `local_sensitive_file`.
- For an ordinary cloud resource, a sensitive argument the API never returns
  is left out of its record.

It does not mean the run keeps nothing secret. Two things are outside it.

- A record-backed resource that your configuration hands a secret is recorded
  whole. `terraform_data { input = var.db_password }`, or a `null_resource`
  trigger built from a secret, is admitted under `refuse`, and the value goes
  into the record in clear with a note of which paths were sensitive. The
  refusal is by resource type, and these types have no sensitive attribute of
  their own.
- The cache file, above.

What `refuse` costs: the seven types cannot be in the estate, so generate the
secret somewhere built to hold one and pass a reference. And an argument that
is neither returned by the API nor remembered has no prior value, so every
plan shows it as a change.

## Keeping secret values in SSM

Optional, and nothing requires it. Some organisations keep secret material in
SSM because their rotation, audit and access review already live there. For
them the records stay in the record store, and the sensitive attributes and
the provider's private data alone are written to SSM as `SecureString` under
a KMS key, with the record carrying a reference in place of the value.

It means depending on two services, and a secret's write is then conditional
on SSM's terms and not the store's. Weigh that against
`strict { secrets = "refuse" }` with secrets passed in by reference, which
keeps one store. [Reference]({{< relref "/docs/use/reference" >}}) covers the
setting and the environment pin that stops a configuration relaxing it.

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
