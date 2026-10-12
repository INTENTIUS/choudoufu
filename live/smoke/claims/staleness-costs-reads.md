---
title: "Claim 3: The cache changes the cost, never the answer"
claim: staleness-costs-reads
---

# Claim 3: The cache changes the cost, never the answer

A stale state file is the classic failure: the file is the record, so its
lies become your plans. Here the state file is a cache, never consulted
for ownership, and live reads win every disagreement. A fresh cache, an
ancient one and none at all give the same plan; what differs is how much
reading the run does. Where a record is the only copy of an identity,
losing the cache costs nothing and losing the record is named, never
guessed. That last sentence is shown on AWS by claim 5's
[a-killed-apply-hides-nothing](recovery-is-a-rerun.md#on-aws), whose step
7 deletes the cache and `.terraform` around record-only identities and
plans `No changes.`; it was record-only-survives-cache-loss (claim 17)
until that scenario was dropped as a duplicate.

The cost half was claim 9, "Unchanged is free", until it merged here.
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

### staleness-costs-reads

    just smoke staleness-costs-reads
    BREAK=1 just smoke staleness-costs-reads

The run manufactures a genuinely ancient cache (apply, save it, destroy,
apply again, so it remembers only dead ids), then plans against the fresh
cache, the ancient one and no cache: the outputs are byte-identical. A
setting changed out of band shows through a fresh cache as drift.
`-refresh=false` is the one path that serves reads from cache, only for
instances the sweep has verified, and the run prints its request count
beside the uncached one with equal outputs. The same holds for values in
the record store, including a phantom the cache remembers and nothing else
does. `BREAK=1` moves the live world mid-comparison, and the equality
check must notice.

### unchanged-is-free (claim 9 until it merged here)

    just smoke unchanged-is-free
    BREAK=1 just smoke unchanged-is-free

The bill is counted live, in the run's own debug stream; for record-backed
resources the record is the attestation on every default plan. `BREAK=1`
overwrites the record with garbage, and the run must refuse naming the
address.

### cache-serves-the-whole-estate (claim 10 until #1817, then claim 9)

    just smoke cache-serves-the-whole-estate
    BREAK=1 just smoke cache-serves-the-whole-estate

A converged estate's VPCs, subnets and security groups are served from
cache on `-refresh=false`, vouched by the estate sweep, so one estate of a
terralith plans at the speed of reading a file. `BREAK=1` deletes a
resource out of band; the sweep no longer vouches it and the plan must
surface it.

## On Kubernetes

### k8s-staleness-costs-reads

    just smoke k8s-staleness-costs-reads
    BREAK=1 just smoke k8s-staleness-costs-reads

Needs `kind` and `kubectl`. The run applies an estate plus one extra
ConfigMap, keeps that cache aside, destroys everything, drops the extra
block and applies again, so the kept cache remembers four dead uids and
an object that exists nowhere. Plans under the fresh cache, the ancient
one and no cache are byte-identical and the phantom is in none of them. A
ConfigMap edited with kubectl then shows through a fresh cache as one
in-place update, and one apply puts it back. `BREAK=1` makes that edit
before the comparison is read back, and the plan must move. The
`-refresh=false` path, the one that serves from the cache, is the next
proof.

### k8s-unchanged-is-free (claim 9 until it merged here)

    just smoke k8s-unchanged-is-free
    BREAK=1 just smoke k8s-unchanged-is-free

Needs `kind` and `kubectl`. An unchanged estate is planned with
`-refresh=false` through `live/smoke/k8sproxy.py`, under the default and
under `CHOUDOUFU_READS=full`. The default serves the objects the label
sweep vouched for from the cache and skips their reads (65 requests
against 74 on the first run), and the two plans print the same answer.
`BREAK=1` deletes the cache first, and the plan must serve nothing and pay
the full price.
