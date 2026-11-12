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

That is the default, and it is the same bargain a state file makes. Whoever
can read the store can read the secrets in it, so pick who that is before you
pick a backend. This page
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

### A Secret Manager secret version

A `google_secret_manager_secret_version` is an ordinary resource too, and
its `secret_data` is in the same position as a Kubernetes Secret's `data`:
the provider's read accesses the version and returns the payload, so it is
not what a read cannot give back and does not go into the record. The value
is in Secret Manager and in the cache file on the machine that applied. Its
write-only form, `secret_data_wo`, is never recorded and never cached. This
has not been measured on GCP the way the Kubernetes record above was.

## Who holds the read

| Who | Why they can read it |
|---|---|
| The estate's own role | It has to. [The published policy](https://intentius.io/choudoufu/docs/use/bucket/) scopes it to the estate's prefixes and denies it objects tagged as another estate's |
| Anyone with `s3:GetObject` on `tofu-records/<estate>/*`, `get` on Secrets in the records namespace, or blob read on the Azure records container | A broad read grant on the bucket, the account or the cluster, a read-only audit role, a leaked read-only credential |
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

On Azure the same row is blob read on the records container, which
Storage Blob Data Reader and every broader data role grant. The
[ABAC condition](azure/record-store-abac.condition) narrows an Azure AD
principal's read to blobs whose index tag names its own estate. It binds
nothing else: an account key, a SAS token, or anyone holding
`Microsoft.Storage/storageAccounts/listKeys/action` on the account reads
every estate's records. The fence holds only with `use_azuread_auth =
true` and shared-key access off on the account
([`examples/record-store-azurerm/CONTRACT.md`](../examples/record-store-azurerm/CONTRACT.md),
"The read fence"). Previous blob versions are readable the same way until
the lifecycle rule deletes them.

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
[the way out to stock](https://intentius.io/choudoufu/how-it-works/cache/) open for such an
estate.

So under `store` the values this page is about are in two places: the bucket,
and the working directory of each laptop and CI runner that applied. `.terraform` is
gitignored by convention, which keeps the file out of a commit and does
nothing about who can read the disk or what a CI system caches between jobs.
`CHOUDOUFU_STATE_CACHE=off` stops the file being written, at the cost of
[what the cache buys](STORAGE.md#the-cache).

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

`strict { secrets = "ssm" }` keeps the records in the S3 record store and
moves the secret values out of them, for organisations whose rotation, audit
and access review already live in SSM. Designed in #1515, with its four
rulings made on 2026-09-22, and built in #2393. It is opt-in: the default
stays `"store"`.

What moves is the values at the paths a record's `SensitiveAttrs` names, and
the provider's private data. Each goes to SSM Parameter Store as a
`SecureString` under a customer managed KMS key, and the record carries a
reference in its place. Identity and non-secret residue stay in the record,
so a plan still reads them from one store.

```hcl
strict {
  secrets = "ssm"
  ssm {
    kms_key_id = "arn:aws:kms:eu-west-1:111122223333:key/1234abcd"
    path       = "/choudoufu/prod/secrets"
    # region         = "eu-west-1"
    # allow_insecure = ["parameter_access"]
  }
}
```

`region` defaults to the record store's. The 10,000 standard parameters
are per account and region, so the choice has a capacity consequence.

### Before the first write

Lint refuses four arrangements that could not work
(`live/LIMITATIONS.md`, "strict-secrets-ssm"): the setting with no `ssm`
block, a block with no `kms_key_id`, the setting with a record store other
than `"s3"`, and a block under a secrets setting that would never read it.
A customer managed key is required because SSM's own `alias/aws/ssm` is
usable by every principal in the account holding `ssm:GetParameter`, no
narrower than the read on the bucket the values would be leaving.

The first run that writes then checks what lint cannot see, the way the
bucket contract checks the bucket (`pkg/staterecord/ssmcontract.go`):

| Setting | Asserted |
|---|---|
| `customer_managed_key` | `kms:DescribeKey` on `kms_key_id` reports `KeyManager` `CUSTOMER`, the key enabled, `SYMMETRIC_DEFAULT`, `ENCRYPT_DECRYPT` |
| `parameter_access` | The role creates a `SecureString` probe parameter under `path` with that key, with `Overwrite` false and a name starting `choudoufu-contract-probe-`, reads it back decrypted, and deletes it |

`allow_insecure` in the `ssm` block waives either setting by name and nothing
else. Waiving `customer_managed_key` lets a run proceed when the role may not
call `kms:DescribeKey`, or the key is disabled or asymmetric. Waiving
`parameter_access` lets it proceed when the probe fails, at the cost of
failing at the first secret write, or of superseded values counting against
the quota if the role cannot delete. An AWS managed key is refused whatever
the waiver names: an `alias/aws/` name by its spelling, before any call, and
a key ID or customer alias that `DescribeKey` reports as AWS managed. A
refusal leaves nothing in Parameter Store and nothing in the record store. A
probe the role could create and not delete is named in the refusal, for
deleting by hand.

### The write and the read

Lands with S1 (#2393). Each secret value goes to a new, content-addressed
parameter name with `Overwrite: false`, so every SSM write is a create of a
name no other write uses. The S3 record's `If-Match` write then commits the
reference, and stays the only thing that decides a concurrent write: the
loser's parameter is an orphan nobody references. A plan resolves
references with `GetParameters` in batches of 10, with retry. A superseded
parameter is deleted after the commit, and a sweep removes orphans from lost
races and failed runs, never a parameter a record references.

One parameter per sensitive path. A single value over 4 KB, such as one of
the three PEM encodings a `tls_private_key` holds, goes to the advanced tier,
announced on every run with its cost of $0.05 per parameter-month. If the
planned parameter count exceeds the tier's ceiling the run is refused before
its first write.

No local cache file is written or read under the setting, as under
`refuse`, unless `CHOUDOUFU_STATE_CACHE` names a path.
`CHOUDOUFU_STRICT_PIN=1` refuses the setting, because it keeps secret
material and so relaxes a pin that forces `"refuse"`.

### Who holds the read

A record's reader gets references, not values. A value's reader needs
`ssm:GetParameters` on the path and `kms:Decrypt` on the key, which the key
policy also has to allow. `examples/record-store-bucket/iam/render-policy.sh
--ssm-path <path> --ssm-kms <key-arn>` renders the grants
([the IAM page](../examples/record-store-bucket/iam/README.md#secret-values-in-ssm)):

| Role | Under the path | On the key, through SSM |
|---|---|---|
| Plan (`--read-only`) | `ssm:GetParameters` | `kms:Decrypt` |
| Apply | `ssm:GetParameters`, `ssm:PutParameter`, `ssm:DeleteParameter`, `ssm:GetParametersByPath` | `kms:Decrypt`, `kms:Encrypt`, `kms:GenerateDataKey`, and `kms:DescribeKey` directly |

Receipts are unaffected: `receipt-value` keeps refusing a `SecureString`.

[Reference](https://intentius.io/choudoufu/docs/use/reference/) covers the
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
