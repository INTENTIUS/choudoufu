---
title: "Claim 20: Scale: the estate boundary holds when the account is a terralith"
weight: 20
claim: plan-cost-under-foreign-load
---

# Claim 20: Scale: the estate boundary holds when the account is a terralith

Every claim above runs against an account holding a handful of resources.
That is the wrong size to ask claim 14's question at, because an account
that small cannot tell an estate-scoped plan from an account-scoped one.
This claim asks it at the size the design is for: the estate under test is
held still and a whole second terralith - up to 3,705 resources under a
different `tofu-estate` marker - is applied into the account around it.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info), the AWS CLI is installed, and Go is on PATH (this scenario
generates its terraliths). From the repo root run:

  just smoke plan-cost-under-foreign-load

Explain each step's verdict line to me as it prints, and tell me what the
step 3 list of unfiltered types means. Then run
BREAK=1 just smoke plan-cost-under-foreign-load and report the "caught"
line: it asks the same estate the account-wide question instead of the
estate one, and the cost must jump.
```

The steps as they print:

1. `stand up the estate, and plan it alone` - `tools/terralith-gen -scale
   1` generates a 79-resource estate, choudoufu applies it into an empty
   account, and its plan's request count is recorded.
2. `grow the ACCOUNT into a terralith, and replan the estate` - a second
   generated terralith is applied under its own `tofu-estate` marker. The
   first estate replans, unchanged. The threshold is not "the number did
   not move": a bound state file is read end to end, so cost that tracked
   the account would climb by at least one call per foreign resource, and
   the check is that the growth stays under half of that floor.
3. `what grew, and what did not` - the growth is not zero and the scenario
   prints where it came from, out of the run's own debug log: the types
   this plan could not filter server-side, and the account-wide Cloud
   Control list count beside it.
4. `what reading the whole terralith would cost` - the same account, the
   same directory, `-adoption-only` instead of the estate question.
5. `teardown` - both terraliths destroyed.

`FOREIGN_SCALE` and `OWNED_SCALE` set the two terraliths' sizes in
generator scale (`74N + 5` resources each), so the five-minute run a reader
does and the run that produced the table below are the same scenario.

**Measured, on the emulator.** `TestForeignLoadAgainstFloci`
(`internal/live/discovery/foreignload_bench_test.go`) at commit
`fa8a32fed0`, floci pin `sha256:d9207de1`, 2026-09-09. Every row is one
account: the owned estate is applied, then measured. The plan column is
what an operator's `choudoufu plan` put on the wire, counted at a proxy in
front of the emulator; the sweep and read-pass columns are the same in-
process split [what a plan costs]({{< relref "/docs/model/plan-cost" >}})
reports, taken with no record store.

| Foreign | Owned | `choudoufu plan` | Sweep | Read pass | Cloud Control list | Total |
|---|---|---|---|---|---|---|
| 0 | 79 | 187 | 579 | 148 | 435 | 727 |
| 79 | 79 | 197 | 602 | 148 | 435 | 750 |
| 3,705 | 79 | 687 | 1,631 | 148 | 435 | 1,779 |
| 79 | 3,705 | 8,396 | 2,676 | 6,812 | 435 | 9,488 |

Read the last row against the third. They describe accounts of almost
exactly the same size - 3,784 resources either way - and they differ only
in which side of the boundary the 3,705 sit on. **The read pass is 148 in
every row where the estate is 79 resources and 6,812 in the row where the
estate is 3,705.** It tracks ownership and nothing else. Between rows one
and three the account grew by 3,705 resources and the read pass did not
move by one call.

**The Cloud Control column is flat, and that answers the question this
claim was expected to lose.** Cloud Control's `ListResources` offers no
server-side tag filter on any type at all, so every call it makes
enumerates the account and the estate filter is applied on this side of
the wire. It reads **435 calls in all four rows**, and its one
`GetResource` refinement fires once in all four. 435 is one call per
admitted type the provider offers no native list resource for: an
account-wide plan of the same fixture, captured with `TF_LOG=debug`, logs
exactly 435 `listing <type> via Cloud Control` lines. That count is a
property of the provider's admission table, not of the account, which is
why 3,705 more resources do not move it.

Two of the thirteen types a terralith declares have no native list
resource either, and neither of them goes this way. The same capture says
where they go instead:

```
stateless/discovery: sweeping aws_ecs_cluster via the Tagging API (AWS::ECS::Cluster), 1 resources
stateless/discovery: sweeping aws_iam_instance_profile via the Tagging API (AWS::IAM::InstanceProfile), 10 resources
```

That is the estate-filtered leg - one `GetResources` carrying a
`tofu-estate` tag filter, answered server-side - so those two types cost
the account nothing. A steady-state `choudoufu plan` with a warm record
store reaches Cloud Control zero times at all.

**What does grow is the native leg, and it is not Cloud Control.** The
plan column climbs 187 to 197 to 687 as the account fills, and every call
of that growth is `GetPolicyVersion`: `aws_iam_policy`'s list resource
offers the provider no filter block, so the sweep lists the account's
policies and the provider reads each one's default version. The scenario's
step 3 prints the same fact from the run's own log - `aws_ecs_service`,
`aws_iam_policy` and `aws_iam_role` listed unfiltered, each naming "the
list configuration has no filter argument". Per foreign resource the
growth is 0.13 calls, against the 1.0 a bound state file pays; the shape
is not the state file's, but it is not zero either, and this page will not
print it as zero. It is the per-object refinement
[#622](https://github.com/INTENTIUS/choudoufu/issues/622) named, measured
here at a size where it shows.

**The `BREAK=1` control.** On the same account carrying 79 foreign
resources, the same estate asked the account-wide question instead of its
own costs **736 calls against 197** - the branch that drops the
server-side estate filter is exactly `-adoption-only`'s, and if defeating
it did not explode the cost then the scoped column was cheap for some
reason other than the scoping.

**Wall clock, recorded because it is a number too.** Applying a
3,705-resource terralith to the pinned emulator took **1,048 s** as the
foreign estate and **1,044 s** as the owned one; 79 resources take 34 s.

Everything below this line is real AWS and stays cited rather than re-run,
because the emulator does not throttle and no scenario on this page can
say anything about what happens when a service does.

**A 745-resource estate migrates in one pass, with a single state file
behind it.** Real AWS, `us-east-2`, recorded in
[`live/gauntlet.json`](https://github.com/INTENTIUS/choudoufu/blob/main/live/gauntlet.json)'s
`live_cert` block at commit `1d06e1d177`: stock `terraform` applied 745
resources holding its own state file; `choudoufu live-import -approve`
verified 335 of the 745 and stamped every one it verified, and left the
other 410 alone because they compose their identity from an already-stamped
parent and need no marker of their own - nobody typed one by hand. The
post-migration plan came back empty and the no-op apply changed nothing.
One estate, one state file, one migration pass, at a scale most terraliths
never reach.

**Provider call counts hold at parity with stock, or under it, at that
scale.** The same real account, both sides planning a no-change estate -
[what you pay, and when]({{< relref "/docs/what-you-pay#planning-an-adopted-estate" >}})'s
"same comparison on real AWS" table:

| Resources | stock | choudoufu | Difference | Commit |
|---|---|---|---|---|
| 79 | 149 | 155 | +6 (+4.0%) | `d359210978` |
| 745, session 1 | 1416 | 1413 | -3 (-0.2%) | `d359210978` |
| 745, session 2 | 1449 | 1404 | -45 (-3.1%) | `02885d2fd6` |

At 79 resources choudoufu costs six more requests than stock. At 745, in two
separate real-AWS sessions, it costs fewer. The comparison does not worsen
as the estate grows; at this one scale it inverts.

**The sweep that makes migration and recovery possible is shaped by the
provider's admission table, not by the size of the account it runs
against.** [What a plan costs]({{< relref "/docs/model/plan-cost#the-two-terms" >}})'s
own reproduction, no cloud and no emulator, commit `5d55f4aa9f`:

```
go test ./internal/live/discovery/ -run TestSweepUniversePartitionIsMostlyNative
sweep universe=1027 tagging_leg=35 native_leg=992
```

That bounds the sweep's shape - one call per admitted type, not one call per
object the account holds. Whether the account's own object count could
still leak in through the one per-object refinement call the native leg
makes was a real, named risk
([#622](https://github.com/INTENTIUS/choudoufu/issues/622)). On a real,
populated account of its own - 24 IAM roles, 5 buckets, 2 hosted zones, 11
active ECS task definitions, none of them this estate's - at commit
`eb1d145dc5`, that call fired **zero** times, at a small scale and at ten
times it, on the first plan and the steady-state one alike. The
foreign-load table above is where it does not fire zero times: that account
carries 3,705 foreign resources including several hundred IAM policies,
which is a population the `eb1d145dc5` account did not have. Both readings
are true of their own accounts, and the difference between them is which
types the neighbours are made of.

**What this claim does not say.** It says nothing about incremental plan
time within one already-adopted state; the day-2 call counts on
[what you pay, and when]({{< relref "/docs/what-you-pay#planning-an-adopted-estate" >}})
and [what a plan costs]({{< relref "/docs/model/plan-cost#the-measured-split-on-a-migrated-estate" >}})
are their own, separately measured figures, and this claim does not restate
them as if they were part of it. And the seconds comparison - how long a
plan takes on the wall clock, never how many requests it issues - stays
exactly where
[what you pay, and when]({{< relref "/docs/what-you-pay#wall-clock-withdrawn-because-the-comparison-was-not-like-for-like" >}})
leaves it: withdrawn, because the sessions that produced one compared a
cached plan against an uncached one. This claim will not restate a number
its own source page has already taken back; re-measure it there; this page
will follow once that page does.

The scenario proves the estate boundary on an emulator and nothing else.
Throttling is the part of scale it cannot reach - floci does not throttle -
so the real-AWS paragraphs above stay citations, and a reader who wants to
challenge them should challenge `site/content/docs/what-you-pay.md`,
`site/content/docs/model/plan-cost.md` and issue #622 rather than the
scenario.
