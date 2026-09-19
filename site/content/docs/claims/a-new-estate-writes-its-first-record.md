---
title: "Claim 34: Under the published IAM policy a new estate's first write succeeds, and so does every write after it"
weight: 34
claim: a-new-estate-writes-its-first-record
---

# Claim 34: Under the published IAM policy a new estate's first write succeeds, and so does every write after it

**This claim runs against real AWS and is maintainer-run.** The pinned
emulator does not evaluate `s3:ExistingObjectTag` or
`s3:RequestObjectTag`, so there a policy that works and a policy that
locks an estate out look the same.

An estate's role may write only objects tagged as that estate's. The
obvious way to say so is a condition on `s3:ExistingObjectTag`, and it is
wrong. That key reads the tags an object already has, and an object that
does not exist yet has none. A policy written that way reviews correctly
and denies the first write into every new estate. The condition on a
create is `s3:RequestObjectTag`.

```text
Clone https://github.com/INTENTIUS/choudoufu. You need AWS credentials
for an account you may create an S3 bucket and IAM roles in, the AWS CLI
and jq. From the repo root run:

  SMOKE_REAL_AWS=1 just smoke a-new-estate-writes-its-first-record

It removes the bucket and the roles it made. Explain each step's verdict
line to me as it prints. Then run it again with BREAK=1 and report the
"caught" line.
```

Without `SMOKE_REAL_AWS=1` the scenario refuses to start.

As the run prints them:

1. A control comes first. A role that is allowed nothing in S3 is
   denied, so an "allowed" later on is the policy's doing.
2. `a brand-new estate, an empty prefix, and the published policy` - the
   policy is the output of `render-policy.sh`, unedited. The estate
   applies as its role into a prefix that holds nothing, and every object
   it writes carries `tofu-estate`.
3. `and every write after the first` - an update, a replan from the
   records alone and a destroy, all as the role, with every count
   checked. This step matters as much as the first. A policy can let a
   create through and still break the estate, because every update and
   delete is conditional and AWS authorizes a conditional write as a read
   as well.

IAM is eventually consistent, so a policy just written is not yet the
policy a request is judged under. Every policy the scenario installs
carries one extra statement granting a marker object nothing else ever
granted, and the role is polled until it can read the marker. Only then
is the policy tested.

The `BREAK=1` run changes one key in the published policy, from
`s3:RequestObjectTag` to `s3:ExistingObjectTag`. The estate's first
create is `AccessDenied` on `s3:PutObject` and nothing is written.
