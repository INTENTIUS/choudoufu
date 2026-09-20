# Encryption at rest for the record store

choudoufu asserts three things about a record store bucket
([the three settings](CONTRACT.md)), and
encryption at rest is not one of them. That is a decision, and this page is
the reasoning.

## Why it is not asserted

S3 encrypts every new object by default, with SSE-S3, whether or not anyone
configured anything. An assertion that "the bucket encrypts at rest" could not
fail against any bucket that exists, and a check that cannot fail is not a
check. It would print a reassuring line and measure nothing.

What differs between buckets is *which* encryption, and choudoufu has no
opinion to enforce there. The store sends no encryption header on a write, so
the bucket's own default is what applies, and the bucket's default is yours.

## Every flavour works

The record store's consistency rests on conditional writes against an object's
ETag, and an ETag is computed differently under different encryption: it is
the MD5 of the content under SSE-S3 and an opaque value under any KMS flavour.
The store treats it as opaque under all of them.
[Claim 33](../../live/smoke/claims/cas-holds-under-every-sse-flavour.md)
runs the store's whole conformance suite against real S3 under each of:

| Flavour | Bucket default |
|---|---|
| SSE-S3 | `AES256` |
| SSE-KMS, AWS managed key | `aws:kms` with no key named |
| SSE-KMS, customer managed key | `aws:kms` with your key |
| DSSE-KMS | `aws:kms:dsse` |

That run is also where one real difference from the emulators turned up: S3
answers a conditional update of a key that no longer exists with `404`, where
an overwrite race gets `412`. The store reports both as the same version
conflict.

## The recommendation: a customer managed key

This is a recommendation, and it is a measured one:
[claim 37](../../live/smoke/claims/the-recommended-secure-configuration.md)
stands the whole arrangement up on real AWS and runs an estate's life in it.

A customer managed key is the one flavour where you write the key policy.
Under SSE-S3 and the AWS managed key, anyone IAM lets read the object can read
it. Under your own key a reader also needs the key, the key policy says who
has it, and revoking it cuts access to every record at once without touching
IAM. Records hold secret material by default
([Secrets](https://intentius.io/choudoufu/docs/use/secrets/)), which is the reason to want
that second gate.

What it takes:

- The bucket's default encryption names your key.
  `examples/record-store-bucket` does that when `RECORD_KMS_KEY_ARN` is set,
  turns bucket keys on so that a plan reading a whole namespace does not make
  one KMS call per object, and adds a bucket policy that refuses a put asking
  for a different algorithm or key. It never creates the key: a key `up` made
  is a key `down` could delete, and deleting it makes every record unreadable.
- The estate's role is allowed `kms:Decrypt` and `kms:GenerateDataKey` on the
  key. `render-policy.sh --kms <key-arn>` adds that statement.
- **The key policy names the role as well.** A customer managed key is usable
  only by the principals its own policy allows, and the role's IAM policy is
  not enough by itself. `render-key-statement.sh <role-arn>...` prints the
  statement to add. Name whoever would recover a deleted record too, since
  recovery reads the record.

Those two KMS actions are the only ones an estate's whole life needed.

### The mistake this produces

A key policy that leaves the estate's role out. Everything else is correct and
the first run fails. S3 reports a KMS refusal as `AccessDenied` on its own
operation, which sends people to the bucket and the role's S3 statements, and
those are fine. choudoufu reads the message and says
`The record store bucket's KMS key refused this run`, with the key, the
action, the role, and which policy AWS blamed: a key policy that does not
allow it, an IAM policy that does not, or an explicit deny. Claim 37's
`BREAK=1` arm is this mistake.

### What a lost key costs

A lost key is a lost bucket, because record-backed resources have no marker
and cannot be imported under a live block, so their records have no recovery
path that does not go through the key. That is the argument against adding
encryption inside choudoufu on top of SSE: a second unrecoverable failure mode
on the one thing with no recovery path. Schedule key deletion with the same
care as deleting the bucket.
