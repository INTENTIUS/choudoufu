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

Three of the six actions it denies are granted by no statement above:
`s3:GetObjectVersionTagging`, `s3:GetObjectAcl` and
`s3:GetObjectVersionAcl`. They are denied anyway, so that a role somebody
later widens with one of them still cannot use it on another estate's
object. A Deny written for the actions of the day stops covering the
boundary the moment the Allow list grows.

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
[Claim 35](../../../live/smoke/claims/one-bucket-many-estates.md) now makes
the attempt.

For listing, writing and deleting there is one: the prefix. S3 has no
condition key for the tags of an object being overwritten or deleted, so
a role scoped by mistake to `tofu-records/*` can overwrite and delete a
neighbour's records. Get the prefix right. The renderer refuses anything
that is not an estate name for that reason.

An object with no `tofu-estate` tag at all is readable by any role whose
prefix reaches it. choudoufu tags every object it writes, so an untagged
object under an estate's prefix was put there by something else.

## A role that plans and never applies

```
render-policy.sh prod <bucket> --read-only
```

This is for a CI plan job, a pull-request check, or a reviewer who should
see what a change would do and be unable to do it. It composes with every
other flag on this page.

It is the same policy with four grants taken out, and each one is something
a plan never uses. `s3:PutObject` and `s3:PutObjectTagging`, because a plan
writes no record. `s3:DeleteObject`, because deleting a record is what an
apply does when a block goes away. The three `ReadTheBucketsAssertedSettings`
reads, because the bucket assertions run on an estate's first contact with
its store and again before an apply, and a read-only plan is neither. And
under `--kms` the grant drops to `kms:Decrypt` alone, since
`kms:GenerateDataKey` is what S3 asks for on a PUT.

Both Deny statements stay, including the one over tagging actions this
rendering allows none of. That is the same rule the full policy follows for
the three read actions it denies and never allows: a Deny written for the
actions of the day stops covering the boundary the moment somebody widens
the Allow list.

Every run still sends one conditional write, for the store's sentinel. Under
this policy it is denied, and the run carries on when the sentinel is already
there. A store that has never been written is refused by name instead, so run
the estate once under the full policy and read-only plans work from then on.
[Claim 38](../../../live/smoke/claims/a-read-only-role-can-plan.md) measures
both halves on real AWS, and reconciles what such a plan asks S3 for against
what this rendering grants.

A Kubernetes estate's plan job gets this same rendering. Such a job holds two
credentials and nothing routes one to the other: the kubernetes provider's
kubeconfig or in-cluster ServiceAccount token reaches the cluster, and the
process's own AWS credentials reach the record store (or, for a `local`
store, the process's filesystem user). The record store is never opened with
the cluster identity, and there is no cluster-backed record store to open it
with. So a ServiceAccount bound to `get`, `list` and `watch` says nothing
about whether the run may write the sentinel, and the AWS role beside it is
what this flag renders.

## What a recovery needs

The rendered policy is for running an estate, and it cannot recover a deleted
record. Recovery removes a delete marker, which takes `s3:ListBucketVersions`
on the bucket and `s3:DeleteObjectVersion` on the estate's prefixes, and
reading a noncurrent version takes `s3:GetObjectVersion`. Give those to the
person who recovers and leave them off the estate's role.
[Recover an estate](https://intentius.io/choudoufu/docs/use/recover-an-estate/) has the
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

adds `kms:Decrypt` and `kms:GenerateDataKey` on the key, conditioned on
`kms:ViaService` being the S3 endpoint in the key's own region. That
condition is what keeps the grant to what it is for: S3 asking the key on
the role's behalf. Without it, `kms:Decrypt` on the key is `kms:Decrypt` on
the key, and the role can decrypt anything encrypted under it from
anywhere.

There is deliberately no `kms:EncryptionContext:aws:s3:arn` condition
beside it. S3 sets that context to the bucket ARN when S3 Bucket Keys are
on and to the object ARN when they are off, so either literal is wrong for
half the buckets this renderer is pointed at, and a wrong one denies every
write, starting with the first one a new estate makes.

That is half of it. A customer managed key is usable only by the principals
its own key policy allows, so the key policy has to name the role as well:

```
render-key-statement.sh --key arn:aws:kms:us-east-2:111122223333:key/<key-id> \
  arn:aws:iam::111122223333:role/prod-estate
```

prints the statement to add to it, with the same `kms:ViaService` condition
and for the same reason. The rest of the key policy is yours. Pass every
principal that uses the bucket, including whoever would recover a deleted
record. The script refuses the account root and wildcards, and it refuses
principals from a partition the key is not in. `--key` is optional, so an
invocation written before it keeps working; without it the statement carries
no condition and the render says so on stderr.

A key policy that leaves the role out is the usual reason an estate's
first run against a new bucket fails. S3 reports a KMS refusal as
`AccessDenied` on its own operation, so choudoufu reads the message and
says `The record store bucket's KMS key refused this run`, with the key,
the action, the role, and which policy AWS blamed.
[Claim 37](../../../live/smoke/claims/the-recommended-secure-configuration.md)
measures the refusal and its message on real AWS, for an estate with no root
outputs.

## GovCloud and China

```
render-policy.sh prod <bucket> --partition aws-us-gov
```

The partition is the second field of every ARN the policy names, and it was
the literal `aws` until
[#1381](https://github.com/INTENTIUS/choudoufu/issues/1381). A policy that
names commercial-partition resources attaches to a GovCloud role without
complaint, reviews correctly, and matches no request that role ever makes.
The three values are `aws`, `aws-us-gov` and `aws-cn`, and nothing about a
bucket name says which one it is in, so it is a flag with `aws` as the
default.

`--kms` has to name a key in the same partition, and the renderer stops and
names both if it does not. The `kms:ViaService` value follows the key: in
China the service principal ends in `.amazonaws.com.cn`.

## An estate that sets key_prefix

```
render-policy.sh prod <bucket> --key-prefix team/prod
```

`key_prefix` in a `record_store "s3"` block moves where the estate's
records live. It moves the records and nothing else: the guided-discovery
hint stays under `tofu-hints/<estate>/` and the root output values stay
under `tofu-outputs/<estate>/`. Render with `--key-prefix` set to the same
string the block sets, and the three prefixes in the policy are the three
namespaces the store writes under. A test renders the policy and compares
its object ARNs against what the store's own
`projection.BucketNamespaces` returns for that prefix and estate, so the
two cannot drift.

Without the flag the policy scopes the records to
`tofu-records/<estate>/`, which such an estate never writes to, and its
first run is denied.

A `key_prefix` that would put this estate's records under another estate's
namespace is refused when the configuration loads, not here. See
[Reference](https://intentius.io/choudoufu/docs/use/reference/).

## Reading another estate's outputs

No choudoufu run reads another estate's objects. An estate reads a value from
another one off the live resource, with a data source
([Reading a value from another estate](https://intentius.io/choudoufu/docs/use/cross-estate/)),
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
