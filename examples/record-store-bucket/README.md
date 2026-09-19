# The record store bucket

A live estate that declares `record_store "s3"` keeps its records in a
bucket. This project stands that bucket up, tells you whether it is
correct, and takes it down again.

```
just up        # create or update the bucket; prints RECORD_STORE_BUCKET
just verify    # is this bucket correct? asks the choudoufu binary
just down      # delete the stack; refuses while the bucket holds anything
```

Then point an estate at it:

```hcl
terraform {
  live {
    estate = "prod"

    record_store "s3" {
      bucket = "choudoufu-records-111122223333-us-east-2"
    }
  }
}
```

One bucket serves every estate. Each estate writes under its own
prefixes (`tofu-records/<estate>/`, `tofu-hints/<estate>/`,
`tofu-outputs/<estate>/`), so adding an estate is a configuration
change and not another `just up`.

## What a correct bucket is

This section is the specification. The project is one way to meet it,
and a bucket built with your own tooling is just as correct if it
satisfies the same three things. choudoufu checks them itself, before an
apply changes anything and on an estate's first contact with the bucket,
and refuses a bucket that fails one.

**Versioning is enabled.** A record can be the only copy of what it
says. A record-backed resource carries no marker and cannot be imported
under a live block, so in an unversioned bucket an overwrite or a delete
is final. Suspended does not count.

**A lifecycle rule expires noncurrent versions.** Versioning is on and
every apply writes records, so without this the bucket keeps every
version of every record forever. The rule must be enabled, must set
`NoncurrentVersionExpiration` to at least one day, and must reach every
key an estate writes: no filter at all, or a prefix filter that all
three of the estate's prefixes sit under. A rule filtered by tag or
object size does not count, and neither does a rule that only
transitions storage classes or only aborts multipart uploads. It must
never expire current objects: a record is not a log.

The number of days is the recovery window. A deleted record is a delete
marker over a noncurrent version, and an overwritten one leaves its
predecessor behind as a noncurrent version, and both last exactly that
long. For the record-backed slice it is the only answer to "how long do
we have to notice that a record was destroyed by mistake". Pick it
deliberately.

The rule this project ships also sets `ExpiredObjectDeleteMarker`, which
removes a delete marker once nothing is left under it. That is not one
of the three settings and choudoufu does not ask for it. Without it the
markers left by every deleted record stay in the bucket forever, and
each one is a version, so `just down` would refuse a bucket whose
records are long gone. S3 rejects a rule that combines it with an
expiry by days or date, or with tag filters; this rule has none of
those.

**Public-access block is on, all four settings.** Records hold secret
material, protected by the bucket's encryption at rest and by IAM.

Encryption at rest is not on this list. S3 has encrypted every new
object by default since January 2023, so checking for it would pass on
every bucket that exists. choudoufu works under every SSE flavour
(SSE-S3, SSE-KMS with the AWS-managed key, SSE-KMS with a customer
managed key, DSSE-KMS) and asserts none. A customer managed key is the
recommendation, because it is the one flavour where you write the key
policy and can cut access by revoking the key.

To check any bucket, from anywhere:

```
choudoufu live-bucket -bucket=<name> -region=<region>
```

It needs `s3:GetBucketVersioning`, `s3:GetLifecycleConfiguration` and
`s3:GetBucketPublicAccessBlock`. A setting the caller may not read is
reported `UNREADABLE`, which is not a pass.

## `just verify` reports the bucket, not a configuration

`verify` calls `choudoufu live-bucket`. Nothing in this project checks
the three settings a second time, so the project and the tool cannot
come to disagree about what a correct bucket is.

An estate can waive an assertion (`allow_insecure = ["versioning"]`),
and its plans and applies then proceed with a warning on every run. That
changes whether the estate's runs proceed. It does not change what
`verify` says: a bucket with versioning off is reported `NOT correct`
whoever has waived it. Run `choudoufu live-bucket` with no options inside
an estate's directory and it also names that estate's waivers and says
whether each is hiding a real failure.

## Parameters

| variable | default | what it is |
|---|---|---|
| bucket name (an argument to any recipe) | `choudoufu-records-<account>-<region>` | Globally unique, so derived from the account rather than guessed. Any name works. |
| `RECORD_NONCURRENT_DAYS` | `30` | The recovery window, above. |
| `RECORD_KMS_KEY_ARN` | unset | A customer managed key **you** own. |
| `AWS_REGION` | `us-east-2` | Where the bucket and its stack live. |

The bucket name is an argument, not an environment variable: `just up
my-bucket`, `just verify my-bucket`. `RECORD_BUCKET` is what the chant
project under `src/` reads, and the recipes set it for you; it matters
only if you run `npx chant build` yourself.

`just up` is safe to run again, and it reads two things off the live
bucket rather than assuming them.

The recovery window: when you do not pass `RECORD_NONCURRENT_DAYS` and
the bucket already exists, `up` builds with the window the bucket has
now, so a window somebody changed since is not reset to 30. If that read
fails for any reason other than the bucket or its lifecycle
configuration being absent, `up` stops and shows the error. A throttle
or an AccessDenied is not a bucket without a window.

The key: if the bucket's default encryption names a KMS key and
`RECORD_KMS_KEY_ARN` is unset or names a different one, `up` refuses
instead of rebuilding the bucket as SSE-S3 and dropping the two Deny
statements. It prints the key it found and what to do about it. There is
no flag for the downgrade, on purpose: change the bucket's encryption
yourself first, then run `up` again.

### The key is yours

With `RECORD_KMS_KEY_ARN` set, the bucket's default encryption becomes
SSE-KMS under that key with bucket keys on, the bucket policy refuses a
put that asks for a different algorithm or a different key, and `verify`
tests that policy under `_verify/`: a put that asks for AES256, which
has to be refused, a put with no encryption header, which has to be
accepted, and a head of what that second put wrote, to see which key it
landed under.

This project never creates the key. A key minted by `up` would be a key
`down` could delete, and deleting it makes every record in the bucket
unreadable.

The key policy has to let the estate's role use it, and the role's own
IAM policy is not enough: a customer managed key is usable only by the
principals its key policy allows. The statement to add is printed by

```
just key-statement arn:aws:iam::111122223333:role/prod-estate arn:aws:iam::111122223333:role/records-operator
```

Name every estate's role, and whoever would recover a deleted record,
since recovery reads the record. It refuses the account root and
wildcards: naming the account hands the decision to every IAM policy in
it. Without the statement an estate's first run stops with
`The record store bucket's KMS key refused this run`, naming the key, the
action and the role. Smoke claim 37 runs this whole arrangement on real
AWS, and its BREAK arm is a key policy with the role left out.

Each `Deny` in that policy is also conditioned on the header being
present. The obvious form, `StringNotEquals` on
`s3:x-amz-server-side-encryption` alone, denies the header-less put the
record store actually sends, because the condition key is null when the
header is absent. The first version of this project shipped that policy:
every configuration check passed and the bucket was unusable.

## Access is not granted here

The bucket policy grants nobody anything. An estate's role gets its
access through IAM, and that policy has one source, `iam/render-policy.sh`:

```
just policy prod                                   # for this project's bucket
just policy prod "" --reads-outputs-of network     # with a declared dependency
just policy prod my-bucket --kms arn:aws:kms:...   # with your own key
just policy prod "" --account 111122223333         # pin the bucket's owner
```

`--account` adds `aws:ResourceAccount` to every `Allow`, so the policy
reaches a bucket of that name in that account and nowhere else. A bucket
name is global and a free name can be taken by anyone, so without it the
policy grants its estate a bucket of the right name in a stranger's
account. Rendering without the flag prints a warning saying so. The other
half is `bucket_owner` in the estate's `record_store` block, which puts
`ExpectedBucketOwner` on every request the run makes.

The documentation's IAM page shows the same output and says why each
statement is there. Read it before editing the result: two statements
look like they could be tightened, and both were measured against AWS.
Conditioning `ReadAndDeleteByPrefix` on the object's existing tag leaves
an estate that can create records and can never update or delete one.
Writing `WriteOnlyObjectsTaggedAsThisEstate` with `s3:ExistingObjectTag`
instead of `s3:RequestObjectTag` denies the first write into every new
estate. `site/content/docs/use/iam.md` has both in full, under "What each
statement is for".

## `just down`

It removes the stack, and `verify`'s own probe objects under `_verify/`.

It refuses while the bucket holds **any object version**, current or not,
anywhere outside `_verify/`. Any version, because a deleted record's
recovery copy lives only here. Anywhere, because an estate with a
`key_prefix` override writes its records somewhere other than `tofu-*`,
and a count under `tofu-` alone read that bucket as empty. The refusal
names what it found, by prefix, and the command that empties each one.

A `choudoufu destroy` is not enough to satisfy it. A destroyed estate
leaves its sentinel, its hint, every `tofu-outputs/` object and a
tombstone envelope per record, all as current objects that no lifecycle
rule here removes. Emptying those prefixes is a deliberate step, and
then the versions and delete markers it creates age out on the bucket's
own recovery window.

The bucket outlives the stack. It carries `DeletionPolicy` and
`UpdateReplacePolicy` `Retain`, so no stack operation can delete it: not
a `delete-stack`, not a rollback, not a replacement during an update. So
`down` deletes the stack and leaves an empty bucket behind, says so, and
prints the two commands that remove it. Until you run them, `just up`
under the same name cannot recreate the stack, and the bucket has no
bucket policy, since the policy is a stack resource and goes with it.

## The governed path

`just up` deploys the built template directly. The same template goes
through chant's Ops with an approval gate:

```
just ops          # the Ops resolve and lint
just chant-plan   # read-only
just chant-apply  # stops at its gate; chant approve bucket-apply apply
```

## Where this runs

Real AWS. The pinned floci emulator reports `CREATE_COMPLETE` for this
stack and applies none of the bucket's properties (lex00/floci#213), and
`just verify` is what noticed. The S3 API calls themselves work on floci,
which is how choudoufu's own smoke claims about this bucket run locally.
