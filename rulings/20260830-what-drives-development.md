# What Drives Development

Issue: https://github.com/INTENTIUS/choudoufu/issues/522

Ruled 2026-08-29, amended the same day, recorded here 2026-08-30.

The estate board was this fork's work queue from the day it existed. It
stops being one. Fast, purpose-built fixtures drive daily work; estates
become a release cadence and keep the coverage claim; real AWS answers the
three questions no emulator can.

Every figure below was recomputed at `b20a144ab0` rather than copied from
the issue that asked for the ruling. Nine disagree with the text they came
from, including two the ruling itself rests on, and each disagreement is
stated where it lands.

## The ruling and its amendment

The 2026-08-29 ruling accepted the tiered strategy and chose defaults for
velocity. Its first section said:

> **`behaviors_proven: N of 14`** becomes the headline number and the gate.
> [...] **`estates_clear: M of 26`** keeps its exact current definition and
> computation. It stops being the gate and becomes breadth reporting.

The amendment, the same day, reversed exactly that:

> `estates_clear` **remains the coverage claim** and stays on the public
> page as the answer to "does this work across real-world configurations."
> [...] `behaviors_proven` **is internal-facing** [...] It is a measure of
> the development loop's reach, not of correctness.

and stated the distinction the rest of this page depends on:

> Being proven slowly is not the same as being unproven, and conflating the
> two is the same defect class this repo keeps catching: a number that reads
> as evidence about something it does not measure.

Both are quoted because the first is still in the tree.
`site/layouts/shortcodes/gauntlet-bars.html`'s header comment calls
behaviors proven "issue #522's headline and gate" and the shortcode renders
that bar above the two estate bars, while the prose beside it on the
progress page (`site/content/docs/progress/_index.md`, "How close AWS is")
carries the amended framing. The prose is right and the layout is stale. That is a layout fix,
named here so the next reader does not read the comment as the decision.

## 1. Tier 1, the behavior matrix, drives development

**The tier.** `live/behaviors.json` is the catalogue,
`go run ./tools/gauntlet behaviors` is the runner, and `live/e2e/<id>/run.sh`
is where each fixture lives.

**The unit.** One fixture: a purpose-built configuration that exercises one
seam and states in the artifact what it would take for it to pass
vacuously. The catalogue holds 34 entries; 12 are `category: shape` with
`runner: true` and are what the matrix runs by default. Those 12 declare 48
resources between them, one at the smallest (`deterministic-recreate`) and
nine at the largest (`per-element`). The other 22 are catalogued and not
run: 20 cold-adoption crossings ([#274](https://github.com/INTENTIUS/choudoufu/issues/274),
outside the promise per `HANDOFF.md`), one shape fixture with no entry
point of its own (`estate-block`), and `live/e2e/run.sh`, the pre-protocol
demo.

**The budget.** The whole default set inside five minutes of wall clock,
and the budget is deliberately not an assertion.

That last clause looks like a contradiction and is not. A wall clock
measured against the emulator grades the machine that ran it.
`live/plan-budget.json` says so in its own note - `wall_clock_bucket` and
`measured_at` are "informational only, never gated: floci's performance on
whatever machine runs the test, not this repository's code, is what a
wall-clock assertion would actually be grading" - and `live/FLOCI.md`'s
section 3 generalises it. What gates in tier 1 is what gates everywhere
else here: a verdict, and where a cost is claimed, a call count. Call
counts are deterministic, reproduce on any machine, and are measured in
seconds against the emulator. `live/plan-budget.json` fits
`calls_total = 22*N + 8` with no residual at N=20, 200 and 1000 for exactly
that reason.

So the five minutes is a bar the matrix is held to by re-measuring it, not
a test. A fixture that cannot meet it is split or moved up a tier. Nothing
fails red at five minutes and one second, because a check that grades the
machine is worse than no check.

**Where the bar actually stands, recomputed.** The 11 default fixtures
carrying a recorded run at `283b99d3c5` sum to **652.7 s (10 m 52 s)**
serially, the longest single fixture being `repeated-module` at 99.8 s. The
twelfth, `destroy-teardown`, has never recorded a run. The ruling's
"currently 2m28s" was true of a smaller set; a serial run of today's set
misses the bar by more than double.

The bar survives because of
[#541](https://github.com/INTENTIUS/choudoufu/issues/541), which made the
runner concurrent at `defaultBehaviorsParallel = 8`, each fixture on its
own emulator and its own port. Its note records that the matrix "passed the
five-minute bar by four seconds on a loaded machine, which is a coin flip,
not a margin". The consequence for the growth rule is worth stating: the
quantity the bar now constrains is the parallel makespan, not the serial
sum, and the makespan is bounded below by the longest single fixture. One
fixture near 300 s would blow the bar however wide the runner gets.

**What tier 1 reaches today.** `behaviors_proven` is **1 of 14**. The
denominator is the stage count (`len(Stages())`), not a count of anything
in `live/behaviors.json`. Of the 12 default fixtures, seven map to
`test_plan`, two to `day2_remove`, and three (`dataread-projection`,
`destroy-teardown`, `provisioner-taint`) map to no stage yet. A stage with
no mapped fixture, or with a mapped fixture that last failed, is not
proven; `tools/gauntlet/behaviors.go`'s `BehaviorsProven` refuses vacuous
agreement, which is why the number is 1 rather than 3.

**The mandatory shapes stand as ruled**: a real `count` block, a real
`for_each` map, a module-nested resource, and for a stage that touches
identity resolution, one fixture per identity kind (server-minted,
deterministic, none at all). The catalogue has since grown a fourth
identity kind the ruling did not name, `server-minted-untaggable` - the
record rung's own `ClassRecordLocated` case, carried by `record-located`.
The ruling's three-kind list came from one day's findings and the fourth
arrived the way the growth rule intends. That rule is unchanged: the set
extends when a defect escapes it, with the issue cited, and not before.

## 2. Estates are a release cadence and the coverage claim

Estates stop being the work queue and keep two jobs, both of which nothing
else in the tiering can do.

**The coverage claim.** `estates_clear` is the number a customer should
read, with its computation and meaning untouched. Recomputed:
**12 of 27 estates clear** across the whole board, **11 of 26** in `core`.
The board is 27 rows by 14 stage columns, 378 cells, **249 pass, 129
not_run, zero fail**. Every row carries all 14 stage keys and the declared
`sets` aggregates agree with the rows.

The issue text says 26 estates, 364 cells, 241 pass, 123 not_run. That was
exactly right at `1944a34e4b` on 2026-08-29 and is now one estate and eight
passes behind: `terralith-scale` became a board row on 2026-08-30.

**The cadence.** Nightly at `17 3 * * *` and on demand
(`.github/workflows/gauntlet.yml`, `workflow_dispatch` taking a set or a
list of estate names), plus a per-release snapshot into `live/history/` on
a `v*` tag. That is what the automation already does when it works, and
today it does not.
[#496](https://github.com/INTENTIUS/choudoufu/issues/496) is open: the
nightly's verdict pull request has failed on every scheduled run since
2026-08-21 with "GitHub Actions is not permitted to create or approve pull
requests", so the measurement is computed and then discarded. Moving
estates onto a cadence while the cadence discards its own results moves
them onto nothing. #496 is this section's load-bearing dependency and is
named as one.

**What stage activation costs now.** This is the whole cost argument, and
the issue's version of it needs one correction to be usable.

Eleven of the 14 stages are `active`; three (`day2_crash`, `day2_teardown`,
`plan_approval`) are `planned`. Restricted to active stages the board has
**49 not_run cells** across all 27 estates, 23 of them on the ten headline
stages. The issue's "~122 sections, roughly 80 hours" counts all 14
columns, so 80 of its cells belong to the three planned stages no estate
has ever been asked to run - 27 + 27 + 26 = 80, and 129 - 49 = 80 exactly.

That locates the argument rather than weakening it. The 49 outstanding
cells are catch-up on stages already activated. The 80 are the price of
activating three more the old way, and they are what this ruling refuses to
pay. A stage now activates when its representative-set fixtures pass, which
for `day2_teardown` means one fixture - `destroy-teardown`, five objects
across all three mandatory shapes with a real destroy-order dependency,
already written and already in the default set - instead of 27 hand-written
estate sections.

**Two premises of the ruling do not survive recomputation**, and both were
arguments about estate cost.

The issue says "a median estate run is ~175s of which ~106s is
`cold_deploy`". Sixteen of the 27 rows now carry `last_run.duration_s` and
`last_run.stage_seconds`. Their median run is **187.7 s** and their pooled
`cold_deploy` share is **618.0 s of 3711.6 s, 16.7%**, ranging from 4.5%
(`corpus-leynos-monitoring`) to 36.8% (`reference-ec2-vpc`). The 106 s is
`reference-ec2-vpc`'s own `cold_deploy`, the largest in the sample, quoted
as if it were the median. (`terralith-scale` reads 81.0 of 80.9 s, an
oddity from a row whose only recorded stage is `cold_deploy`; dropping it
moves the pooled share to 14.8%.)

The ruling therefore says of
[#438](https://github.com/INTENTIUS/choudoufu/issues/438)'s `cold_deploy`
cache that it "cuts ~60% off every estate run" and that "the duration
profile it waited for now exists". Neither holds. The profile covers 16 of
27 rows, not the board, and the share it shows is 16.7% pooled. **#438 was
declined and closed on 2026-08-30** by the unit that went to build it, on
the grounds that designing a provenance-sensitive cache - the
[#413](https://github.com/INTENTIUS/choudoufu/issues/413)/[#414](https://github.com/INTENTIUS/choudoufu/issues/414)
defect family by name - from the cheapest fixtures in the set is guessing
with extra steps. That decline stands, and this ruling withdraws the cache
as a dependency.

The ruling's other named dependency, a scoped mode for `TestIdentityGolden`
"at 434s", has not landed and its premise cannot be checked: **no such
duration is recorded anywhere in the tree**, and `434` matches issue #434,
which is the issue that added `duration_s` recording in the first place.
What the test does pin is real and larger than the issue claimed.
`HANDOFF.md` and `live/identity_golden_pin_test.go` both say **1795
rendered identities across 642 configuration directories**, not "1320+",
and 1320 is a figure `measuring-choudoufu` already flags by name as one
this repository carried stale. `internal/live/check/identitygolden_test.go`
offers no subset mode, no environment scope and no `testing.Short` branch,
so the local-iteration cost the ruling wanted reduced is unmeasured and
unreduced. It is real work. It is not evidence, and nothing here should be
read as having measured it.

## 3. What only real AWS can answer

Three questions, and the price of asking them.

**The price.** One certification round of `terralith-scale` at scale 10 -
745 resources, `us-east-2`, four stages - is `duration_s: 4040.6`, 67.3
minutes, recorded in `live/gauntlet.json`'s `live_cert` block at commit
`ca77a50e32`. The resource count is generated rather than asserted:
`live/live-cert/terralith-scale.sh` computes `EXPECTED=$((74 * SCALE + 5))`.
For that hour it returns four stage verdicts and one plan-timing triple
([#617](https://github.com/INTENTIUS/choudoufu/issues/617): 328, 323, 322 s
against stock's 19, 20, 33 s). That ratio is why real AWS is a question
asked deliberately rather than a loop to develop in.

**Latency.** The rates are what floci cannot supply, and two of them have
already been mixed once
([#618](https://github.com/INTENTIUS/choudoufu/issues/618)), so each is
stated with its denominator.

| rate | arithmetic | what it divides | source |
|---|---|---|---|
| ~0.138 s / read call | 19 s x 10 slots / 1372 calls | stock's own refresh at scale 10, `-parallelism 10` | `internal/live/projection/readconcurrency.go` |
| 0.36 s / sweep call | 201.3 / 558 | the mean difference over stock, whole sweep | `internal/live/discovery/sweepconcurrency.go` |
| 0.367 s / sweep call | 205 / 558 | the whole plan, whole sweep | same, and `sweep_parallelism_bench_test.go` |
| 0.39 s / native call | 203 / 521 | the native leg alone | same |

`0.367 x 521 = 191` is a number nothing measured, which is the error #618
made. The 0.138 is derived rather than measured - the comment carrying it
says the 19-second refresh "implies" it - and both its denominators live in
#617's body rather than in a committed artifact; the one committed sibling
figure, `live/gauntlet.json`'s scale-10 `test_plan`, reads 328 s where
`readconcurrency.go` says 322. **No sweep rate anywhere in the tree reaches
0.46**, and the ~0.37 in `terralith_ceiling_bench_test.go` that looks like
a match is floci's per-resource apply latency, which is neither real AWS
nor per call.

Against those, the emulator: the same 558 sweep calls run sequentially in
**433.6 ms** (`sweep_parallelism_bench_test.go`, scale 1), which is 0.78 ms
per call, and the ceiling benchmark's own per-call series falls from 0.70
to 0.09 ms/call as N grows. floci is two to three orders of magnitude
cheaper per call than an account, and the round number "about a
millisecond" is a safe upper bound rather than a measurement.

**Throttling.** floci applies none, and there is no scale at which it will
(`live/FLOCI.md` section 4; the ceiling benchmark reads
`throttle_total = 0` at every tier including its 4817-call run). The
scale-10 certification row is the largest real evidence held here:
`cold_deploy` 112 throttle / 112 retry over 418 s, `migrate` 179 / 179 over
215 s, `test_plan` 1 / 1 over 328 s. The read path barely throttles and the
write path does, the same shape
[#567](https://github.com/INTENTIUS/choudoufu/issues/567) found at scales 1
and 4 (1 and 35 events) and which `live/FLOCI.md`'s table still records.
The SDK's backoff absorbed all of them and the whole cost appeared as wall
clock.

This is also the question bearing on a default chosen three times.
`DefaultSweepParallelism`, `DefaultReadParallelism` and
`liveimport.DefaultParallelism` are all 10, on stock's `-parallelism 10`
precedent rather than on measured read-side limits, and
`readconcurrency.go` says outright that "no measurement taken against the
emulator can justify a number above the one real AWS is already known to
tolerate." Tier 3 is where that gets tested or does not.

**Value semantics.** `live/FLOCI.md` section 1 is the enumeration: floci
accepts values real AWS rejects, silently, with a 200. The recorded case is
`tools/terralith-gen`'s Route 53 TXT records carrying their own pair of
double quotes, which the AWS provider then quote-wrapped again. Three
separate measurement runs (#564, #565, #566) accepted it; the first
live-AWS run rejected it immediately with `InvalidCharacterString` (#567).
A fixture's argument shape, resource count and plan structure are fair game
against the emulator. Whether a string is one AWS will take is not, and no
number of green emulator runs converts one into the other.

A fourth question belongs to tier 3 by default rather than by choice: the
real page size of the Resource Groups Tagging API.
`cloudcontrol.Client.GetResources` sets no `ResourcesPerPage`, floci's 100
is floci's own constant, and `live/FLOCI.md` section 2 records that this is
still unmeasured.

## 4. What is lost

A tiered strategy that treats the fast tier as a smaller copy of the slow
one will mislead. This is the accounting.

**floci understates every concurrency win by construction.** The sweep
parallelism table measures 433.6 ms at parallelism 1 and 154.9 ms at 20 for
the same 558 calls, and its own doc comment says why the ceiling is about
2x rather than 10x: over loopback roughly half of discover time is
non-overlappable bookkeeping, comparable to the call time itself. Against
an account where each call costs 0.367 s that same half is a fraction of a
percent and the ceiling is the parallelism. The emulator's numbers measure
the overlap and not the saving, and the real-AWS projection - 521 x 0.39 s
= 203 s sequential, roughly 20 s at ten - **stays a projection**: nobody
has re-run the real-AWS timing since
[#605](https://github.com/INTENTIUS/choudoufu/issues/605) made the sweep
concurrent. Three concurrency changes landed on that evidence (#583
stamping, #605 the sweep list calls, #585 the read pass) and the fast tier
proved call parity and determinism for all three. It cannot prove the
saving for any of them.

**The account is empty, so account-scaled cost is invisible.**
[#622](https://github.com/INTENTIUS/choudoufu/issues/622) is open and
exists precisely because of this. `scanTypeCloudControl` issues one
`GetResource` per listed object arriving without tags, in the sequential
consuming loop, at every parallelism setting, and per
[#586](https://github.com/INTENTIUS/choudoufu/issues/586) it is the one
part of the sweep bounded by the customer's account rather than by their
estate. On floci it measures about 40 calls and reads as negligible. On a
consultant's populated client account it is the term that grows, and it is
the term still running strictly one call at a time. No fixture at scale 1
against an empty emulator can see it, and no amount of tier-1 discipline
will surface it.

**Value semantics, pagination and wall clock**, per section 3 and
`live/FLOCI.md`. Each has an incident behind it, and the pagination one is
the most instructive: the claim "no floci-backed N will ever produce a
nonzero `pagination_total`" came from an instrument blind to the one field
that was actually paging. A zero from a counter is only as good as the
counter's own coverage, and nothing about a zero announces which it is.

**A small fixture can only ask what a small configuration can express.**
The board's high-yield era produced 15 compatibility-defect issues and 31
`[gauntlet:...]` issues, and they came from configurations people wrote for
their own reasons rather than for a test. `live/GAUNTLET.md`'s "estates buy
behaviors, cohorts buy types" still holds. What changes is that the buying
happens on a cadence instead of one hand-written section at a time.

**And tier 1's own reach is 1 of 14 today.** Three of twelve default
fixtures map to no stage, `destroy-teardown` has never recorded a run, and
`gauntlet behaviors` appears in no `justfile` recipe, no CI step and no
line of `scripts/pickup.sh`. The amendment asked for `behaviors_proven` to
be "reported where contributors look - `pickup.sh`, the contributor docs".
It is on the progress page, correctly framed, and it is not yet where the
amendment asked for it.

## The evidence this rests on

What the fast loop found on 2026-08-29 and 2026-08-30, with the instrument
that found each. Every one is a product defect, which is the class nine
catch-up estate sections produced none of.

| finding | instrument |
|---|---|
| [#613](https://github.com/INTENTIUS/choudoufu/issues/613): a stateful plan after `live-import` proposes stripping every marker off a migrated estate, and applying it silently un-migrates it | `TestStatefulPlanAfterLiveImportAgainstFloci`, **two resources** against the emulator. #611 had measured it at 38 of 79 and 137 of 301 instances; two is all it takes and it fits in a test |
| [#596](https://github.com/INTENTIUS/choudoufu/issues/596): a failed import proposes creating a duplicate of a live, tagged object | `internal/live/projection/sighted_test.go`, a `tofu.MockProvider` and **no cloud at all** - the file contains no reference to the emulator |
| [#605](https://github.com/INTENTIUS/choudoufu/issues/605), [#583](https://github.com/INTENTIUS/choudoufu/issues/583), [#585](https://github.com/INTENTIUS/choudoufu/issues/585): the sweep, stamping and the read pass are each strictly sequential | read from the source - no `go func`, no `errgroup`, no `sync.WaitGroup` in any of the three - then proved by millisecond-resolution probes over loopback |
| [#619](https://github.com/INTENTIUS/choudoufu/issues/619): two divergent refusal sets for one surface, and `live-plan`'s help text at `live_plan.go:3476` still lists `-destroy` as rejected when it is not | reading call sites |

Two costs moved by an order of magnitude once the loop was fast enough to
iterate against, each found and fixed at scale 1 in a single round.
[#627](https://github.com/INTENTIUS/choudoufu/issues/627) took a
steady-state plan on a migrated 79-instance terralith from **710 to 157**
AWS calls against stock's 150, three runs a column with no variance.
[#636](https://github.com/INTENTIUS/choudoufu/issues/636) took the same
plan's record-store round trips from **377 to 1**, having first established
that nothing had ever been counting them, because a record read never
reaches the counting proxy standing in front of the AWS endpoint. (The
issue that asked for this ruling says 706 for the first of those; 706 is
the discovery bench's total for the same estate, and 710 is what the CLI's
counting proxy measured for the plan #627 actually fixed.)

And a number measured the slow way was wrong. The published slicing
figures - stock flat at 148, this fork 744 at k=1 rising to 4530 at k=8,
a 30.6x penalty - were a **refused plan's** cost: the bench wrote its own
`versions.tf` carrying `skip_requesting_account_id = true`, so account
resolution failed, ECS identity resolution failed, and every
`choudoufu plan` in the matrix exited 1 while the bench only `t.Logf`'d it
([#641](https://github.com/INTENTIUS/choudoufu/issues/641), closing
[#634](https://github.com/INTENTIUS/choudoufu/issues/634)). Re-measured on
the fast loop with every plan exiting 0 and reporting no changes, the
penalty is **1.21x at k=8**, and stock is not flat either - it is
`148 + 2k`. Worse for anyone tempted to salvage the old ratio: the
refusal's overhead is not uniform, it is `18 - 4k`, and it **changes sign**
near k=4.5, so from about k=5 the broken configuration reads cheaper than
the correct one and no ratio built on those numbers is distorted the same
way at two different k.

That is the argument in one line. The slow instrument produced one number
per hour and one of them was wrong in a way its own harness could not
report. The fast one produced four product defects and two order-of-
magnitude fixes in a day.

## What was not verified

- **The real-AWS latency table predates the concurrency work.** Every rate
  in section 3 comes from #578 and #617, re-read rather than re-run. #605,
  #583 and #585 all landed afterwards. Until a certification round runs
  against an account on current `main`, "roughly a tenth of 203 s" is
  arithmetic.
- **The five-minute bar was not re-measured for this ruling.** 652.7 s is
  the serial sum of recorded `last_run.duration_s` values at `283b99d3c5`;
  the parallel makespan under `-parallel 8` was reasoned about, not timed.
  `destroy-teardown` has no recorded duration at all, so the current set's
  makespan is not known.
- **`TestIdentityGolden`'s wall clock is unmeasured**, here and everywhere.
  The "434s" the ruling cites has no source in the tree, and this ruling
  did not run the test to supply one.
- **The `cold_deploy` share covers 16 of 27 rows.** Eleven estates,
  including every heavy one (`corpus-eks-basic`, `corpus-ecs-fargate`,
  `corpus-rds-complete-postgres`), carry no duration at all, which is the
  gap #438's decline rests on. 16.7% is the pooled share of what has been
  measured, not of the board.
- **`live/cohort-acceptance.json` is behind the tree.** It records 31
  cohorts, 5 pass and 26 fail over 710 resource blocks at commit
  `296fca17b3`; `live/e2e/estates/` at HEAD holds 711 blocks and one more
  distinct type, `aws_ecs_task_definition` added to `ecs-eks` by #554.
  Tier 2's own wall clock is recorded nowhere - the only timing facts in
  `internal/live/acceptance` are per-phase timeouts - so the issue's
  "~23 min" is unsourced and is not repeated here.
- **No call count was re-measured for this ruling.** Every figure in
  section 3 is read from a committed artifact, a doc comment or a merged
  issue, and each is cited so the next reader can re-derive rather than
  re-quote.
