---
title: "The three things a record store bucket must have"
weight: 8
---

# The three things a record store bucket must have

choudoufu asserts three settings about the bucket behind `record_store "s3"`
and refuses a bucket that fails one. Each exists because of something
specific about what a record is.

| Setting | What is asserted | What it protects against |
|---|---|---|
| `versioning` | Versioning is `Enabled` | A record can be the only copy of what it says. A record-backed resource carries no marker and cannot be imported under a live block, so in an unversioned bucket an overwrite or a delete is final |
| `lifecycle` | Enabled rules expire **noncurrent** versions under every key an estate writes, and no enabled rule expires **current** objects there | With versioning on, every apply adds versions. Without this rule the bucket keeps all of them forever, and nobody has chosen how long a record destroyed by mistake stays recoverable |
| `public_access_block` | All four settings are on | Records hold secret material by default ([Secrets]({{< relref "/docs/use/secrets" >}})). A public bucket policy or ACL would publish them |

Encryption at rest is deliberately not a fourth.
[Encryption at rest]({{< relref "/docs/use/encryption" >}}) says why.

## The lifecycle number is a recovery window

The days in the noncurrent-expiration rule are how long you have to notice
that a record was destroyed or overwritten by mistake. In a versioned bucket
a delete writes a delete marker and the record survives underneath as a
noncurrent version until the rule expires it. Removing the delete marker
brings the record back, and
[claim 37]({{< relref "/docs/claims/the-recommended-secure-configuration" >}})
does exactly that on real AWS.

So choose the number as the answer to "how long until we would notice", and
do not inherit it from an example. `examples/record-store-bucket` defaults to
thirty days and keeps whatever a bucket already has when `just up` runs again.

What counts, exactly:

- A rule must expire noncurrent versions. One that only transitions storage
  classes, or only aborts multipart uploads, does not count.
- A rule with a prefix filter counts for the namespaces that sit under its
  prefix. The three namespaces may be covered by one rule or by one rule
  each. A rule with no filter covers every estate that will ever share the
  bucket, which is why the shipped project uses one.
- A rule filtered by tag or by object size never counts. It may cover every
  record today and stop tomorrow with nothing to notice.

And one thing refuses the bucket whatever else it has: an enabled rule that
expires **current** objects and could reach the estate's keys. A converged
estate does not rewrite its records, so they age, and such a rule deletes
them on a timer. The next plan then reads an estate with those resources
missing and proposes creating what already exists. This refusal has its own
headline, `The record store bucket's lifecycle deletes records`, and it is
the one finding `allow_insecure` does not cover. A rule that only removes
expired delete markers is fine. A deleting rule filtered by tag or size is
refused too, since nothing shows the filter misses the records.

Until #1377 the check looked only for the noncurrent expiry and passed a
bucket with a deleting rule beside it.

## When it is checked

Not on every plan. The settings are facts about the bucket that do not change
between two plans, and checking them costs three reads and three permissions.

- An estate's **first contact** with a bucket, whatever the command is
  (`live-plan` and `live-mv` included, since #1376). The
  run that writes the store's sentinel is the first, and it is the one moment
  a wrong bucket costs nothing to walk away from. A refusal takes the sentinel
  back out, so the next run is a first contact again.
- **Before every apply.**
- Whenever you ask: `choudoufu live-bucket -bucket <name>` reports all three
  and exits non-zero on a failure. It reports the bucket, so a waiver in some
  estate's configuration never changes its answer.

A setting the role cannot read is refused the same as one that failed,
because a bucket nobody could check is not a bucket anyone checked. The three
reads are `s3:GetBucketVersioning`, `s3:GetLifecycleConfiguration` and
`s3:GetBucketPublicAccessBlock`, and the
[published policy]({{< relref "/docs/use/iam" >}}) carries them.

[Claim 29]({{< relref "/docs/claims/a-wrong-bucket-is-refused" >}}) measures
each refusal by name, the first-contact refusal that leaves nothing behind,
and that a plan against an established estate goes through without asking.

## Waiving a setting, and what each waiver costs

`allow_insecure` names the settings an estate proceeds without. It is a list
of names, never a boolean, so a waiver reaches exactly what it names and a
failure of either other setting still refuses.

```hcl
record_store "s3" {
  bucket         = "my-records-bucket"
  allow_insecure = ["lifecycle"]
}
```

What you give up, per name, in the words the run itself prints:

| Waived | The cost |
|---|---|
| `versioning` | An overwritten or deleted record cannot be brought back, and a record can be the only copy of what it says |
| `lifecycle` | Nothing is known to expire noncurrent versions: the bucket may keep every version of every record forever, and nobody has chosen how long a record destroyed by mistake stays recoverable. It does not waive a rule that deletes records |
| `public_access_block` | Nothing is known to stop a bucket policy or an ACL from publishing the records, which hold secret material |

A waiver is loud on every run that opens the store, `plan`, `live-plan` and
`live-mv` included, and names the setting and its cost each time. When the bucket does fail the waived setting, an apply also
says the assertion would have refused it. When the bucket passes, `live-bucket`
says the waiver is hiding nothing and can be removed. It reports on waivers
only when run in the estate's directory with no `-bucket` flag, because a
bucket named on the command line has no configuration to read a waiver from. An unknown name, a
repeated name, a boolean, and `allow_insecure` on a store that is not a bucket
are all configuration errors.
[Claim 30]({{< relref "/docs/claims/a-waiver-names-what-it-waives" >}})
measures this.

The legitimate uses are narrow: a role that is not allowed to read a bucket's
configuration, where someone who is has confirmed the setting, and a bucket an
organization runs differently on purpose.

## A correct bucket, stated without the project

`examples/record-store-bucket` makes a correct bucket, and nothing depends on
using it. A bucket is correct when:

- versioning is `Enabled`;
- enabled lifecycle rules carry a `NoncurrentVersionExpiration` for every key
  an estate writes (one rule with no filter is the simple way), and no enabled
  rule expires current objects there;
- all four public-access block settings are on;
- it exists before the first plan, and it is not declared by an estate that
  uses it as its own store.

`choudoufu live-bucket` is the authority on the first three, for a bucket made
any way at all. [What you set up by hand]({{< relref "/docs/use/setup" >}})
walks through creating one.
