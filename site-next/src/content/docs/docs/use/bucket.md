---
title: Set up a record store bucket
description: Create the one bucket many estates share, check its three settings, and render each estate's role policy.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/bucket/. Run
  `choudoufu live-bucket -bucket <name>` on the bucket I name and render the
  policy for this estate with `render-policy.sh`, and show me both. Create
  nothing.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

One bucket serves any number of estates. choudoufu never creates it: you
create it once and give each estate's role a policy scoped to that estate.

## Create it

```
cd examples/record-store-bucket
npm install
just up
just verify
```

`just up` makes the bucket with CloudFormation. `just verify` runs
`choudoufu live-bucket -bucket <name>`, which also checks a bucket made any
other way.

## The three settings

choudoufu refuses a bucket that fails one.

| Setting | Why |
|---|---|
| Versioning on | Versioning is the undo for a deleted record |
| A lifecycle rule expires noncurrent versions, and none expires current ones | Otherwise versions pile up forever, or records vanish on a timer |
| Public-access block fully on | Records hold secrets by default |

`allow_insecure = ["lifecycle"]` waives one by name, and every run says so. S3
encrypts by default; pass `RECORD_KMS_KEY_ARN` to `just up` for a key of your
own.

## The policy for an estate's role

Render it. Do not type it.

```
examples/record-store-bucket/iam/render-policy.sh prod <bucket> --account 111122223333
```

The role reads and writes only its own estate's objects. `--account` pins the
bucket's owner. Add `--read-only` for a CI plan job or a reviewer.

## What a recovery needs

Undeleting a record takes `s3:ListBucketVersions`, `s3:DeleteObjectVersion`
and `s3:GetObjectVersion`. Give those to the person who recovers, not the
estate's role.

[`live/STORAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STORAGE.md)
and the
[bucket example's README](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-bucket/README.md)
have the details.
