---
title: Set up a record store bucket
description: Create the shared bucket, check it, and render each estate's role policy.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/bucket/. Run
  `choudoufu live-bucket -bucket <name>` on the bucket I name, render this
  estate's policy with `render-policy.sh`, and show me both. Create nothing.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

One bucket serves all your AWS estates. On Kubernetes, records can live in the
cluster instead ([operate on Kubernetes](../../../kubernetes/operate/#records)).

```
git clone https://github.com/INTENTIUS/choudoufu.git
cd choudoufu/examples/record-store-bucket
npm install
just up
just verify
```

`just up` creates the bucket with CloudFormation. `just verify` runs
`choudoufu live-bucket -bucket <name>` to check its versioning and access
settings. Pass `RECORD_KMS_KEY_ARN`
to `just up` for your own KMS key.

## The estate role's policy

From the same directory:

```
iam/render-policy.sh prod <bucket> --account 111122223333
```

The role reaches only its estate's objects. Add `--read-only` for a plan job.

## What a recovery needs

Grant `s3:ListBucketVersions`, `s3:DeleteObjectVersion` and
`s3:GetObjectVersion` to the person who recovers
([`live/STORAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STORAGE.md)).
