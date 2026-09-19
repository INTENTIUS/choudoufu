---
title: "Claim 35: One bucket, many estates: reading a neighbour's records takes two mistakes, not one"
weight: 35
claim: one-bucket-many-estates
---

# Claim 35: One bucket, many estates: reading a neighbour's records takes two mistakes, not one

**This claim runs against real AWS and is maintainer-run**, for the
reason [claim 34]({{< relref "/docs/claims/a-new-estate-writes-its-first-record" >}})
gives.

Every estate's records live in one bucket, and what separates them is
IAM. For reading another estate's objects there are two defences, and
they fail to different things. The prefix scope is defeated by a prefix
written wrong. The object tag is not, because a neighbour's object
carries the neighbour's tag however the prefix was written. For a list,
a write or a delete there is only the prefix: S3 has no condition key
for the tags of an object being overwritten or deleted.

```text
Clone https://github.com/INTENTIUS/choudoufu. You need AWS credentials
for an account you may create an S3 bucket and IAM roles in, the AWS CLI
and jq. From the repo root run:

  SMOKE_REAL_AWS=1 just smoke one-bucket-many-estates

It removes the bucket and the roles it made. Explain each step's verdict
line to me as it prints. Then run it again with BREAK=1 and report the
"caught" line.
```

As the run prints them:

1. `two estates, two roles, one bucket` - each estate applies under its
   own role and the published policy.
2. `estate a's role, at estate b's door` - with the AWS CLI and no
   choudoufu in the loop. The role reads its own record, which is the
   control. It is denied `b`'s record, `b`'s outputs and a list of `b`'s
   prefix. It is denied a list of the bare prefix `tofu-records/smoke-a`,
   the one that would also name an estate called `smoke-a-eu`. It is
   denied a write under `b`'s prefix, and a write under its own prefix
   tagged as `b`'s.
3. `the prefix written wrong, and the tag still holding` - the role's
   allows are widened to every estate's objects. It still cannot read
   `b`'s record.
4. `what the tag cannot defend` - under the same widened policy the role
   overwrites and deletes an object tagged as `b`'s, and both are
   allowed. A wrong prefix is enough to destroy a neighbour's records.
   The policy renderer refuses anything that is not an estate name for
   this reason.
5. `a declared dependency` - reading `b`'s outputs is denied until the
   policy is rendered with `--reads-outputs-of smoke-b`, and allowed
   after. `b`'s records stay denied. Everything in the outputs crosses,
   sensitive values included.

The `BREAK=1` run widens the prefix and also removes the tag's `Deny`.
The read of `b`'s record then succeeds. If it were still denied,
something other than the two defences this claim names would be doing
the denying.
