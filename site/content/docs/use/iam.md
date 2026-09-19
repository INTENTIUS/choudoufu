---
title: "IAM for the record store bucket"
weight: 9
---

# IAM for the record store bucket

One bucket serves every estate, and each estate's role is scoped to that
estate's own keys. This page is the policy for that role. Do not copy half
of it: each statement is there because leaving it out breaks something
specific, and two of them look removable.

Render it rather than typing it:

```
examples/record-store-bucket/iam/render-policy.sh prod choudoufu-records-111122223333-us-east-2
```

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ListOwnNamespaces",
      "Effect": "Allow",
      "Action": "s3:ListBucket",
      "Resource": "arn:aws:s3:::choudoufu-records-111122223333-us-east-2",
      "Condition": {
        "StringLike": {
          "s3:prefix": [
            "tofu-records/prod/*",
            "tofu-hints/prod/*",
            "tofu-outputs/prod/*"
          ]
        }
      }
    },
    {
      "Sid": "ReadAndDeleteByPrefix",
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:DeleteObject"
      ],
      "Resource": [
        "arn:aws:s3:::choudoufu-records-111122223333-us-east-2/tofu-records/prod/*",
        "arn:aws:s3:::choudoufu-records-111122223333-us-east-2/tofu-hints/prod/*",
        "arn:aws:s3:::choudoufu-records-111122223333-us-east-2/tofu-outputs/prod/*"
      ]
    },
    {
      "Sid": "WriteOnlyObjectsTaggedAsThisEstate",
      "Effect": "Allow",
      "Action": [
        "s3:PutObject",
        "s3:PutObjectTagging"
      ],
      "Resource": [
        "arn:aws:s3:::choudoufu-records-111122223333-us-east-2/tofu-records/prod/*",
        "arn:aws:s3:::choudoufu-records-111122223333-us-east-2/tofu-hints/prod/*",
        "arn:aws:s3:::choudoufu-records-111122223333-us-east-2/tofu-outputs/prod/*"
      ],
      "Condition": {
        "StringEquals": {
          "s3:RequestObjectTag/tofu-estate": "prod"
        }
      }
    },
    {
      "Sid": "DenyReadingAnotherEstatesObjects",
      "Effect": "Deny",
      "Action": [
        "s3:GetObject",
        "s3:GetObjectVersion",
        "s3:GetObjectTagging",
        "s3:GetObjectVersionTagging",
        "s3:GetObjectAcl",
        "s3:GetObjectVersionAcl"
      ],
      "Condition": {
        "StringNotEquals": {
          "s3:ExistingObjectTag/tofu-estate": "prod"
        },
        "Null": {
          "s3:ExistingObjectTag/tofu-estate": "false"
        }
      },
      "Resource": "arn:aws:s3:::choudoufu-records-111122223333-us-east-2/*"
    },
    {
      "Sid": "DenyRelabellingAnotherEstatesObjects",
      "Effect": "Deny",
      "Action": [
        "s3:PutObjectTagging",
        "s3:DeleteObjectTagging",
        "s3:PutObjectVersionTagging",
        "s3:DeleteObjectVersionTagging"
      ],
      "Resource": "arn:aws:s3:::choudoufu-records-111122223333-us-east-2/*",
      "Condition": {
        "StringNotEquals": {
          "s3:ExistingObjectTag/tofu-estate": "prod"
        },
        "Null": {
          "s3:ExistingObjectTag/tofu-estate": "false"
        }
      }
    },
    {
      "Sid": "ReadTheBucketsAssertedSettings",
      "Effect": "Allow",
      "Action": [
        "s3:GetBucketVersioning",
        "s3:GetLifecycleConfiguration",
        "s3:GetBucketPublicAccessBlock"
      ],
      "Resource": "arn:aws:s3:::choudoufu-records-111122223333-us-east-2"
    }
  ]
}
```

The script is the single source. This page shows its output, and a test
fails if the two differ.

## What each statement is for

`ListOwnNamespaces` lets the role list, and the condition key is
`s3:prefix` because it is the only one a LIST has: a LIST touches no
object, so no object tag can condition it. Every prefix ends in a slash.
S3 matches a prefix as a plain string, so `tofu-records/prod` would also
be `tofu-records/prod-eu`.

This statement does a second job that is easy to miss. With it, reading a
key that does not exist answers `404`. Without it, the same read answers
`AccessDenied`. choudoufu reads keys that do not exist yet for every new
resource, so an estate whose role lacks this statement fails on its first
plan with an error that says nothing about listing.

`ReadAndDeleteByPrefix` allows reads and deletes under the estate's three
prefixes, with no tag condition. That is deliberate, and it is the
statement most likely to be "tightened" by someone reading this page.
Conditioning it on `s3:ExistingObjectTag` breaks the estate in two ways.
Both were measured against AWS in
[#1342](https://github.com/INTENTIUS/choudoufu/issues/1342), and no claim
re-runs them. The key does not work on `s3:DeleteObject` at
all, so every delete is denied. And a write that carries `If-Match` is
also authorized as `s3:GetObject`, without the object's tags in the
request, so every conditional update is denied. Every record update and
delete choudoufu makes is conditional. An estate under that policy can
create records and can never change one.

`WriteOnlyObjectsTaggedAsThisEstate` allows a write only when the request
tags the object as this estate's, which is `s3:RequestObjectTag`. It
needs `s3:PutObjectTagging` beside `s3:PutObject`, because a `PutObject`
that carries tags needs both and every write choudoufu makes carries
tags. Do not write this statement with `s3:ExistingObjectTag` instead.
That key reads the tags an object already has, a new object has none, and
the result is a policy that reviews correctly and denies the first write
into every new estate.

`DenyReadingAnotherEstatesObjects` is the tag doing its work. It denies a
read of any object in the bucket whose `tofu-estate` tag is present and
is not this estate's. It is written as a deny with `Null: "false"` so
that it applies only when the tag is there, which is what lets the
conditional writes above through.

`DenyRelabellingAnotherEstatesObjects` is what makes that tag worth
trusting. `s3:RequestObjectTag` on the write statement constrains the tag a
request SENDS and says nothing about the object it lands on, so without this
statement a role that can reach a neighbour's object can call
`PutObjectTagging` on it, relabel it as its own, and read it. Measured against
AWS: with this Deny the relabel and a tag removal are both refused, and every
write the store makes (a tagged create, a tagged update under `If-Match`, a
tagged overwrite of an untagged object) still goes through. Until #1381 the
published policy did not have it.

`ReadTheBucketsAssertedSettings` is what the three bucket assertions
read. A role without it is refused the same way a wrong bucket is, and
the waiver for that is `allow_insecure`, with its cost.

## What this does and does not defend

For reading another estate's objects there are two, and both have to
fail. The prefix scope has to be wrong, and the object has to carry the
wrong tag or none. A role scoped by mistake to `tofu-records/*` still
cannot read a neighbour's records, because they carry the neighbour's
tag, and it cannot change that tag either. An earlier version of this policy
lacked the relabel Deny, and under it this paragraph was false: measured
against AWS, such a role retagged a neighbour's record and then read it.
[Claim 35]({{< relref "/docs/claims/one-bucket-many-estates" >}}) now makes
the attempt.

For listing, writing and deleting there is one: the prefix. S3 has no
condition key for the tags of an object being overwritten or deleted, so
a role scoped by mistake to `tofu-records/*` can overwrite and delete a
neighbour's records. Get the prefix right. The renderer refuses anything
that is not an estate name for that reason.

An object with no `tofu-estate` tag at all is readable by any role whose
prefix reaches it. choudoufu tags every object it writes, so an untagged
object under an estate's prefix was put there by something else.

## What a recovery needs

The rendered policy is for running an estate, and it cannot recover a deleted
record. Recovery removes a delete marker, which takes `s3:ListBucketVersions`
on the bucket and `s3:DeleteObjectVersion` on the estate's prefixes, and
reading a noncurrent version takes `s3:GetObjectVersion`. Give those to the
person who recovers and leave them off the estate's role.
[Recover an estate]({{< relref "/docs/use/recover-an-estate" >}}) has the
procedure.

The same permissions clean up after a first run that was refused. A refusal
on first contact deletes the sentinel it had just written, and in a versioned
bucket that leaves a delete marker behind until the lifecycle rule removes it.

## Pinning the account that owns the bucket

```
render-policy.sh prod <bucket> --account 111122223333
```

adds `"aws:ResourceAccount": "111122223333"` as a `StringEquals` condition
to every `Allow` in the policy, merged into whatever condition that
statement already had. No `Deny` gets it: a `Deny` that stopped applying
once the account was wrong would stop applying in the case it is there
for.

A bucket name is global. Nothing about a name says which account the
bucket is in, and a name nobody holds can be created by anyone. So a
policy that names the bucket only by name grants its estate the right to
read and write a bucket of that name wherever it turns up. If the real
bucket is ever deleted, someone who knows the name can create it in their
own account, admit this role with a bucket policy, turn on the three
settings choudoufu asserts, and take delivery of the next apply. Records
hold secret material.

With the condition, every statement here matches a bucket in that one
account and nothing else. A render without the flag says so on stderr and
still prints the policy, so nothing that already calls the script breaks.

The other half is in the configuration:

```
record_store "s3" {
  bucket       = "my-records-bucket"
  bucket_owner = "111122223333"
}
```

which puts `ExpectedBucketOwner` on every S3 request the run makes, so S3
itself refuses a bucket owned by anyone else. Either half alone leaves a
gap. The policy binds a role; the argument binds a run, including one
whose credentials came from somewhere this policy does not cover.

S3 answers an owner mismatch with `403 AccessDenied`, which is exactly
what an IAM denial looks like, so choudoufu cannot tell you which it was.
It says the bucket may be owned by an account other than the expected
one, names both, and leaves the conclusion to you.

## A bucket encrypted with your own key

```
render-policy.sh prod <bucket> --kms arn:aws:kms:us-east-2:111122223333:key/<key-id>
```

adds `kms:Decrypt` and `kms:GenerateDataKey` on the key. That is half of
it. A customer managed key is usable only by the principals its own key
policy allows, so the key policy has to name the role as well:

```
render-key-statement.sh arn:aws:iam::111122223333:role/prod-estate
```

prints the statement to add to it. The rest of the key policy is yours.
Pass every principal that uses the bucket, including whoever would
recover a deleted record. The script refuses the account root and
wildcards.

A key policy that leaves the role out is the usual reason an estate's
first run against a new bucket fails. S3 reports a KMS refusal as
`AccessDenied` on its own operation, so choudoufu reads the message and
says `The record store bucket's KMS key refused this run`, with the key,
the action, the role, and which policy AWS blamed.
[Claim 37]({{< relref "/docs/claims/the-recommended-secure-configuration" >}})
measures the refusal and its message on real AWS, for an estate with no root
outputs.

## Reading another estate's outputs

No choudoufu run reads another estate's objects. An estate reads a value from
another one off the live resource, with a data source
([Reading a value from another estate]({{< relref "/docs/use/cross-estate" >}})),
and needs nothing extra on the bucket for it. This flag is for a reader you
write yourself, such as a script or a dashboard that shows one estate's
outputs from a role scoped to another. The isolation above denies that read,
and the policy has to grant it back explicitly:

```
render-policy.sh prod <bucket> --reads-outputs-of network
```

That changes four statements, which is why it is a flag and not an edit:
the list prefixes gain `tofu-outputs/network/*`, a statement allows
`s3:GetObject` there, the read Deny steps around that one prefix, and a
second Deny accepts `network`'s tag beside `prod`'s under that prefix and
nowhere else. `network`'s tag used to be accepted across the whole bucket,
which left `network`'s records defended by the prefix alone.

What the grant exposes is everything the other estate wrote under
`tofu-outputs/`: its root output values. An output marked `sensitive` is
never written there, and neither is one whose value is not wholly known, so
neither can be read this way. The other estate's records stay denied.
