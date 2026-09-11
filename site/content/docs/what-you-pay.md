---
title: "What you pay, and when"
weight: 4
---

# What you pay, and when

choudoufu puts a resource's identity on the resource, as two AWS tags, so that
a state file stops being the record of what you own. That swap has a price.
This page is the measured statement of it, for someone deciding whether to put
an estate on this fork.

The price is not one number, because it is not paid on every run. It depends
on which of three things the run is doing:

| The run | What it costs against stock OpenTofu | Measured on |
|---|---|---|
| A configuration with no `live` block | Nothing. The same API calls, exactly. | emulator |
| A plan of an estate already adopted, `live` block on | API calls at parity, or slightly fewer: **-3 on 1416**, then **-45 on 1449** | real AWS |
| The same plan, in seconds | **withdrawn**, see below | — |
| Adopting, auditing, or rebuilding identity from markers | The estate-wide sweep: [**about 512 calls, per state file**](#the-sweep-is-the-real-cost-of-slicing-and-it-does-not-shrink-per-slice) | emulator |

**Every figure on this page describes choudoufu {{< version >}}.** Each one
names its fixture, its commit, and whether it came from the pinned AWS
emulator or from a real AWS account. The two are not interchangeable and are
never combined.

**There is currently no wall-clock figure on this page.** The three real-AWS
sessions that produced one were comparing a cached plan against an uncached
one, and [Wall clock](#wall-clock-withdrawn-because-the-comparison-was-not-like-for-like)
sets out why they are withdrawn rather than restated.

## With no live block, nothing at all

A configuration with no `live` block and no `estate.chdf.hcl` sidecar runs as
stock OpenTofu. That is measured, not promised.

Same generated estate, same emulator, same session, every column an empty plan,
three runs each, no variance in any column:

| Binary | 79 instances | 301 instances |
|---|---|---|
| `terraform plan` (Terraform 1.15.8) | 150, 150, 150 | 558, 558, 558 |
| `tofu plan` (OpenTofu 1.12.5) | 150, 150, 150 | 558, 558, 558 |
| `choudoufu plan` | 150, 150, 150 | 558, 558, 558 |

The 79-instance column was re-run at `b20a144ab0` for this page; the
301-instance column is the ruling's and has not been re-run since.
Both oracles are behind the current pin, which `live/oracle-versions.json` puts
at terraform `1.16.0` and tofu `1.12.6`. Nothing in this table has been re-run
against those.

OpenTofu is in that table because choudoufu is an OpenTofu fork and Terraform
is not OpenTofu. Without the middle row, any difference between the top and
bottom rows could not be attributed to this fork rather than to the
Terraform/OpenTofu split.

The code half holds too. Every fork addition to plan and apply sits behind a
guard that a missing live block turns off. Seven things do run
unconditionally; they are enumerated in the ruling, and the one among them that
can change a verdict changes it in the accepting direction, so a configuration
stock refuses can succeed here and never the reverse. Method, per-guard
reading and raw values:
[the stateful-equivalence measurement](https://github.com/INTENTIUS/choudoufu/issues/588) (#588).

## Planning an adopted estate

Turn the live block on, migrate the estate, and plan it the way an operator
plans on an ordinary Tuesday. Same fixture, same emulator, three runs each,
every plan exit 0 with `No changes`:

| Column | API calls |
|---|---|
| stock `terraform plan`, state file | 150, 150, 150 |
| stock `tofu plan`, state file | 150, 150, 150 |
| `choudoufu plan`, live block, migrated | **157, 157, 157** |
| `choudoufu live-plan`, the same estate | 157, 157, 157 |

157 against 150 is **+4.7%**, and the residual is seven calls rather than a
percentage, because the two sides can be diffed action by action. Of stock's
150 calls, **148 across 18 AWS actions are matched exactly** - same actions,
same counts:

```
ListAttachedRolePolicies                32    DescribeVpcAttribute        3
GetRole                                 21    DescribeSecurityGroups      2
GetRolePolicy                           20    DescribeVpcs                1
GET /{bucket}/...                       12    DescribeSubnets             1
ListRolePolicies                        11    DescribeRouteTables         1
GetPolicy                               10    DescribeNetworkAcls         1
GetPolicyVersion                        10    ECS DescribeClusters        1
GetInstanceProfile                      10    ECS DescribeServices        1
GET ?maxitems&name&type  (Route 53)     10    ECS DescribeTaskDefinition  1
```

That is the refresh, and it is the AWS provider's own `Read` implementations.
Stock invokes them on the same resources when it refreshes; nothing in this
fork adds to them or can subtract from them. Stock's other two calls are
`GetCallerIdentity` and `GetUser`, resolving the account.

choudoufu's other nine are those same two, doubled, plus seven that are only
on this side:

| Call | n | Why |
|---|---|---|
| `GetResources` | 1 | The tag index. This is the state file's substitute, and it is `ceil(tagged/100)`, so 1 at this scale. |
| `ListRoles` | 1 | Native sweep, `aws_iam_role`: bindable only from a native list's own resource object |
| `ListServices` | 1 | Native sweep, `aws_ecs_service`: no ARN-join row, so the tag sweep cannot place it |
| `ListTaskDefinitions` | 1 | ECS identity resolution |
| `DescribeTaskDefinition` | 1 | A second one, for ECS identity resolution |
| `GetCallerIdentity` | 1 | A second one: the provider is configured twice, once for discovery and once for the plan graph |
| `GetUser` | 1 | Same |

The first three are the estate-scoped sweep, and they do not shrink further
without giving up removal coverage for those types. The last four are this
fork's own structure and have nothing to do with the sweep.

The 157 has now been produced three separate times on this fixture and pin: by
[#627](https://github.com/INTENTIUS/choudoufu/pull/627), which landed the
narrowing that produced it; by
[the slicing measurement](https://github.com/INTENTIUS/choudoufu/issues/584)
(#584, corrected by #634)'s re-measure at `5ff7f43f5b`, which reproduced its
seven-call residual call for call; and by the run reported here, at
`b20a144ab0`. Reproduce it with
`TF_FLOCI_TEST=1 go test ./internal/live/statefulcost/`, which is also where
the no-live-block table above comes from.

Until `09d180f921` a plan enumerated the whole admission table on every run,
about 512 native-leg list calls whatever the estate contained, and this same
fixture read 710 where it now reads 157. The 301-instance emulator figure was
taken before that change and has not been re-run, so it describes a plan that
no longer exists.

### The same comparison on real AWS, at 79 and 745 resources

Real AWS in account `...3429` and region `us-east-2`. Both sides no-change
plans on every run:

| Resources | stock | choudoufu, steady state | Difference | Commit |
|---|---|---|---|---|
| 79 | 149 | 155 | +6 (+4.0%) | `d359210978` |
| 745, session 1 | 1416 | **1413** | **-3 (-0.2%)** | `d359210978` |
| 745, session 2 | 1449 | **1404** | **-45 (-3.1%)** | `02885d2fd6` |

79 and 745-session-1 were re-measured together at `d359210978`; session 2 is
a separate run at `02885d2fd6`, added to widen the 745 sample rather than to
replace session 1. **At 745 resources choudoufu makes fewer provider
requests than stock**, in both sessions, which supersedes the +7 this table
carried before. Per-operation the two are near-identical:
`ListAttachedRolePolicies` 325 against 324, `GetRole` 215 against 211. The one
real difference is `DescribeTaskDefinition`, 21 against 10.

Note before anything else that the 79-resource row disagrees with the emulator
row above it: +16 against +7 on the same fixture at the same scale. Do not
average them or pick the flattering one. They come from **different
instruments** - the emulator figure counts every HTTP request through a proxy,
this one counts only what the AWS provider itself logs - from different
accounts, and from a comparison where the two sides plan different directories.
Both are reported; neither is a correction of the other.

The residual shrinks between the two scales because the two sides move in
opposite directions, not because one simply adds to the other: at 745
resources choudoufu makes fewer IAM calls than stock and more ECS calls
(`DescribeTaskDefinition` 31 against 10, plus extra `ListTaskDefinitions`,
`ListServices` and `ListRoles` calls); Route 53 is identical on both sides at
both scales. Why the IAM side comes out lower is not explained by this run,
and this page will not guess.

Two conditions travel with that table and change how it should be read.

**It counts provider-mediated requests only.** The figures come from
`terraform-provider-aws`'s own request log; choudoufu's Cloud Control and
Tagging clients log no line per request, so their calls are *not* in the
1413 - only a type count is known (0 types via Cloud Control, 31 via one
`GetResources` sweep call plus pagination). The emulator tables above count
every request through a proxy instead, so the two instruments have different
denominators and should not be subtracted from each other.

**The two sides are not planning the same directory.** Stock plans its own
converged state after the cold deploy and before anything migrates it;
choudoufu plans the migrated estate. That is the honest like-for-like
available on real AWS, and it is a weaker control than the emulator's, where
both sides plan the same estate through the same proxy.

If you want a number for your estate, measure your estate. The composition
above is a property of what this fixture declares rather than of this fork.

## The record store, which no call count used to see

Every managed instance also has a record: the arguments the provider never
echoes back, sensitivity marks, taint, the deposed key. It lives in a local
directory, an SSM parameter or an S3 object, and it therefore never crosses
the AWS endpoint the counting proxy stands in front of. So until
[#636](https://github.com/INTENTIUS/choudoufu/pull/636), every "a plan costs N
calls" figure in this repository was a figure about one of the two things a
plan does, presented as if it were both.

Counted, at 79 instances with 78 records: **377 round trips**, over 80 distinct
keys, 297 of them re-reading a key an earlier trip in the same plan had already
read. Each accessor on the record store decoded the same physical key and each
went to the store for itself. Stock OpenTofu pays one read for the same
information.

It now pays one too. A bulk read plus a run cache that switches itself off
permanently the first time anything writes through it takes the same plan to
**1 trip, one `GetAll`**, with the three plan outputs byte-identical to the
377-trip baseline's, run for run, reproduced here at `b20a144ab0`:

```
run 1: aws-calls 157, record-trips 1
run 2: aws-calls 157, record-trips 1
run 3: aws-calls 157, record-trips 1
```

**Stale**: this is `b20a144ab0`'s number, not {{< version >}}'s. The record
path it measured has moved since - the local cache going default-on and the
cache-vouch listing pass (`8b1760f582`, `e15b23eb7b`, `2008d71be2`), plus the
record-orphan leg reading the live tag first (`d15ce4456c`) - and none of
that has been re-run through this same one-trip-vs-377-trip harness
(`TF_FLOCI_TEST=1 go test ./internal/live/statefulcost/`). The mechanism this
page describes (decode once, serve every accessor from the same read) has
not been reverted, but the exact "1" has not been re-measured against
today's record path and is not claimed current.

On SSM an estate of N records costs `ceil(N/10)` API calls rather than N,
because the bulk path keeps the values the paged call already returned. S3
cannot bulk-fetch bodies at all, so its floor stays `1 + N` and only the seam
moved. See [Storage]({{< relref "/docs/use/storage" >}}) for which store to
pick.

## Migration writes no markers by hand

The cost that would decide an adoption is human, not API: how much of an
existing estate needs a `tofu-address` typed by a person before the first
plan.

Measured on real AWS rather than an emulator, at 745 resources in `us-east-2`,
recorded in
[`live/gauntlet.json`](https://github.com/INTENTIUS/choudoufu/blob/main/live/gauntlet.json)'s
`live_cert` block at `1d06e1d177`:

| Stage | Result |
|---|---|
| `cold_deploy` | 745 resources applied by stock `terraform` in 413 s, holding its own state file |
| `migrate` | `choudoufu live-import -approve`: 335 of 745 verified, **335 stamped**, 410 skipped, in 222 s |
| `test_plan` | Post-migration plan is empty |
| `test_apply` | No-op apply: 0 added, 0 changed, 0 destroyed |

`migrate` is the write-side cost and the one stage that is genuinely serial:
335 unbatched tag writes, one per resource, plus 190 throttle responses all
absorbed by the SDK's own backoff. It is paid once.

**Nobody wrote a marker by hand.** The 410 skipped are untaggable by the
provider's own schema and need no marker at all: their identity composes from
an already-stamped parent, which is a role name plus an inline-policy name, or
a zone ID plus a record name and type.

This is a stronger result than an earlier emulator run of the same ratio,
whose generator emitted no `count` or `for_each` anywhere
([#566](https://github.com/INTENTIUS/choudoufu/issues/566)) - a real gap,
since content matching never offers a `count`/`for_each` instance for
adoption. [#574](https://github.com/INTENTIUS/choudoufu/issues/574) added
root-level `count`, `for_each` over a map, and a `count`-carrying module
call; the run above is that estate, and `live-import` reads each instance's
identity from the state file directly, so it never reaches that wall at all.

What that leaves is the day the state file is gone or wrong, which is the
adoption path proper and the one place the expensive sweep is worth its price.
[Migrate an existing estate]({{< relref "/docs/use/migrate" >}}) covers both
routes.

### The old state file stops being a safe fallback

This is the part of migration that costs something, and it is worth knowing
before you migrate rather than after.

`live-import` stamps markers onto live objects. The stock state file it read
was taken before that stamp, so it does not know the tags exist. Run stock
`terraform plan` in that same untouched directory afterwards and it refreshes,
finds two tags the configuration does not declare, and proposes removing them.

Measured here at `b20a144ab0`, on the same 79-instance estate, with a control
run immediately before the stamp:

```
ROUND TRIP before live-import:            terraform plan is empty
ROUND TRIP after live-import -approve:    Plan: 0 to add, 38 to change, 0 to destroy
```

All 38 changes are the same shape, and this is the whole of one of them:

```
~ tags = {
    - "tofu-address" = "aws_ecs_cluster.main" -> null
    - "tofu-estate"  = "sfcost-scd" -> null
  }
```

Applying that un-migrates the estate, and it reads as routine tag drift while
it does so. 38 of 79 instances at this scale, 137 of 301 at the next one.

**choudoufu refuses this run; stock cannot.** Since
[#613](https://github.com/INTENTIUS/choudoufu/issues/613), a state-backed plan
that would strip a migrated estate's markers is computed and rendered in
full - so the operator sees the exact drift stock would show - and then
refused rather than applied. The refusal covers `apply` and `plan -out` +
`apply`, since the damaging form is an unattended `apply -auto-approve` where
a warning would go unread. `CHOUDOUFU_UNMIGRATE=<estate>` downgrades it to a
warning when backing out is intentional; it takes an estate name so a setting
left in CI can't cover an estate migrated a year later.

None of that helps a directory running stock. Delete or archive the old state
file as part of the migration, and treat any surviving stock directory
pointing at the same estate as the hazard it now is.

## Day two at scale

Everything above answers whether a plan costs less. A different question is
whether the day-two mechanisms still run at all once an estate is thousands
of resources rather than dozens: drift detection, rename, remove, a count
pool edit, replace, the approval gate. This is not a cost claim and makes no
seconds-based comparison between tools, so it sits outside the page's own
rule against emulator wall clock ([below](#and-an-emulator-cannot-answer-this-question)).
What it records is a pass/fail result at scale, plus which stage a large
run's seconds actually went to.

`live/e2e/terralith-scale/run.sh` ran the eleven active stages -
`cold_deploy`, `migrate`, `test_plan`, `test_apply`, `greenfield`,
`drift_reconverge`, `plan_approval`, `day2_rename`, `day2_remove`,
`day2_count`, `day2_replace` - at scale 10 (745 resources) and scale 50
(3,705 resources), recorded by hand in
[`live/e2e/terralith-scale/scale-history.json`](https://github.com/INTENTIUS/choudoufu/blob/main/live/e2e/terralith-scale/scale-history.json)
at commit `283d2833b3`, emulator pin `sha256:d9207de1`, 2026-09-09 and
2026-09-10. All eleven passed at both scales.

| Scale | Resources | Total | `cold_deploy` | `greenfield` | Every other stage, summed |
|---|---|---|---|---|---|
| 1 | 79 | 329 s | 122 s | 60 s | 147 s |
| 10 | 745 | 1,332 s | 647 s | 381 s | 304 s |
| 50 | 3,705 | 5,656 s | 3,001 s | 1,746 s | 909 s |

`cold_deploy` and `greenfield` together are 4,747 of the 5,656 seconds at
scale 50, 84%. Of what is left, every day-two stage stays under two
minutes: `day2_rename` 70 s, `day2_remove` 54 s, `day2_count` 99 s,
`day2_replace` 60 s, `drift_reconverge` 70 s, `plan_approval` 101 s.
`migrate`, the one-time stamp pass rather than a day-two stage, takes 410 s
of the remainder - the section above covers what that pass does and why it
is serial.

A negative control ran at scale 50 too: `BREAK_APPROVAL=1` expects the
post-approval apply to wrongly succeed, and choudoufu still refused it, so
only that wrong expectation failed while `cold_deploy` through
`plan_approval` passed exactly as the row above. The refusal
[claim 15]({{< relref "/docs/claims#claim-15-apply-exactly-what-was-approved" >}})
covers still holds at 3,705 resources.

## Splitting an estate into several states

Slicing a terralith into several smaller states, rather than planning it as
one, costs almost nothing extra under choudoufu. Measured at `5ff7f43f5b`,
same pin, scale 1, every plan exiting 0 with `No changes`:

| Configuration | stock | choudoufu | Ratio |
|---|---|---|---|
| Whole estate (k=1) | 150 | 157 | **1.05x** |
| Two states, summed | 152 | 163 | **1.07x** |
| Eight states, summed | 164 | 198 | **1.21x** |

**Stock is not flat under slicing either.** Stock is `148 + 2k`, two calls per
slice to resolve the account; choudoufu pays about six calls per slice,
roughly four more per state than stock. That difference is where the 1.21x
at eight states comes from.

> A 30.6x figure for this same comparison circulated earlier and should not be
> quoted. It came from a benchmark whose own provider block broke ECS identity
> resolution, so every `choudoufu plan` in that run actually refused, and its
> exit-1 call count was written up as a clean plan's cost.

### The sweep is the real cost of slicing, and it does not shrink per slice

Day-2 planning is cheap to slice; adopting or recovering an estate from
markers alone is not. The estate-wide native sweep does not scale down with a
slice's type count: sliced at a fixed 79-instance estate it is **512 calls
per slice regardless of slice count**, 4096 summed across eight states,
`native_sweep_calls` re-measured at `5ff7f43f5b`. A slice declaring five
types still pays what the whole estate pays, because the sweep builds its
universe by *subtracting* the types a configuration declares from the
admission table, not by listing only what that slice has.

That per-slice figure has not been re-run at a larger estate. The adjacent
axis - one slice, larger estate - stopped being flat for a while: 521, 548
and 612 calls at 79, 301 and 745 instances, re-measured at `56099dcd63` for
[#1032](https://github.com/INTENTIUS/choudoufu/issues/1032), up from the
512-ish figure this page carried until then.
[#1037](https://github.com/INTENTIUS/choudoufu/issues/1037) and
[#1039](https://github.com/INTENTIUS/choudoufu/issues/1039) isolated why -
`aws_iam_policy` and `aws_iam_role` were listed a second time, in full, by
the native sweep even when the config-driven scan had already listed the
whole account for either a moment earlier (both are in the one service the
Resource Groups Tagging API never indexes), and `aws_ecs_service` had no
`arnJoinTable` row for its own ARN shape and so took the whole-account
native leg instead of the Tagging API's one estate-filtered call the way
every other joinable type does - and fixed both, at `c0632fa3b7`
(2026-09-11), floci pin `sha256:9ec3fa64...`: 508, 510 and 510 on the same
three rows, flat again. [What a plan
costs]({{< relref "/docs/model/plan-cost#the-native-leg-is-flat-across-slices-but-not-across-scale" >}})
carries the fixed numbers and what the old crossover fit's retirement means
for this page's own claims. Only the k=1 (unsliced) row of the scale axis
was re-measured for this fix; the per-slice figure two paragraphs up (k=2,
k=8) has not been re-run since either fix landed.

`09d180f921` took that leg off the steady-state plan path once an estate has
a record store to narrow by, and left it everywhere else: a plan with no
record store, a store that will not list, or an empty store still takes the
full universe.

Slicing costs about 1.2x stock to plan day to day, and one full sweep per
slice to adopt or recover - the number to weigh before splitting an estate you
expect to adopt piecemeal.

## What this page does not claim

**There is no crossover.** The hypothesis this work was filed under was that
choudoufu's plan is a high fixed cost plus a low marginal cost, so that past
some estate size the two curves cross and choudoufu wins outright; the
first half of that is right, the second is not.

The two-point fit this section used to carry - `stock = 1.84N + 5` against
`choudoufu, migrated = 1.99N + 553`, built from 710 calls at N=79 and 1152
at N=301 - is dropped rather than carried forward to the point that now
exists. Both of its own two numbers have since moved to 696 and 1262 ([what
a plan
costs]({{< relref "/docs/model/plan-cost#the-measured-split-on-a-migrated-estate" >}})'s
re-measured split table), a third point exists that the fit was never built
to predict (2332 at N=745), and at the time that re-measurement was taken
the leg behind all three was not flat with itself either
([#1037](https://github.com/INTENTIUS/choudoufu/issues/1037), fixed since at
`c0632fa3b7` - the native leg on its own is flat again, but the totals
696/1262/2332 have not been re-measured against the fix, since they also
carry the configuration scan, boundary and post-sweep terms this unit did
not touch).
A line drawn through two points that have since moved is not a fit worth
extending to a third; the current re-fit, with its own honest residual,
lives on [what a plan
costs]({{< relref "/docs/model/plan-cost#the-measured-split-on-a-migrated-estate" >}})
rather than repeated here.

What still holds is visible directly in the current rows, without needing a
line drawn through them. Stock's full-sweep total is 150, 558 and 1374
calls at 79, 301 and 745 instances; choudoufu's migrated full-sweep total is
696, 1262 and 2332 over the same three points. The difference between the
two grows across the three points - 546, 704 and 958 calls - while the
ratio between the two falls toward one without reaching it - 4.64x, 2.26x
and 1.70x across that same span. A falling ratio beside a growing
difference is the shape the old fit described too, and there is still
nothing to cross. Since `09d180f921`,
none of these three rows describes a steady-state plan - each describes a
run that sweeps the whole admission table, which today means an adoption or
a recovery.

Nothing in the seconds crosses either. As measured at `5dc10cc781` the
wall-clock ratio narrowed the same way the call ratio does, roughly 5x at 79
resources to roughly 4x at 745, and both sides climbed. A narrowing ratio is
not an approaching crossover, and this page will not draw one until a
measurement brackets it on both sides. Both of those ratios are superseded by
the fix described below and have not been re-measured on real AWS.

**No claim is made about another provider.** Every figure here is AWS.

**No claim is made from one resource type's slope.**
[`live/plan-budget.json`](https://github.com/INTENTIUS/choudoufu/blob/main/live/plan-budget.json)
ratchets an `aws_s3_bucket` estate at 22 calls per instance; the terralith
above reads 1.84. Same code, twelve times apart, because `aws_s3_bucket` has an
unusually chatty `Read`. Extrapolating from somebody else's resource type will
be wrong by whatever the ratio between the two providers' `Read`
implementations happens to be.

## Wall clock: withdrawn, because the comparison was not like for like

Withdrawn at `2989f9b073`. Three real-AWS sessions measured wall-clock ratios
of 2.95x, 3.53x and 2.05x on the median at 745 resources. **None of them is
stated here as a ratio, because the comparison underneath was invalid.**

The harness ran stock in one directory and choudoufu in another. Stock's
directory holds `terraform.tfstate`. choudoufu's holds `.tofu-records` and **no
state file at all**: `live-import` reads stock's state file to perform the
migration and never writes one into the estate it migrates to.

So the measurement was not stock against this fork. It was **a cached plan
against an uncached one.** choudoufu read all 745 objects live because nothing
had told it any of them were already known, which is what a plan does when its
cache is missing rather than stale.

This is a defect in the test harness, not a discovery about the fork, and the
figures cannot be salvaged by reinterpreting them. They are withdrawn rather
than restated.

### What this fork actually keeps, and what the test was missing

choudoufu is stock OpenTofu plus identity hooks. It keeps **the ordinary state
file, as a cache**, and adds three pieces that live in the cloud: identity as
two tags on the resource, values in a record store, and effects as receipts.
The state file is demoted from being the record of what you own to being a copy
you are allowed to lose or to find stale. Demoted is not deleted, and a cache
you never write is not a cache.

Two things were therefore missing from the comparison. choudoufu's estate had
no state file to cache into, and its record store was configured as
`record_store "local"`, a directory on disk, so the values piece was never
exercised against the cloud either. Of the three pieces, only identity - the
tags - was genuinely under test.

### What survives

The **API call counts** survive: they were counted per request on both sides
and do not depend on the cache being present. choudoufu issues fewer provider
requests than stock at 745 resources in all three sessions.

**The head-of-line defect survives**
([#683](https://github.com/INTENTIUS/choudoufu/issues/683)). A block profile
puts 46.14 s of a 51 s plan in one channel receive, and one throttled read
stalls the whole read pass regardless of why the reads are being made. That is
a real defect in the read pass's slot accounting. What changes is its
importance: it was stalling a pass that should not have been reading 745
objects in the first place.

A corrected comparison, with choudoufu given the state file it is designed to
keep and a record store in the cloud, has not been run yet. Nothing replaces
these figures until it has.


### And an emulator cannot answer this question

A wall clock measured against the pinned emulator grades the machine the test
ran on rather than this repository's code, which is why
`live/plan-budget.json` records one and never gates on it. Every second in
this comparison, and everywhere else on this page that measures one tool
against another, is real AWS for that reason.
[`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md)
sets out the rule and the three other questions an emulator-backed
measurement cannot answer.

[Day two at scale](#day-two-at-scale) above is the one exception, named as
one rather than left as a quiet violation of the rule: it makes no
cross-tool comparison and draws no ratio a slower laptop could move. It
answers a pass/fail question (did the eleven stages still run at 3,705
resources) and a share-of-total question (which stage the seconds went to),
neither of which needs the machine to be graded against anything.

### What evaluating this costs in money

Effectively **$0.00**, against a $15 ceiling, for the whole 745-resource run.
Every write the fixture makes is free - IAM, VPC, subnet, security group,
Route 53 record changes, ECS cluster, service and task definitions - and
`desired_count = 0`, so no Fargate task ever ran. Both hosted zones were
deleted well inside the twelve hours below which AWS does not charge for one.

Teardown was confirmed by listing rather than inferred from an exit code, and
then verified again independently through the AWS CLI: the account is back to
its baseline, and the 21 ARNs still answering the run's own tag were described
one at a time to confirm each was `INACTIVE` at zero running and zero desired.

## Where the mechanism is

This page is the decision. [What a plan costs]({{< relref "/docs/model/plan-cost" >}})
is the mechanism: the two terms a plan is made of and how each one grows, the
per-leg split at three scales, and the two concurrency bounds you can turn
down when a real account starts answering `Rate exceeded`.
