# What the emulator can and cannot show

Almost every measurement in this repository is taken against floci, the AWS
emulator `live/floci-image` pins. That is deliberate: it is fast, free, and
it starts empty, so a fixture's own resources are the only ones in the
account. This file is the other half of that bargain. Four things floci
cannot show, each because of how the emulator is built rather than because
the fixture was too small, and each with a real incident behind it.

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

floci returns every list in a single page, at any size these fixtures reach
(lex00/floci#185).

`internal/live/discovery/terralith_ceiling_bench_test.go` reads
`pagination_total = 0` at every tier it measures, including a scale=80 run
carrying 480 `aws_iam_policy` instances — 4.8x real AWS's documented 100-item
default page size for `ListPolicies` — and 80 `aws_ecs_task_definition`
instances returned by one `ListTaskDefinitions` call. Confirmed outside
choudoufu entirely, with the plain `aws` CLI against a fresh container and
`--max-items`/`--max-results` given explicitly: 150 IAM policies and 120 ECS
task definitions each come back in one response, `IsTruncated`/`nextToken`
unset.

So **`GetResources = 1` in a floci measurement is a property of the
emulator**, not a finding about the sweep. `cloudcontrol.Client.GetResources`
paginates `PaginationToken` to exhaustion and sets no `ResourcesPerPage`; the
real page count is a property of the Resource Groups Tagging API that no
floci-backed run reports. This is not a "the estate needs to be bigger"
limit. No floci-backed N will ever produce a nonzero answer here.

Real AWS does paginate, and the one run that looked found something worth
keeping. #567's `test_plan` stage counted **9 pagination-continuation lines
at scale 1 and the same 9 at scale 4**, traced to SageMaker's `ListHubs`
returning a `NextToken` for an AWS-managed hub the estate never declares:
flat across a 4x resource increase, from a type the configuration does not
contain, which is the O(types) shape observed rather than argued. Those are
debug-log continuations across the whole sweep, not `GetResources` pages.
**The real `GetResources` page count is still unmeasured**, and nothing that
runs against floci will change that.

## 3. Wall clock

A wall-clock number measured against floci grades the machine, not the code.

`live/plan-budget.json` carries `wall_clock_bucket` and says so in its own
note: informational only, never gated, because "floci's performance on
whatever machine runs the test, not this repository's code, is what a
wall-clock assertion would actually be grading."

The contrast sits inside that one artifact. At N=200 and N=1000 its call
counts are 4413 and 22013, fitting `calls_total = 22*N + 13` with no
residual, reproducible on any machine. Beside them, in the same file and from
the same runs, `wall_clock_bucket` records "apply 9.8s / plan 8.2s at N=200,
apply 22.9s / plan 44.3s at N=1000" and is explicitly annotated as local to
the machine that recorded it. One of those two is a ratchet; the other cannot
be one.

Real-AWS wall clock is a different quantity again, dominated by network
latency rather than by the estate: #567's `test_plan` stage took 199s at
scale 1 and 223-226s at scale 4, near flat across a 4x resource increase. A
floci wall clock and a real-AWS wall clock for the same stage are not two
measurements of one thing and should never be subtracted or divided.

**What to do.** Count calls. If a wall clock has to be reported, report it
beside the machine and the pin, and never assert on it. `live/e2e/`'s
tagging-sweep harness prints both wall clocks and asserts neither, for
exactly this reason.

## 4. Throttling

floci applies no rate limiting. There is nothing to measure and no scale at
which there will be.

The only `throttleSettings`/`rateLimit` strings in
`live/floci-capabilities.json` are API Gateway *resource attributes* —
fields in a resource's own schema, not emulator behaviour. The ceiling
benchmark reads `throttle_total = 0` at every tier including its 4817-call
scale=80 run.

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
