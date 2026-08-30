---
title: "What a plan costs"
weight: 7
---

# What a plan costs

There is no file of record, so prior state is rebuilt by reading the live
system every time you plan. That reading is two costs, not one, and they grow
along different axes. Which of the two dominates depends on the size of your
estate, and the answer flips.

## The two terms

**The sweep asks what this estate owns. It is O(types).** One estate-filtered
tagging call covers the types whose ARNs the hand-curated join table can
resolve; every other admitted type gets its own list attempt, most of which
have no list route and cost nothing. The work is set by the size of the
admission table, not by the size of your estate. Counted at `cfd0dc58d4`, and
reproducible in under a second with no cloud and no emulator:

```
go test ./internal/live/discovery/ -run TestSweepUniversePartitionIsMostlyNative
sweep universe=1027 tagging_leg=35 native_leg=992
```

**The read pass asks what each owned resource currently looks like. It is
O(resources).** Roughly one provider Read per instance the plan materializes,
which is exactly the work a stock refresh does.

Measured against a generated estate at three scales (commit `f4611196e5`,
floci pin `sha256:c55d74e1`, reported in
[`rfc/20260830-marker-verified-fast-projection.md`](https://github.com/INTENTIUS/choudoufu/blob/main/rfc/20260830-marker-verified-fast-projection.md)):

| Instances | Sweep | Read pass | Total | Read pass share |
|---|---|---|---|---|
| 79 | 560 | 86 | 646 | 13.3% |
| 301 | 593 | 341 | 934 | 36.5% |
| 745 | 659 | 851 | 1510 | 56.4% |

Both terms are linear and the fit is exact to one call at the middle point:
`sweep = 548.3 + 0.1486N`, `read pass = 1.1486N - 4.7`. They cross at about
**553 instances**. Below that a plan is mostly the fixed sweep and adding
resources barely moves it; above it, cost tracks your estate.

Two bounds on that table. The fixture is unmigrated, so nothing was bound and
the read pass is a **lower bound** — it measures the adoption case, not
day-2. And it was taken with the tagging leg available; with
`TOFU_LIVE_CLOUDCONTROL=off` the sweep falls back to per-type listing across
the whole universe, so these are the cheapest production shape rather than
the worst one.

## The read pass is the provider's own cost

The per-instance calls are the AWS provider's `Read` implementations. They
are the same calls stock OpenTofu makes when it refreshes, on the same
resources, and nothing in this fork adds to them or can subtract from them.
`live/plan-budget.json` says so of its own figures: the shape "is a property
of the AWS provider's own Read, not of anything this fork adds."

The sweep is the term that is genuinely ours. Stock has no equivalent,
because a state file already answers the question the sweep asks.

The honest difference, then, is not the read pass. It is that stock can
decline to refresh with `-refresh=false` and choudoufu cannot: the projection
*is* the refresh, so `-refresh` is accepted and has no effect. What stock
buys by skipping it is a plan against remembered state; there is no
remembered state here to plan against.

## Do not carry one resource type's slope to another

This is the mistake most worth avoiding, and it has already been made once in
an issue.

`live/plan-budget.json` ratchets an `aws_s3_bucket` estate at **22 calls per
instance**, fitting `calls_total = 22*N + 13` exactly at N=20, 200 and 1000
(453, 4413, 22013). That number is not a property of choudoufu. `aws_s3_bucket`
is an unusually chatty Read: a dozen subresource GETs for ACL, CORS,
encryption, lifecycle, logging, object lock, policy, replication, request
payment, versioning, website and acceleration, plus the parent-read children
beside them.

The generated estate in the table above measures **1.15 calls per resolved
instance**. Same tool, same code, a factor of twenty apart, because the
composition is different. An estate of IAM roles, inline policies and DNS
records reads cheaply; an estate of S3 buckets does not.

If you want a number for your estate, measure your estate. Extrapolating from
somebody else's resource type will be wrong by whatever the ratio between the
two providers' Read implementations happens to be.

The `+ 13` in that fit is worth one line of its own: three of those calls are
account- and service-level probes that fire once regardless of N. They are
2.9% of the total at N=20 and 0.06% at N=1000. A fixed term looks expensive
on a small estate and disappears on a large one, which is the opposite of how
the sweep behaves and a good reason to fit a line rather than divide once.

## Wall clock is not on this page

Every figure here is a call count, deliberately. Seconds measured against the
pinned emulator grade the machine the test ran on, which is why
`live/plan-budget.json` records a wall clock and never gates on it.
[`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md)
covers that and the three other questions an emulator-backed measurement
cannot answer.
