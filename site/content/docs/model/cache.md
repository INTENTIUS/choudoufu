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

The cache is a file on the machine that ran the plan. It is not in the record
store, no other machine sees it, and nothing about it is shared between two
people or two CI jobs. The backend is the markers and the record store.

So two runs never write the same cache, and it has no bearing on concurrency.
A fresh checkout, a new laptop or a new runner plans correctly with no cache
at all. A pipeline whose runner is thrown away after every job starts cold
every time, which is how it is meant to work, and restoring the file from a CI
cache buys nothing on a default plan, as the section after next says.

## It holds what a state file holds, secrets included

The file is written unencrypted and nothing scrubs it. Every sensitive
attribute and every sensitive root output is in it, in clear. For that reason
an estate that sets `strict { secrets = "refuse" }` gets no cache at all,
written or read, unless `CHOUDOUFU_STATE_CACHE` names a path on purpose.
Everything below about what the cache buys, and about it being the exit,
applies to such an estate only with that path set. Treat a working directory that has applied the way
you would treat one holding `terraform.tfstate`. That matters most in CI,
where a cache or an artifact step that sweeps up `.terraform` carries the
values to wherever that goes.
[Secrets in the record store]({{< relref "/docs/use/secrets" >}}) has the
rest, and `CHOUDOUFU_STATE_CACHE=off` below stops the file being written.

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
out on the one opt-in path, `-refresh=false`, where an instance the run
can vouch for is served from the cache and its wire reads are never made.
Vouching means its marker was verified by this run's sweep, or its ownership
is attested by the record store while this run's own listing proves it
exists. The
[unchanged-is-free claim]({{< relref "/docs/claims/unchanged-is-free" >}})
measures the saving, and the live block's `reads = "full"` argument
turns the whole pass off
([reference]({{< relref "/docs/use/reference" >}})).

## The cache is also the exit

The file is a stock-format state file, deliberately. Copy it to
`terraform.tfstate`, remove the live block, and stock OpenTofu plans,
converges and destroys with it. The
[roundtrip claim]({{< relref "/docs/claims/roundtrip" >}})
walks the whole loop and lets stock do the teardown. A cache you may
lose without cost is also a state file you may keep without ceremony,
and that symmetry is what makes leaving cheap.

## Knobs

`CHOUDOUFU_STATE_CACHE` names a different path for the file, or the
literal `off` disables it. `reads = "selective" | "full"` in the live
block (or `CHOUDOUFU_READS` per run) governs whether `-refresh=false`
may serve from it. Neither knob changes what a default plan does.
