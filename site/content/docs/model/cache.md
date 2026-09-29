---
title: "The disposable cache"
weight: 8
---

# The disposable cache

Every live-block run keeps an ordinary state file as a cache:
`choudoufu-cache.tfstate`, under the `.terraform` directory stock
already gitignores. It is written at the end of each run and read at
the start of the next, and everything about it follows from one rule:
it is never consulted for ownership. Identity lives on the resources, and
the cache only remembers attributes.

## It is on the client, and it is not part of the backend

The cache is a file on the machine that ran the plan: not in the record
store, seen by no other machine, shared between no two people or CI jobs.
The backend is the markers and the record store. Two runs never write the
same cache, so it has no bearing on concurrency - a fresh checkout, a new
laptop or a thrown-away CI runner plans correctly with no cache at all, and
restoring one from a CI cache buys nothing on a default plan, as the section
after next says.

Ruled 2026-09-26: this is by ruling, not accident. The cache stays
local - disposable, per working copy, never consulted for ownership. A
shared cache is not wanted; if it ever is, that is a separate feature with
its own name, not a setting here.

## It holds what a state file holds, secrets included

The file is written unencrypted and nothing scrubs it: every sensitive
attribute and root output is in it, in clear. Treat an applied working
directory like one holding `terraform.tfstate`, especially in CI, where an
artifact step sweeping up `.terraform` carries the values along.

An estate that sets `strict { secrets = "refuse" }` gets no cache at all,
written or read, unless `CHOUDOUFU_STATE_CACHE` names a path on purpose.
[Secrets in the record store]({{< relref "/docs/use/secrets" >}}) has the
rest.

## What losing it costs

A read. Delete the file, corrupt it, or let it go stale for a month,
and the next plan answers identically to a fresh one. The
[staleness claim]({{< relref "/docs/claims/staleness-costs-reads" >}})
runs that experiment on every smoke, with a cache full of dead ids.
Stale is the expected condition here; the name of the project is
fermented tofu.

## What having it buys

On a default plan: nothing, on purpose. The read pass is drift
detection; no cache freshness excuses skipping it. The cache pays
out on the one opt-in path, `-refresh=false`, where a vouched-for instance is
served from the cache with no wire read. Vouching means the marker was
verified by this run's sweep, or the record store attests ownership while
this run's own listing proves the instance exists. The
[unchanged-is-free claim]({{< relref "/docs/claims/unchanged-is-free" >}})
measures the saving, and the live block's `reads = "full"` argument
turns the whole pass off
([reference]({{< relref "/docs/use/reference" >}})).

## The cache is also the exit

The file is a stock-format state file on purpose. Copy it to
`terraform.tfstate`, remove the `live` block, and stock OpenTofu carries on
with it. The [roundtrip claim]({{< relref "/docs/claims/roundtrip" >}}) walks
the whole loop.

## Knobs

`CHOUDOUFU_STATE_CACHE` names a different path for the file, or the
literal `off` disables it. `reads = "selective" | "full"` in the live
block (or `CHOUDOUFU_READS` per run) governs whether `-refresh=false`
may serve from it. Neither knob changes what a default plan does.
