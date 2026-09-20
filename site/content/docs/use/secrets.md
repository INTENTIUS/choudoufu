---
title: "Secrets in the record store"
weight: 9
---

# Secrets in the record store

Anyone who can read an estate's records can read every value that estate
recorded, secrets included, in clear. On a bucket that is anyone holding
`s3:GetObject` on the estate's prefix. On a cluster it is anyone who can `get`
Secrets in the records namespace. On the local store it is anyone who can read
the directory. It is the same bargain a state file makes.

Encryption at rest does not change this, because the store decrypts for any
caller that access control allows. A customer managed KMS key is the one
arrangement that puts a second thing in the way, since the reader then also
needs `kms:Decrypt`.

## What is recorded

The default is `strict { secrets = "store" }`.

A record-backed resource is recorded whole: a `random_password` or a
`tls_private_key` has no live object, so the record is the only copy of the
value.

An ordinary resource is recorded in part. Its record holds what a read cannot
give back, and that includes arguments the API never returns, such as a
database's master password. So an estate with no `random_*` in it can still
have secrets in its store.

A `kubernetes_secret` is an ordinary resource and the API returns its `data`,
so its record holds none of it. The value stays in the cluster Secret, and in
the cache file on the machine that applied.

A write-only argument is never recorded. A root output marked `sensitive` is
never written.

## Who holds the read

| Who | Why |
|---|---|
| The estate's own role | It has to |
| Anyone with a broad read on the bucket, the account or the cluster | A read-only audit role reads private keys here |
| Anyone who can assume either | Including CI |
| Whoever recovers a deleted record | Recovery reads the record |

## The cache file holds them too

Under `store`, every apply also writes `.terraform/choudoufu-cache.tfstate`
on the machine that ran it. It is a stock state file, unencrypted, with every
sensitive attribute and output in it. `.terraform` is gitignored by
convention, which does nothing about who can read the disk or what a CI
system caches between jobs. `CHOUDOUFU_STATE_CACHE=off` stops it being
written.

## What `secrets = "refuse"` does

- Seven types that generate secrets are refused outright: `random_password`,
  `random_bytes`, the four `tls_*` types and `local_sensitive_file`.
- A sensitive argument the API never returns is left out of its record, so
  every plan shows it as a change.
- The cache file is not written or read, unless you name a path for it.

One thing is outside it. A record-backed resource that your configuration
hands a secret, `terraform_data { input = var.db_password }` for example, is
recorded whole, because the refusal is by resource type.

With `refuse`, generate secrets somewhere built to hold them and pass a
reference. `CHOUDOUFU_STRICT_PIN=1` in the environment stops a configuration
relaxing the setting.

[`live/SECRETS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/SECRETS.md)
has the full account: the measured record of a `kubernetes_secret`, the
optional arrangement that keeps sensitive values in SSM instead, and the two
kinds of sensitive argument that stay out of a record under either setting.
