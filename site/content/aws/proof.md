---
title: "Proof"
weight: 5
description: "Which claims hold on AWS, how many real estates clear every stage, what a plan costs against stock, and how to run any of it yourself."
deeper:
  - "[What a plan costs]({{< relref \"/docs/model/plan-cost\" >}}): the short answer, with links to every figure, its fixture and its commit."
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

With no `live` block, nothing: `terraform plan`, `tofu plan` and `choudoufu
plan` issued exactly the same API calls over the same estate. A plan of an
adopted estate is at call parity with stock, within a handful of calls. The
real cost is the estate-wide sweep, which an adoption, an audit or a brand-new
estate pays and an established one does not.
[What a plan costs]({{< relref "/docs/model/plan-cost" >}}) has the numbers.
