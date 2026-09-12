---
title: "Claim 9: Unchanged is free"
weight: 9
claim: unchanged-is-free
---

# Claim 9: Unchanged is free

Re-planning an estate that did not change should not cost a full
re-read of it, and here it does not. On the `-refresh=false` path, an
instance the run can vouch for is served from the state cache and its
wire reads are never made - the bill is measured live, in the run's own
debug stream. The whole pass answers to one estate-level argument:
`reads = "full"` in the live block turns it off (`CHOUDOUFU_READS`
overrides per run), and turning it off may change the price but never
the plan. For record-backed resources the attestation is the record
itself, on every default plan, with nothing opted into.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke unchanged-is-free

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke unchanged-is-free and report the "caught" line: it
overwrites a record with garbage, and the run must refuse by name
rather than plan against made-up values.
```

The steps as they print:

1. `stand the estate up` - an estate and a fresh cache, nothing changed
   since.
2. `the free re-plan, and the argument that refuses it` - the same
   `-refresh=false` plan runs under the default policy and again under
   `CHOUDOUFU_READS=full`. Selective serves the vouched instances and
   the request count drops; full serves nothing and pays every read;
   the two outputs must not differ by a byte. The toggle prices the
   plan, never changes it.
3. `teardown the cloud estate`.
4. `the record-backed half - the record is the attestation` - a
   `terraform_data`'s record is edited behind the tool's back, and the
   next default plan surfaces the named reconvergence (`~ input`). The
   record is not a cache of the values; it is the values.

The `BREAK=1` run overwrites the record with garbage. The run must fail
with the record refusal, naming the exact address - a store that cannot
answer never improvises. Default plans are untouched by all of this:
they read fully under either policy, because the read is drift
detection (claim 3 pins that forever).
