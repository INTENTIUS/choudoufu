# The record store bucket

A live estate that declares `record_store "s3"` keeps its records in a
bucket. This project stands that bucket up, tells you whether it is
correct, and takes it down again.

```
just up        # create or update the bucket; prints RECORD_STORE_BUCKET
just verify    # is this bucket correct? asks the choudoufu binary
just down      # tear it down; refuses while any record version is present
```

Then point an estate at it:

```hcl
terraform {
  live {
    estate = "prod"

    record_store "s3" {
      bucket = "choudoufu-records-111122223333-us-east-2"
    }
  }
}
```

One bucket serves every estate. Each estate writes under its own
prefixes (`tofu-records/<estate>/`, `tofu-hints/<estate>/`,
`tofu-outputs/<estate>/`), so adding an estate is a configuration
change and not another `just up`.

## What a correct bucket is

This section is the specification. The project is one way to meet it,
and a bucket built with your own tooling is just as correct if it
satisfies the same three things. choudoufu checks them itself, before an
apply changes anything and on an estate's first contact with the bucket,
and refuses a bucket that fails one.

**Versioning is enabled.** A record can be the only copy of what it
says. A record-backed resource carries no marker and cannot be imported
under a live block, so in an unversioned bucket an overwrite or a delete
is final. Suspended does not count.

**A lifecycle rule expires noncurrent versions.** Versioning is on and
every apply writes records, so without this the bucket keeps every
version of every record forever. The rule must be enabled, must set
`NoncurrentVersionExpiration` to at least one day, and must reach every
key an estate writes: no filter at all, or a prefix filter that all
three of the estate's prefixes sit under. A rule filtered by tag or
object size does not count, and neither does a rule that only
transitions storage classes or only aborts multipart uploads. It must
never expire current objects: a record is not a log.

The number of days is the recovery window. A deleted record is a delete
marker over a noncurrent version, and an overwritten one leaves its
predecessor behind as a noncurrent version, and both last exactly that
long. For the record-backed slice it is the only answer to "how long do
we have to notice that a record was destroyed by mistake". Pick it
deliberately.

**Public-access block is on, all four settings.** Records hold secret
material, protected by the bucket's encryption at rest and by IAM.

Encryption at rest is not on this list. S3 has encrypted every new
object by default since January 2023, so checking for it would pass on
every bucket that exists. choudoufu works under every SSE flavour
(SSE-S3, SSE-KMS with the AWS-managed key, SSE-KMS with a customer
managed key, DSSE-KMS) and asserts none. A customer managed key is the
recommendation, because it is the one flavour where you write the key
policy and can cut access by revoking the key.

To check any bucket, from anywhere:

```
choudoufu live-bucket -bucket=<name> -region=<region>
```

It needs `s3:GetBucketVersioning`, `s3:GetLifecycleConfiguration` and
`s3:GetBucketPublicAccessBlock`. A setting the caller may not read is
reported `UNREADABLE`, which is not a pass.

## `just verify` reports the bucket, not a configuration

`verify` calls `choudoufu live-bucket`. Nothing in this project checks
the three settings a second time, so the project and the tool cannot
come to disagree about what a correct bucket is.

An estate can waive an assertion (`allow_insecure = ["versioning"]`),
and its plans and applies then proceed with a warning on every run. That
changes whether the estate's runs proceed. It does not change what
`verify` says: a bucket with versioning off is reported `NOT correct`
whoever has waived it. Run `choudoufu live-bucket` with no options inside
an estate's directory and it also names that estate's waivers and says
whether each is hiding a real failure.

## Parameters

| variable | default | what it is |
|---|---|---|
| bucket name (argument, or `RECORD_BUCKET`) | `choudoufu-records-<account>-<region>` | Globally unique, so derived from the account rather than guessed. Any name works. |
| `RECORD_NONCURRENT_DAYS` | `30` | The recovery window, above. |
| `RECORD_KMS_KEY_ARN` | unset | A customer managed key **you** own. |
| `AWS_REGION` | `us-east-2` | Where the bucket and its stack live. |

`just up` is safe to run again. When you do not pass
`RECORD_NONCURRENT_DAYS` and the bucket already exists, it builds with
the window the live bucket has now, so a window somebody changed since is
not reset to 30.

### The key is yours

With `RECORD_KMS_KEY_ARN` set, the bucket's default encryption becomes
SSE-KMS under that key with bucket keys on, the bucket policy refuses a
put that asks for a different algorithm or a different key, and `verify`
tests that policy with three writes under `_verify/`.

This project never creates the key. A key minted by `up` would be a key
`down` could delete, and deleting it makes every record in the bucket
unreadable. The key policy has to let the estate's role use it
(`kms:Decrypt`, `kms:GenerateDataKey`), or the estate's first run fails.

Each `Deny` in that policy is also conditioned on the header being
present. The obvious form, `StringNotEquals` on
`s3:x-amz-server-side-encryption` alone, denies the header-less put the
record store actually sends, because the condition key is null when the
header is absent. The first version of this project shipped that policy:
every configuration check passed and the bucket was unusable.

## Access is not granted here

The bucket policy grants nobody anything. An estate's role gets its
access through IAM, and that policy is published once, in the
documentation: a prefix scope plus both object-tag condition keys. A
second copy here would be a second thing to keep correct.

## `just down`

It removes what `up` created and nothing else: the stack, and `verify`'s
own probe objects. It refuses while **any version** of anything under
`tofu-*` exists, current or not, because a deleted record's recovery copy
lives only here. Destroy the estates first, or empty those prefixes
yourself on purpose. If you stored anything else in the bucket,
CloudFormation refuses a non-empty bucket and says so.

## The governed path

`just up` deploys the built template directly. The same template goes
through chant's Ops with an approval gate:

```
just ops          # the Ops resolve and lint
just chant-plan   # read-only
just chant-apply  # stops at its gate; chant approve bucket-apply apply
```

## Where this runs

Real AWS. The pinned floci emulator reports `CREATE_COMPLETE` for this
stack and applies none of the bucket's properties (lex00/floci#207), and
`just verify` is what noticed. The S3 API calls themselves work on floci,
which is how choudoufu's own smoke claims about this bucket run locally.
