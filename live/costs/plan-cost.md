
# What a plan costs

> A figure written as a field name in backticks, such as `plan_calls.cold.choudoufu`,
> names a field of the scale 1 `terralith-scale` record in
> [`live/gauntlet-scale.json`](../gauntlet-scale.json). It is not typed here, so
> that it cannot drift from the measurement. The docs site prints the current
> values at https://intentius.io/choudoufu/docs/model/plan-cost/.

Prior state is rebuilt by reading the live system every time you plan, because
nothing stored is trusted. That reading is two costs, and they grow along
different axes. Which of the two dominates depends on the size of your
estate, and the answer flips.

Both belong to hooks, and the hooks differ in when they are needed. The read
pass is unconditional and costs what stock's refresh costs. The sweep is the
adoption hook, which answers a question an operator needs during a migration
or an audit and not on an ordinary plan of an estate that is already adopted.
That it should not run on every plan was
[the stale-state ruling](https://github.com/INTENTIUS/choudoufu/issues/604)
(#604), and `09d180f921` implemented it. This page is the measurement that
ruling rested on, and it is still the measurement of what the sweep costs when
a run does take it.

> Read this page as the sweep's cost. Since `09d180f921` a plan of an estate
> that has its own evidence to narrow by (types declared in configuration, or
> a key held in the record store) no longer enumerates the whole admission
> table. The 79-instance fixture measured throughout this page went from
> **710 API calls to 157**, against stock's 150. Every full-sweep figure below
> describes a run where the narrowing has nothing to narrow by: an adoption,
> an audit, or a rebuild from markers. The exact gates are
> [below](#when-the-native-leg-is-narrowed-and-when-it-is-not). The scales
> above 79 instances have not been re-measured in calls since `09d180f921`.
> What a steady-state plan costs is on
> [what you pay, and when](what-you-pay.md).

> A machine reader wants `live/gauntlet-scale.json`
> ([issue #1051](https://github.com/INTENTIUS/choudoufu/issues/1051),
> [chant-bench#33](https://github.com/INTENTIUS/chant-bench/issues/33)). The
> figures this page and [what you pay](what-you-pay.md)
> describe are emitted there as records, one per (estate, target,
> scale): `terralith-scale` at 79, 301, 745 and 3,705 resources. Each
> record keeps two numbers apart
> ([issue #1053](https://github.com/INTENTIUS/choudoufu/issues/1053)).
> `plan_calls` is what an ordinary CLI `tofu plan` of the migrated estate
> costs, as a `cold` pass and a back-to-back `warm` one. `audit_calls` is the
> sweep and read-pass split in the table below, taken with
> `Request.CollectUnclaimed` forced true, which takes the whole admission
> table. The 79-instance point is recorded both ways. 301 and 745 still want a
> re-run, and the real-AWS side has `plan_calls.cold` only.

The one recorded point is read straight from that file, measured at commit
`commit` on
`date`. At the 79-instance point a
`tofu plan` of the migrated estate cost choudoufu
`plan_calls.cold.choudoufu` calls against
stock's `plan_calls.cold.stock`, and a warm
plan right after cost
`plan_calls.warm.choudoufu`, the same figure
to the call.

The account-inventory audit this page measures below is a larger number at
the same scale: sweep
`audit_calls.sweep.choudoufu` calls, read
pass `audit_calls.read_pass.choudoufu`
against stock's `audit_calls.read_pass.stock`,
for a total of `audit_calls.total.choudoufu`
against `audit_calls.total.stock`. When a
re-run lands a newer record for the same scale, these figures follow it with
no edit.

## The two terms

**The sweep asks what this estate owns, and it is O(types).** One estate-filtered
tagging call covers the types whose ARNs the hand-curated join table can
resolve; every other admitted type is routed to the native leg and gets its
own list attempt. The work is set by the size of the admission table, and it
does not grow with your estate. Counted at `5d55f4aa9f`, and reproducible in
under a second with no cloud and no emulator:

```
go test ./internal/live/discovery/ -run TestSweepUniversePartitionIsMostlyNative
sweep universe=1027 tagging_leg=35 native_leg=992
```

Not all 992 of those reach the network. Only 502 of them can issue a
`ListResources` at all; the rest report a sweep gap without a call, because
the type is either not listable or not taggable. Measured at `5dbe452a1e` for
[#586](https://github.com/INTENTIUS/choudoufu/issues/586), where 435 of the
502 fired against the 79-instance fixture below in its unmigrated state.

**The read pass asks what each owned resource currently looks like, and it is
O(resources).** One or more provider Reads per instance the plan materializes,
how many depending entirely on which resource types you have. That is the same
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
in full. Take a live object carrying this estate's marker, of a type that:

- the configuration does not declare;
- the record store has no entry for; and
- the ARN join table cannot place from an ARN.

Its destroy is not proposed.
Every other removal is unaffected, which
`TestNarrowedNativeSweepStillProposesRemovals` and the `day2_remove` gauntlet
stages check by value rather than by argument.

A narrowed plan says so. The "Foreign resources" section prints the count it
skipped and the command that asks anyway. The 987 below is the fixed sample
that `TestForeign_narrowedSweepSaysSo`
(`internal/command/views/live_plan_nativesweep_test.go`) renders the message
with. It is a different quantity from the sweep universe (1027) above and
from the admission-table size elsewhere on this page:

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
[the slicing measurement](https://github.com/INTENTIUS/choudoufu/issues/584)
(#584, corrected by #634):

| Instances | Tagging leg | Native leg | Sweep | Read pass | Total | Read pass share |
|---|---|---|---|---|---|---|
| 79 | 1 | 512 | 548 | 148 | 696 | 21.3% |
| 301 | 2 | 552 | 706 | 556 | 1262 | 44.1% |
| 745 | 4 | 612 | 960 | 1372 | 2332 | 58.8% |

The 79-instance row is the re-measure at `5ff7f43f5b` (2026-08-30, floci pin
`sha256:c55d74e1`). Its legs read tagging 1, native 512, configuration scan
26, boundary 9 and post-sweep 0, for a sweep of 548 and a total of 696.

The 301- and 745-instance rows were re-measured for
[#1032](https://github.com/INTENTIUS/choudoufu/issues/1032) at commit
`56099dcd63` (2026-09-09), floci pin `sha256:d9207de1`, with the same
harness: `SLICE_SCALE=4 SLICE_K=1` and `SLICE_SCALE=10 SLICE_K=1`,
`TF_FLOCI_TEST=1 env -u PWD go test ./internal/live/discovery/ -run
TestSlicingMatrixAgainstFloci`. The read pass matched the earlier published
figures to the call at both scales, 556 and 1372, and so did the stock-side
comparison two sections down (558 and 1374).

The native leg grew with scale in that run, to 552 at 301 instances and 612
at 745. [#1037](https://github.com/INTENTIUS/choudoufu/issues/1037) and
[#1039](https://github.com/INTENTIUS/choudoufu/issues/1039) found and fixed
the two causes, and the native leg is flat again as of `c0632fa3b7`; see
["The native leg is flat across slices but not across scale"](#the-native-leg-is-flat-across-slices-but-not-across-scale)
below for the fixed numbers. The rows and fits in the rest of this section
describe the state before that fix and are kept as the historical record.

The two legs do not add up to the sweep on their own. At `56099dcd63`, the
rest of it is the configuration scan (60 at 301 instances, 128 at 745,
against 26 published at 79) plus a boundary and post-sweep pass that no
longer reads as "about ten calls" at the larger scales: boundary 8 and 6,
post-sweep 84 and 210, at 301 and 745 respectively. Tagging, native,
configuration scan, boundary and post-sweep sum to the sweep column above
exactly at all three scales in this run.

Fitted by least squares to the three rows above, the line is `sweep = 508.5 +
0.612N`, crossing `read pass = 1.8378N + 2.8` at **413 instances**. The fit
predicts 557, 693 and 964 against the measured 548, 706 and 960. The pairwise
slopes are 0.71 per instance from 79 to 301 and 0.57 from 301 to 745, so the
growth was decelerating, and 413 is where a straight approximation of that
curve meets the read pass. It replaces an earlier fit,
`sweep = 545.9 + 0.15315N` crossing at 322 instances, taken before the rows
were re-measured. Both fits are stale: they rest on rows measured at
`5ff7f43f5b` and `56099dcd63`, before the native leg went flat again at
`c0632fa3b7`.

[#1039](https://github.com/INTENTIUS/choudoufu/issues/1039) traced part of
the growth to three types, `aws_iam_policy`, `aws_iam_role` and
`aws_ecs_service`, whose provider list resource carries no filter block, so
their cost tracks how many of them exist in the account. In the table above
the account holds nothing but the estate under test, so a bigger N means a
longer unfiltered list. The claims page's [foreign-load
table](../smoke/claims/plan-cost-under-foreign-load.md) shows the
same mechanism from the other side, growing a neighboring estate: there the
plan calls climb 187, 197 and 687.

[#1037](https://github.com/INTENTIUS/choudoufu/issues/1037) found the rest.
Since `e15b23eb7b` (2026-09-01), `sweepTypes` added a declared
`aws_iam_policy` or `aws_iam_role` back into the native sweep universe even
when the config-driven scan had already listed the whole account for it. Both
causes are fixed at `c0632fa3b7`, and
[the flat-again table](#the-native-leg-is-flat-across-slices-but-not-across-scale)
is below.

No crossover has been refit against the flat native leg. Refitting the sweep
also means re-measuring the configuration scan, boundary and post-sweep terms
together, which has not been done. Any such crossover is between choudoufu's
own two terms on a full-sweep run. There is no crossing between choudoufu and
stock, as [what you pay, and when](what-you-pay.md) sets
out.

### The read pass is the number stock pays to read the same resources

Measured on both sides of the same estate, the per-resource work is the same
and the totals differ by a constant:

| Instances | Stock `terraform plan` | choudoufu read pass (`BuildFrom`) |
|---|---|---|
| 79 | 150 | 148 |
| 301 | 558 | 556 |
| 745 | 1374 | 1372 |

A constant two calls separates the two. Stock's provider block resolves its
own account with one `GetCallerIdentity` and one `GetUser`; the read pass has
no equivalent, since nothing in it needs the account identity. The read pass
fits `1.8378N + 2.8`, and stock's own two-point fit is `1.84N + 5`, the same
line with two more calls of constant. The `56099dcd63` re-measure above ran a
stock `terraform plan` on the same estates in the same harness run and read
exactly 558 at 301 instances and 1374 at 745, matching the fit to the call.

So the shared term is the resource reads: the read pass is the AWS provider's
own `Read` implementations, which stock invokes on the same resources when it
refreshes, and **nothing in this fork adds to them or can subtract from
them.** `live/plan-budget.json` says the same of its own figures: the shape
"is a property of the AWS provider's own Read".

Above stock, everything choudoufu spends in API calls is the sweep, on a run
that sweeps in full. That does not describe a steady-state plan, and it does
not carry over to seconds. At 745 resources on real AWS, counting the
requests the AWS provider itself logs, stock issues 1392 and choudoufu 1399,
seven apart, while the wall clock reads 22 to 39 s against 123 to 124 s. That
count excludes choudoufu's own Cloud Control and Tagging clients, which log no
line per request, so it is a floor. What is spending the ninety seconds is
[unaccounted for](what-you-pay.md).

The sweep is the term that is genuinely ours. Stock has no equivalent, because
a state file already answers the question the sweep asks.

Both tools refresh at the same size on a default plan. The honest difference
shows up under `-refresh=false`, in what each side may skip. Stock
skips everything and trusts its state file outright. Choudoufu skips only
what the run can vouch for (an instance the sweep verified by marker, or one
the record store attests while the run's own listing proves it exists);
everything else still reads. The state cache supplies attributes for what is
vouched, the plan launcher never plans those wire reads, and `reads = "full"`
turns the whole pass off. The
[unchanged-is-free claim](../smoke/claims/unchanged-is-free.md)
measures it; default plans are untouched, since the read is drift detection.

### The native leg is flat across slices but not across scale

Two different axes share this leg, and they no longer behave the same way.

Sliced at a fixed 79-instance estate, `native_sweep_calls` measures **512 in
every configuration** the slicing work covered, re-measured at `5ff7f43f5b`:
the whole estate, both slices of a two-way split, and each of eight slices of
an eight-way split. It does not shrink when a configuration declares fewer
types, for the mechanism below. The slicing measurement has never been run at
a scale other than 79.

Held at one slice and varied by estate scale, this used to grow: the split
table above gave 512, 552 and 612 at 79, 301 and 745 instances before
[#1037](https://github.com/INTENTIUS/choudoufu/issues/1037) and
[#1039](https://github.com/INTENTIUS/choudoufu/issues/1039) were fixed at
`c0632fa3b7`.

Two causes were isolated by measurement. `aws_iam_policy` and `aws_iam_role`
are in the service the Resource Groups Tagging API never indexes
("aws_iam_"), so a declared instance of either was listed a second
time by the native sweep after the config-driven scan had already listed the
whole account for it. Every finding of the second call was already in
`res.Orphans` and was discarded, so it paid a full per-object provider Read
(`GetPolicyVersion` per policy) for nothing. `aws_ecs_service` had no
`arnJoinTable` row for its "service" ARN segment, so it took the
whole-account native leg even though the Tagging API serves ECS.

`dedupAlreadyConfigScanned` fixed the first and the `ecs`/`service` row fixed
the second, at `c0632fa3b7` (2026-09-11), floci pin `sha256:9ec3fa64...`
(`live/floci-image`), with the same harness as above
(`TestSlicingMatrixAgainstFloci`, `SLICE_K=1`):

| Instances | Native leg, before the fix | Native leg, after |
|---|---|---|
| 79 | 521 | 508 |
| 301 | 548 | 510 |
| 745 | 612 | 510 |

Flat again, to within the 2-call spread that the tagging leg's own page size
(floci's 100) would explain as noise. Two offline unit tests (a fake provider
handle, no emulator) pin each mechanism by value against a deliberate revert:
`TestSweepDoesNotReListAConfigScannedUnservedType` (the duplicate listing)
and `TestECSServiceRoutesThroughTheTaggingLeg` (the missing join row), both
in `internal/live/discovery`.

The mechanism behind the flat-across-slices half runs the wrong way round
from most people's intuition, so here it is. `sweepTypes` builds its
universe by *removing* the types the configuration declares from the
admission table, so a slice declaring five types has a sweep universe of
1022 to 1026 against the whole estate's 1021. A small slice pays slightly
more than the whole estate does, at whatever the estate's own scale is.

The consequence for an already-sliced estate is where the sweep hurts. Because
it does not shrink per slice, its cost multiplies with slice count on any run
that sweeps in full, even though a steady-state plan's does not: the 512
calls per slice that `5ff7f43f5b` measured at 79 instances, times the number
of slices, is 4096 summed at eight. Whether that multiplier grows with estate
scale has not been measured.
[What you pay](what-you-pay.md#splitting-an-estate-into-several-states)
has the steady-state ratio table (1.05x/1.07x/1.21x at k=1/2/8) and the
choice this leaves an adopter with.

### One leg is deliberately not flat, and this is it

The per-service tag-read leg
([#1131](https://github.com/INTENTIUS/choudoufu/issues/1131), the repair for
[#881](https://github.com/INTENTIUS/choudoufu/issues/881)) costs **one call
per candidate object**, not one per type. It is the only part of the sweep
that does, and it is written down here rather than left in a source comment
because every other number on this page is a flat one.

It runs for a resource type only when all three of the ordinary marker
routes have already failed on that run. The type's CloudFormation schema
carries no `Tags` property, so Cloud Control's `ListResources` and
`GetResource` can never return a marker for it however the object is tagged.
The estate's Resource Groups Tagging API index holds no object of the type
either, so #266's join has nothing to say.

Both are checked per run against what the target answered. A leg selected by
service name would be wrong about one of two targets:
[#1134](https://github.com/INTENTIUS/choudoufu/issues/1134) measured a real
account serving `iam:instance-profile` through `GetResources` in `us-east-1`,
while the pinned emulator serves no IAM at all
([#1152](https://github.com/INTENTIUS/choudoufu/issues/1152)). On a target
where the index serves the type, the leg never runs and the sweep is flat
exactly as the tables above measure it.

Where it does run, the bill is the number of live objects of the covered
types in the account. On `terralith-scale` that is `aws_iam_instance_profile`
and the estate declares `10 x SCALE` of them, derived from the generator's
own expansion (`6 x SCALE` named blocks, `2 x SCALE` from the `count_team`
block, `2 x SCALE` across the two `team_pod` module instances) and confirmed
against a scale-1 run, whose sweep line reads "9 live resources found so
far" with one profile already destroyed by `day2_remove`:

| Instances | Live instance profiles | Extra `iam:ListInstanceProfileTags` calls per sweep |
|---|---|---|
| 79 (scale 1) | 10 | 10 |
| 745 (scale 10) | 100 | 100 |
| 4005 (scale 80) | 800 | 800 |

Two things bound that. The type was already paying a per-object call on this
leg before #1131 existed: Cloud Control sends no `Tags` key for an instance
profile, so `cloudControlTags` was already refining every listed one with an
individual `GetResource` (`TypeScan.Refined`). The tag read doubles an
existing per-object constant for this one type and adds no new term to the
sweep's shape.

No batch alternative exists to build a flat shape out of.
`iam:ListInstanceProfiles` omits tags by design (AWS's own reference says
"this operation does not return tags, even though they are an attribute of
the returned object"), `GetInstanceProfile` and `ListInstanceProfileTags` are
both per-object, and neither takes a tag filter.

The measured tables above are unaffected and were not re-taken: the
`plan-budget` estate `TestPlanCallBudgetAgainstFloci` measures is a single
`aws_s3_bucket` cohort, which this leg does not cover and never fires for.
`TypeScan.ServiceTagReads` counts the calls per type in the scan row, so a
run that pays for this leg says how much.

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
The sweep is the remaining 200 seconds. Spread over the 558 sweep calls
counted at that scale it is about 0.36s each, which is one network round trip
apiece, and at the time of that run the sweep made them one after another.

Two bounds on that paragraph. The seconds are real AWS and the call counts
are the emulator, so 0.36s per call is an estimate built from two
measurements;
[`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md)
sets out when two wall clocks may be combined. The table is stale for an
ordinary plan: it was taken in #578's run, before the sweep went concurrent
(next section) and before the narrowing, and the same pair now reads 3, 4, 3s
against 17, 18, 17s. The 200s column is what a full sweep cost sequentially
on a real account.

### At 3,705 resources the pair could not be formed

[#1032](https://github.com/INTENTIUS/choudoufu/issues/1032) took the same
real-AWS harness to scale 50, 3,705 resources, `us-east-2`, commit
`15f5dcd5d5`, 2026-09-11. Stock's side of the comparison is measured cleanly:
three converged, no-change `terraform plan` runs at 192s, 289s and 184s
(`TF_LOG` unset, warm provider), and one further instrumented plan (also
empty) counting **7,248 provider-mediated AWS API requests exactly**, from
`rpc.method` entries. 435 throttling-error lines and 435 retries landed on
this one plan alone, all absorbed.

The count is dominated by IAM (1,658 `ListAttachedRolePolicies`, 1,074
`GetRole`, 1,024 `GetRolePolicy`, 560 `ListRolePolicies`, 523 `GetPolicy`,
522 `GetPolicyVersion`, 507 `GetInstanceProfile`) and Route 53 (641
`GetHostedZone`, 627 `ListResourceRecordSets`). Those are the same two
services
[What you pay](what-you-pay.md#the-same-comparison-on-real-aws-at-79-and-745-resources)
names as the account-tracking term at 745 resources, and they keep growing
with estate scale.

choudoufu's side of the pair was not measured on that first run. `test_plan`
found a real defect: the post-migrate plan was not empty (see
[what you pay](what-you-pay.md#at-3705-resources-migration-itself-still-holds---the-post-migrate-plan-does-not)
for what it proposed and why). A harness bug in the same stage's own identity
check ([#1047](https://github.com/INTENTIUS/choudoufu/issues/1047)) then
failed the stage before any choudoufu-side `timed_plans` or API-call count
was taken.

The identity check was fixed at `ab70b1018d`, and a second scale-50 run
(commit `8bbef274d6`, 2026-09-11) reached the timing fallback, so the cell is
measured now. It is still short of the empty, like-for-like pair the table
above needs. choudoufu's gating plan proposed `Plan: 358 to add, 0 to change,
4 to destroy`, the `aws_iam_policy` defect still open as
[#1046](https://github.com/INTENTIUS/choudoufu/issues/1046) (see
[what you pay](what-you-pay.md#the-real-mechanism-a-cross-service-indexing-lag-not-a-page-size)
for the mechanism). Three timed re-plans of that same non-empty state read
**375s, 356s and 373s** (`TF_LOG` unset, warm provider, each verdict
self-labelled `Plan:_358_to_add,_0_to_change,_4_to_destroy`).

The first post-migration instrumented plan of that run counted **8,305
provider-mediated AWS API requests exactly**, against stock's 7,207 on the
same run. It is dominated by IAM (1,530 `GetRolePolicy`, 1,477
`ListAttachedRolePolicies`, 1,115 `ListRolePolicies`, 1,075 `GetRole`, 823
`GetPolicyVersion`, 517 `GetInstanceProfile`, 325 `GetPolicy`) and Route 53
(648 `GetHostedZone`, 614 `ListResourceRecordSets`). A steady-state
instrumented replan afterward counted 8,340. `TypeScan.Refined` was 0 in both
counts: this estate's policies and roles resolve their tags from the list
call or the tag-index join.

These numbers are no cost comparison. A plan proposing 358 creates does
strictly more work than an empty one, on both sides, so 375s against 183s
says only that the two plans are doing different things. The 79-versus-745
comparison above stays the only like-for-like real-AWS pair on this page
until a clean (empty) post-migrate plan at 3,705 resources produces one,
which needs #1046 resolved first.

### The sweep now overlaps its own waiting

The admission table fixes how many calls there are. Nothing requires them to
be made one after another, and since
[#605](https://github.com/INTENTIUS/choudoufu/issues/605) they are not:
`Discover` prefetches the sweep's per-type listings through a bounded worker
pool, `DefaultSweepParallelism = 10`
(`internal/live/discovery/sweepconcurrency.go`), the same bound stock plans an
estate at. It covers the sweep's per-type listing and nothing else. The
config-driven scan, the tagging leg's single `GetResources`, and the parent
and record-orphan reads are untouched.

**The call count does not move, which is the point.** Measured at `177a2579c1`
against the pinned emulator at four settings and both scales, 558 calls at 79
instances and 591 at 301, identical at parallelism 1, 2, 10 and 20, with the
scan-row order and the diagnostic sequence identical too:

| Scale | Instances | par 1 | par 2 | par 10 | par 20 |
|---|---|---|---|---|---|
| 1 | 79 | 433.6ms | 266.4ms | 188.9ms | 154.9ms |
| 4 | 301 | 419.4ms | 286.7ms | 219.2ms | 173.1ms |

Those are milliseconds over loopback, so they measure the overlap and not the
saving. A repeat of each parallelism-1 row landed 18% lower (357.4ms and
355.7ms), so read the ratios as approximate.

This prefetch pool is one of four things that bound a plan's seconds in
the current release, alongside the read pass learning the same
([#626](https://github.com/INTENTIUS/choudoufu/issues/626)), the narrowing
that takes the native leg off a steady-state plan entirely
([#627](https://github.com/INTENTIUS/choudoufu/pull/627)), and the record
store's round trips falling to one per plan
([#636](https://github.com/INTENTIUS/choudoufu/pull/636); [what you pay, and
when](what-you-pay.md#the-record-store-which-no-call-count-used-to-see)
carries the figure and its own staleness note). Overlapping a leg
and not running it are different mechanisms, and the narrowing does most of
the work at 79 resources.

What that adds up to in seconds is on
[what you pay, and when](what-you-pay.md); it carries
the wall-clock figures and states what each one rests on. This page is the
mechanism; that page is the number.

## Turning a phase down

Both terms overlap their own waiting, and each has its own bound. The two are
separate settings because they are separate phases. Neither of them is
stock's `-parallelism`, which bounds the graph walk and nothing on this page.

| Variable | Bounds | Default | Honoured by |
|---|---|---|---|
| `TOFU_LIVE_SWEEP_PARALLELISM` | the sweep's per-type list calls | 10 | `live-plan`, and plain `plan`/`apply` of a configuration with a `live` block |
| `TOFU_LIVE_READ_PARALLELISM` | the read pass's per-instance import and read | 10 | the same two, and `live-mv` |

Each bounds the calls a phase has *in flight*. Each also has a second bound
behind that one, on the answers it has fetched and the consuming loop has not
used yet, and neither of those has a variable of its own: turning a phase down
is turning down what the account is asked for, which is the width, and the
buffer follows it.

For the read pass that is a hundred per in-flight slot, so a thousand at the
default width. Until [#683](https://github.com/INTENTIUS/choudoufu/issues/683)
one number was both, and an answer that had landed went on holding the width
until the loop reached that instance in build order. A single read in a
provider backoff, 26 seconds of it on a 745-resource plan, stopped the pass
from starting anything else at all.

The sweep had the same shape and the same defect, one phase over
([#839](https://github.com/INTENTIUS/choudoufu/issues/839)): its listings were
released by the scan loop in universe order, so one throttled list call held
the sweep's whole width behind it. Its buffer is ten per slot where the read
pass has a hundred, because an unconsumed listing here is every live object
of its type, and the scan drops those objects once it has filed its row. Ten
per slot is worth about thirty-six seconds of sweeping at the rate the timing
table above measures, which is what the straggler it covers costs.

Peak memory is still a multiple of the two bounds and never of the estate or
of the admission table, which is what the single number was protecting.

### What the split was worth, measured

[#867](https://github.com/INTENTIUS/choudoufu/issues/867) re-took #683's trace
on the same estate after both fixes landed: `us-east-2`, provider 6.59.0,
choudoufu built from `d455a2fed4`, harness and instrument at `3889d2476c`,
2026-09-06. Three steady-state `choudoufu plan` runs, and three stock
`terraform plan` runs of the same estate in the same session, so that the
account's own throttling is roughly the same on both sides of the comparison.
An idle gap is a stretch of at least 0.8 seconds during which no AWS request
is in flight at all; `live/live-cert/wallclock-gaps.py` is the instrument.

| plan, all at `d455a2fed4` | span | idle at or above 0.8s | largest stall | closed by an SDK retry | provider requests |
|---|---|---|---|---|---|
| `choudoufu plan` 1 | 56.0s | 8.1s (14%) | 4.68s | 4 of 4 | 1,735 requests |
| `choudoufu plan` 2 | 50.0s | 3.1s (6%) | 1.63s | 2 of 2 | 1,734 requests |
| `choudoufu plan` 3 | 57.1s | 11.5s (20%) | 4.30s | 5 of 5 | 1,732 requests |
| stock 1 | 20.0s | 0.0s (0%) | - | 0 of 0 | 1,409 requests |
| stock 2 | 38.2s | 15.9s (42%) | 8.04s | 6 of 6 | 1,418 requests |
| stock 3 | 29.2s | 10.6s (36%) | 7.79s | 2 of 2 | 1,409 requests |

The fork's extra three hundred are outside the read pass, which still makes
stock's calls call for call. They are the sweep's two client-side-filtered
listings, `aws_iam_policy` and `aws_iam_role`, which enumerate the whole
account: `GetPolicyVersion` 102 to 201, `ListRolePolicies` 113 to 226,
`GetRolePolicy` 203 to 308 between the stock column and the fork's. The test
account also held objects earlier runs had left behind, so that column
describes more than this estate, and it is why the fork's span here is longer
than #683's on the same estate.

The fork's idle share is not the number to read on its own. An account does
not throttle the same way twice: stock's own share moved from 20% in #683's
session to somewhere between 0% and 42% in this one. What compares is the
fork's share against stock's *in the same session*. #683's captures, put
through this same instrument (`3889d2476c`), read 49% and 56% idle against
stock's 20%, about two and a half times stock. Here the fork is 6% to 20%
against stock's 0% to 42%, which is below stock, and the worst single stall a
`choudoufu plan` took, 4.68s, is shorter than the worst stock took on the same
estate minutes earlier, 8.04s.

Every stall on both sides, nineteen of them, ends in a `retrying request`
line, so what is left of the idle at `d455a2fed4` is the provider's own
backoff schedule rather than anything either binary decides. On the fork's side every
read-pass stall falls in the last quarter of its run: while there are reads
left to launch, a stalled one holds an in-flight slot and no buffer slot, so
the launcher keeps going, and the residue is the tail, where fewer instances
remain than the width and a slow one has nothing left to overlap with. #683's
stalls were spread across the whole run, because back then any one of them
stopped everything.

The sweep showed no straggler, and this estate cannot produce one. Two
throttled list calls across the three runs cost 1.23s and 1.51s, measured at
`d455a2fed4`. Thirty-two of this estate's swept types are answered by the
single estate-filtered `GetResources` described above, which takes no
per-type slot at all. Only 3 types, `aws_ecs_service`, `aws_iam_policy` and
`aws_iam_role`, take the per-type list path the sweep's bounds cover, on the
first post-migration plan and on a steady-state one alike.

Three outstanding calls against a width of ten means the sweep's buffer is
never reached, and a factor of one would have produced the identical run. Ten
per slot therefore still rests on the derivation above. Measuring it needs an
estate whose types mostly lack a server-side tag filter, which is also the
only shape in which #839's defect could have cost anything.

Set either to `1` for the sequential loop, one call at a time in the order the
phase would have made them. A value below 1 is refused, never read as "no
limit". The read bound's refusal lands before the run reads anything at all,
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
call the same requests a stock refresh of the same estate makes (the
stock-versus-choudoufu table earlier on this page), so ten asks an account for
exactly what it already answers for OpenTofu. floci does not throttle, so
read-side throttling was measured on a real account: at `d455a2fed4` a
steady-state plan of the 745 instances above was throttled 43 to 46 times per
run at this width, every one of them retried and answered. The account
tolerates ten concurrent reads, and the section just above has what the
waiting cost.

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
[the fast-projection ruling](https://github.com/INTENTIUS/choudoufu/issues/579)
(#579)):

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
- **One fixture, one composition.** The native leg is mostly a property of
  the admission table and the ARN join table. It grew with estate scale until
  [#1037](https://github.com/INTENTIUS/choudoufu/issues/1037) and
  [#1039](https://github.com/INTENTIUS/choudoufu/issues/1039) were fixed at
  `c0632fa3b7` (508, 510, 510 at 79, 301 and 745 instances). Only this estate
  was measured, and it declares thirteen types.
- **AWS only.** Nothing here says anything about another provider.
- **Every call-count table on this page measures a full-sweep run.** None of
  those tables has been re-measured under the narrowing; what has is the
  79-instance fixture's headline, 157 against 710, and [the real-AWS pair
  at 745 resources](what-you-pay.md#the-same-comparison-on-real-aws-at-79-and-745-resources).
  Where a figure here disagrees with a plan you actually ran, the narrowing
  is the first thing to suspect.

## Do not carry one resource type's slope to another

This is the mistake most worth avoiding, and it has already been made once in
an issue.

`live/plan-budget.json` (re-measured at `3690d38143`, checked every run by
`TestPlanCallBudgetAgainstFloci`) ratchets an `aws_s3_bucket` estate at **22
calls per instance**, fitting `calls_total = 22*N + 8` exactly at N=20, 200
and 1000 (448, 4408, 22008). That number is not a property of choudoufu.
`aws_s3_bucket` is an unusually chatty Read: a dozen subresource GETs for
ACL, CORS, encryption, lifecycle, logging, object lock, policy, replication,
request payment, versioning, website and acceleration, plus the parent-read
children beside them.

The generated estate in the tables above measures **1.84 calls per instance**
migrated, and 1.15 unmigrated. Same tool, same code, twelve and nineteen times
below the S3 figure, because the composition is different. An estate of IAM
roles, inline policies and DNS records reads cheaply; an estate of S3 buckets
does not.

If you want a number for your estate, measure your estate. Extrapolating from
somebody else's resource type will be wrong by whatever the ratio between the
two providers' Read implementations happens to be.

The `+ 8` in that fit is eight fixed calls. Six of the eight are
`ListBuckets`: five issued by the parent-read sweep, one by the provider's
own account and region resolution. The remaining two are `GetCallerIdentity`
and `GetUser`. They are 1.8% of the total at N=20 and 0.04% at N=1000. A
fixed term looks expensive on a small estate and disappears on a large one,
which is the opposite of how the sweep behaves and a good reason to fit a
line.

## Emulator wall clock is not on this page

Every cost figure here is a call count, with one deliberate exception. Seconds
measured against the pinned emulator grade the machine the test ran on, which
is why `live/plan-budget.json` records a wall clock and never gates on it. The one
timing table above is real AWS, where the seconds are network latency rather
than a property of whatever laptop ran the suite.
[`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md)
covers the distinction and the three other questions an emulator-backed
measurement cannot answer.
