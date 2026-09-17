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

## What it builds, and why each piece

**A customer-managed KMS key**, rotation on. SSE-S3 would be simpler and is a
legitimate choice; a CMK is used here because the protection is then auditable
and revocable — its own key policy, its own grants, its own CloudTrail
entries, and revoking it makes every record unreadable in one action. To use
SSE-S3 instead, set `sse_algorithm = "AES256"` and drop `kms_master_key_id`,
and the `DenyWrongKey` statement with it.

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

**A bucket policy that denies unencrypted `PutObject`** — and denies the wrong
KMS key, and denies insecure transport. This is the part most often missing
from a hand-written setup, and it is the part that matters: default encryption
is a *default*, and a client that asks for something else gets it. The deny is
what makes the guarantee enforced rather than advisory.

**Least-privilege access for the runs**, scoped to the `choudoufu/` prefix, if
you pass `estate_role_arns`. Empty by default, because a first apply should not
grant anything you have not named. This is the bucket-side mirror of what
`aws:ResourceTag/tofu-estate` does for the resources themselves: an estate
reaches its own records and no other estate's.

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

`just up` prints that block filled in, as the `record_store_block` output.

## Why this root is stock, and has a state file

It runs `tofu`, not `choudoufu`, and declares no estate. That is deliberate: it
builds the bucket a record store writes into, and a store cannot hold the
records of the thing that creates it. Bootstrapping is the one place a state
file is the right answer.

## `just verify`, and why it writes

Four of its checks read configuration. Two of them write, and those are the
only ones that test the *policy* rather than the default:

- an **unencrypted** `PutObject` must be refused
- an **encrypted** one must still succeed

The pair is the point. A bucket policy that refused everything would pass the
first check while being completely broken, and a reader looking at one green
line would not know. Both are needed for either to mean anything.

```
  default encryption                         ok (aws:kms)
  versioning                                 ok
  public access blocked                      ok
  noncurrent versions expire                 ok (30d)
  unencrypted PUT refused                    ok
  encrypted PUT accepted                     ok
```

## `just down` refuses while records exist

Destroying this bucket destroys an estate's identity for every resource it
records — and records are not a cache that can be re-derived. They carry
identity for untaggable resources, residue for taggable ones, and the
tombstone path depends on them. So `down` counts what is under `choudoufu/`
first and refuses rather than asking.

## What this does not do

It does not create one bucket per estate. One bucket with a key prefix per
estate is the shape the `record_store` block already expects, and the policy
above scopes access by prefix. If you want hard isolation between estates —
different keys, different accounts — that is a different topology and this
example is not it.

It does not put secrets in Parameter Store. If your organisation requires
secret material in SSM specifically, that is
[#1244](https://github.com/INTENTIUS/choudoufu/issues/1244)'s third part and
does not exist yet; today the answer is `strict { secrets = "refuse" }`, which
keeps secret material out of the store entirely and refuses loudly rather than
dropping it silently.
