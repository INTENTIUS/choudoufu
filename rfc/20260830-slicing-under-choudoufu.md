# Does Slicing Still Pay Under choudoufu?

Issue: https://github.com/INTENTIUS/choudoufu/issues/584

This is a measurement document, not a design proposal. It answers #584's
matrix and, as its prerequisite, #582's open question 3 — "does the read pass
dominate on a MIGRATED terralith?" — which #581 could only bound from below
because its fixture carried no markers at all.

Two of its results contradict documents already in this repository, including
one instruction in #584's own body. Those lead.

## Summary of findings

1. **Yes, the read pass dominates once the estate is migrated, and it takes
   over sooner than #581's lower bound suggested.** At the three measured
   scales the read pass is 21.0%, 48.4% and 67.5% of a full projection's API
   calls, against #581's unmigrated 13.3%, 36.5% and 56.4%. The crossover
   moves from N=553 instances to **N=322**. #579's premise holds.

2. **The estate-wide sweep does not scale down with a slice's type count. It
   is flat at 521 native-leg calls in every configuration measured** — three
   scales, three slice counts, every individual slice of an eight-way split.
   A slice declaring five types pays exactly what the whole estate pays, and
   very slightly more, because `sweepTypes` builds its universe by *removing*
   declared types from the admission table.

3. **So slicing multiplies choudoufu's plan cost and leaves stock's
   unchanged.** Summed over the estate at scale 1: stock costs 148 API calls
   whether it is one state or eight; choudoufu costs 744 at one state, 1288 at
   two and **4530 at eight**. Per slice — what a consultant actually feels —
   stock costs 11–22 calls and choudoufu 555–598.

4. **Under the `live-verify` mode #579 proposes, slicing would be
   irrelevant.** That mode costs the tagging leg alone, which measured 1, 2
   and 8 calls for the whole estate at k=1, k=2 and k=8. This is a projection
   from measured legs, not a measurement of an implemented mode.

5. **Guided discovery is unreachable on the shipped path.** Its narrowing
   lives in the `else` branch of `Discover`'s sweep, which `Request.TaggingSweep`
   bypasses — and `statelessApplyGuidedDiscovery` plus `cloudControlTarget`
   turn `TaggingSweep` on for every run, real AWS included, unless
   `TOFU_LIVE_CLOUDCONTROL=off`. Measured: guided on and guided off produce
   byte-identical call counts in every default-path configuration. On the
   `cloudcontrol-off` path, where guided is reachable, it saves 24 calls of
   293 (8.2%) — and only after an apply, because a plan never writes the hint
   it would read.

6. **floci paginates `GetResources`, and the harness was blind to it.**
   #584's own "Do NOT" says "floci returns a single page unconditionally
   (lex00/floci#185), so every `GetResources` = 1 is an emulator artifact."
   That is not true of the Resource Groups Tagging API:
   `ResourceGroupsTaggingService.java` defaults `resourcesPerPage` to 100 and
   returns a `nextPaginationToken` whenever more remain. The measured counts —
   1, 2 and 4 calls at 38, 137 and 335 tagged resources — are `ceil(n/100)`
   exactly. `flocitest.CountingProxy`'s continuation detector did not know
   `PaginationToken`, which is why every prior run in this repository reported
   `pagination_total = 0` for the one API where paging was actually
   happening. Fixed here, with a guard proven red.

7. **The scale-4 and scale-10 rows have no CLI column, because at the commit
   they were measured the terralith could not be planned at all.** That is
   #580's count-index refusal on `modules/team_pod`, which #578's real-AWS
   re-measure had already recorded as terralith-scale's scale-4 `test_plan`
   failure. It was **fixed on `main` at `5f2402e95a` (#593) while these runs
   were in flight**, and is verified gone here. Those two rows are measured in
   process, through `Discover` and `projection.BuildFrom`, which the lint rule
   never touched; the CLI column at those scales is now fillable and is not
   filled here.

## The measurement

Commit `cfd0dc58d4` (`origin/main`, "Merge pull request #581"), against
`ghcr.io/lex00/floci@sha256:c55d74e13e96c8b132056677337dba0084bb0b427cb039be2dbf9a8b7efc0948`
on darwin/arm64. Emulator only; nothing here touched real AWS.

`main` moved three times while these runs were in flight — #590's concurrent
stamping, #592's in-process formatting, #593's count-index fix. Scales 1 and 4
were therefore re-measured at `5a5145a244` (this branch on top of all three)
and **every API call count reproduces exactly**: 744 for the scale-1 CLI plan,
558 / 148 for its legs, 592 / 556 at scale 4, `native = 521` and
`universe = 1021/29/992` at both. Two things did change, both visibly and both
for the better, and they are noted where they appear: the scale-4 CLI plan now
runs at all (#593), and `live-import -approve` at scale 1 fell from 54.0s to
7.1s for the same 598 calls (#583/#590's concurrent stamping).

The instrument is `internal/live/discovery/slicing_bench_test.go`, added by
this work. It differs from `sweep_split_bench_test.go` (#581) in exactly two
respects, both required by the questions:

- **The estate is migrated before anything is measured.** The run applies the
  generated terralith with stock `terraform`, then runs
  `choudoufu live-import -state=… -estate=… -approve` against that state, then
  measures. #581 deleted the state file and measured an estate with no marker
  on any object.
- **The sweep is reported as two legs.** `sweepViaTagging` (tagging.go) costs
  the estate-filtered `GetResources` call(s) and nothing else — it is pure
  post-processing over `markerIndex`'s one answer — while the native leg costs
  one list attempt per type `arnJoinReaches` cannot resolve. The two have
  different scaling laws and #579's proposed mode costs only the first, so a
  single "sweep = 560" figure hides the question.

Legs are attributed by snapshotting the counting proxy inside
`Request.Progress` and bucketing each interval by the event's `Sweep` flag,
with the tagging leg taken out by action name (`GetResources`). A type whose
scan records nothing fires no progress event, so its calls — zero, in
practice — fold into the next interval.

Reproduce any row with:

```
SLICE_SCALE=4 SLICE_K=1 SLICE_OUT=/tmp/s4k1.json TF_FLOCI_TEST=1 \
  env -u PWD go test ./internal/live/discovery/ \
    -run TestSlicingMatrixAgainstFloci -v -timeout 90m
```

### Wall clock is not usable in these runs, and why

Every CLI plan of a slice containing the ECS layer took 273–274 seconds, to
within half a second, across nine independent runs. Every slice without it
took 2–4 seconds. The in-process `Discover` on the same estate takes 0.5–0.7s
and `BuildFrom` 0.1–1.0s. The distinguishing API is
`AmazonEC2ContainerServiceV20141113.DescribeServices`, 18–36 calls: the AWS
provider's `aws_ecs_service` read retrying against an emulator whose service
never reaches a stable state. That is a property of floci and of one resource
type, not of choudoufu's plan, and no wall-clock figure in this document should
be quoted as plan cost. `live/plan-budget.json` already treats wall clock as
never-gated for the related reason that it grades the machine; these runs were
also deliberately concurrent, which makes them worse still.

API call counts are deterministic and unaffected by either.

## Part 1 — The migrated sweep/read split (#582 question 3)

Whole estate, one state, after `live-import -approve`. Every instance
materializes (`materialized` equals `resolved_instances` at all three scales),
which is itself the difference from #581: its scale-10 run materialized 431 of
745.

| scale | instances | stamped | tagging leg | native leg | config scan | post-sweep | **sweep** | **read pass** | total | read share |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 79 | 38 | 1 | 521 | 26 | 10 | 558 | 148 | 706 | **21.0%** |
| 4 | 301 | 137 | 2 | 521 | 58 | 10 | 592 | 556 | 1148 | **48.4%** |
| 10 | 745 | 335 | 4 | 521 | 124 | 10 | 660 | 1372 | 2032 | **67.5%** |

`bound` is 15, 48 and 114; `unclaimed` is 5 at every scale. `discover_pages`
reads 0 at scale 1 and **1 at scale 4** on the re-measured runs — the tagging
leg's continuation page, counted for the first time because of the
`PaginationToken` fix in this change, and exactly one fewer than that scale's
`GetResources` calls. The scale-10 row was taken before the fix, so its
`discover_pages` reads 0 and means nothing; its four `GetResources` calls imply
three pages. Both legs are exactly linear through all three points, to the
call:

```
sweep     = 545.9 + 0.15315 * N      (592 predicted and measured at N=301, 660 at N=745)
read pass = 1.8378 * N + 2.8         (556 predicted and measured at N=301, 1372 at N=745)
```

so the read pass overtakes the sweep at **N = 322 instances**, about scale 4.3
of this fixture.

Against #581's unmigrated table on the same fixture and the same pin:

| scale | sweep (unmig. → mig.) | read pass (unmig. → mig.) | read share |
|---|---|---|---|
| 1 | 560 → 558 | 86 → 148 | 13.3% → 21.0% |
| 4 | 593 → 592 | 341 → 556 | 36.5% → 48.4% |
| 10 | 659 → 660 | 851 → 1372 | 56.4% → 67.5% |

**Migration leaves the sweep alone and raises the read pass by 61% to 72%.**
Per resolved instance the read pass goes from 1.09/1.13/1.14 calls
(unmigrated) to **1.87/1.85/1.84** (migrated). The RFC's own extrapolation —
"roughly 1471 calls and roughly 69% at scale 10, crossover roughly 300" —
was close: measured 1372, 67.5%, and 322.

One check worth keeping. **choudoufu's read pass costs exactly what stock's
refresh costs**, at every scale, to the call:

| scale | stock `terraform plan` | choudoufu read pass (`BuildFrom`) |
|---|---|---|
| 1 | 148 | 148 |
| 4 | 556 | 556 |
| 10 | 1372 | 1372 |

That is the shared term. Everything choudoufu spends above stock is the sweep.

## Part 2 — #584's matrix

Scale 1, migrated, one floci per scenario so no scenario sees another's
resources. Slices are produced by partitioning the generator's output into
weakly-connected components of its own reference graph and distributing them
largest-first — never by hand-writing fixtures, so both sides are the same
estate.

### Per slice and summed, API calls

| configuration | states | stock plan | choudoufu plan | choudoufu, `TOFU_LIVE_CLOUDCONTROL=off` |
|---|---|---|---|---|
| whole | 1 | **148** | **744** | 328 |
| sliced k=2, per slice | 2 | 77, 71 | 624, 664 | 216, 251 |
| sliced k=2, **summed** | 2 | **148** | **1288** | 467 |
| sliced k=8, per slice | 8 | 11–22 | 555–598 | not run |
| sliced k=8, **summed** | 8 | **148** | **4530** | not run |

and the same whole-estate row at scale 4, measured at `5a5145a244` once #593
made the plan possible:

| configuration | states | stock plan | choudoufu plan |
|---|---|---|---|
| whole, scale 4 (301 instances) | 1 | **556** | **1294** |

Stock's summed cost is 148 in all three, exactly: slicing redistributes
stock's refresh, it does not add to it. choudoufu's summed cost is 1.73x the
whole estate at k=2 and **6.09x at k=8**.

Two k values, as #584 requires, and they separate linear from worse. Each
additional state costs a flat **~541 API calls**: 744 at k=1, 1288 at k=2
(+544 for one more slice), 4530 at k=8 (+3242 for six more, 540.3 each).
Linear in k, not worse — and the multiplier is the whole sweep.

The in-process legs give the same model exactly, with no CLI overhead in it.
Summed `Discover` is 558, 1102 and 4344 calls at k=1, 2 and 8; summed
`BuildFrom` is 148 in all three. Predicted from the legs as
`k * (521 native + 1 tagging + 10 post-sweep) + sum(config scan) + 148`, that
is 706, 1250 and 4491 against 706, 1250 and 4492 measured.

The ratio a buyer would quote:

| | choudoufu / stock |
|---|---|
| whole estate, scale 1 | 5.0x |
| whole estate, scale 4 | **2.3x** |
| k=2, summed | 8.7x |
| k=8, summed | 30.6x |
| k=8, per slice | 25x–51x |

The scale-4 whole-estate row is the one that shows where this is going: the
gap closes as the estate grows, because the term choudoufu adds is flat. It
opens as the estate is sliced, for the same reason.

**Slicing is where choudoufu's cost model is worst, and it is the
configuration an already-sliced adopter starts from.** An operator's intuition
from stock — "slicing makes each plan cheaper" — is true of the per-slice
*latency* and false of the per-slice *API cost*, which barely moves.

### The three legs, per slice

| configuration | tagging leg | native leg | read pass | config scan | post-sweep |
|---|---|---|---|---|---|
| whole (k=1) | 1 | 521 | 148 | 26 | 10 |
| k=2, per slice | 1, 1 | 521, 521 | 77, 71 | 15, 23 | 10, 10 |
| k=2, summed | 2 | 1042 | 148 | 38 | 20 |
| k=8, per slice | 1 (each) | 521 (each) | 11–22 | 3–12 | 10 (each) |
| k=8, summed | 8 | 4168 | 148 | 87 | 80 |

`native_sweep_calls` is **521 in all thirteen slice-configurations measured**
— three whole estates at scales 1, 4 and 10, two slices at k=2, eight at k=8. The sweep universe is
1021 types for the whole estate and 1022–1026 for a slice, because
`sweepTypes` starts from `identity.AdmittedTypes()` and subtracts what the
configuration declares: a slice declaring fewer types has a *larger* sweep
universe, not a smaller one. Of that universe, `partitionSweepTypes` routes
29–34 types to the tagging leg and 992 to the native leg in every case.

That is #584's question 1, answered: **flat, and very slightly worse for a
small slice.**

### The third column, projected

`live-verify` does not exist. This is arithmetic on the tagging leg, which is
what #579's RFC says the mode would cost.

| configuration | full plan | projected `live-verify` |
|---|---|---|
| whole (k=1) | 744 | 1 |
| k=2, summed | 1288 | 2 |
| k=8, summed | 4530 | 8 |
| scale 4, whole | 1148 | 2 |
| scale 10, whole | 2032 | 4 |

Under such a mode, slicing costs one extra `GetResources` call per slice per
plan and nothing else — **slicing becomes irrelevant**, which is a much
stronger result than anything about the full plan's slicing behaviour.

But the tagging leg is **not** O(1), and this document is the first to say so:
it is `ceil(tagged_resources / page)` and floci's page is 100, measured at 1, 2
and 4 calls for 38, 137 and 335 tagged resources. `cloudcontrol.Client.
GetResources` sets no `ResourcesPerPage`, so the real page size is the Resource
Groups Tagging API's own default, which no floci-backed run can report. A
`live-verify` costed at "one call" would be wrong; costed at "one call per page
of tagged resources" it is right, and still two to three orders of magnitude
below the full plan at every scale measured.

## Part 3 — Guided discovery, on and off

Measured rather than reasoned about, as #584 asks, and the answer is that it
does nothing on the path that ships.

| variant | no hint (fresh migration) | after an apply wrote a hint |
|---|---|---|
| default | 744 | 709 |
| `TOFU_DISABLE_GUIDED_DISCOVERY=1` | 744 | 709 |
| `TOFU_LIVE_CLOUDCONTROL=off` | 328 | **269** |
| `TOFU_LIVE_CLOUDCONTROL=off` + guided off | 328 | **293** |

The same identity held at k=2: 624 == 624 on slice 0 and 664 == 664 on slice 1
for guided on versus off, and 216 == 216 / 251 == 251 with Cloud Control off.

Two mechanisms, both in the code and both confirmed by the numbers:

1. **`Request.Guided` is unreachable whenever `Request.TaggingSweep` is set.**
   `Discover`'s sweep is an if/else: the `TaggingSweep` branch calls
   `partitionSweepTypes` and `sweepViaTagging`, and only the `else` branch
   calls `guidedSweepUniverse`. `internal/command`'s `cloudControlTarget`
   returns `on` for every run that does not set `TOFU_LIVE_CLOUDCONTROL=off` —
   including real AWS, where the endpoint string is empty but the gate is
   still open — so `req.TaggingSweep = true` is the default everywhere.
   Guided discovery, `GuidedMaxAge`, `GuidedVerify` and `GuidedVerifyAge` are
   all dead code on that path.

2. **A plan never writes the hint guided reads.** `live_mode.go` says it
   outright: "A plan never persists, so a plan never writes one." The hint is
   written by `projection.Manager.PersistState`, which only an apply reaches.
   So a freshly migrated estate — migrate, then plan, which is the entire
   adoption path — has no hint at all, and guided falls back to full
   enumeration silently. That is why the "no hint" column shows 328 == 328
   even with Cloud Control off.

Once a hint exists, guided saves **24 calls out of 293, or 8.2%** of the
`cloudcontrol-off` plan and 3.4% of what the default plan costs. Recorded with
a caveat: the apply that wrote that hint failed on its last resource
(`aws_ecs_service`, floci rejecting a re-create as non-idempotent), so the hint
was written by an interrupted apply. The direction and the mechanism are not in
doubt — guided-on measured strictly lower than guided-off, on runs seconds
apart, and a plan mutates nothing — but the exact 24 deserves one clean
re-measurement.

### What the 24-hour verify sweep costs

`GuidedVerifyAge` (24h) forces a full sweep even when the hint is trusted, and
#584 asks what a slice pays for that on the same cadence as a whole estate.

**On the shipped path: nothing, because every sweep is already a full one.**
There is no narrowed state for the verify pass to interrupt. On the
`cloudcontrol-off` path the verify sweep costs the 24 calls guided would
otherwise have saved — 293 instead of 269 — once per 24 hours per estate, and
therefore k times per 24 hours for a k-way split. At 24 calls that is not a
duty cycle anyone needs to plan around; the thing that would need planning is
the 521-call native leg, which is paid on **every** plan, k times over.

## Part 4 — What slicing costs an operator in configuration

Two partitions were computed on the same estate.

**Component partition (measured).** Weakly-connected components of the
estate's own reference graph: at scale 1 there are ten, and both k=2 and k=8
distribute them with **zero cross-slice references**. Every slice's
configuration loads and plans on its own with no edit at all. This is the
best case for slicing and it is what the matrix above measures, deliberately:
the conservative choice when asking "does slicing still pay" is to give
slicing its cheapest form.

**Layer partition (computed, not applied).** One slice per generated file —
`iam.tf`, `network.tf`, `ecs.tf`, `dns.tf`, `pods.tf` — which is how a human
actually cuts an estate. It cuts one component and produces exactly three
cross-slice references, all in the ECS layer:

| block | reference | what it must become |
|---|---|---|
| `aws_ecs_task_definition.svc_0000` | `aws_iam_role.svc_0000_exec_role.arn` | a data source, a remote-state output, or a hardcoded ARN |
| `aws_ecs_service.svc_0000` | `aws_subnet.main.id` | same |
| `aws_ecs_service.svc_0000` | `aws_security_group.ecs.id` | same |

Three conversions for a 79-resource estate is a small number, and it stays
small as scale grows because the coupling is between *layers*, not between
instances: at scale 4 and scale 10 the same three references are the only ones
the layer cut breaks. The conversion cost of slicing this estate is real but
it is not what makes slicing expensive under choudoufu — the flat sweep is.

The harness materializes a cross-slice reference as a literal read from the
whole-estate state, and refuses outright if such a reference carries an index
expression, rather than emitting a slice whose configuration is silently
wrong.

## Part 5 — Two defects this measurement surfaced

Neither is about slicing; both were found by running the thing.

**#580's count-index refusal, met and then overtaken.** At `cfd0dc58d4`,
`modules/team_pod` rendered `count.index` into three identity-bearing arguments
(`aws_iam_role.pod_role`, `aws_iam_role_policy.pod_inline`,
`aws_iam_instance_profile.pod_profile`) and the count-index rule refused all
three once the module's own `count` — `podSizePerScale * scale` — exceeded 1.
Resolution and `live-import -approve` were never affected (301 and 745
instances resolve, and 137 and 335 stamp without complaint); only the plan
refused.

This is #580. #578's real-AWS re-measure at `e41ba8a4d4` had already recorded
it as terralith-scale's scale-4 `test_plan` failure, and **#593 fixed it at
`5f2402e95a` while these runs were in flight** — "lint: render count.index per
module instance across an expanded call". Verified here on the rebased tree:
the same `choudoufu plan` on the same unmodified scale-4 fixture reports zero
`count.index is not available` errors and proceeds past lint into discovery.

One thing about the shape is worth keeping even though the defect is gone. It
reproduced with **no cloud and no emulator at all** — a plain `plan` against a
dead endpoint refused in 1.1 seconds having made zero API calls — so a whole
class of lint regression on a generated fixture is catchable for free, and was
being caught only by a real-AWS certification run.

**The three ECS resources a migrated plan proposes creating are a floci
artifact.** On the emulator, stock's plan on the same state reports no
differences while choudoufu's reports `Plan: 3 to add` —
`aws_ecs_cluster.main`, `aws_ecs_service.svc_0000` and
`aws_ecs_task_definition.svc_0000` — at k=1 and in whichever slice holds the
ECS layer at k=2 and k=8. Stock is the oracle, so on the emulator alone that
reads as a fidelity gap.

It is not one. `live/live-cert/terralith-scale.sh`'s `test_plan` stage asserts
literally "No changes. Your infrastructure matches the configuration." after
migration, and #578's real-AWS run at scale 1 passes all four stages on this
same estate. So the same three resources plan empty against AWS and non-empty
against floci. The emulator's own behaviour around them is visibly broken in
two other ways in these runs — the 273-second `DescribeServices` retry loop,
and a rejected re-create ("Creation of service was not idempotent") that failed
the apply-for-hint step — which is consistent. Worth a floci issue; not a
choudoufu one.

## What this does not cover

- **Real AWS.** Emulator only, by instruction. Every wall-clock figure and
  the real `GetResources` page size need a real-AWS run before they mean
  anything about production. #578 owns that spend. (The ECS discrepancy is the
  one thing here already settled that way: #578's scale-1 real-AWS run passes
  `test_plan`'s empty-plan assertion.)
- **`TOFU_LIVE_CLOUDCONTROL=off` at k=8.** Measured at k=1 and k=2 only.
- **The scale-4 and scale-10 CLI plan.** Refused at the measured commit by
  #580, fixed since at `5f2402e95a`; those rows are in-process `Discover` +
  `BuildFrom` only, and the CLI column at those scales is now fillable.
- **A clean guided-on measurement.** The apply that wrote the hint errored on
  its last resource. See Part 3.
- **k beyond 8, and a non-component partition measured rather than computed.**
  The layer cut's three conversions are computed from the reference graph; no
  layer-cut slice was applied or planned.
- **Anything about non-AWS providers**, or about an estate whose types differ
  from this one's thirteen. The 521-call native leg is a property of the
  admission table and the ARN join table, not of the estate — but that is an
  argument, and only this estate was measured.
