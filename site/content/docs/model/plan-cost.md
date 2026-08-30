---
title: "What a plan costs"
weight: 7
---

# What a plan costs

Prior state is rebuilt by reading the live system every time you plan, because
nothing stored is trusted. That reading is two costs, not one, and they grow
along different axes. Which of the two dominates depends on the size of your
estate, and the answer flips.

Both belong to hooks, and the hooks differ in when they are needed. The read
pass is unconditional and costs what stock's refresh costs. The sweep is the
adoption hook, which answers a question an operator needs during a migration
or an audit and not on an ordinary plan of an estate that is already adopted.
It runs on every plan today. That it should not is
[`rfc/20260830-stale-state-charter.md`](https://github.com/INTENTIUS/choudoufu/blob/main/rfc/20260830-stale-state-charter.md)'s
ruling; this page is the measurement that ruling rests on.

## The two terms

**The sweep asks what this estate owns. It is O(types).** One estate-filtered
tagging call covers the types whose ARNs the hand-curated join table can
resolve; every other admitted type is routed to the native leg and gets its
own list attempt. The work is set by the size of the admission table, not by
the size of your estate. Counted at `5d55f4aa9f`, and reproducible in under a
second with no cloud and no emulator:

```
go test ./internal/live/discovery/ -run TestSweepUniversePartitionIsMostlyNative
sweep universe=1027 tagging_leg=35 native_leg=992
```

Not all 992 of those reach the network. Only 502 of them can issue a
`ListResources` at all; the rest report a sweep gap without a call, because
the type is either not listable or not taggable. Measured at `5dbe452a1e` for
[#586](https://github.com/INTENTIUS/choudoufu/issues/586), where 435 of the
502 fired against the 79-instance fixture below in its unmigrated state.

**The read pass asks what each owned resource currently looks like. It is
O(resources).** One or more provider Reads per instance the plan materializes,
how many depending entirely on which resource types you have. It is the same
work a stock refresh does, and it measured equal to stock's, call for call, at
every scale below.

## The measured split, on a migrated estate

This is the day-2 shape and the one to start from: an estate already adopted,
every declared instance carrying its markers, which is where an operator
actually plans. Generated terralith at three scales, applied with stock
`terraform` and then migrated with `choudoufu live-import -approve` before
anything was counted (commit `cfd0dc58d4`, floci pin `sha256:c55d74e1`,
reported in
[`rfc/20260830-slicing-under-choudoufu.md`](https://github.com/INTENTIUS/choudoufu/blob/main/rfc/20260830-slicing-under-choudoufu.md)):

| Instances | Tagging leg | Native leg | Sweep | Read pass | Total | Read pass share |
|---|---|---|---|---|---|---|
| 79 | 1 | 521 | 558 | 148 | 706 | 21.0% |
| 301 | 2 | 521 | 592 | 556 | 1148 | 48.4% |
| 745 | 4 | 521 | 660 | 1372 | 2032 | 67.5% |

The two legs do not add up to the sweep on their own. The rest of it is the
configuration scan, 26, 58 and 124 calls at the three scales, plus a flat
ten-call pass after the sweep proper. Those four account for the sweep exactly
at 79 instances and to within one call at the two larger scales, where a type
whose scan records nothing fires no progress event and its calls fold into the
next attribution interval.

Both terms are linear and the fit is exact to one call at every point:
`sweep = 545.9 + 0.15315N`, `read pass = 1.8378N + 2.8`. They cross at **322
instances**, just past this fixture's scale 4. Below that a plan is mostly the
fixed sweep and adding resources barely moves it; above it, cost tracks your
estate.

### The read pass is the same number stock pays

Measured on both sides of the same estate, at the same three scales, the
figures are identical:

| Instances | Stock `terraform plan` | choudoufu read pass |
|---|---|---|
| 79 | 148 | 148 |
| 301 | 556 | 556 |
| 745 | 1372 | 1372 |

That is the shared term, and it is the most useful sentence on this page:
**everything choudoufu spends above stock is the sweep.** The read pass is the
AWS provider's own `Read` implementations, which stock invokes on the same
resources when it refreshes; nothing in this fork adds to them or can subtract
from them. `live/plan-budget.json` says the same of its own figures: the shape
"is a property of the AWS provider's own Read, not of anything this fork
adds."

The sweep is the term that is genuinely ours. Stock has no equivalent, because
a state file already answers the question the sweep asks.

So the honest difference is not the size of the read pass. It is that stock
can decline to refresh with `-refresh=false` and choudoufu cannot: the
projection *is* the refresh, so `-refresh` is accepted and has no effect. What
stock buys by skipping it is a plan against remembered state, and there is no
remembered state here to plan against.

### The native leg does not move

`native_sweep_calls` measured **521 in all thirteen configurations** the
slicing work covered: whole estates at all three scales above, both slices of
a two-way split, and each of eight slices of an eight-way split. It does not
grow with the estate, and it does not shrink when a configuration declares
fewer types.

The second half of that runs the wrong way round from most people's intuition,
so here is the mechanism. `sweepTypes` builds its universe by *removing* the
types the configuration declares from the admission table, so a slice
declaring five types has a sweep universe of 1022 to 1026 against the whole
estate's 1021. A small slice pays slightly more than the whole estate does.

The consequence for an already-sliced adopter is the sharp edge of this cost
model. Summed across the estate at the smallest scale, stock costs 148 API
calls whether it is one state or eight; the same estate through choudoufu
costs 744 calls at one state, 1288 at two and **4530 at eight**, because each
additional state pays the whole sweep again. Slicing redistributes stock's
refresh. It multiplies choudoufu's sweep.

## On real AWS the sweep is nearly the whole plan

Call counts say what the two terms are. Seconds say which one an operator
notices, and on this estate those are not the same answer.

`live/live-cert/terralith-scale.sh` times both binaries on the same estate
inside one certification run. From #578's real-AWS run at scale 1, 79
resources in `us-east-2`, provider warm on both sides, `TF_LOG` unset on both,
three runs each, every plan reporting zero changes so that each pair is the
same operation:

| | run 1 | run 2 | run 3 |
|---|---|---|---|
| stock `terraform plan` | 3s | 4s | 3s |
| `choudoufu plan` | 203s | 211s | 200s |

Stock finishes the read pass, the term both sides share, in three seconds.
The remaining 200 seconds is the sweep. Spread over the 558 sweep calls
counted at that scale it is about 0.36s each, which is one network round trip
apiece, and at the time of that run the sweep made them one after another.

Two bounds on that paragraph. The seconds are real AWS and the call counts are
the emulator, so 0.36s per call is an estimate built from two measurements
rather than a measured quantity;
[`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md)
sets out when two wall clocks may be combined and when they may not. And the
table predates the sweep becoming concurrent, which is the next section.

### The sweep now overlaps its own waiting

The admission table fixes how many calls there are. Nothing requires them to
be made one after another, and since
[#605](https://github.com/INTENTIUS/choudoufu/issues/605) they are not:
`Discover` prefetches the sweep's per-type listings through a bounded worker
pool, `DefaultSweepParallelism = 10`
(`internal/live/discovery/sweepconcurrency.go`), the same bound stock plans an
estate at. It covers the sweep's per-type listing and nothing else — the
config-driven scan, the tagging leg's single `GetResources`, and the parent
and record-orphan reads are untouched.

**The call count does not move, which is the point.** Measured against the
pinned emulator at four settings and both scales, 558 calls at 79 instances
and 591 at 301, identical at parallelism 1, 2, 10 and 20, with the scan-row
order and the diagnostic sequence identical too:

| Scale | Instances | par 1 | par 2 | par 10 | par 20 |
|---|---|---|---|---|---|
| 1 | 79 | 433.6ms | 266.4ms | 188.9ms | 154.9ms |
| 4 | 301 | 419.4ms | 286.7ms | 219.2ms | 173.1ms |

Those are milliseconds over loopback, so they measure the overlap and not the
saving. A repeat of each parallelism-1 row landed 18% lower (357.4ms and
355.7ms), so read the ratios as approximate. On real AWS the same change
should turn the 521-call native leg's sequential 203s into roughly a tenth of
that — `521 x 0.39s` is where the 203 comes from, and dividing it by 10 is
arithmetic, not a measurement. **Nobody has re-run the real-AWS table above
since the change.** Until someone does, the 200s column is what this page can
say happened, and the projection is a projection.

## The unmigrated estate, for contrast

The same fixture and the same pin, measured before migration with no marker on
any object (commit `f4611196e5`,
[`rfc/20260830-marker-verified-fast-projection.md`](https://github.com/INTENTIUS/choudoufu/blob/main/rfc/20260830-marker-verified-fast-projection.md)):

| Instances | Sweep | Read pass | Total | Read pass share |
|---|---|---|---|---|
| 79 | 560 | 86 | 646 | 13.3% |
| 301 | 593 | 341 | 934 | 36.5% |
| 745 | 659 | 851 | 1510 | 56.4% |

`sweep = 548.3 + 0.1486N`, `read pass = 1.1486N - 4.7`, crossing at 553
instances.

This is the adoption case, the plan you run on the way in rather than the ones
you run afterwards, and it is a lower bound on the read pass: nothing was
bound, so much of the estate never materialized. Migration leaves the sweep
alone and raises the read pass by 61% to 72%. Per instance the read pass goes
from 1.15 calls to **1.84**, and the crossover moves from 553 instances to
322. If you are budgeting from the unmigrated table, you are budgeting for a
state your estate passes through once.

## Bounds on all of the above

- **The tagging leg was available.** With `TOFU_LIVE_CLOUDCONTROL=off` the
  sweep falls back to per-type listing across the whole universe, so every
  figure here is the cheapest production shape rather than the worst one.
- **The call counts are emulator-measured.** The tagging leg is
  `ceil(tagged_resources / page)` and floci's page is 100, which is why it
  reads 1, 2 and 4 rather than 1 everywhere. `cloudcontrol.Client.GetResources`
  sets no `ResourcesPerPage`, so the real page size is the Resource Groups
  Tagging API's own default and no emulator-backed run can report it.
- **One fixture, one composition.** The 521-call native leg is a property of
  the admission table and the ARN join table rather than of the estate, but
  that is an argument; only this estate was measured, and it declares thirteen
  types.
- **AWS only.** Nothing here says anything about another provider.

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

The generated estate in the tables above measures **1.84 calls per instance**
migrated, and 1.15 unmigrated. Same tool, same code, twelve and nineteen times
below the S3 figure, because the composition is different. An estate of IAM
roles, inline policies and DNS records reads cheaply; an estate of S3 buckets
does not.

If you want a number for your estate, measure your estate. Extrapolating from
somebody else's resource type will be wrong by whatever the ratio between the
two providers' Read implementations happens to be.

The `+ 13` in that fit is worth one line of its own: three of those calls are
account- and service-level probes that fire once regardless of N. They are
2.9% of the total at N=20 and 0.06% at N=1000. A fixed term looks expensive
on a small estate and disappears on a large one, which is the opposite of how
the sweep behaves and a good reason to fit a line rather than divide once.

## Emulator wall clock is not on this page

Every cost figure here is a call count, with one deliberate exception. Seconds
measured against the pinned emulator grade the machine the test ran on, which
is why `live/plan-budget.json` records a wall clock and never gates on it. The one
timing table above is real AWS, where the seconds are network latency rather
than a property of whatever laptop ran the suite.
[`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md)
covers the distinction and the three other questions an emulator-backed
measurement cannot answer.
