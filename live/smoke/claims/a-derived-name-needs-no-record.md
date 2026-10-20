---
title: "Claim 48: A derived identity needs no record and no cache"
claim: a-derived-name-needs-no-record
---

# Claim 48: A derived identity needs no record and no cache

Some types cannot carry a marker: the platform gives them no tags or
labels. Their identity is derivable instead, recomputed from the
configuration on every run. This claim says that derivation is enough on
its own: with the record store emptied and the cache deleted, a declared
instance of such a type still plans `No changes.`. It is D1 of
[#2119](https://github.com/INTENTIUS/choudoufu/issues/2119), the inventory
of claims for derivable but unmarkable types.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

No scenario is written yet, so the cell is open. AWS composes a
declaration-carried type's identity from configuration and identified
parents on every run (`live/RECOVERY.md`).

## On Kubernetes

No scenario is written yet, so the cell is open. Kubernetes has no
whole-object type of this tier; its six field-granular types
([#1191](https://github.com/INTENTIUS/choudoufu/issues/1191)) are the form
it takes there.

## On GCP

### gcp-a-derived-name-needs-no-record

    just smoke gcp-a-derived-name-needs-no-record
    BREAK=1 just smoke gcp-a-derived-name-needs-no-record

A `google_compute_network` and a `google_compute_firewall` on it are
applied on floci-gcp. Neither has a labels argument; each is client-named,
so its project and name are its whole identity. The scenario deletes
`.tofu-records` and the state cache, and the next plan must read
`No changes.` from the emulator and the configuration alone.

`BREAK=1` renames the firewall in the configuration once the store and
the cache are gone. The derived name now matches nothing live, so the
plan must propose exactly one create and leave the old firewall alone.
It has no dated run yet, so the cell reads unproven.
