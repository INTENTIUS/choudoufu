---
title: "Claim 9: Unchanged is free"
claim: unchanged-is-free
---

# Claim 9: Unchanged is free

Re-planning what did not change should not cost a full re-read. On
`-refresh=false` an instance the run can vouch for is served from the
cache and its reads are never made, for the whole estate, server-assigned
resources included. A default plan still refreshes, because that read is
drift detection, and `reads = "full"` turns the saving off without
changing any plan.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### unchanged-is-free

    just smoke unchanged-is-free
    BREAK=1 just smoke unchanged-is-free

The bill is counted live, in the run's own debug stream; for record-backed
resources the record is the attestation on every default plan. `BREAK=1`
overwrites the record with garbage, and the run must refuse naming the
address.

### cache-serves-the-whole-estate (claim 10 until #1817)

    just smoke cache-serves-the-whole-estate
    BREAK=1 just smoke cache-serves-the-whole-estate

A converged estate's VPCs, subnets and security groups are served from
cache on `-refresh=false`, vouched by the estate sweep, so one estate of a
terralith plans at the speed of reading a file. `BREAK=1` deletes a
resource out of band; the sweep no longer vouches it and the plan must
surface it.
