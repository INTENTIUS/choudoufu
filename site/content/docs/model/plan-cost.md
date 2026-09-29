---
title: "What a plan costs"
weight: 7
aliases: ["/docs/what-you-pay/"]
---

# What a plan costs

A plan reads the live system and not a file, so it makes more API calls than
a plan that trusts a state file. How many more depends on which of two things
it has to do.

## The two terms

The **read pass** reads each resource you own. It costs what stock's refresh
costs for the same resources, call for call.

The **sweep** looks for resources carrying your estate's marker that no block
declares. Its width comes from how many resource types are admitted, and not
from how big your estate is. It is the expensive one.

An established estate does not pay the full sweep. Its own records say which
types it holds, so the sweep narrows to those. The first plan of a new estate,
an adoption, and an audit have nothing to narrow by and sweep in full.

## The numbers

Measured on a 79-instance estate against the pinned emulator, at commit
`{{< scale-num scale="1" path="commit" short="true" >}}` on
{{< scale-num scale="1" path="date" >}}:

| | choudoufu | stock |
|---|---|---|
| An ordinary plan | {{< scale-num scale="1" path="plan_calls.cold.choudoufu" >}} calls | {{< scale-num scale="1" path="plan_calls.cold.stock" >}} |
| The same plan again | {{< scale-num scale="1" path="plan_calls.warm.choudoufu" >}} | |
| A full-sweep audit | {{< scale-num scale="1" path="audit_calls.total.choudoufu" >}} | {{< scale-num scale="1" path="audit_calls.total.stock" >}} |

These are API calls against an emulator. They have not been measured in money,
and wall-clock time on an emulator says little about AWS.

## Turning a phase down

Each phase has its own bound on how many calls it has in flight. Neither is
stock's `-parallelism`, which bounds the graph walk.

| Variable | Bounds | Default |
|---|---|---|
| `TOFU_LIVE_SWEEP_PARALLELISM` | the sweep's per-type list calls | 10 |
| `TOFU_LIVE_READ_PARALLELISM` | the read pass's per-instance import and read | 10 |

Lower them if an account's APIs push back. `-refresh=false` serves unchanged
resources from [the cache]({{< relref "/docs/model/cache" >}}) and skips
their reads.

## The full measurements

Every figure, fixture and commit behind this page is in the repository:
[`live/costs/plan-cost.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/costs/plan-cost.md)
for the sweep and read pass at each scale, and
[`live/costs/what-you-pay.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/costs/what-you-pay.md)
for adoption, migration, day two at scale and the real-AWS runs. The machine
readable records are `live/gauntlet-scale.json`.
