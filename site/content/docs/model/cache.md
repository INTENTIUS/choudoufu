---
title: "The disposable cache"
weight: 8
---

# The disposable cache

Every live-block run keeps an ordinary state file as a cache:
`choudoufu-cache.tfstate`, under the `.terraform` directory stock
already gitignores. It is written at the end of each run and read at
the start of the next, and everything about it follows from one rule -
it is never consulted for ownership. Identity lives on the resources;
the cache only remembers attributes.

## What losing it costs

A read. Delete the file, corrupt it, or let it go stale for a month,
and the next plan answers identically to a fresh one - the
[staleness claim]({{< relref "/docs/claims#claim-3-staleness-costs-reads-never-results" >}})
runs that experiment on every smoke, with a cache full of dead ids.
Stale is the expected condition here; the name of the project is
fermented tofu.

## What having it buys

On a default plan: nothing, on purpose. The read pass is drift
detection; no cache freshness excuses skipping it. The cache pays
out on the one opt-in path, `-refresh=false`, where an instance the run
can vouch for - its marker verified by this run's sweep, or its
ownership attested by the record store while this run's own listing
proves it exists - is served from the cache and its wire reads are
never made. The
[unchanged-is-free claim]({{< relref "/docs/claims#claim-9-unchanged-is-free" >}})
measures the saving, and the live block's `reads = "full"` argument
turns the whole pass off
([reference]({{< relref "/docs/use/reference" >}})).

## The cache is also the exit

The file is a stock-format state file, deliberately. Copy it to
`terraform.tfstate`, remove the live block, and stock OpenTofu plans,
converges and destroys with it - the
[roundtrip claim]({{< relref "/docs/claims#claim-6-the-roundtrip---one-command-in-one-file-out" >}})
walks the whole loop and lets stock do the teardown. A cache you may
lose without cost is also a state file you may keep without ceremony,
and that symmetry is what makes leaving cheap.

## Knobs

`CHOUDOUFU_STATE_CACHE` names a different path for the file, or the
literal `off` disables it. `reads = "selective" | "full"` in the live
block (or `CHOUDOUFU_READS` per run) governs whether `-refresh=false`
may serve from it. Neither knob changes what a default plan does.
