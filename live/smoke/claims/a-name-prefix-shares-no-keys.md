---
title: "Claim 28: Two estates whose names prefix one another share a bucket and none of each other's keys"
claim: a-name-prefix-shares-no-keys
---

# Claim 28: Two estates whose names prefix one another share a bucket and none of each other's keys

Every estate's records live in one bucket, one key prefix per estate.
S3's LIST matches a plain string prefix, so `tofu-records/prod` also
names `tofu-records/prod-eu`, and estate names prefix one another all the
time. An object tag cannot condition a LIST, which touches no object, so
the trailing slash on the prefix is the only thing that keeps one
estate's listing, and the bulk read that follows it, out of its
neighbour's records.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info), the AWS CLI is installed, and Go is installed. From the
repo root run:

  just smoke a-name-prefix-shares-no-keys

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke a-name-prefix-shares-no-keys and report the "caught"
line: it rebuilds choudoufu with the delimiter dropped and the run must
be seen fetching its neighbour's record.
```

As the run prints them:

1. `two estates, one bucket` - `smoke-prod` and `smoke-prod-eu` each
   apply one record-backed resource into the same bucket. The listing
   shows both estates' objects side by side.
2. `the hazard, with no choudoufu in the loop` - the AWS CLI lists
   `tofu-records/smoke-prod/` and gets one estate, then lists
   `tofu-records/smoke-prod` and gets both. If the bare prefix did not
   return the neighbour, the store would not have the hazard and the
   scenario would stop there rather than pass.
3. `what smoke-prod asks the bucket for` - `smoke-prod` plans with the
   request log on. Every LIST it sends carries a prefix ending in a
   slash, and no request in the run names `smoke-prod-eu`. The step
   first requires the run's own LIST requests to be in the log, so an
   empty log cannot pass.
4. `smoke-prod tears itself down` - the neighbour's keys are identical
   before and after, and its plan is still empty.

The `BREAK=1` run cannot corrupt anything in the cloud, because the
delimiter is a line inside the binary. It rebuilds choudoufu from the
checkout with `staterecord.NamespacePrefix` no longer appending its
slash, using `go build -overlay` so the source tree is untouched, and
requires the wire to show `smoke-prod` fetching `smoke-prod-eu`'s
record. It refuses to run against `CHOUDOUFU_BIN` or
`CHOUDOUFU_VERSION`.

What the defect reaches was measured on #1335, and it is narrower than
"one estate destroys another's records". An estate that lists its
neighbour's keys reads the neighbour's record payloads, secret material
included. An estate with no records of its own stops sweeping in full,
because its listing is no longer empty, and a removal it should have
proposed goes missing. It does not propose destroying the neighbour's
resources: a listed key is decoded only if it sits under the estate's
own delimited prefix, and the record is then re-read under that prefix
before anything acts on it. Both of those checks are independent of how
the keys were listed, and both are pinned by unit tests beside this
claim.
