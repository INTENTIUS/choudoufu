---
title: "Claim 14: A plan costs its estate"
claim: plan-cost-tracks-the-estate
---

# Claim 14: A plan costs its estate

A bound state file makes every plan pay for everything in it. Here
ownership is a tag, so a plan of one estate reads that estate's
resources, and the cost stays put however much else the account
holds.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### plan-cost-under-foreign-load (claim 20 until #1817)

    just smoke plan-cost-under-foreign-load
    BREAK=1 just smoke plan-cost-under-foreign-load

The estate is held still while a second terralith, under another
estate's marker, is applied around it. `FOREIGN_SCALE=1` (the default)
puts 79 foreign resources there in about five minutes; `FOREIGN_SCALE=50`
puts 3,705, the row in `live/gauntlet-scale.json`; `OWNED_SCALE` sizes the
estate. The replan must grow by less than half a call per foreign
resource (a state file pays at least one), and the legs that read the
account rather than the estate are named from the run's own log: Cloud
Control lists stay flat, and the unfiltered IAM policy list is where the
growth comes from. Needs Go. `BREAK=1` asks the account-wide question
(`-adoption-only`), and the cost must explode past three times the scoped
plan. #1817 folded `plan-cost-tracks-the-estate.sh`, the same steps
against eight hand-written log groups, into this one; the measured tables
are in [what a plan costs](https://intentius.io/choudoufu/docs/model/plan-cost/).

## On Kubernetes

### k8s-plan-cost-tracks-the-estate

    just smoke k8s-plan-cost-tracks-the-estate
    BREAK=1 just smoke k8s-plan-cost-tracks-the-estate

Needs `kind` and `kubectl`. One plan's requests are counted through
`live/smoke/k8sproxy.py`, which sees this client's requests and not the
controllers'. Adding 80 ConfigMaps, marked for another estate or not
marked at all, leaves the count where it was (74 for a four-object estate
on the first run), and no ConfigMap list goes out without the estate's
label selector. `BREAK=1` gives 40 of them this estate's label, and the
count must rise. There is no cross-kind label-filtered list, so a plan's
floor is one label-selected list per kind the cluster serves.
