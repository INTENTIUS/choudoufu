---
title: "Claim 29: A record store bucket that cannot keep its records is refused by name before anything is applied"
weight: 29
claim: a-wrong-bucket-is-refused
---

# Claim 29: A record store bucket that cannot keep its records is refused by name before anything is applied

A record in the bucket can be the only copy of what it says. A
record-backed resource carries no marker and cannot be imported under a
live block, so the bucket's own settings are its recovery path, and three
of them are asserted: versioning, a lifecycle rule that expires
noncurrent versions, and public-access block. The number of days in that
lifecycle rule is how long a record destroyed by mistake can still be
brought back.

Encryption at rest is not asserted. S3 has encrypted every new object by
default since January 2023, so the check would pass on every bucket that
exists and measure nothing.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke a-wrong-bucket-is-refused

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke a-wrong-bucket-is-refused and report the "caught"
line: it runs a refusal arm with nothing corrupted, and the check for a
refusal must find none.
```

As the run prints them:

1. `a correct bucket, and an apply that goes through` - with all three
   settings in place the apply is not interrupted.
2. `five ways to be wrong` - versioning suspended, no lifecycle
   configuration, a lifecycle that only transitions storage classes, no
   public-access block, and a lifecycle that has the right rule and also
   a rule that expires current objects. Each apply fails, its headline
   names what is wrong, and the record store holds exactly the object
   versions it held before. Until #1377 the check passed the fifth
   bucket, and that arm fails against a binary from before the fix.
3. `what not asking on every plan costs` - the assertions run before an
   apply changes anything, and on an estate's first contact with the
   bucket. They do not run on an ordinary plan, which would be three
   requests and three permissions spent on almost every run for nothing.
   The cost is shown here: a plan against a bucket whose versioning was
   suspended goes through without a word, and the operator hears it at
   apply time.
4. `first contact` - a brand-new estate's very first plan is refused
   against the same bucket, twice running, and leaves nothing under its
   prefix. The store sentinel is how a run knows it is first, so a
   refusal takes the sentinel back out; otherwise the second plan would
   pass the bucket the first one refused.
5. `teardown`.

The check needs `s3:GetBucketVersioning`, `s3:GetLifecycleConfiguration`
and `s3:GetBucketPublicAccessBlock`. A role that is denied one of them
gets the same refusal a wrong setting gets, worded as unreadable: a
bucket nobody could check is not a bucket that passed.

A lifecycle rule filtered by tag or object size does not count, and a
prefix-filtered rule counts only when the estate's records, hint and
outputs all sit under its prefix.
