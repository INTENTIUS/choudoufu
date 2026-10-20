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

## On Azure

### az-a-markerless-type-is-held-by-its-record

    just smoke az-a-markerless-type-is-held-by-its-record
    BREAK=1 just smoke az-a-markerless-type-is-held-by-its-record

No Azure type is minted outright, so none is on the markerless list. Seven
admitted untaggable azurerm types mint a GUID when their name is left out
(`live/READINESS.md`, Azure). An `azurerm_management_group` with no name
is planned and applied under a live block; the name azurerm chose goes
into the estate's record, and with the cache deleted the plan must read
`No changes.` from that record.

`BREAK=1` puts the same block in a root with no live block, so no record
store exists to hold the minted name, and `live-check` must refuse
`azurerm_management_group.minted` by name ("Unmarked apply of a
marker-only resource", the name omitted). floci-az's generic ARM store,
served since 30000b0, keeps subscription-scoped ids only, so a management
group's tenant-scoped id still answers 404 there, and resource Graph is served by floci-az since 30000b0, but the
sweep's query uses `union`, `mv-expand` and `bag_keys`, which its KQL
subset refuses ([live/FLOCI.md](../../FLOCI.md#azure-floci-az)); the
`BREAK=1` arm reads the configuration offline and needs neither. It has no dated run yet, so the cell reads unproven.
