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
routes. The default is `strict { secrets = "store" }`. On the first route it
keeps what a stock `terraform.tfstate` keeps. On the second it keeps less, and
the two exceptions are below.

A record-backed resource is recorded whole. `random_password`,
`tls_private_key` and the rest have no live object to carry a marker, so the
record is the only copy of the value: a generated password, a private key, as
the provider returned them, with the provider's `private` blob and the list
of which attributes were sensitive.

An ordinary resource is recorded in part. Its record holds what a
read of the live resource cannot give back, and that includes arguments the
API never returns. A database's master password is the usual one. So an estate
with no `random_*` or `tls_*` in it can still have secrets in its store.

Two kinds of sensitive argument stay out of that record under either setting,
because a record is stored unmarked and its sensitivity is put back from the
provider's schema when it is read. An argument that is sensitive only because
a `sensitive = true` variable fed it has no schema mark to put back. A
sensitive argument inside a nested block carries its mark at a path the schema
pass cannot reproduce exactly, so the whole block stays out. Neither value is
in the store. The cost is a plan that keeps proposing that argument or that
block, which is visible.

A write-only argument is never recorded, under either setting.

Root output values are recorded too, except that an output marked
`sensitive` is never written. It renders as `(sensitive value)` in a plan the way it always did.

### A Kubernetes Secret

A `kubernetes_secret` or `kubernetes_secret_v1` is an ordinary resource, and
the part of it that is recorded is none of its data. Measured on a kind
cluster with the local store and the default `secrets = "store"`, applying a
Secret with one `data` key writes this 239-byte record and nothing else:

```json
{
  "format_version": 2,
  "address": "kubernetes_secret.app",
  "kind": "identity",
  "provider": "provider[\"registry.opentofu.org/hashicorp/kubernetes\"]",
  "residue": {
    "attributes": {
      "wait_for_service_account_token": { "attrType": "bool", "attrValue": true }
    }
  }
}
```

The value is not there in clear and not there base64-encoded, and neither is
the `data` key's name. The API server gives a Secret's `data` back on a read,
so none of it is what a read cannot give back, and what is left as residue is
`wait_for_service_account_token`, an argument that only ever existed in the
configuration. Deleting the cache file and planning again against the same
record answers `No changes.`

So the value is in two places, neither of them the record store. It is in the
cluster, in the Secret itself, readable by anyone RBAC lets read that
namespace. And it is in `.terraform/choudoufu-cache.tfstate` on the machine
that applied, in clear, where a plain `grep` for the value finds it. An estate
that generates the value instead, `random_password` feeding
`kubernetes_secret.data`, is the other case: the `random_password` is
record-backed and its record holds the value whole.

## Who holds the read

| Who | Why they can read it |
|---|---|
| The estate's own role | It has to. [The published policy](https://intentius.io/choudoufu/docs/use/bucket/) scopes it to the estate's prefixes and denies it objects tagged as another estate's |
| Anyone with `s3:GetObject` on `tofu-records/<estate>/*`, or `get` on Secrets in the records namespace | A broad read grant on the bucket, the account or the cluster, a read-only audit role, a leaked read-only credential |
| Anyone who can assume either | Including CI, for the pipeline's role |
| Whoever recovers a deleted record | Recovery reads the record |

The middle row is the one to look for. A read-only role is usually thought of
as harmless, and on a record store it reads private keys. The public-access block
the store [asserts](https://intentius.io/choudoufu/docs/use/bucket/) stops the
bucket being published. It does nothing about a principal inside the account,
and nothing about a bucket policy that names another specific account, since
a named account is not "public".

On a cluster the same row is `get secrets` in the records namespace: anyone
holding it reads every value that estate recorded, and `kubectl get secret
tofu-record-<hash> -o jsonpath='{.data.tfstate}' | base64 -d | gunzip` is the
whole of the work. RBAC cannot condition on a label and admission is never
consulted for a get or a list, so nothing narrows that grant below the
namespace. A ClusterRole granting secrets cluster-wide, which a monitoring
agent or an operator's own bundle may already carry, reaches every estate's
records in every namespace. That is why each estate's records get a namespace
of their own and the Role that reaches it names that namespace alone.

Noncurrent versions are readable the same way, to a caller with
`s3:GetObjectVersion`, for as long as the lifecycle rule keeps them. A secret
that was rotated is still in the bucket until then.

## The cache file holds them too

Every apply also writes `.terraform/choudoufu-cache.tfstate` on the machine
that ran it. It is a stock-format state file, written unencrypted, and it
holds what a state file holds: every attribute of every resource, sensitive
ones included, and every root output, sensitive ones included. That is
what it holds under the default setting, `store`.

Under `strict { secrets = "refuse" }` the file is not written and not read,
as if `CHOUDOUFU_STATE_CACHE=off` (#1375). If an earlier run left one in the
data directory, a run under `refuse` warns about it by name and leaves it for
you to delete, since the file is yours. Naming a path in
`CHOUDOUFU_STATE_CACHE` still writes one there under `refuse`: that is you
asking for the file on purpose, and it is what keeps
[the way out to stock](https://intentius.io/choudoufu/docs/model/cache/) open for such an
estate.

So under `store` the values this page is about are in two places: the bucket,
and the working directory of each laptop and CI runner that applied. `.terraform` is
gitignored by convention, which keeps the file out of a commit and does
nothing about who can read the disk or what a CI system caches between jobs.
`CHOUDOUFU_STATE_CACHE=off` stops the file being written, at the cost of
[what the cache buys](https://intentius.io/choudoufu/docs/model/cache/).

## What `strict { secrets = "refuse" }` does, exactly

It is two refusals.

- Seven resource types whose provider schema marks an attribute sensitive are
  refused at lint and never recorded: `random_password`, `random_bytes`,
  `tls_private_key`, `tls_cert_request`, `tls_self_signed_cert`,
  `tls_locally_signed_cert` and `local_sensitive_file`.
- For an ordinary cloud resource, a sensitive argument the API never returns
  is left out of its record.

It also turns the cache file off, above. It does not mean the run keeps
nothing secret. One thing is outside it.

- A record-backed resource that your configuration hands a secret is recorded
  whole. `terraform_data { input = var.db_password }`, or a `null_resource`
  trigger built from a secret, is admitted under `refuse`, and the value goes
  into the record in clear with a note of which paths were sensitive. The
  refusal is by resource type, and these types have no sensitive attribute of
  their own.

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
keeps one store. [Reference](https://intentius.io/choudoufu/docs/use/reference/) covers the
setting and the environment pin that stops a configuration relaxing it.

## What a customer managed key adds

A second gate, and a revocation that does not go through IAM.
[Encryption at rest](https://intentius.io/choudoufu/docs/use/bucket/) has the
arrangement. It narrows the middle row of the table above to principals the
key policy also names. It does not help against the estate's own role being
misused, since that role has to hold both.

## The local store

`record_store "local"`, and the implied store when none is declared, write the
same values to `.tofu-records` beside the module, files `0600` inside
directories `0700`. Nothing gitignores that directory for you. A commit
publishes it to everyone with the repository.
