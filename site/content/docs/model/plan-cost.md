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
That it should not run on every plan was
[`rulings/20260830-stale-state-charter.md`](https://github.com/INTENTIUS/choudoufu/blob/main/rulings/20260830-stale-state-charter.md)'s
ruling, and `09d180f921` implemented it. This page is the measurement that
ruling rested on, and it is still the measurement of what the sweep costs when
a run does take it.

> **Read this page as the sweep's cost, not as a plan's cost.** Since
> `09d180f921` a plan of an estate that has its own evidence to narrow by —
> types declared in configuration, or holding a key in the record store — no
> longer enumerates the whole admission table, and the 79-instance fixture
> measured throughout this page went from **710 API calls to 157**, against
> stock's 150. Every full-sweep figure below still describes an adoption, an
> audit, a rebuild from markers, or any run where the narrowing has nothing to
> narrow by, because every gate fails toward doing the work. The exact gates
> are [below](#when-the-native-leg-is-narrowed-and-when-it-is-not). It no
> longer describes an ordinary plan of an adopted estate. The scales above 79
> instances have not been re-measured in calls since. For what a steady-state
> plan costs, and what is still outstanding, see
> [what you pay, and when]({{< relref "/docs/what-you-pay" >}}).

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
work a stock refresh does, and it measured equal to stock's per resource at
every scale below, to the call.

## When the native leg is narrowed, and when it is not

The narrowing is the whole difference between the figures on this page and
what a steady-state plan costs, so the conditions under which it happens are
operator-facing rather than an implementation note. They are in
[`internal/live/discovery/nativesweep.go`](https://github.com/INTENTIUS/choudoufu/blob/main/internal/live/discovery/nativesweep.go),
and every one of them fails toward doing the full work.

**It narrows the native per-type leg and nothing else.** The tagging leg's
single estate-filtered `GetResources`, the record store's own orphan walk, and
the parent-read and fold-child legs all run exactly as before. The one
question being declined is the account inventory, which asks what is in my
account that this estate does not know about.

**It narrows only where there is positive evidence to narrow by, and that
evidence is the estate's own record store.** All four of these take the full
universe:

- the run asked for the account inventory (`-adoption-only`, or
  `TOFU_LIVE_COLLECT_UNCLAIMED=1`);
- no record store opened for the pass;
- a record store opened and would not list;
- its listing came back **empty**.

The last two are the ones worth planning around. A fresh estate, and an estate
whose store has not been written yet, still pay the whole admission table.
That is by design rather than an oversight, because an estate with no record
of itself has only its markers to say what it owns, and it is also the
rebuild-from-markers path. Note what the gate is not: declaring a
`record_store` block, since an estate that names none gets an implied local
one anyway.

Given a non-empty store, the kept set is deliberately generous. It holds every
type the configuration declares an instance of, every type the declared set
routed through discovery or through the record rung, and every type the store
holds a key for. A false positive there costs one list call; a false negative
costs a removal nobody proposes.

A narrowed plan gives up exactly one shape of removal, and it is worth stating
in full. Take a live object carrying this estate's marker, of a type that the
configuration does not declare, that the record store has no entry for, and
that the ARN join table cannot place from an ARN. Its destroy is not proposed.
Every other removal is unaffected, which
`TestNarrowedNativeSweepStillProposesRemovals` and the `day2_remove` gauntlet
stages check by value rather than by argument.

**A narrowed plan says so.** The "Foreign resources" section prints the count
it skipped and the command that asks anyway, rather than letting silence read
as "there is nothing out there":

```
This run did not ask which live resources carry no ownership marker at all, so
987 admitted types this estate has no record of ever having used were not
listed. Every resource this estate owns was still swept for. Run "choudoufu
plan -adoption-only" for the account-wide question.
```

There is one case where narrowing is deliberately not attempted at all.
`TOFU_LIVE_CLOUDCONTROL=off` selects the other sweep leg, which has no cheap
estate-wide oracle standing behind it. No `GetResources` call covers the types
the narrowing would skip, so skipping them there would remove coverage with
nothing underneath. That run pays the full universe whatever the record store
holds.

## The measured split, on a migrated estate

This is the day-2 shape and the one to start from: an estate already adopted,
every declared instance carrying its markers, which is where an operator
actually plans. Generated terralith at three scales, applied with stock
`terraform` and then migrated with `choudoufu live-import -approve` before
anything was counted (commit `cfd0dc58d4`, floci pin `sha256:c55d74e1`,
reported in
[`rulings/20260830-slicing-under-choudoufu.md`](https://github.com/INTENTIUS/choudoufu/blob/main/rulings/20260830-slicing-under-choudoufu.md)):

| Instances | Tagging leg | Native leg | Sweep | Read pass | Total | Read pass share |
|---|---|---|---|---|---|---|
| 79 | 1 | 512 | 548 | 148 | 696 | 21.3% |
| 301 | 2 | 521 | 592 | 556 | 1148 | 48.4% |
| 745 | 4 | 521 | 660 | 1372 | 2032 | 67.5% |

**The three rows are not the same vintage, and only the first has been
re-measured.** As published, the 79-instance row read `521 / 558 / 706 /
21.0%`. Re-run at `5ff7f43f5b` its legs read tagging 1, native 512,
configuration scan 26, boundary 9, post-sweep 0, so the sweep is 548 and the
total 696. The read pass did not move. That nine-call drift in the native leg
is unrelated to anything on this page. In particular it is *not* the `#628`
provider-block defect, which corrupted CLI-plan counts elsewhere in the same
document and cannot have touched these, because the in-process bench
configures its provider from a literal three-flag body that never carried
`skip_requesting_account_id`. The 301- and 745-instance rows have not been
re-run and stand at their published values.

The two legs do not add up to the sweep on their own. The rest of it is the
configuration scan, 26, 58 and 124 calls at the three scales, plus a boundary
and post-sweep pass of about ten calls. Those four account for the sweep
exactly at 79 instances and to within one call at the two larger scales, where
a type whose scan records nothing fires no progress event and its calls fold
into the next attribution interval.

Both terms are linear, and fitted to the three rows as published the fit was
exact to one call at every point: `sweep = 545.9 + 0.15315N`,
`read pass = 1.8378N + 2.8`, crossing at **322 instances**, just past this
fixture's scale 4. Take that crossover as the shape rather than as a current
number. The line was fitted before the 79-instance row moved by ten calls, and
re-fitting it across one re-measured row and two published ones would describe
no run that ever happened. The shape has not changed: below the crossing a
plan is mostly the fixed sweep and adding resources barely moves it; above it,
cost tracks your estate. It is a crossover between choudoufu's *own* two terms
on a full-sweep run, and says nothing about where choudoufu meets stock. There
is no such crossing, as
[what you pay, and when]({{< relref "/docs/what-you-pay" >}}) sets out.

### The read pass is the number stock pays to read the same resources

Measured on both sides of the same estate, the per-resource work is the same
and the totals differ by a constant:

| Instances | Stock `terraform plan` | choudoufu read pass (`BuildFrom`) |
|---|---|---|
| 79 | 150 | 148 |
| 301 | 558 | 556 |
| 745 | 1374, not measured | 1372 |

**An earlier version of this table read 148, 556 and 1372 in the stock column
and called the two identical, call for call.** They are not identical; they
are parallel. Every stock figure in that column came from a `terraform plan`
run against a provider block setting `skip_requesting_account_id`, which
suppresses the provider's own account resolution, one `GetCallerIdentity` and
one `GetUser`. With the block corrected,
`rulings/20260830-stateful-equivalence.md` measured stock at **150** and
**558** at the two smaller scales. 745 was not re-run, and 1374 is what the
shared slope implies rather than anything anyone counted.

Those two calls are the whole of the difference, and the slope is untouched.
The read pass fits `1.8378N + 2.8`, and stock's own two-point fit is
`1.84N + 5`, the same line with two more calls of constant. The per-resource
work is identical and the constant is not. That is the claim to carry, and it
is the stronger one. The coincidence that made the old column look exact was a
defect deleting from stock a constant the read-pass term never had.

So the shared term is the resource reads: the read pass is the AWS provider's
own `Read` implementations, which stock invokes on the same resources when it
refreshes, and **nothing in this fork adds to them or can subtract from
them.** `live/plan-budget.json` says the same of its own figures: the shape
"is a property of the AWS provider's own Read, not of anything this fork
adds."

**"Everything choudoufu spends above stock is the sweep" was this page's
headline sentence, and it needs two bounds now.** It is a statement about API
calls on a run that sweeps in full, and on that run it holds. It does not
describe a steady-state plan, and it does not survive the move to seconds. At
745 resources on real AWS, counting the requests the AWS provider itself logs,
stock issues 1392 and choudoufu 1399, seven apart, while the wall clock reads
22–39 s against 123–124 s. Seven requests do not cost ninety seconds. That
count excludes choudoufu's own Cloud Control and Tagging clients, which log no
line per request, so it is a floor rather than a total; what is spending the
ninety seconds is
[unaccounted for]({{< relref "/docs/what-you-pay" >}}), and this page will not
guess.

The sweep is the term that is genuinely ours. Stock has no equivalent, because
a state file already answers the question the sweep asks.

So the honest difference is not the size of the read pass. It is that stock
can decline to refresh with `-refresh=false` and choudoufu cannot: the
projection *is* the refresh, so `-refresh` is accepted and has no effect. What
stock buys by skipping it is a plan against remembered state, and there is no
remembered state here to plan against.

### The native leg does not move

`native_sweep_calls` measures **512 in every configuration** the slicing work
covered: whole estates at all three scales above, both slices of a two-way
split, and each of eight slices of an eight-way split. It does not grow with
the estate, and it does not shrink when a configuration declares fewer types.
(It read **521** in all thirteen when that work was published, and 512 on the
re-measure at `5ff7f43f5b`; the split table above accounts for the nine calls.
Flat is the property that matters, and it is still flat. What did change is
who pays it, which the section above and `09d180f921` cover.)

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

> **Superseded on both sides, and the paragraph above should not be quoted.**
> Every CLI-plan figure in it was taken with a provider block setting
> `skip_requesting_account_id`, so every one of those choudoufu plans exited 1
> on a refusal and its cost was written up as a clean plan's. Re-measured at
> `5ff7f43f5b`, every plan exiting 0 with `No changes`: stock **150 / 152 /
> 164** at k=1/2/8, choudoufu **157 / 163 / 198**. That is 1.05x, 1.07x and
> **1.21x**, not 5.0x, 8.7x and 30.6x. Stock's "148 whether one state or
> eight" was an artifact of the same block; stock is `148 + 2k`, two calls per
> slice to resolve the account. The error was not uniform either — it was
> `18 − 4k` calls and changed sign near k=4.5 — so no ratio built on those
> numbers could be rescaled. The sentence that survives is the one about the
> sweep: it is still **512 calls per slice**, 4096 summed at eight, for every
> run that actually sweeps. Since `09d180f921` a steady-state plan is not one
> of them. Full correction:
> [`rulings/20260830-slicing-under-choudoufu.md`](https://github.com/INTENTIUS/choudoufu/blob/main/rulings/20260830-slicing-under-choudoufu.md).

## On real AWS the sweep was nearly the whole plan

Call counts say what the two terms are. Seconds say which one an operator
notices, and on this estate those were not the same answer.

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

Three bounds on that paragraph. The seconds are real AWS and the call counts
are the emulator, so 0.36s per call is an estimate built from two measurements
rather than a measured quantity;
[`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md)
sets out when two wall clocks may be combined and when they may not. The table
predates the sweep becoming concurrent, which is the next section. **And it
predates the narrowing, so a steady-state plan of this estate no longer looks
like the second row at all.** The same pair now reads 3, 4, 3 s against 17, 18,
17 s. Keep the 200s column as the record of what a full sweep cost
sequentially on a real account; do not quote it as what a plan costs.

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
355.7ms), so read the ratios as approximate.

This prefetch pool is one of four things that bound a plan's seconds in
{{< version >}}, alongside the read pass learning the same
([#626](https://github.com/INTENTIUS/choudoufu/issues/626)), the narrowing
that takes the native leg off a steady-state plan entirely
([#627](https://github.com/INTENTIUS/choudoufu/pull/627)), and the record
store going from 377 round trips to one
([#636](https://github.com/INTENTIUS/choudoufu/pull/636)). Overlapping a leg
and not running it are different mechanisms, and the narrowing does most of
the work at 79 resources.

What that adds up to in seconds is on
[what you pay, and when]({{< relref "/docs/what-you-pay" >}}), which carries
the wall-clock figures and states what each one rests on. This page is the
mechanism; that page is the number.

## Turning a phase down

Both terms overlap their own waiting, and each has its own bound. The two are
separate settings because they are separate phases, and neither of them is
stock's `-parallelism`, which bounds the graph walk and nothing on this page.

| Variable | Bounds | Default | Honoured by |
|---|---|---|---|
| `TOFU_LIVE_SWEEP_PARALLELISM` | the sweep's per-type list calls | 10 | `live-plan`, and plain `plan`/`apply` of a configuration with a `live` block |
| `TOFU_LIVE_READ_PARALLELISM` | the read pass's per-instance import and read | 10 | the same two, and `live-mv` |

Set either to `1` for the sequential loop, one call at a time in the order the
phase would have made them. A value below 1 is refused rather than read as "no
limit" — the read bound's refusal lands before the run reads anything at all,
because it is resolved before the configuration is even loaded.

Neither changes what a plan costs in calls. The sweep's counts were measured
identical at 1, 2, 10 and 20 in the timing table just above; the read pass
makes one import and one read per instance whatever its width, which is a
property of the loop rather than something anyone had to measure. What the
settings change is how much of the waiting overlaps, which is why the reason to
touch them is a real account answering `Rate exceeded` rather than a wish for a
cheaper plan.

Both defaults are 10 because stock plans an estate at `-parallelism 10`. That
argument is the stronger of the two for the read pass, which makes call for
call the same requests a stock refresh of the same estate makes — the
stock-versus-choudoufu table earlier on this page — so ten asks an account for
exactly what it already answers for OpenTofu. Read-side throttling has not been
measured, and cannot be from an emulator, since floci does not throttle.

`live-mv` honours the read bound and has no sweep to bound: a rename lists one
resource type rather than the estate. `live-import`'s own `-parallelism` flag
is a third thing again, the width of its stamp pass, which neither variable
moves.

### Turning the account inventory off, or back on

`TOFU_LIVE_COLLECT_UNCLAIMED` is not a width. It is the on/off for the account
inventory, the question the
[narrowing](#when-the-native-leg-is-narrowed-and-when-it-is-not) declines, and
it is the only one of the three settings here that changes what a plan costs in
calls rather than how much of the waiting overlaps.

| Value | Effect |
|---|---|
| unset | the command decides: on under `-adoption-only`, off otherwise |
| `1`, `true`, `on`, `yes` | ask the account-wide question, whatever the command would have chosen |
| `0`, `false`, `off`, `no` | do not ask it, **even under `-adoption-only`** |

Anything else errors and quotes the value it could not read. The variable
exists beside the flag rather than instead of it because `live-plan`'s own
`-estate` form and plain `apply` have no `-adoption-only` to reach for.

Turning it on is the expensive direction and it is the one to reach for
deliberately: on the 79-instance fixture it is the difference between 157 and
710 API calls.

## The unmigrated estate, for contrast

The same fixture and the same pin, measured before migration with no marker on
any object (commit `f4611196e5`,
[`rulings/20260830-marker-verified-fast-projection.md`](https://github.com/INTENTIUS/choudoufu/blob/main/rulings/20260830-marker-verified-fast-projection.md)):

| Instances | Sweep | Read pass | Total | Read pass share |
|---|---|---|---|---|
| 79 | 560 | 86 | 646 | 13.3% |
| 301 | 593 | 341 | 934 | 36.5% |
| 745 | 659 | 851 | 1510 | 56.4% |

`sweep = 548.3 + 0.1486N`, `read pass = 1.1486N - 4.7`, crossing at 553
instances.

**This is the one table on the page the narrowing does not move.** An estate
with no marker on any object has no record store keys either, and an empty
store is one of the four gates that takes the full universe. The adoption case
still pays the whole admission table, by design.

It is the plan you run on the way in rather than the ones you run afterwards,
and it is a lower bound on the read pass: nothing was bound, so much of the
estate never materialized. Migration leaves the sweep alone and raises the read
pass by 61% to 72%. Per instance the read pass goes from 1.15 calls to
**1.84**, and the crossover moves from 553 instances to 322. If you are
budgeting from the unmigrated table, you are budgeting for a state your estate
passes through once.

## Bounds on all of the above

- **The tagging leg was available.** With `TOFU_LIVE_CLOUDCONTROL=off` the
  sweep falls back to per-type listing across the whole universe, so every
  figure here is the cheapest production shape rather than the worst one.
- **The call counts are emulator-measured.** The tagging leg is
  `ceil(tagged_resources / page)` and floci's page is 100, which is why it
  reads 1, 2 and 4 rather than 1 everywhere. `cloudcontrol.Client.GetResources`
  sets no `ResourcesPerPage`, so the real page size is the Resource Groups
  Tagging API's own default and no emulator-backed run can report it.
- **One fixture, one composition.** The 512-call native leg is a property of
  the admission table and the ARN join table rather than of the estate, but
  that is an argument; only this estate was measured, and it declares thirteen
  types.
- **AWS only.** Nothing here says anything about another provider.
- **Every call-count table on this page measures a full-sweep run.** None of
  those tables has been re-measured under the narrowing; what has is the
  79-instance fixture's headline, 157 against 710, and the real-AWS pair at
  745 resources. Where a figure here disagrees with a plan you actually ran,
  the narrowing is the first thing to suspect.

## Do not carry one resource type's slope to another

This is the mistake most worth avoiding, and it has already been made once in
an issue.

`live/plan-budget.json` ratchets an `aws_s3_bucket` estate at **22 calls per
instance**, fitting `calls_total = 22*N + 8` exactly at N=20, 200 and 1000
(448, 4408, 22008). That number is not a property of choudoufu. `aws_s3_bucket`
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

The `+ 8` in that fit is worth one line of its own, because an earlier version
of this page described the fixed term wrongly and the correction is the more
useful fact. These are not account-level probes. Six of the eight are
`ListBuckets`: five issued by the parent-read sweep, one by the provider's own
account and region resolution. The remaining two are `GetCallerIdentity` and
`GetUser`. They are 1.8% of the total at N=20 and 0.04% at N=1000. A fixed
term looks expensive on a small estate and disappears on a large one, which is
the opposite of how the sweep behaves and a good reason to fit a line rather
than divide once.

## Emulator wall clock is not on this page

Every cost figure here is a call count, with one deliberate exception. Seconds
measured against the pinned emulator grade the machine the test ran on, which
is why `live/plan-budget.json` records a wall clock and never gates on it. The one
timing table above is real AWS, where the seconds are network latency rather
than a property of whatever laptop ran the suite.
[`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md)
covers the distinction and the three other questions an emulator-backed
measurement cannot answer.
