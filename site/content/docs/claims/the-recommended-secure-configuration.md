---
title: "Claim 37: The recommended secure configuration works end to end, including recovering a deleted record"
weight: 37
claim: the-recommended-secure-configuration
---

# Claim 37: The recommended secure configuration works end to end, including recovering a deleted record

**This claim runs against real AWS and is maintainer-run.** It is about
a customer managed key's policy, a CloudFormation stack and IAM, all
evaluated together, and the pinned emulator evaluates none of them the
way AWS does.

The documentation recommends a customer managed KMS key for the record
store bucket. choudoufu asserts nothing about encryption, so nothing
else in the repository would notice if that advice stopped working.
This claim builds the recommended stack from what ships, with no
hand-written substitutes: the bucket comes from
`examples/record-store-bucket` with `just up`, and the role's policy is
the output of `render-policy.sh --kms --account`, unedited, plus one statement the
harness adds over a single marker key so it can tell when IAM has
propagated. The live policy is read back and compared to a fresh render
with that one statement dropped.

```text
Clone https://github.com/INTENTIUS/choudoufu. You need AWS credentials
for an account you may deploy a CloudFormation stack, create an IAM role
and create a KMS key in, plus the AWS CLI, jq, just, node and npm. From
the repo root run:

  SMOKE_REAL_AWS=1 just smoke the-recommended-secure-configuration

It removes the stack and the role and schedules the key for deletion.
To borrow a key you already have, set SMOKE_KMS_KEY_ARN; its policy is
put back at the end. Explain each step's verdict line to me as it
prints. Then run it again with BREAK=1 and report the "caught" line.
```

As the run prints them:

1. `the key, and a key policy that names who may use it` - the account
   administers the key, and `kms:Decrypt` and `kms:GenerateDataKey` go to
   two named principals: the estate's role and the operator. Usage is
   not delegated to IAM wholesale, because writing that list yourself is
   the reason to have a customer managed key. The statement is rendered
   with `--key`, so it carries `kms:ViaService` and the key is usable
   only through S3 in its own region.
2. `the bucket, from the project that ships` - `just up` with
   `RECORD_KMS_KEY_ARN`, then `just verify`, which asks the choudoufu
   binary about the three asserted settings and then probes the key
   policy statements on the bucket.
3. `the estate's role, from the published policy` - proven live before
   anything is measured under it.
4. `an estate's life, as that role` - create, update under `If-Match`,
   and a replan from the records alone. A record is read back with
   `head-object` to show it is encrypted under the key. The configuration
   sets `bucket_owner` to this account and the policy requires
   `aws:ResourceAccount` on every `Allow`, so the whole life runs with
   the owner pinned from both sides. The run prints the next part as
   step 4b, `the same bucket name, expected in another account`:
   `bucket_owner` is moved one digit off. The plan stops on its first request and says
   the bucket may be owned by an account other than the one it expected,
   naming both. With the right owner back the same plan is empty. One
   account cannot stage a stranger's bucket, so the pin is moved and the
   bucket is not. S3 answers the two cases the same way.
5. `a record destroyed by mistake, and brought back` - an instance is
   removed from the configuration and applied. Its record is gone from a
   listing, and a delete marker sits over the earlier versions. The
   estate's role tries to remove the delete marker and is refused: the
   published policy has no `s3:DeleteObjectVersion`, so a run cannot
   rewrite history. The operator removes it. With the instance back in
   the configuration the plan is empty, and the object version that is
   current is the same version id that was current before the mistake.
6. `what the run actually used, against what the policy grants` - every
   S3 operation the role made is read out of the request log, mapped to
   its IAM action, and compared with the actions the rendered policy
   allows. The two sets must be equal in both directions. The two KMS
   actions are made by S3 on the role's behalf and never appear as the
   role's own requests, so they are not in that comparison; step 4
   cannot pass without them.
7. `teardown` - the role destroys the estate, and `just down` then
   refuses, because the bucket still holds recoverable versions. The
   scenario empties the bucket deliberately on exit and runs `just down`
   again.

The `BREAK=1` arm is the mistake people make with a customer managed
key: a key policy that does not name the estate's role. Everything else
is correct, including the `kms` statement in the role's IAM policy. The
arm installs the role's policy with the role in the key policy, because
that is the only way to prove the IAM policy is live, then takes the
role out and waits until the role can no longer read an object it could
read before.

S3 relays a KMS refusal as `AccessDenied` on its own operation. Before
this claim existed the run said `Cannot open the record store` followed
by `AccessDenied` on `PutObject`, which points at the bucket and the S3
permissions, and those are correct. Now the error is titled
`The record store bucket's KMS key refused this run`, names the key, the
action and the role, and says which policy to read. Which policy that is
follows what AWS blamed: a key policy that does not allow it, an IAM
policy that does not, or an explicit deny.
