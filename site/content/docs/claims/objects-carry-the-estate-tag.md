---
title: "Claim 36: Every record store object carries its estate's tag, and the tag is load-bearing"
weight: 36
claim: objects-carry-the-estate-tag
---

# Claim 36: Every record store object carries its estate's tag, and the tag is load-bearing

**This claim runs against real AWS and is maintainer-run.** The pinned
emulator does not evaluate the object-tag condition keys, so a claim
that a tag is load-bearing could not fail there.

Every object choudoufu writes to the bucket carries the estate's own
markers: `tofu-estate` on all of them, and `tofu-address` on a record,
the same pair every managed resource carries and built by the same
functions. They are for authorization and provenance. They are not how
anything is found: objects are found by listing a known bucket, and the
Resource Groups Tagging API does not index S3 objects.

```text
Clone https://github.com/INTENTIUS/choudoufu. You need AWS credentials
for an account you may create an S3 bucket and an IAM role in, the AWS
CLI, jq and Go. From the repo root run:

  SMOKE_REAL_AWS=1 just smoke objects-carry-the-estate-tag

It removes the bucket and the role it made. Explain each step's verdict
line to me as it prints. Then run it again with BREAK=1 and report the
"caught" line.
```

As the run prints them:

1. `an estate applies under the published policy` - as its scoped role.
   Every object is read back with `get-object-tagging`: the sentinel, the
   hint and the output carry `tofu-estate`, and each record also carries
   `tofu-address` in the marker form of its address, so `effect["a.b"]`
   is `terraform_data.effect:a@db`.
2. `a record tagged as another estate's is refused to this one` - one
   record is retagged out of band as `someone-else`, under this estate's
   own prefix. The role's prefix scope still reaches it, so the tag is
   the only thing that says it is not this estate's. The role is denied
   the read. The plan fails, names the record, and proposes nothing. A
   record the role may not read is not a record that is gone, and a plan
   that read it that way would propose creating a resource that exists.
3. `the tag put back` - the plan is empty again and the role destroys
   both instances.

This claim was first written as "strip a tag, and tag-conditioned IAM
must deny the read". Measured against AWS, a policy that denies an
untagged object also denies every conditional write, which is every
update and delete choudoufu makes
([claim 34]({{< relref "/docs/claims/a-new-estate-writes-its-first-record" >}})).
So the published policy denies a foreign tag and leaves an untagged
object readable, and this claim measures what is true: a foreign tag is
denied.

The `BREAK=1` binary sends no tags. Under the published policy its first
write is `AccessDenied` on `s3:PutObject` and nothing is written. Tagging
is not a convention the store could drop without anyone noticing.
