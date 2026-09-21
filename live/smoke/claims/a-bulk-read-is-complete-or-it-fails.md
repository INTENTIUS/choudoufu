---
title: "Claim 31: A record read that fails mid-fanout fails the read; a short map never reaches a plan"
claim: a-bulk-read-is-complete-or-it-fails
---

# Claim 31: A record read that fails mid-fanout fails the read; a short map never reaches a plan

A plan reads an estate's records in one bulk read: a LIST, then one GET
per record, eight at a time. What comes back is read as complete. A
declared resource with no record is something to create, and a record
with no configuration is something to destroy. So the worst thing that
read can do is succeed with a record missing, and running the GETs in
parallel is where that would come from: a loop returns on its first
error for free, and a fan-out has to be written to.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info), the AWS CLI and python3 are installed, and Go is
installed. From the repo root run:

  just smoke a-bulk-read-is-complete-or-it-fails

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke a-bulk-read-is-complete-or-it-fails and report the
"caught" line: it rebuilds choudoufu so a failed GET drops its key, and
the plan must be caught proposing to create a resource that exists. Then
run BREAK_CROSSCHECK=1 just smoke a-bulk-read-is-complete-or-it-fails
and report its "caught" line too: it rebuilds choudoufu without the
plan-time cross-check, and a destroy must be caught reporting one
destroyed of two.
```

As the run prints them:

1. The first step applies twelve record-backed resources straight to the
   emulator, one record each, and singles one record out. Twelve is the
   fixture's size, chosen to be more than the eight GETs that run at
   once.
2. `the proxy, a control plan through it, and how wide the fan-out is` -
   a small proxy sits in front of S3 because nothing else can fail one
   GET out of a fan-out: the emulator has no fault injection and nothing
   in the cloud can be corrupted into a 500. With nothing failing, the
   plan through it is empty and the proxy saw every record's GET. A
   proxy that changed the answer by itself would stop the scenario here.
   The proxy also counts record GETs in flight and keeps the high-water
   mark, which is where "eight at a time" is measured: more than one in
   flight by default, and exactly one when the same plan runs with
   `TOFU_LIVE_RECORD_READ_PARALLELISM=1`.
3. `one GET fails once, mid-fanout` - one record's GET is answered 500,
   once. No resource is proposed for creation, and the plan the run
   produces is required to be the true one: empty if the run exits zero,
   and a refusal naming the record if it does not. The bulk read fails
   as a whole and the run reads its records one at a time instead.
4. `the same GET fails every time` - there is no true plan to be had, so
   the run refuses and names the record. An unreadable record is not an
   absent one.
5. `a record the listing names and the GET does not find` - a second
   estate of two instances, the one GitHub issue #1355 was filed over.
   The proxy answers 404 NoSuchKey to every GET of one record, on the
   bulk read, on its second look and on the per-key read the run falls
   back to, while the listing goes on naming the key. A 404 is a read
   that succeeded and said no record is there, so nothing fails by
   itself: the instance has no prior state, a plan would propose
   creating it, and a destroy would propose nothing for it. `plan`,
   `plan -destroy` and `apply -destroy` each refuse with `The record
   store contradicts itself about a record`, naming the address and the
   key. The step counts the 404s the proxy served and the listings it
   forwarded in each run, and reads the bucket's versions and delete
   markers from the emulator before and after: they are the same.
6. `teardown` - both estates, each asserted by its full destroyed count.

The estate sets `retry { max_attempts = 1 }`. With the SDK's default of
three attempts a single 500 is retried away below the code this claim is
about, and the scenario would measure nothing.

The `BREAK=1` binary swallows a failed call instead of failing the read.
Its plan in step 3 reads `terraform_data.effect[4] will be created` and
`Plan: 1 to add`, for a resource that exists. The claim is proved at the
plan and not at the store's return value, because the plan is the harm.

The `BREAK_CROSSCHECK=1` binary is built without the one call to
`refuseListedButReadAsAbsent`. It is a second variable and not a second
meaning of `BREAK=1` because it corrupts a different file for a
different step, and because the two cannot stand in for each other: a
snapshot torn by `BREAK=1` is self-consistent, so the cross-check has
nothing to catch there, and only a 404 on the per-key path reaches it.
In step 5 that binary's plan reads `terraform_data.effect["plain"] will
be created`, and its `apply -destroy` reads `Apply complete! Resources:
0 added, 0 changed, 1 destroyed.` with exit 0 and the record still in
the bucket. That is #1355's output, manufactured. What produced the 404
on real S3 is still not known; the proxy stands in for it.

How many GETs run at once is `TOFU_LIVE_RECORD_READ_PARALLELISM`,
default 8. It is an environment variable and not a `record_store`
argument because it is a lever for a run that is misbehaving, not a
decision a team checks in. The unit tests behind this claim run at 1, 8
and 32: a sibling's cancellation being reported in place of the real
failure is a defect that exists only above 1.
