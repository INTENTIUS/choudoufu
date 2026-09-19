---
title: "Claim 4: The backend is a bucket with no lock table and no lock: nothing is held, so nothing gets stuck"
claim: backend-sets-itself-up
---

# Claim 4: The backend is a bucket with no lock table and no lock: nothing is held, so nothing gets stuck

**This claim runs against real AWS and is maintainer-run, for now.** One
step stands the bucket up with the shipped `just up`, and the pinned
emulator's CloudFormation reports `CREATE_COMPLETE` while applying none
of an S3 bucket's properties. Nothing else in the claim needs an
account, and it returns to the emulator when that is fixed.

Stock remote state has a day one: create a bucket, enable versioning,
create a lock table, write IAM for both. Three of those four are still
here. A cloud record store is a bucket, it wants versioning, and it
wants IAM. The one that is gone is the lock table, and with it the lock.
Every write to the store is a single conditional request that succeeds
or fails atomically and leaves nothing behind. Nothing is held across a
run, so a run that dies cannot strand the next one, and there is no
`force-unlock` to reach for at the worst moment.

This is a swap and not a subtraction. The bucket also wants a lifecycle
rule and a public-access block, which stock's list never mentioned, so
the list is no shorter. What changes is the kind of thing that can go
wrong. A lock table is in the path of every apply and can strand one. A
lifecycle rule and a public-access block are set once and are in the
path of none.

This claim used to be titled "Declaring the backend is the whole setup".
That is still true of the local store, and steps 1 and 2 still measure
it. It was never going to be true of a bucket.

```text
Clone https://github.com/INTENTIUS/choudoufu. You need AWS credentials
for an account you may deploy a CloudFormation stack with one S3 bucket
in, plus the AWS CLI, jq, just, node and npm. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  SMOKE_REAL_AWS=1 just smoke backend-sets-itself-up

It removes the stack it made. Explain each step's verdict line to me as
it prints. Then run it again with BREAK=1 and report the "caught" line:
it makes the store unreachable and the run must refuse by name rather
than plan an empty-looking estate.
```

As the run prints them:

1. `no store declared - the local one appears unbidden` - a live block
   with nothing about storage gets a `.tofu-records` directory beside
   the module at first use, sentinel already written. Zero setup steps.
2. `it works: the effect survives between runs` - the recorded
   resource survives a replan, so the store is real, not scaffolding.
3. `the cloud store is a bucket, stood up once with one command` -
   `just up` from `examples/record-store-bucket` makes it, and
   `just verify` asks the choudoufu binary whether it is correct:
   versioning, a lifecycle rule that expires noncurrent versions, and
   public-access block. The configuration then says
   `record_store "s3" { bucket = ... }` and nothing else. On first use
   the store writes its sentinel into the bucket, and the AWS CLI reads
   it back. No lock table is created and nothing names one.
4. `a run killed in the middle of an apply strands nothing` - a second
   resource takes a while to create, and the apply is killed with
   `SIGKILL` while that is in flight, so no handler runs and nothing
   cleans up. The very next apply, with nothing done in between, finishes
   the work on its first try. Every object in the bucket is then listed:
   a sentinel, a hint and the records. None of them is a lock.
5. `teardown - and this time there IS something to deprovision` - both
   estates are destroyed, and `just down` then refuses, because the
   bucket still holds the recoverable versions of the destroyed records.
   A bucket is a thing somebody stood up and somebody has to take down.
   The scenario empties it on exit, deliberately.

The `BREAK=1` run makes only the record store unreachable, through the
SDK's S3 endpoint override. A store that cannot answer must refuse
loudly, naming itself, because a store that answers with silence would
read as an empty estate and the next plan would propose rebuilding
everything. That property does not depend on which store it is, and it
stays measured.

Two writers racing on one record, which is the other half of "nothing
is locked", is
[claim 32](two-writers-one-record.md).
