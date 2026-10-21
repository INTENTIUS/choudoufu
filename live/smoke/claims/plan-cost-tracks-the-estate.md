---
title: "Claim 14: A plan costs its estate"
claim: plan-cost-tracks-the-estate
---

# Claim 14: A plan costs its estate

A plan of one estate reads that estate's resources, so its cost stays put
however much else the account holds.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### plan-cost-under-foreign-load

    just smoke plan-cost-under-foreign-load
    BREAK=1 just smoke plan-cost-under-foreign-load

Needs Go. The estate is held still while a second terralith, under another
estate's marker, is applied around it. `FOREIGN_SCALE=1` (the default)
puts 79 foreign resources there; `FOREIGN_SCALE=50` puts 3,705, the row in
`live/gauntlet-scale.json`; `OWNED_SCALE` sizes the estate. The replan
must grow by less than half a call per foreign resource (a state file pays
at least one), and Cloud Control lists stay flat. `BREAK=1` asks the
account-wide question (`-adoption-only`), and the cost must explode past
three times the scoped plan.

## On Kubernetes

### k8s-plan-cost-tracks-the-estate

    just smoke k8s-plan-cost-tracks-the-estate
    BREAK=1 just smoke k8s-plan-cost-tracks-the-estate

Needs `kind` and `kubectl`. One plan's requests are counted through
`live/smoke/k8sproxy.py`, which sees this client's requests alone. Adding
80 ConfigMaps, marked for another estate or not marked at all, leaves the
count where it was (74 for a four-object estate), and every ConfigMap list
carries the estate's label selector. `BREAK=1` gives 40 of them this
estate's label, and the count must rise.
