---
title: "Set up a record store bucket"
weight: 9
aliases: ["/docs/use/iam/", "/docs/use/bucket-contract/", "/docs/use/encryption/"]
---

# Set up a record store bucket

One bucket serves any number of estates. choudoufu never creates it. You
create it once, and give each estate's role a policy scoped to that estate.

## Create it

```
cd examples/record-store-bucket
npm install
just up
just verify
```

`just up` makes a correct bucket with CloudFormation and prints its name.
`just verify` runs `choudoufu live-bucket -bucket <name>`, which is also how
you check a bucket made any other way.

## The three settings

choudoufu refuses a bucket that fails one of these.

| Setting | Why |
|---|---|
| Versioning is on | A record can be the only copy of what it says. Versioning is the undo for a deleted record |
| A lifecycle rule expires noncurrent versions, and none expires current objects | Without the first the bucket keeps every version forever. The second would delete your records on a timer |
| Public-access block is fully on | Records hold secrets by default |

They are checked on an estate's first run against the bucket, before any run
that writes a record, and whenever you run `live-bucket`. They are not checked
on an ordinary plan. `allow_insecure = ["lifecycle"]` waives a setting by
name, and every run under a waiver says so.

Encryption is not a fourth setting, because S3 encrypts every object by
default and every SSE flavour works. A key of your own adds a second gate on
reads: pass `RECORD_KMS_KEY_ARN` to `just up`.

## The policy for an estate's role

Render it. Do not type it, and do not copy half of it.

```
examples/record-store-bucket/iam/render-policy.sh prod <bucket> --account 111122223333
```

The role can list, read, write and delete under its own three prefixes, must
tag what it writes as its own, and is denied reading or relabelling an object
tagged as another estate's. `--account` pins the bucket's owner, so a bucket
of the same name in someone else's account is not the one the role trusts.
`--kms <key-arn>` adds the key grants.

## A role that plans and never applies

```
render-policy.sh prod <bucket> --read-only
```

For a CI plan job or a reviewer. It is the same policy without the write,
tag and delete grants, which a plan never uses.

## What a recovery needs

Recovering a deleted record removes a delete marker. That takes
`s3:ListBucketVersions` and `s3:DeleteObjectVersion`, and reading an old
version takes `s3:GetObjectVersion`. Give those to the person who recovers
and leave them off the estate's role.
[Recover an estate]({{< relref "/docs/use/recover-an-estate" >}}) has the
steps.

## The details

In the repository, beside the code:
[the policy, statement by statement](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-bucket/iam/README.md),
[the three settings and the waivers](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-bucket/CONTRACT.md), and
[encryption and a key of your own](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-bucket/ENCRYPTION.md).
