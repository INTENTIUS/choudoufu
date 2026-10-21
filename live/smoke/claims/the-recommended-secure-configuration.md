---
title: "The recommended secure configuration (a demo; claim 37 until #1817)"
retired: 37
---

# The recommended secure configuration

A demo beside `full`: it runs the recommended stack end to end on real
AWS with `SMOKE_REAL_AWS=1 just smoke the-recommended-secure-configuration`.
Real AWS, maintainer-run.

The recommended record store bucket uses a customer managed KMS key, and
the stack is built from what ships: the bucket comes from
`examples/record-store-bucket` with `just up`, and the role's policy is
the output of `render-policy.sh --kms --account`, unedited, plus one
statement the harness adds over a single marker key so it can tell when
IAM has propagated. The live policy is read back and compared to a fresh
render with that one statement dropped.

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
   two named principals: the estate's role and the operator. The
   statement is rendered with `--key`, so it carries `kms:ViaService` and
   the key is usable only through S3 in its own region.
2. `the bucket, from the project that ships` - `just up` with
   `RECORD_KMS_KEY_ARN`, then `just verify`, which asks the choudoufu
   binary about the three asserted settings and then probes the key
   policy statements on the bucket.
   Step 2b, `the same up again, with the key forgotten`, runs `just up` a
   second time with `RECORD_KMS_KEY_ARN` unset. It refuses before
   deploying, the refusal names the key exactly as
   `get-bucket-encryption` reports it, and the bucket's key is unchanged
   afterwards (#1421).
3. `the estate's role, from the published policy` - proven live before
   anything runs under it.
4. `an estate's life, as that role` - create, update under `If-Match`,
   and a replan from the records alone. A record is read back with
   `head-object` to show it is encrypted under the key. The configuration
   sets `bucket_owner` to this account and the policy requires
   `aws:ResourceAccount` on every `Allow`, so the whole life runs with
   the owner pinned from both sides. Step 4b, `the same bucket name,
   expected in another account`, moves `bucket_owner` one digit off: the
   plan stops on its first request and says the bucket may be owned by an
   account other than the one it expected, naming both. With the right
   owner back the same plan is empty.
   Step 4c, `the same bucket name, really in another account`, uses
   `SMOKE_OTHER_ACCOUNT_PROFILE`, an AWS CLI profile for a second account.
   In that account it creates a bucket with the three asserted settings
   and a policy that lets this account in. As the operator with no pin,
   the plan reads the bucket. With `bucket_owner` set to this account, the
   same plan stops on its first request, and the other account's bucket
   gains nothing (#1408, #1421).
5. `a record destroyed by mistake, and brought back` - an instance is
   removed from the configuration and applied. Its record is gone from a
   listing, and a delete marker sits over the earlier versions. The
   estate's role tries to remove the delete marker and is refused: the
   published policy has no `s3:DeleteObjectVersion`, so a run cannot
   rewrite history. The operator removes it. With the instance back in
   the configuration the plan is empty, and the current object version is
   the same version id that was current before the mistake.
6. `what the run actually used, against what the policy grants` - every
   S3 operation the role made is read out of the request log, mapped to
   its IAM action, and compared with the actions the rendered policy
   allows. The two sets must be equal in both directions.
7. `teardown` - the role destroys the estate, and `just down` then
   refuses, because the bucket still holds recoverable versions. The
   scenario empties the bucket deliberately on exit and runs `just down`
   again.

The `BREAK=1` arm is the common mistake with a customer managed key: a
key policy that leaves out the estate's role, with everything else
correct, including the `kms` statement in the role's IAM policy. The arm
installs the role's policy with the role in the key policy, which proves
the IAM policy is live, then takes the role out and waits until the role
can no longer read an object it could read before.

S3 relays a KMS refusal as `AccessDenied` on its own operation. The run's
error is titled `The record store bucket's KMS key refused this run`,
names the key, the action and the role, and says which policy to read,
following what AWS blamed: a key policy that does not allow it, an IAM
policy that does not, or an explicit deny.
