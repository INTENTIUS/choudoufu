---
title: "What you pay, and when"
weight: 3
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
| The same plan, in seconds | **1.55x** at 79 resources, **about 3x** at 745 | real AWS |
| Adopting, auditing, or rebuilding identity from markers | The estate-wide sweep: **about 512 calls, per state file** | emulator |

**Every figure on this page describes choudoufu {{< version >}}.** Each one
names its fixture, its commit, and whether it came from the pinned AWS
emulator or from a real AWS account. The two are not interchangeable and are
never combined.

The seconds are the least settled figure here. Three independent real-AWS
sessions at 745 resources land between 2.05x and 3.53x on the median, and
[Wall clock](#wall-clock-155x-at-79-resources-about-3x-at-745-and-now-a-mechanism)
gives all three alongside what is now known about where the time goes.

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
301-instance column is the ruling's, unchanged, and has not been re-run since.
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
[`rulings/20260830-stateful-equivalence.md`](https://github.com/INTENTIUS/choudoufu/blob/main/rulings/20260830-stateful-equivalence.md).

## Planning an adopted estate

Turn the live block on, migrate the estate, and plan it the way an operator
plans on an ordinary Tuesday. Same fixture, same emulator, three runs each,
every plan exit 0 with `No changes`:

| Column | API calls |
|---|---|
| stock `terraform plan`, state file | 150, 150, 150 |
| stock `tofu plan`, state file | 150, 150, 150 |
| `choudoufu plan`, live block, migrated, **no state file at all** | **157, 157, 157** |
| `choudoufu live-plan`, the same estate | 157, 157, 157 |

157 against 150 is **+4.7%**, and the residual is seven calls rather than a
percentage, because the two sides can be diffed action by action. Of stock's
150 calls, **148 across 18 AWS actions are matched exactly** — same actions,
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
[`rulings/20260830-slicing-under-choudoufu.md`](https://github.com/INTENTIUS/choudoufu/blob/main/rulings/20260830-slicing-under-choudoufu.md)'s
re-measure at `5ff7f43f5b`, which reproduced its seven-call residual call for
call; and by the run reported here, at `b20a144ab0`. Reproduce it with
`TF_FLOCI_TEST=1 go test ./internal/live/statefulcost/`, which is also where
the no-live-block table above comes from.

Until `09d180f921` a plan enumerated the whole admission table on every run,
about 512 native-leg list calls whatever the estate contained, and this same
fixture read 710 where it now reads 157. The 301-instance emulator figure was
taken before that change and has not been re-run, so it describes a plan that
no longer exists.

### The same comparison on real AWS, at 79 and 745 resources

Real AWS, account `...3429`, `us-east-2`, on a `main` containing all of the
concurrency and narrowing work. Both sides no-change plans on every run:

| Resources | stock | choudoufu, steady state | Difference |
|---|---|---|---|
| 79 | 149 | 155 | +6 (+4.0%) |
| 745, session 1 | 1416 | **1413** | **-3 (-0.2%)** |
| 745, session 2 | 1449 | **1404** | **-45 (-3.1%)** |

Re-measured at `d359210978`. **At 745 resources choudoufu makes fewer provider
requests than stock**, in both sessions, which supersedes the +7 this table
carried before. Per-operation the two are near-identical:
`ListAttachedRolePolicies` 325 against 324, `GetRole` 215 against 211. The one
real difference is `DescribeTaskDefinition`, 21 against 10.

Note before anything else that the 79-resource row disagrees with the emulator
row above it: +16 against +7 on the same fixture at the same scale. Do not
average them or pick the flattering one. They come from **different
instruments** — the emulator figure counts every HTTP request through a proxy,
this one counts only what the AWS provider itself logs — from different
accounts, and from a comparison where the two sides plan different directories.
Both are reported; neither is a correction of the other.

The residual *shrinks in absolute terms* between the two scales, which is not
what a fixed overhead does, and the reason is that the two sides are not
merely one adding to the other. At 745 resources choudoufu makes **fewer** IAM
calls than stock — `ListAttachedRolePolicies` 320 against 324, `GetRole` 210
against 215, `GetRolePolicy` 200 against 204, `ListRolePolicies` 110 against
114 — and more ECS calls, `DescribeTaskDefinition` 31 against 10 plus a
`ListTaskDefinitions`, a `ListServices`, two `ListRoles` and one extra
`GetCallerIdentity`. Route 53 is identical on both sides at both scales:
`GetHostedZone` 101, `ListResourceRecordSets` 100, `ListTagsForResource` 1.
Why the IAM side comes out lower is not explained by that run, and this page
is not going to invent a reason for it.

Two conditions travel with that table and change how it should be read.

**It counts provider-mediated requests only.** The figures come from
`terraform-provider-aws`'s own `HTTP Request Sent` log entries. choudoufu's
Cloud Control and Tagging clients log no line per request, so their HTTP calls
are *not* in the 1413. What is known about them is a type count rather than a
call count: 0 types went via Cloud Control, and 31 went through the
estate-filtered tagging sweep, which is one `GetResources` for all 31 plus
pagination. Small, and unmeasured, and the run says so itself. The emulator
tables higher up the page count every request through a proxy, so the two
instruments have different denominators and their numbers should not be
subtracted from one another.

**The two sides are not planning the same directory.** Stock plans its own
converged state after the cold deploy and before anything migrates it;
choudoufu plans the migrated estate. That is the honest like-for-like
available on real AWS, and it is a weaker control than the emulator's, where
both sides plan the same estate through the same proxy.

If you want a number for your estate, measure your estate. The composition
above is a property of what this fixture declares, not of this fork.

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
377-trip baseline's, run for run. Reproduced here at `b20a144ab0`:

```
run 1: aws-calls 157, record-trips 1
run 2: aws-calls 157, record-trips 1
run 3: aws-calls 157, record-trips 1
```

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
335 tag writes, one per resource, unbatched, and 190 throttle responses all
absorbed by the SDK's own backoff. It is paid once.

**Nobody wrote a marker by hand.** The 410 skipped are untaggable by the
provider's own schema and need no marker at all: their identity composes from
an already-stamped parent, which is a role name plus an inline-policy name, or
a zone ID plus a record name and type.

This matters more than the same ratio measured earlier on the emulator,
because that earlier run could not test its own predicted bottleneck. The
documented hard limit is that a `count`/`for_each` instance is never offered
for adoption by content matching, and the generator that
[#566](https://github.com/INTENTIUS/choudoufu/issues/566) measured emitted no
`count` and no `for_each` anywhere. It reported that scoping limit itself.
[#574](https://github.com/INTENTIUS/choudoufu/issues/574) added root-level
`count`, root-level `for_each` over a map, and a module call whose body also
carries `count`, and the run above is that estate. `live-import` reads each
instance's identity straight out of the state file, so it never reaches the
content-matching wall that limit describes.

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
[#613](https://github.com/INTENTIUS/choudoufu/issues/613) a state-backed plan
under this fork that would strip a migrated estate's markers is computed and
rendered in full, so the operator sees exactly the drift stock would have
shown, and then refused rather than applied. The refusal covers `apply`
and `plan -out` followed by `apply` on the saved file, because the damaging
form is an unattended `apply -auto-approve` where a warning would be correct
and unread. `CHOUDOUFU_UNMIGRATE=<estate>` downgrades it to a warning when
backing out is what you actually want, and it takes an estate name rather than
an on/off value so that a setting left behind in CI cannot cover an estate
migrated a year later.

None of that helps a directory running stock. Delete or archive the old state
file as part of the migration, and treat any surviving stock directory
pointing at the same estate as the hazard it now is.

## Slicing: the 30.6x figure is withdrawn

The question this page was written to answer was whether it is still worth
slicing a terralith into separate states under choudoufu, and the published
answer was that slicing was catastrophic here: stock cost 148 calls whether it
was one state or eight, and choudoufu cost 4530 at eight, a 30.6x ratio.

**Do not quote that number.** It was measured on a configuration where
`choudoufu plan` refused. The bench wrote its own provider block setting
`skip_requesting_account_id`, which breaks ECS identity resolution, and every
`choudoufu plan` in that matrix exited 1 while its call count was written up as
a clean plan's cost.

Re-measured at `5ff7f43f5b` against the same pin, scale 1, every plan exiting 0
with `No changes`:

| Configuration | stock | choudoufu | Ratio |
|---|---|---|---|
| Whole estate (k=1) | 150 | 157 | **1.05x** |
| Two states, summed | 152 | 163 | **1.07x** |
| Eight states, summed | 164 | 198 | **1.21x** |

Two things in the old answer were wrong, and the second is the one worth
carrying away.

**Stock is not cost-neutral under slicing.** Its "148 whether one state or
eight" was itself an artifact of the same provider block. Stock is `148 + 2k`:
it pays two calls per slice to resolve the account. choudoufu pays about six
per slice, so an extra state costs roughly four calls more here than it costs
stock.

**The broken configuration was not uniformly more expensive, and past k≈5 it
was cheaper.** An A/B at one commit and pin, varying only the provider block,
puts the overhead at +14, +10 and −14 calls at k=1, 2 and 8. Two constants of
opposite sign make it: the refusal costs +18 in whichever single slice holds
the ECS layer, however large k is, while failing to resolve the account saves 4
in every slice. Net `18 − 4k`, zero near k=4.5. So the error depended on k and
changed sign, which is why the old ratio could not be scaled or salvaged.

### But the sweep did not go away, and it is still per state

The flat leg that made the old answer alarming is still there. The
estate-wide native sweep still does not scale down with a slice's type count:
**512 calls per slice at every k measured**, which is 4096 summed across eight
states. A slice declaring five types pays what the whole estate pays, and
fractionally more, because the sweep builds its universe by *subtracting* the
types a configuration declares from the admission table.

What changed is who pays it. `09d180f921` took that leg off the steady-state
plan path when the estate has its own evidence to narrow by, and left it
everywhere else. A plan with no record store, a store that will not list, or an
empty store still takes the full universe, because every gate fails toward
doing the work.

So the honest split, and it is a split rather than a verdict:

- **As a claim about day-2 planning, "don't slice under choudoufu" is dead.**
  A steady-state plan of a sliced estate costs 1.21x stock's at eight states.
- **As a claim about adoption and recovery, it stands.** Every run that
  actually sweeps pays about 512 calls per state, and eight states means eight
  sweeps.

## What this page does not claim

**There is no crossover.** The hypothesis this work was filed under was that
choudoufu's plan is a high fixed cost plus a low marginal cost, so that past
some estate size the two curves cross and choudoufu wins outright. The first
half is right and the second is not. Fitted to the two scales measured on a
full-sweep plan:

```
stock                = 1.84N + 5      (150 at N=79, 558 at N=301)
choudoufu, migrated  = 1.99N + 553    (710 at N=79, 1152 at N=301)
```

The fixed cost is real. The marginal cost is about **8% higher**, not lower,
because a migrated instance is read by the projection and also pays its share
of the sweep. The *ratio* therefore falls with N — 4.7x at 79 instances, 2.1x
at 301 — while the absolute *difference* never closes. There is nothing to
cross.

Two caveats on those two lines, in opposite directions. Two points determine a
line exactly, so that fit has no residual and is a description of two
measurements rather than a tested model. And since `09d180f921` it no longer
describes a steady-state plan at all: it describes a run that sweeps the whole
admission table, which today means an adoption or a recovery.

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

## Wall clock: 1.55x at 79 resources, about 3x at 745, and now a mechanism

This is the number that will decide whether you can live with the fork.

Real AWS, account `...3429`, `us-east-2`, at `d359210978`. Warm provider on
both sides, `TF_LOG` unset inside every timed region, every run a no-change
plan verified by reading `No changes. Your infrastructure matches the
configuration.` out of that plan's own output rather than from an exit code.
The 745 row pools two independent sessions, six runs a side:

| Resources | stock `terraform plan` | `choudoufu plan` | median | mean |
|---|---|---|---|---|
| 79 | 4, 3, 4 s | **6, 5, 6 s** | 1.50x | **1.55x** |
| 745, session 1 | 17, 20, 41 s | 59, 84, 46 s | 2.95x | 2.42x |
| 745, session 2 | 19, 41, 18 s | 67, 132, 58 s | 3.53x | 3.29x |
| 745, session 3 | 19, 45, 22 s | 34, 45, 50, 63, 37 s | 2.05x | 1.60x |
| **745, pooled** | 17-45 s | **34-132 s** | **2.90x** | **2.28x** |

**Read 745 as "about 3x" and no more precisely than that.** Three independent
sessions land at 2.95x, 3.53x and 2.05x on the median, and the spread inside a
single session reaches 141% on the stock side. An earlier version of this page
published a 2.4x to 3.0x band from session 1 alone; session 2 came in above it
and session 3 below it. The pooled median is 2.90x and the pooled mean 2.28x,
which is the honest width of what nine stock runs and eleven choudoufu runs
support.

At 79 resources both spreads sit under 50%, so a single figure is defensible,
but the timer has whole-second resolution and the plans run 3 to 6 seconds, so
one tick is a third of the value. Treat 1.55x as coarse.

### Where the seconds go

A wall-clock block profile of a real-AWS plan at 745 resources puts the cost
on one frame. The goroutine running the plan blocks for 49.72 s of a 51 s run,
and 46.14 s of that is a single channel receive:

| Blocked in | seconds |
|---|---|
| `statelessRunner.PriorState` | 48.97 |
| `projection.BuildWith` → `builder.materialize` | 46.49 |
| `builder.applyRecordFirst` → `readFor` | 46.22 |
| **`readPrefetch.take`, waiting on one read's answer** | **46.14** |
| `discovery.Discover` → `scanType`, the estate sweep | 2.05 |

Two things to take from that. **The estate-wide sweep, the mechanism this fork
exists for, costs 2.05 s of a 51 s plan.** And the read pass is running at an
effective concurrency of **3.8 against a configured bound of 10**: its 745
workers block 176.25 s in aggregate, compressed into 46.14 s of wall clock.

The read pass takes its answers strictly in order. A slot is claimed before
each read and released only when the plan consumes *that* instance's answer,
so a single slow read does not occupy one slot, it stops the window advancing.
Measured on a real account, one `aws_route53_record` in SDK backoff stalled all
745 reads for 10.7 seconds with no request, no response and no log line in
between. Requests in flight fell to zero and stayed there until it cleared.

What matters is the amplification. A retry costs stock **0.09 s** of pipeline
dead air and costs choudoufu **1.06 s**, because stock's graph walk lets
independent work proceed past a throttled read where an in-order prefetch
cannot. Over a whole plan that leaves choudoufu with **22.9 s of 42.8 s in
which nothing at all is in flight**, against stock's 12.2 s of 40.7 s.

This is [issue #683](https://github.com/INTENTIUS/choudoufu/issues/683), and it
is a defect rather than a design limit. What it is *not* known to be is the
whole of the ratio: the two plans profiled per-request ran only 1.06x apart,
so the mechanism is proven and its share is not.

### What the 3x is not

Four candidate explanations were measured and eliminated on the way to that,
and each remains a useful negative with a control behind it.

**It is not the API calls.** choudoufu issues *fewer* provider requests than
stock at this size, in every session: 1413 against 1416, 1404 against 1449,
then 1409 against 1460. The per-operation breakdown is in the table higher up
this page.

**It is not request serialisation, and peak concurrency is why this took so
long to find.** Both binaries reach ten requests in flight at 745 resources,
on the emulator and on real AWS. Ten reads really are outstanding; they just
cannot retire, so the peak statistic reads clean while the mean sits at 2.97
against stock's 3.35. Anyone checking this needs to measure occupancy over
time, not the peak.

**It is not compute.** At 745 resources CPU is 7.99 s for choudoufu against
6.11 s for stock, over twenty runs a side. That 1.88 s is 4.5% of the gap, and
most of the absolute is stock's, so compute at this size is a property of the
estate rather than of the fork.

**It is not throttling, and this one runs backwards.** Both plans were
instrumented in the same session, minutes apart. Stock was throttled **76**
times against choudoufu's **15**, concentrating on one hosted zone's
account-wide limit, with 56 of those 76 in Route 53. Measured backoff covered
84.5% of stock's log span against choudoufu's 57.4%. So the faster binary is
the one carrying five times the throttling headwind.

That last one was nearly a wrong answer. Comparing throttle *counts* says
throttling cannot be the cause, and comparing what a throttle *costs* says the
opposite: stock absorbs five times as many and pays a tenth as much for each.
The defect is not that choudoufu is throttled. It is that choudoufu cannot
absorb being throttled.

### And an emulator cannot answer this question

A wall clock measured against the pinned emulator grades the machine the test
ran on rather than this repository's code, which is why
`live/plan-budget.json` records one and never gates on it. Every second on
this page is real AWS for that reason.
[`live/FLOCI.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/FLOCI.md)
sets out the rule and the three other questions an emulator-backed measurement
cannot answer.

### What evaluating this costs in money

Effectively **$0.00**, against a $15 ceiling, for the whole 745-resource run.
Every write the fixture makes is free — IAM, VPC, subnet, security group,
Route 53 record changes, ECS cluster, service and task definitions — and
`desired_count = 0`, so no Fargate task ever ran. Both hosted zones were
deleted well inside the twelve hours below which AWS does not charge for one.

Teardown was confirmed by listing rather than inferred from an exit code, and
then verified again independently through the AWS CLI: the account is back to
its baseline, and the 21 ARNs still answering the run's own tag were described
one at a time to confirm each was `INACTIVE` at zero running and zero desired.

## Where the mechanism is

This page is the decision. [What a plan costs]({{< relref "/docs/model/plan-cost" >}})
is the mechanism: the two terms a plan is made of, how each one grows, the
per-leg split at three scales, and the two concurrency bounds you can turn
down when a real account starts answering `Rate exceeded`.
