---
title: "Claim 38: A role with the read-only policy plans an established estate and writes nothing, and a store with no sentinel is still refused by name"
claim: a-read-only-role-can-plan
---

# Claim 38: A role with the read-only policy plans an established estate and writes nothing, and a store with no sentinel is still refused by name

**This claim runs against real AWS and is maintainer-run.** It is about
what one IAM policy permits and another refuses, and the pinned emulator
evaluates neither the policy nor its conditions.

A plan changes nothing, so a CI plan job is usually given a role that may
read and not write. Opening the record store used to break that: every run
sends a conditional write for the store sentinel, and a role without
`s3:PutObject` got `AccessDenied` where a second run gets a version
conflict, so the run stopped before any plan was built.

```text
Clone https://github.com/INTENTIUS/choudoufu. You need AWS credentials
for an account you may create an S3 bucket and two IAM roles in, the AWS
CLI, jq and Go. From the repo root run:

  SMOKE_REAL_AWS=1 just smoke a-read-only-role-can-plan

It removes the bucket and the roles it made. Explain each step's verdict
line to me as it prints. Then run it again with BREAK=1 and report the
"caught" line.
```

As the run prints them:

1. `an estate applied under the FULL policy` - one run under a role that
   may write, which is what provisions the sentinel. That run is what an
   estate does once.
2. `a second role, from the read-only rendering` -
   `render-policy.sh --read-only`, unedited, with the Allow statements of
   a second read-only render merged in for the estate name step 4 uses.
   The second render's Deny statements are left out on purpose: each
   render's read Deny accepts its own estate's tag and no other, so two of
   them together refuse every tagged object in the bucket. The first
   render's Deny still covers the whole bucket, so the reader can read no
   object of the second estate either, only list its namespace.
3. `the reader plans, and the store is untouched` - a fresh checkout with
   no state cache, so everything the plan knows it read out of the bucket.
   The plan is empty, the reader's own `put-object` under the estate's
   prefix is denied, and the three namespaces hold exactly the object
   versions they held before, the sentinel included.
4. `an estate with no sentinel is still refused, by name` - the same
   reader, the same bucket, an estate name nothing has ever written. The
   reader may list that name's namespaces, so what it meets is not a list
   denial. The run says the store holds no sentinel and this identity may
   not write one, names the key, and proposes nothing. A store with no
   sentinel and an identity that cannot provision one is indistinguishable
   from an empty estate, and planning it as empty would propose creating
   everything in it again.
5. `what the reader's plan asked S3 for, against what the policy grants` -
   the request log against the rendering, both directions. The policy
   grants nothing the plan did not use. The plan used one thing the policy
   does not grant, and it is the point of the claim: the sentinel write,
   denied and survived. No bucket-configuration read appears at all, which
   is why the read-only rendering leaves those three actions out.

The read-only rendering is the full one with the grants a plan never uses
taken away: `s3:PutObject` and `s3:PutObjectTagging`, `s3:DeleteObject`,
the three bucket-configuration reads, and `kms:GenerateDataKey` under
`--kms`. Both Deny statements stay, including the one over tagging actions
it allows none of.
[IAM for the record store bucket](https://intentius.io/choudoufu/docs/use/iam/) has the
rendering and who it is for.

The `BREAK=1` binary makes `provisionStoreSentinel` return a denied
sentinel write instead of carrying it past to its List, which is what every
binary before the fix did. With it, the same role and the same policy
cannot plan at all.
