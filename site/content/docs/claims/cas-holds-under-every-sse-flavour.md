---
title: "Claim 33: Compare-and-swap holds under every SSE flavour"
weight: 33
claim: cas-holds-under-every-sse-flavour
---

# Claim 33: Compare-and-swap holds under every SSE flavour

**This claim runs against real AWS and is maintainer-run.** An emulator's
object store does not reproduce the ETag semantics that are its subject,
so there is no honest local form of it and it is not in CI.

choudoufu asserts nothing about how a record store bucket is encrypted
at rest, because S3 encrypts every object by default and that check
could not fail ([claim 29]({{< relref "/docs/claims/a-wrong-bucket-is-refused" >}})).
What it owes instead is that every flavour works, whichever the operator
chose: SSE-S3, SSE-KMS under the AWS-managed `aws/s3` key, SSE-KMS under
a customer managed key, and DSSE-KMS.

The reason it should is that the ETag is opaque here. Under SSE-KMS an
ETag stops being the MD5 of the content, but it is still a valid entity
tag for `If-Match`, and the store hands back what S3 gave it, untouched.
That is an argument. This claim is the measurement.

```text
Clone https://github.com/INTENTIUS/choudoufu. You need AWS credentials
for an account you may create S3 buckets and a KMS key in, the AWS CLI,
python3 and Go. From the repo root run:

  SMOKE_REAL_AWS=1 just smoke cas-holds-under-every-sse-flavour

It creates one bucket per flavour and one KMS key, and removes the
buckets at the end. The key is scheduled for deletion, which AWS makes
wait seven days and does not bill for. To reuse a key you already have,
and have it left alone, also set SMOKE_KMS_KEY_ARN.

Explain each step's verdict line to me as it prints. Then run it again
with BREAK=1 and report the "caught" line.
```

Without `SMOKE_REAL_AWS=1` the scenario refuses to start. It never
spends anything by being pasted.

As the run prints them:

1. `one customer managed key, and a bucket per flavour` - each bucket's
   default encryption is set and read back from S3.
2. `the conditional-write suite, against each` - the conformance suite
   every record store backend is held to: `If-None-Match` creates,
   `If-Match` updates and deletes, and a failed precondition becomes a
   version conflict naming both versions. Before it runs, each flavour
   is checked for what it is. The object's `ServerSideEncryption` must be
   that flavour's, so a mislabelled bucket fails instead of measuring
   SSE-S3 several times. And the ETag must equal the payload's MD5 under
   SSE-S3 and must not under any KMS flavour, so the KMS runs are known
   to have exercised an ETag that is not a hash of anything.
3. `an estate's whole life, under each` - choudoufu end to end: create,
   update under `If-Match`, replan empty from the records alone, destroy.
   The count is checked at every step, the destroy included.
4. `teardown`.

The issue that asked for this claim expected a red arm to be hard to
find, and said a claim with no possible red arm is not a claim. There is
one: the only plausible way to break an opaque-ETag design is to stop
treating the ETag as opaque. The `BREAK=1` binary checks, after every
write, that the ETag S3 returned is the MD5 of what it sent, the kind of
sanity check someone adds in good faith. It works under SSE-S3 and fails
under all three KMS flavours, and the control requires each failure to
be that check and nothing else.

Record payloads never reach multipart. The store writes with a single
`PutObject` whatever the size, and multipart upload is something a
caller opts into through a separate API, so the `-N` suffixed ETag of a
multipart object cannot arise here.

A KMS-encrypted bucket adds `kms:GenerateDataKey` to every write and
`kms:Decrypt` to every read, on the key and in the key's own policy.

## What the first run found

The first time the conformance suite met real S3 it failed, under every
flavour, on a case that has nothing to do with encryption. S3 answers a
`PutObject` that carries `If-Match` for a key that does not exist with
`404 NoSuchKey`, not with `412`. The store treated only `412` as a
conflict, so a writer whose record had been deleted underneath it got an
opaque error where every other backend names a version conflict. The
test double answered `412` there, which is why nothing had noticed. Both
are fixed, and the double now answers the way S3 does.
