---
title: "Proof"
weight: 5
description: "Which claims hold on AWS, how many real estates clear every stage, what a plan costs against stock, and how to run any of it yourself."
deeper:
  - "[What you pay, and when]({{< relref \"/docs/what-you-pay\" >}}): every figure with its fixture, commit and whether it came from the emulator or a real account."
  - "[What a plan costs]({{< relref \"/docs/model/plan-cost\" >}}): the sweep and the read pass, measured separately."
  - "[The claims]({{< relref \"/docs/claims\" >}}): each one a scenario, with its steps, its `BREAK=1` inversion, and where a real account confirmed it."
  - "[How close AWS is]({{< relref \"/docs/progress\" >}}): every stage, every estate, every run's commit and emulator pin."
  - "[The smoke harness](https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/README.md): every knob, including pinning the emulator and the binary."
---

# Proof

Two kinds of evidence, never averaged together.

## Real estates through fixed stages

An estate is a real OpenTofu or Terraform configuration, pinned by commit, run
through every active stage side by side with stock OpenTofu against the
pinned emulator, and diffed. An estate is clear when every headline stage
passes.

{{< gauntlet-bars >}}

The stages cover the whole life of an estate: cold deploy by stock, migrate,
replan from nothing, no-op apply, drift and reconverge, rename, remove,
change count, replace, crash between create and destroy, teardown, plan then
apply, greenfield, and the strict profile. A real-account certification run
is recorded separately from the bars and counts toward neither.

## Claims you can run

Each claim is a smoke scenario: Docker plus the local emulator, one to six
minutes, exit 0 only when every assertion held. Every scenario also runs
inverted: under `BREAK=1` it manufactures the corruption the claim guards
against and passes only by catching it.

{{< claims-table >}}

Every AWS cell is proven. The Kubernetes column is what the [Kubernetes
proof page]({{< relref "/kubernetes/proof" >}}) explains.

## Run one now

```
just smoke import
```

That stands a stock estate up, deletes its state file, and plans it empty
from markers alone. Paste the README's agent prompt to a coding agent and it
runs the whole thing and reports each verdict line.

## What it costs against stock

The price is not one number, because it is not paid on every run. It depends
on what the run is doing.

### A configuration with no live block

Nothing. Over the same estate, `terraform plan`, `tofu plan` and `choudoufu
plan` issued exactly the same API calls. Lint, the refusals, marker stamping,
discovery and the projection each sit behind a guard a missing live block
turns off.

### A plan of an adopted estate

At call parity with stock, or slightly under it. On a real account at 745
resources the two sides came in at 1416 calls against 1413 (`d359210978`),
then 1449 against 1404 (`02885d2fd6`). The residual is a handful of calls
that can be diffed action by action, not a percentage.

There is no wall-clock figure. The three real-AWS sessions that produced one
were comparing a cached plan against an uncached one, and the page behind
this one says why they are withdrawn rather than restated.

### Adopting, auditing, or rebuilding identity

The estate-wide sweep is the real cost. It lists everything in the account
that carries this estate's marker and reads the address off what comes back:
one tagging-API call per hundred tagged resources, plus a per-type native leg
that was measured at 512 calls on a 79-instance estate (`5ff7f43f5b`) and
does not shrink when an estate is sliced into several. A plan of an estate
that has its own record store to narrow by does not pay it; a fresh estate,
or one mid-migration, does.

### What the cache buys

On a default plan, nothing, on purpose: the read pass is drift detection. On
`-refresh=false`, an instance the run can vouch for is served from the cache
and its reads are never made. Losing the cache costs a read; a stale cache
cannot change a plan, and one scenario runs that experiment on every smoke.
