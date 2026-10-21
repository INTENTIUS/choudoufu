---
title: Set up a record store bucket
description: Create the shared bucket, check it, and render each estate's role policy.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/bucket/. Run
  `choudoufu live-bucket -bucket <name>` on the bucket I name, render this
  estate's policy with `render-policy.sh`, and show me both. Create nothing.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

On AWS, create one bucket for all your estates. On Kubernetes the records
can live in the cluster instead, as Secrets in one namespace per estate
([operate on Kubernetes](../../../kubernetes/operate/#records)).

```
cd examples/record-store-bucket
npm install
just up
just verify
```

`just up` uses CloudFormation. `just verify` runs
`choudoufu live-bucket -bucket <name>`, which checks any bucket for three
settings:

- versioning on, so a deleted record can be restored
- a lifecycle rule that expires noncurrent versions only
- the public-access block fully on, since records hold secrets

`allow_insecure = ["lifecycle"]` waives a check by name, and every run reports
it. Pass `RECORD_KMS_KEY_ARN` to `just up` for your own KMS key.

## The estate role's policy

```
examples/record-store-bucket/iam/render-policy.sh prod <bucket> --account 111122223333
```

The role reaches only its estate's objects. `--account` pins the bucket owner.
Add `--read-only` for a plan job or a reviewer.

## What a recovery needs

Grant `s3:ListBucketVersions`, `s3:DeleteObjectVersion` and
`s3:GetObjectVersion` to the person who recovers.

[`live/STORAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STORAGE.md)
and the
[example's README](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-bucket/README.md)
have details.
