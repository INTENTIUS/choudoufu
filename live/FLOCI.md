# What the emulator can and cannot show

Almost every measurement in this repository is taken against floci, the AWS
emulator `live/floci-image` pins. That is deliberate: it is fast, free, and
it starts empty, so a fixture's own resources are the only ones in the
account. This file is the other half of that bargain. Four things a
floci-backed measurement will not tell you, each because of how the emulator
or the harness around it is built rather than because the fixture was too
small, and each with a real incident behind it. Section 2 is the one where
the harness, not the emulator, turned out to be the blind half.

Read this before writing a fixture, and before quoting a number that came
out of one. The per-type question ("does floci implement `aws_foo`?") is a
different question and lives in `live/floci-capabilities.json`, keyed by the
image's content digest. This file is about the questions no per-type
capability entry answers.

**The one rule underneath all four.** floci returning success is evidence
that floci accepted something. It is not evidence that AWS would. A test
whose oracle is "the emulator did not complain" has no oracle. Every blind
spot below is a variation on that.

## 1. Value semantics

floci accepts values real AWS rejects, silently, with a 200.

`tools/terralith-gen` emitted Route 53 TXT records whose value carried its
own pair of double quotes. The AWS provider quote-wraps a TXT value itself,
so the record went to the wire double-wrapped. Real AWS answered
`InvalidCharacterString (Value should be enclosed in quotation marks)
encountered with '""v=text0002""'`. floci had been accepting the same value
without comment across issues #564, #565 and #566 — three separate
measurement runs, none of which could see it. The first live-AWS run found
it immediately (#567).

The follow-on is the transferable part. #567 fixed the line on its own
branch and landed no test. #575 then landed on `main` from the same
merge-base, rewrote that line from a scalar into a list-valued map entry for
an unrelated reason, and carried the same double-quoting into the new
spelling. Nothing on `main` had ever held the invariant, so there was nothing
for the rewrite to trip over, and the fix only arrived when #577 merged and
had to resolve the two by hand. It is now guarded by
`TestRecordValuesAreNotPreQuoted` in `tools/terralith-gen/shape_test.go`,
with `TestRecordValuesAreNotPreQuotedHasTeeth` feeding it main's exact
pre-fix rendering as a control.

**What to do.** A fixture's *values* are unverified until a real provider has
sent them somewhere real. Argument shape, resource count and plan structure
are all fair game against floci. Whether a string is one AWS will take is
not, and no amount of green floci runs converts one into the other.

## 2. Pagination

floci returns the native list APIs these fixtures reach in a single page
(lex00/floci#185). It does **not** do that for `GetResources`, and an earlier
version of this section said it did.

The native half still holds, and it holds because it was checked outside
choudoufu entirely: with the plain `aws` CLI against a fresh container and
`--max-items`/`--max-results` given explicitly, 150 IAM policies and 120 ECS
task definitions each come back in one response, `IsTruncated`/`nextToken`
unset. `internal/live/discovery/terralith_ceiling_bench_test.go`'s scale=80
tier agrees from the inside: 480 `aws_iam_policy` instances — 4.8x real AWS's
documented 100-item default page size for `ListPolicies` — in lists that
never continue.

### The Resource Groups Tagging API does paginate, and the instrument could not see it

#584 read `ResourceGroupsTaggingService.java:165` and then measured it: floci
sets `resourcesPerPage` to 100 and returns a `nextPaginationToken` whenever
more remain. The tagging leg took **1, 2 and 4 `GetResources` calls for 38,
137 and 335 tagged resources** — `ceil(n/100)` exactly, at every point.

Every prior run in this repository nevertheless reported
`pagination_total = 0`, including the ones this section used to cite. The
reason was not the emulator. `flocitest.CountingProxy` recognises a
continuation by the request's token field name, and its list of names did not
include `PaginationToken` — so the one API that was actually paging was the
one API the counter could not count. `internal/live/flocitest/proxy.go` now
carries that name and a note saying why ("PaginationToken was missing until
issue #584 and its absence mattered"), and #584 proved the guard red before
trusting it green.

**This is the most instructive entry in this file**, because it is not floci
being unlike AWS. It is a confident claim — "no floci-backed N will ever
produce a nonzero answer here" — derived from an instrument that was blind to
the field it was reporting on. A zero from a counter is only as good as the
counter's own coverage, and nothing about a zero announces which of the two
it is. Before quoting any `pagination_total`, check whether the run predates
`811df3add9`: if it does, its zero means nothing about `GetResources`.

What is still unmeasured is the **real** page size. floci's 100 is floci's
constant; `cloudcontrol.Client.GetResources` paginates `PaginationToken` to
exhaustion and sets no `ResourcesPerPage`, so what the Resource Groups
Tagging API itself does still needs a real-AWS run.

Real AWS's native lists do paginate, and the one run that looked found
something worth keeping. #567's `test_plan` stage counted **9
pagination-continuation lines at scale 1 and the same 9 at scale 4**, traced
to SageMaker's `ListHubs` returning a `NextToken` for an AWS-managed hub the
estate never declares: flat across a 4x resource increase, from a type the
configuration does not contain, which is the O(types) shape observed rather
than argued. Those are debug-log continuations across the whole sweep, not
`GetResources` pages.

## 3. Wall clock

A wall-clock number measured against floci grades the machine, not the code.

`live/plan-budget.json` carries `wall_clock_bucket` and says so in its own
note: informational only, never gated, because "floci's performance on
whatever machine runs the test, not this repository's code, is what a
wall-clock assertion would actually be grading."

The contrast sits inside that one artifact. At N=200 and N=1000 its call
counts are 4408 and 22008, fitting `calls_total = 22*N + 8` with no
residual, reproducible on any machine. Beside them, in the same file and from
the same runs, `wall_clock_bucket` records "apply 3.3s / plan 1.8s at N=200,
apply 18.3s / plan 9.8s at N=1000" and is explicitly annotated as local to
the machine that recorded it. One of those two is a ratchet; the other cannot
be one.

Real-AWS wall clock is a different quantity again, dominated by network
latency rather than by the estate: #567's `test_plan` stage took 199s at
scale 1 and 223-226s at scale 4, near flat across a 4x resource increase.

### The 273-second stall, and what it says about comparing wall clocks

An earlier version of this section said a floci wall clock and a real-AWS
wall clock for the same stage "should never be subtracted or divided." That
was a holding position taken because two figures for what looked like the
same operation sat 100x apart with no explanation. There is one now, and it
is more useful than the rule it replaces.

The two figures were:

- **~2-3s**, `live/live-cert/terralith-scale.sh`'s `test_plan` stage against
  floci — a full post-migration `choudoufu plan`, asserted empty.
- **273.6s** (scale 1) and **680.5s** (scale 4),
  `live/e2e/terralith-scale/MIGRATION.md`'s `choudoufu plan, post-migration`
  row against floci.

They are the same operation on the same generated estate against the same
pin, and the gap is **one defect, not a cost**. `tools/terralith-gen` emits
`skip_requesting_account_id = true`; with it set, ECS identity resolution
builds an account-ID-less ARN (`arn:aws:ecs:us-east-1::cluster/<name>` —
issue #572), and the AWS provider's `aws_ecs_service` read then retries
`ECS/DescribeServices` against that ARN roughly every 10 seconds until it
gives up. `live/live-cert/terralith-scale.sh` omits that setting on purpose,
citing #572, which is the whole of why its `test_plan` is seconds.

Measured on this branch, four consecutive plans on one directory: **273.95s,
273.56s, 273.48s, 274s wall — against 3.1s of user CPU and 0.7s of sys.**
choudoufu is not computing and floci is not serving; the process is asleep in
a backoff. The debug log carries 36 `unretryable error ...
ClusterNotFoundException: Cluster not found:
arn:aws:ecs:us-east-1::cluster/<name>` attempts spaced 10.0s apart, spanning
essentially the whole run.

Then the single-variable control. Two adopted directories, byte-identical
except that one has `skip_requesting_account_id = true` deleted, same floci
container, same markers, same records:

```
A  wall=274s  plan is NOT empty
B  wall=7s    plan is EMPTY ("No changes. Your infrastructure matches ...")
```

`rulings/20260830-stateful-equivalence.md` reached the same verdict from a
separate run and a different harness, timing four columns on one commit: the
generator's own provider block gives 273.42/274.19/273.42s and a non-empty
plan, and the certification harness's block gives 3.79/2.87/2.73s and an empty
one, on the same emulator at the same pin. Two independent controls, one
conclusion.

So the ~267s is a fixed stall from one configuration line, on one resource
type, independent of estate size — not plan cost, and not floci latency.
`live/e2e/terralith-scale/MIGRATION.md`'s own diagnosis ("network/API-latency
bound — each of the now-tagged resources gets read and diffed individually")
is the wrong reading of its own evidence: it is one resource, not each, and
the idle floci container it cited as proof of network-boundedness was
evidence against that conclusion rather than for it. `docker stats` measures
the emulator, and an idle emulator during a 680-second plan means the caller
is blocked, not that the network is slow.

**The rule this replaces the holding position with.** Two wall clocks for the
same stage in different environments are comparable when the stage is the
same stage — `live/live-cert/terralith-scale.sh` run with `TARGET=floci`
versus `TARGET=aws` is one script, one estate, one code path, and its ~2-3s
against ~200-226s is a fair statement about per-call latency over a ~525-type
sweep. What is never comparable is a wall clock carrying a stall against one
that does not. Before dividing two of them, account for the whole of the
larger one: 267 of MIGRATION.md's 273.6 seconds are #572, and a ratio built
on that number is measuring a bug.

**What to do.** Count calls. If a wall clock has to be reported, report it
beside the machine and the pin, and never assert on it. `live/e2e/`'s
tagging-sweep harness prints both wall clocks and asserts neither, for
exactly this reason. And when one wall clock is far larger than another,
find out where the time went before naming the difference — CPU-versus-wall
(`/usr/bin/time -l`) separates "computing" from "waiting" in one run, and a
timestamped `TF_LOG=DEBUG` capture names what it waited on.

## 4. Throttling

floci applies no rate limiting. There is nothing to measure and no scale at
which there will be.

The only `throttleSettings`/`rateLimit` strings in
`live/floci-capabilities.json` are API Gateway *resource attributes* —
fields in a resource's own schema, not emulator behaviour. The ceiling
benchmark reads `throttle_total = 0` at every tier including its 4817-call
scale=80 run.

That zero survives section 2's lesson, but check the reasoning rather than
taking it: `isThrottleResponse` keys on HTTP 429 and on an error body naming
`Throttling`/`TooManyRequests`/`SlowDown`/`RequestLimitExceeded`, none of
which is a per-API field name, so the `PaginationToken` blindness has no
analogue here. It is still an enumerated list with a safe default, which is
the shape that produced the pagination error — the difference is that this
claim does not rest on the counter alone. floci implements no rate limiting
in the first place, so there is nothing for a counter to miss.

Real AWS throttles, escalates non-linearly, and absorbs it. From #567's
live-AWS run against a real account (`us-east-2`, IAM
`AttachRolePolicy`/`TagInstanceProfile`, stock apply at `-parallelism=10`):

| Stage | Scale 1, 55 resources | Scale 4, 205 resources |
|---|---|---|
| `cold_deploy` (stock apply) | 68s, 1 throttle / 1 retry | 163s, 35 throttle / 35 retry |
| `migrate` (sequential tag writes) | 71s, 1 throttle / 1 retry | 269s, 5 throttle / 5 retry |
| `test_plan` (the O(types) sweep) | 199s, 0 / 0 | 226s, 0 / 0 |

The scale-4 column is the officially recorded run, in `live/gauntlet.json`'s
`live_cert` block; scale 1 is from the same issue's manual runs. A factor of
4 in resources bought roughly an order of magnitude in throttle events, which
is what a fixed-rate account quota under sustained parallel writes looks
like. `test_plan` is in the table as a control: the read-only sweep saw none
at either tier, so the throttling is on the write path.

The SDK's own exponential backoff absorbed every one. Nothing failed, both
write stages completed, and the whole cost showed up as wall clock.

**What to do.** Throttling questions go to `live/live-cert/`, which runs
against a real account under a spend ceiling. `THROTTLE_LOG=1` (the default)
captures `TF_LOG=DEBUG` and counts throttle and retry lines. There is no
floci-side approximation to reach for first.

## What floci is good for

The ceiling benchmark found **no wall** in any metric reflecting choudoufu's
own code — API call count, discovery time, build and materialization time,
peak process memory — across 55 to 4005 resources and 77 to 4817 API calls.
Floci-backed measurements of those components are trustworthy at least
through that range. Call counts, plan shape, identity resolution, marker
round-trips, refusal behaviour and convergence are all things the emulator
answers honestly and cheaply.

The four above are not "floci is small". They are questions about behaviour
the emulator does not implement at all.

## A fifth trap, of a different kind

A `cloudcontrol-list` entry in `live/floci-capabilities.json` does **not**
imply the native API works. It records that Cloud Control's `ListResources`
answered for the CloudFormation type, which LocalStack answers generically.
`aws_cloudwatch_query_definition` and `aws_athena_named_query` are both
recorded `implemented` on that evidence and both return
`UnsupportedOperation` from the API the AWS provider actually calls. The
record-located harness's notes in `live/e2e/README.md` carry more of this
shape.

## Related

- `live/floci-capabilities.json` — per-service and per-type implementation
  evidence, keyed by image digest.
- `live/floci-image` — the pinned digest every harness and measurement uses.
- `live/e2e/README.md` — the harnesses, and per-fixture notes on what floci
  gets wrong for that fixture specifically.
- `live/plan-budget.json` — the call-count ratchet, and its own statement of
  why wall clock is not one.
- `live/live-cert/` — the real-AWS certification path, for the two questions
  above that have no emulator answer.
