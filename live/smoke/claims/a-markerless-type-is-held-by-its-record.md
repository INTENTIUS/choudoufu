---
title: "Claim 51: A markerless type is held by its record, or refused"
claim: a-markerless-type-is-held-by-its-record
---

# Claim 51: A markerless type is held by its record, or refused

Some types have an identity the API mints and nowhere to carry a marker,
so neither a label nor the configuration can find one again. The
estate's record can: the apply writes the identity the API returned, and
every later plan reads it back (the record-located route, #270). Where
the identity cannot be recorded, because the record would have to carry
a secret or there is no record store, the type is refused by name under
the `markerless-type` rule (`live/LIMITATIONS.md`). It is D4 of
[#2119](https://github.com/INTENTIUS/choudoufu/issues/2119), restated to
the record rung.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

No scenario is written yet, so the cell is open. AWS's veto is
`identity.MarkerlessTypes`, 159 types. Under a live block a type whose
identity a record can carry is held by its record, and one it cannot,
such as `aws_wafv2_api_key`, whose id is the secret, is refused by name.

## On Kubernetes

n/a: every Kubernetes object carries `metadata.labels`, so no kind is
untaggable and server-minted.

## On GCP

### gcp-a-markerless-type-is-held-by-its-record

    just smoke gcp-a-markerless-type-is-held-by-its-record
    BREAK=1 just smoke gcp-a-markerless-type-is-held-by-its-record

A labelled Secret Manager secret and a
`google_secret_manager_secret_version`, which Secret Manager numbers
itself and which has no labels argument, are planned and applied under a
live block. The version's identity goes into the estate's record, and
with the cache deleted the plan must read `No changes.` from that record.

`BREAK=1` puts the same two blocks in a root with no live block, so no
record store exists to hold the version's identity, and `live-check` must
refuse it by name under `markerless-type`. The refusal is shown that way
because no GCP type of the other kind exists at the pin: against the real
hashicorp/google and google-beta 8.6.0 schemas, every markerless type the
provider serves can be recorded. It has no dated run yet, so the cell
reads unproven.
