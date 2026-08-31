# Does Slicing Still Pay Under choudoufu?

Issue: https://github.com/INTENTIUS/choudoufu/issues/584

## Correction, 2026-08-30: every CLI-plan figure below was a refused plan's cost

Issue: https://github.com/INTENTIUS/choudoufu/issues/634

Nothing in the original text has been deleted. The superseded numbers stay
where they were written and each affected table carries a pointer back here,
so anyone who quoted one can match it to what replaced it.

### What was wrong

The instrument, `internal/live/discovery/slicing_bench_test.go`, wrote its own
`versions.tf` for each slice instead of reading the one `tools/terralith-gen`
had generated moments earlier. That second copy set
`skip_requesting_account_id = true`. With it set the AWS provider resolves no
account id, every ARN-shaped identity it composes loses its account segment,
ECS identity resolution fails (#572), and since #596 `choudoufu plan` refuses
rather than proposing a duplicate:

```
Error: Live resource listed but not importable
  ... a live aws_ecs_task_definition ... carrying this estate's tofu-estate marker
```

The generator dropped the flag under #628, fixed in #633. The three other
benches in this package that use the generator write no `versions.tf` of their
own, so that change corrected them. This one read nothing, so it kept its copy
of the flag through #633 and beyond, and every
`choudoufu plan` in the matrix below **exited 1**. The bench recorded
`exit_code: 1` on every CLI row and only `t.Logf`'d it, so the counts were
written up as a clean plan's cost. Both defects are fixed at `5ff7f43f5b`: the
bench reads the generator's `versions.tf`, and a non-zero plan exit is now
`t.Errorf`, symmetrical with the stock-plan check that was already loud.

### What the numbers are now

Re-measured at commit `5ff7f43f5b` against
`ghcr.io/lex00/floci@sha256:c55d74e13e96c8b132056677337dba0084bb0b427cb039be2dbf9a8b7efc0948`
on darwin/arm64, scale 1, component partition, cold pass. Every plan below
exits 0 and reports "No changes. Your infrastructure matches the
configuration."

| configuration | stock, published | stock, now | choudoufu, published | choudoufu, now |
|---|---|---|---|---|
| whole (k=1) | 148 | **150** | 744 | **157** |
| k=2, summed | 148 | **152** | 1288 | **163** |
| k=8, summed | 148 | **164** | 4530 | **198** |

Per slice: stock reads 79/73 at k=2 and 13–24 at k=8 (published: 77/71 and
11–22); choudoufu reads 83/80 at k=2 and 17–28 at k=8 (published: 624/664 and
555–598).

### Two causes, and only one of them is this defect

The collapse from 744 to 157 is not one correction. It is two, and they are
separable because both were measured independently:

1. **The provider block.** `rulings/20260830-stateful-equivalence.md` ran the same
   fixture and pin at `b1b1c6a13e`, before the narrowing below landed, with
   only that one line changing, and got 744 → **710**: a 34-call difference,
   plus 273s → 2.7s of SDK retry backoff.
2. **`09d180f921`, "a steady-state plan stops enumerating the whole admission
   table",** landed on `main` after this document was written. It narrows the
   native sweep leg to the types an estate has its own evidence of when
   `CollectUnclaimed` is unset, and its own commit message records 710 →
   **157** on this same fixture and pin. This re-measure reproduces that 157
   exactly, and its seven-call residual over stock call for call:
   `GetResources` 1, `ListRoles` 1, ECS `ListServices`/`ListTaskDefinitions`/a
   second `DescribeTaskDefinition` 3, and a second `GetCallerIdentity`/`GetUser`
   pair 2.

So the provider block is worth tens of calls; the narrowing is worth hundreds.

### Was the refused-plan overhead uniform across k? No, and it changes sign

#634 asks this because the ratio depends on it and nobody had checked. It was
checked by running both provider blocks at the same commit and pin, everything
else held fixed:

| k | provider block | stock | choudoufu | ratio | plan verdict |
|---|---|---|---|---|---|
| 1 | with `skip_requesting_account_id` | 148 | 171 | 1.16x | s0 **refused** |
| 1 | without (correct) | 150 | **157** | **1.05x** | empty |
| 2 | with | 148 | 173 | 1.17x | s1 **refused** |
| 2 | without (correct) | 152 | **163** | **1.07x** | empty |
| 8 | with | 148 | 184 | 1.24x | s3 **refused** |
| 8 | without (correct) | 164 | **198** | **1.21x** | empty |

The overhead on choudoufu's summed cost is **+14 at k=1, +10 at k=2 and −14 at
k=8**. Two constants of opposite sign make it, and both are visible per slice.
The refusal costs **+18** calls, and it is paid once, by whichever single slice
holds the ECS layer, however large k is: at k=8 that slice reads 39 against 25.
Skipping the account lookup *saves* **4** calls in every slice, ECS or not, so
it saves 4k across the estate: every other slice at k=8 reads exactly 4 lower.
Net `18 − 4k`, which is +14, +10 and −14 at the three measured k, crosses zero
near k=4.5, and means that from about k=5 upward **the broken configuration
reads cheaper than the correct one**.

Stock carries the second constant alone, at 2 calls per slice rather than 4,
because choudoufu configures the provider twice. With the flag stock costs 148
at every k, which is exactly the "148 in all three, exactly" this document
reports as a finding; without it, it costs `148 + 2k`. That finding was an
artifact of the missing account lookup. Slicing does add a small constant to
stock's refresh, two calls per extra state, rather than nothing.

None of this is uniform in k, so no ratio built on those numbers is safe at any
k, and the distortion is worst where the document's headline claim sits.

### Does the 30.6x ratio survive? No

| | published | re-measured at `5ff7f43f5b` |
|---|---|---|
| whole estate, scale 1 | 5.0x | **1.05x** |
| k=2, summed | 8.7x | **1.07x** |
| k=8, summed | **30.6x** | **1.21x** |
| k=8, per slice | 25x–51x | **1.13x–1.39x** |
| whole estate, scale 4 | 2.3x | not re-measured; still refused-plan-derived |

The "each additional state costs a flat ~541 API calls" model goes with it. On
today's CLI path an extra state costs about **6** calls: 157 at k=1, 163 at
k=2, 198 at k=8, which is 5.8 per additional slice over the six between k=2 and
k=8.

### What survives

- **Finding 2 stands, and is the one to keep.** The estate-wide sweep still
  does not scale down with a slice's type count: `native_sweep_calls` is
  **512** for every slice at every k measured here (521 when this document was
  written; the table moved by nine calls between the two commits, for reasons
  unrelated to the provider block, see below), the sweep universe is still
  1021 types whole and 1022–1026 sliced, and `partitionSweepTypes` still routes
  992 of them to the native leg. Summed over the estate that is 512, 1024 and
  4096 calls at k=1, 2 and 8. The flat sweep is exactly where it was.
- **What changed is who pays it.** `09d180f921` took that leg off the
  steady-state CLI plan path; it did not remove it. A plan with no record
  store, a store that will not list, or an empty store still takes the full
  universe by that commit's own rule ("any gate fails toward doing the work"),
  and the in-process `Request` this bench issues sets `CollectUnclaimed: true`
  and still pays 512 per slice. **Finding 3's conclusion — that slicing
  multiplies choudoufu's plan cost — is therefore false of a steady-state plan
  today and remains true of any run that sweeps.**
- **Finding 4 stands.** The tagging leg is still 1 call per slice: 1, 2 and 8
  at k=1, 2 and 8.
- **Part 4 stands entirely.** Zero cross-slice references under the component
  partition at both k=2 and k=8, and exactly three under the layer cut,
  reproduced here.
- **Part 5's second half was already corrected elsewhere and is confirmed
  here.** The three ECS creates and the 273-second `DescribeServices` retry
  loop were attributed to floci; `rulings/20260830-stateful-equivalence.md` showed
  they were the provider block. With the block corrected, every plan in this
  re-measure is empty and the slowest is **2.1s** against the 273s recorded
  here. Under the old block at the same commit the ECS slice still takes
  **137s**. No floci issue is warranted.

### One movement that is not this defect

Part 1's in-process table also moved slightly, and the provider block is not
why: `launchAWSProvider` configures its provider from a literal three-flag body
that never carried `skip_requesting_account_id`, so the leg measurements were
always taken against a correct provider. At scale 1 the legs now read
`tagging 1 / native 512 / config scan 26 / boundary 9 / post-sweep 0`, so
sweep = 548 against 558 and total = 696 against 706, moving the read share from
21.0% to 21.3%. Scales 4 and 10 were not re-run and their rows are unverified
at the current commit.

### Not re-measured

Part 3's guided-discovery table (744, 328, 709, 269, 293) is four CLI-plan
columns taken with the broken block and before `09d180f921`. Every figure in it
is superseded and none has been replaced. Its two *mechanisms* are read off the
code rather than the counts and are unaffected. The same applies to the scale-4
whole-estate CLI row (1294) and to the `TOFU_LIVE_CLOUDCONTROL=off` column
throughout.

Reproduce with:

```
SLICE_SCALE=1 SLICE_K=8 SLICE_OUT=/tmp/k8.json TF_FLOCI_TEST=1 \
  env -u PWD go test ./internal/live/discovery/ \
    -run TestSlicingMatrixAgainstFloci -v -count=1 -timeout 120m
```

---

*The original text follows. Not one figure, table or finding in it has been
altered or removed. The additions are the blockquoted correction notes marked
#634, beside the tables they supersede, plus one parenthesis naming the change
to the instrument.*

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

   > **Superseded, #634.** Every number in this finding is a refused plan's
   > cost. Re-measured: stock 150/152/164, choudoufu **157/163/198**. Stock's
   > "148 whether it is one state or eight" was itself an artifact of the
   > missing account lookup; it is `148 + 2k`. See the correction above.

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
this work. (Since `5ff7f43f5b` it reads `tools/terralith-gen`'s `versions.tf`
rather than writing its own, and fails loudly on a non-zero plan exit. Both
changes are #634; see the correction at the top.) It differs from
`sweep_split_bench_test.go` (#581) in exactly two respects, both required by
the questions:

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

> **Wrong diagnosis, #634 and `rulings/20260830-stateful-equivalence.md`.** The
> 273 seconds are the broken provider block, not floci and not one resource
> type. Re-measured at `5ff7f43f5b`: the slowest plan in the whole matrix is
> **2.1s**, and the same slice under the old block at the same commit still
> takes **137s**. Wall clock in these runs is unusable for the stated reasons
> too, but the retry loop this section reasons about was self-inflicted.

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

> **Small movement, and not from #634's defect.** `launchAWSProvider`
> configures its in-process provider from a literal three-flag body that never
> carried `skip_requesting_account_id`, so these legs were always measured
> against a correct provider. Re-measured at `5ff7f43f5b`, the scale-1 row
> reads `1 / 512 / 26 / 0` with a further 9 boundary calls, so sweep 548,
> total 696, read share **21.3%**. Scales 4 and 10 were not re-run.

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
(unmigrated) to **1.87/1.85/1.84** (migrated). `rulings/20260830-marker-verified-fast-projection.md`'s own extrapolation —
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

> **Superseded, #634.** Every `choudoufu plan` column below exited 1. The
> replacement matrix, and the k-by-k decomposition of what the refusal cost,
> are in the correction at the top of this document.

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

> **Superseded, #634. This is the table most often quoted from this document
> and none of it holds.** Re-measured at `5ff7f43f5b`: 1.05x whole, 1.07x at
> k=2, **1.21x at k=8**, 1.13x–1.39x per slice. The scale-4 row was not
> re-measured and is still refused-plan-derived. The "flat ~541 calls per
> additional state" model below reads about 6 calls per additional state on
> today's CLI path.

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

> **Re-measured, #634.** These are in-process legs and were not affected by
> the provider block. At `5ff7f43f5b` the native leg reads **512** rather than
> 521 in every one of the eleven slice-configurations re-run here, the tagging
> leg is still 1 per slice, and the read pass is still 148 summed at every k.
> The claim this table exists to make is unchanged.

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
what #579 says the mode would cost.

| configuration | full plan | projected `live-verify` |
|---|---|---|
| whole (k=1) | 744 | 1 |
| k=2, summed | 1288 | 2 |
| k=8, summed | 4530 | 8 |
| scale 4, whole | 1148 | 2 |
| scale 10, whole | 2032 | 4 |

> **Partly superseded, #634.** The `live-verify` column is arithmetic on the
> tagging leg and is unchanged: 1 call per slice, reproduced at 1, 2 and 8.
> The "full plan" column's first three rows are refused-plan costs and read
> 157, 163 and 198. The gap a `live-verify` mode would close over a
> steady-state plan is therefore two orders of magnitude smaller than this
> table implies, though the argument for the mode is unchanged for any run
> that sweeps.

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

> **Superseded and not replaced, #634.** All four columns are CLI-plan counts
> taken with the broken provider block, and all four predate `09d180f921`. No
> re-measurement of this table was taken. The two mechanisms below are read off
> the code rather than off the counts and are unaffected.

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

> **Wrong, #634.** It is neither. Both symptoms are the bench's own provider
> block, and `rulings/20260830-stateful-equivalence.md` reached this first. With
> the block corrected at `5ff7f43f5b`, every plan in the re-measured matrix
> reports "No changes" and proposes nothing, at k=1, k=2 and k=8 alike. There
> is no AWS/floci fidelity gap here to explain and no floci issue to file. The
> reasoning above is left standing as a worked example of attributing a
> fixture's defect to its environment.

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

Added by #634's re-measure, at `5ff7f43f5b`:

- **Scales 4 and 10.** Only scale 1 was re-run. Every scale-4 and scale-10
  figure in this document, in-process legs included, is unverified at the
  current commit.
- **Part 3's guided-discovery matrix**, the `TOFU_LIVE_CLOUDCONTROL=off`
  column, and the scale-4 whole-estate CLI row. Superseded, not replaced.
- **A plan that is not in steady state.** The re-measured CLI numbers are what
  `09d180f921`'s narrowing costs when an estate has a populated record store.
  A plan with no record store, one that will not list, or an empty one takes
  the full sweep universe by that commit's own rule, and this re-measure did
  not exercise those paths.
- **Whether 4530 still reproduces at `cfd0dc58d4`.** The A/B above holds the
  commit fixed and varies the provider block; it does not re-run the original
  commit. What it shows is that at today's commit the same broken block costs
  184 at k=8, so the bulk of 4530 → 198 is `09d180f921`, not this defect.
