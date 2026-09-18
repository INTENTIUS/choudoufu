# record-store-bucket

The bucket a live estate's `record_store "s3"` writes into, with the hardening
`S3Store` deliberately does not do for you.

```
just plan      # what standing it up would do
just up        # create it, print RECORD_STORE_BUCKET
just verify    # prove the hardening is real, against the live bucket
just down      # tear it down, refusing while records are present
```

Nothing here is a template to fill in. It is a project that runs, and every
claim below is something `just verify` checks against the bucket it made.

## It is declared in a lexicon, not written as HCL

The bucket, its key, its lifecycle rule and its policy are TypeScript in
`src/`, declared through chant's AWS lexicon. `chant build` emits
CloudFormation; `just up` deploys it.

The first version of this example used a stock OpenTofu root with a state file
on disk, and that was circular: you needed a state file to create the bucket
that exists so you would not need state files. Declaring it in a lexicon
removes the circle rather than living with it. CloudFormation holds the
stack's own identity, and nothing on the path needs tofu installed.

It also bought a check for free. The AWS lexicon already lints what this
example was written to demonstrate — WAW042 wants a policy denying non-TLS
requests, WAW006 wants encryption at rest, WAW018 wants the public access
block — so `just ops` runs those rules against this declaration rather than
this README asserting them.

Everything is parameterised. A bucket name is globally unique, so there is no
default correct for two people:

```bash
just up my-own-bucket-name          # any name
RECORD_PREFIX=teamA just up         # namespace inside the bucket
RECORD_ROLE_ARNS=arn:...:role/ci just up   # who may read and write records
just up                             # derives choudoufu-records-<account>-<region>
```

## Two paths, and why both exist

```
just plan / up / verify / down     # deploy the template directly: the quick path
just ops                           # prove the Ops resolve and lint
just chant-plan / chant-apply      # through the Ops: the governed path
```

Both deploy the same template built from the same declaration. The quick path
is for standing a bucket up to try this. The governed path runs it through
chant Ops, and `bucket-apply` stops at an approval gate.

That gate is the reason the Op exists. `gate: "always"`, not the default
`"on-destroy"`, because the usual argument for the default is wrong here in
both directions — a bucket-policy change destroys nothing and can still lock
every run out of its own records, and a KMS key change destroys nothing and can
still make every existing record unreadable. The section below is what that
sentence looks like when it actually happens.

## Why this exists

`S3Store`'s own doc comment is explicit:

> Bucket creation, lifecycle policy, and encryption configuration are the
> caller's concern — S3Store only issues GetObject/PutObject/DeleteObject/
> ListObjectsV2 against a bucket and (optional) key prefix it is given.

That is the right seam for the store: it keeps its surface free of anything
AWS-credential-shaped. It is the wrong place to leave an operator, because
**records can carry secret material.** The envelope has `sensitive_attributes`
and `private` members, and `strict { secrets = "store" }` — the default —
populates them the way stock OpenTofu keeps them in a state file.

So "encryption is the caller's concern" is a real obligation. This discharges
it, and `just verify` is the difference between discharging it and saying you
did.

## The policy trap this example exists to document

The obvious way to write "deny unencrypted puts" is wrong, and it is wrong in
a way that every configuration check passes:

```json
{ "Effect": "Deny", "Action": "s3:PutObject",
  "Condition": { "StringNotEquals": { "s3:x-amz-server-side-encryption": "aws:kms" } } }
```

`s3:x-amz-server-side-encryption` exists as a condition key **only when the
request carries the header.** A client that sends no encryption header —
which is what `S3Store` does, and what most SDK callers do — produces a null
key, `StringNotEquals` against null is true, and the Deny fires on exactly the
well-behaved request that bucket default encryption was about to encrypt
correctly.

This example shipped that policy. It passed every check it had. The live
certification run then failed on its first write:

```
Error: Cannot open the record store
  provisioning the sentinel at "choudoufu/livecert/.../.store-sentinel":
  PutObject ... 403 AccessDenied: not authorized to perform: s3:PutObject
  with an explicit deny in a resource-based policy.
```

The fix is to condition each Deny on the header being *present*, with
`"Null": { "s3:x-amz-server-side-encryption": "false" }`, which narrows it to
what it was always meant to cover: a client asking for the wrong thing. The
header-less request is not left unprotected — it is covered by the bucket's
default encryption, which is the same CMK. Between the two there is no path
that puts a wrongly-encrypted object in this bucket:

| request | what stops it |
| --- | --- |
| no encryption header | bucket default encryption: SSE-KMS under the CMK |
| header, wrong algorithm | `DenyWrongEncryptionAlgorithm` |
| header, wrong key | `DenyWrongKey` |
| plaintext transport | `DenyInsecureTransport` |

`aws:SecureTransport` takes no `Null` clause and should not: S3 sets it on
every request rather than the client, so it is never null and there is no
well-behaved request it can catch by accident.

## What it builds, and why each piece

**A customer-managed KMS key**, rotation on. SSE-S3 would be simpler and is a
legitimate choice; a CMK is used here because the protection is then auditable
and revocable — its own key policy, its own grants, its own CloudTrail
entries, and revoking it makes every record unreadable in one action. To use
SSE-S3 instead, set `SSEAlgorithm: "AES256"`, drop `KMSMasterKeyID`, and drop
the `DenyWrongKey` statement with it.

**Bucket keys enabled.** Records are many and small and every plan reads the
whole namespace; without this the per-object KMS calls cost real money at ten
thousand objects.

**Versioning on**, for a specific reason rather than out of habit. `S3Store`'s
compare-and-swap is ETag-based (`If-Match` / `If-None-Match`), and a record's
delete is how a tombstone gets written. Versioning makes a delete place a
marker rather than destroy history, so a mistaken sweep is recoverable.

**A lifecycle rule expiring old versions at 30 days, never current objects.**
A record is not a log. Expiring a *current* object would delete an estate's
identity for a resource that still exists, and the next plan would propose
creating something already there. Only superseded versions are safe to expire,
and they exist only because versioning is on.

**Block Public Access**, all four switches.

**A bucket policy** — the section above.

**Least-privilege access for the runs**, scoped to the key prefix, if you pass
`RECORD_ROLE_ARNS`. Empty by default, because a first apply should not grant
anything you have not named. This is the bucket-side mirror of what
`aws:ResourceTag/tofu-estate` does for the resources themselves: an estate
reaches its own records and no other estate's.

## `just verify`, and why it writes three times

Four of its checks read configuration. Three write, and those are the only
ones that test the *policy* rather than the default:

- an explicit **`AES256`** `PutObject` must be **refused**
- a **header-less** `PutObject` — the call `S3Store` actually makes — must be
  **accepted**
- and that object must come back **`aws:kms` under the CMK**

They are a set rather than three checks. An earlier version of this file ran
only the first, and it passed against the broken policy above; a check that
proves a Deny fires cannot tell a correct policy from one that refuses
everything. The second catches that, and on its own would pass against a
bucket with no encryption at all, which is what the third is for.

```
  default encryption                         ok (aws:kms)
  versioning                                 ok
  public access blocked                      ok
  noncurrent versions expire                 ok (30d)
  explicit AES256 PUT refused                ok
  header-less PUT accepted                   ok
  header-less PUT landed SSE-KMS             ok (aws:kms)
```

## `just down` refuses while records exist

Destroying this bucket destroys an estate's identity for every resource it
records — and records are not a cache that can be re-derived. They carry
identity for untaggable resources, residue for taggable ones, and the
tombstone path depends on them. So `down` counts what is under the prefix
first and refuses rather than asking.

## What this does not do

It does not create one bucket per estate. One bucket with a key prefix per
estate is the shape the `record_store` block already expects, and the policy
scopes access by prefix. If you want hard isolation between estates —
different keys, different accounts — that is a different topology and this
example is not it.

It does not put secrets in Parameter Store. If your organisation requires
secret material in SSM specifically, that is
[#1244](https://github.com/INTENTIUS/choudoufu/issues/1244)'s third part and
does not exist yet; today the answer is `strict { secrets = "refuse" }`, which
keeps secret material out of the store entirely and refuses loudly rather than
dropping it silently.

## Using it

```bash
just up
# RECORD_STORE_BUCKET=choudoufu-records-<account>-<region>
```

Then in the estate's live block:

```hcl
live {
  estate = "my-estate"
  record_store "s3" {
    bucket     = "choudoufu-records-<account>-<region>"
    key_prefix = "choudoufu/my-estate"
    region     = "us-east-2"
  }
}
```
