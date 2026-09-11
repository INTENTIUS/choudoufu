---
title: "Cost"
weight: 5
description: "What a plan pays against stock, when, and the one step that is genuinely expensive."
deeper:
  - "[What you pay, and when]({{< relref \"/docs/what-you-pay\" >}}): every figure with its fixture, commit and whether it came from the emulator or a real account."
  - "[What a plan costs]({{< relref \"/docs/model/plan-cost\" >}}): the sweep and the read pass, measured separately."
  - "Claims [3]({{< relref \"/docs/claims/staleness-costs-reads\" >}}), [9]({{< relref \"/docs/claims/unchanged-is-free\" >}}), [14]({{< relref \"/docs/claims/plan-cost-tracks-the-estate\" >}}) and [20]({{< relref \"/docs/claims/plan-cost-under-foreign-load\" >}})."
---

# Cost

The price is not one number, because it is not paid on every run. It depends
on what the run is doing.

## A configuration with no live block

Nothing. Over the same estate, `terraform plan`, `tofu plan` and `choudoufu
plan` issued exactly the same API calls. Lint, the refusals, marker stamping,
discovery and the projection each sit behind a guard a missing live block
turns off.

## A plan of an adopted estate

At call parity with stock, or slightly under it. On a real account at 745
resources the two sides came in at 1416 calls against 1413 (`d359210978`),
then 1449 against 1404 (`02885d2fd6`). The residual is a handful of calls
that can be diffed action by action, not a percentage.

There is no wall-clock figure. The three real-AWS sessions that produced one
were comparing a cached plan against an uncached one, and the page behind
this one says why they are withdrawn rather than restated.

## Adopting, auditing, or rebuilding identity

The estate-wide sweep is the real cost. It lists everything in the account
that carries this estate's marker and reads the address off what comes back:
one tagging-API call per hundred tagged resources, plus a per-type native leg
that was measured at 512 calls on a 79-instance estate (`5ff7f43f5b`) and
does not shrink when an estate is sliced into several. A plan of an estate
that has its own record store to narrow by does not pay it; a fresh estate,
or one mid-migration, does.

## What the cache buys

On a default plan, nothing, on purpose: the read pass is drift detection. On
`-refresh=false`, an instance the run can vouch for is served from the cache
and its reads are never made. Losing the cache costs a read; a stale cache
cannot change a plan, and one scenario runs that experiment on every smoke.
