---
title: "Claim 51: An untaggable server-minted type is refused by name"
claim: a-markerless-type-is-refused
---

# Claim 51: An untaggable server-minted type is refused by name

A type whose identity the API mints, and which has nowhere to carry a
marker, has no handle a later run could find it by. Applying one would
make an object every later plan proposes to create again. So it is
refused by name before anything is made, under the `markerless-type`
rule (`live/LIMITATIONS.md`). It is D4 of
[#2119](https://github.com/INTENTIUS/choudoufu/issues/2119).

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

No scenario is written yet, so the cell is open. AWS's veto is
`identity.MarkerlessTypes`, 159 types.

## On Kubernetes

n/a: every Kubernetes object carries `metadata.labels`, so no kind is
untaggable and server-minted.

## On GCP

### gcp-a-markerless-type-is-refused

    just smoke gcp-a-markerless-type-is-refused
    BREAK=1 just smoke gcp-a-markerless-type-is-refused

A root declares a Pub/Sub topic and a `google_monitoring_alert_policy`,
which Cloud Monitoring names itself and which has no labels argument. It
is in the google markerless list of `live/readiness.json`. The plan must
exit 1 with the markerless-type refusal naming the block, and nothing may
be created. `BREAK=1` swaps the alert policy for a service account, also
unlabelled but client-named, and the plan must go through.

Read from the code, this may fail. Every live block implies a local
record store, and lint admits a markerless type whose schema can be
recorded as record-located (`identity.LocatedType`, #270), with no
exclusion for google types, while `live/READINESS.md` says that route is
not established for GCP. It has no dated run yet, so the cell reads
unproven.
