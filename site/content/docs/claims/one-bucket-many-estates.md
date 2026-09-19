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
   tagged as `b`'s. A decoy under `tofu-records/smoke-a-eu/` gets the
   same three refusals, a write, a delete and a read, because for those
   the trailing slash on the object ARN is the whole defence.
3. `the prefix written wrong, and the tag still holding` - the role's
   allows are widened to every estate's objects. It still cannot read
   `b`'s record. It then tries the obvious next move: `put-object-tagging`
   to relabel `b`'s record as its own, and `delete-object-tagging` to
   strip the tag. Both are refused, the record is still tagged `smoke-b`,
   and the read is refused again. The write statement checks the tag a
   request sends and says nothing about the object it lands on, so this
   refusal is a statement of its own,
   `DenyRelabellingAnotherEstatesObjects`.
4. `what the tag cannot defend` - under the same widened policy the role
   overwrites and deletes an object tagged as `b`'s, and both are
   allowed. A wrong prefix is enough to destroy a neighbour's records.
   The policy renderer refuses anything that is not an estate name for
   this reason.
5. `another estate's outputs are readable only with the statement that grants it` - reading `b`'s outputs is denied until the
   policy is rendered with `--reads-outputs-of smoke-b`, and allowed
   after. `b`'s records stay denied, and they stay denied when the prefix
   is also written wrong, because `b`'s tag is accepted under `b`'s
   outputs prefix and nowhere else. What the grant exposes is what `b`
   wrote under `tofu-outputs/`, and an output marked `sensitive` is never
   written there. No choudoufu run makes this read; the grant is for a
   reader you write yourself
   ([Reading a value from another estate]({{< relref "/docs/use/cross-estate" >}})).

The `BREAK=1` run has two arms.

The first widens the prefix and removes one statement, the relabel Deny,
leaving the read Deny in place. The role retags `b`'s record as its own
and then reads it. That is the policy this repository published until
#1381, and until then step 3's headline was false: one mistake was
enough. It was found by an audit and measured against AWS before the
statement was added.

The second widens the prefix and removes the read Deny. The read of
`b`'s record then succeeds. If it were still denied, something other
than the defences this claim names would be doing the denying.
