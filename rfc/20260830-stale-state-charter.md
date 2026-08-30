# A State File That Is Allowed To Be Stale

Issue: https://github.com/INTENTIUS/choudoufu/issues/604

Supersedes part of issue #73's "no state ops" charter and the ruling issue
#109 left in `internal/configs/live.go`. This is a decision for this fork,
recorded here because it changes a charter that a prior ruling settled. See
[`rfc/README.md`](./README.md) for what this directory holds.

## The ruling

Maintainer, 2026-08-30:

> We can do anything OpenTofu does as long as the state file can be stale. No
> failure if it is lost — just a refresh.

That is the whole of it, and it dissolves the old constraint rather than
narrowing it. Three consequences, each of which is now the fork's position:

**A state file is allowed.** A real one, with OpenTofu's own latitude behind
it. What was forbidden was storing anything between runs; what is forbidden
now is *trusting* it.

**It is a cache, never an authority.** Stale is the expected condition, not a
fault to detect. Markers on live resources remain the only thing that settles
what this estate owns.

**Losing it is a refresh, not an incident.** The rebuild-from-markers path is
what makes the file disposable, and it is the one invariant that must never
break.

The product is then plainly **OpenTofu plus identity hooks, where the state
file is disposable because identity lives on the resources.**

And the hooks are not all in play on every run. The maintainer's framing:

> It is OpenTofu + Identity Hooks — which implies improved adoption, migration
> by tag, central policy, lots of things. But not all of those are always in
> play, so choudoufu should not have to use them always.

This document rules that the second sentence governs. A hook engages when the
run needs it. The default for a hook that is expensive and rarely needed is
off.

## What #73 said, and which half of it survives

#73's charter, 2026-08-13:

> the invariant is **"no state ops"** — the user never configures a backend,
> manages a lock, or performs state surgery; the live system supplies what
> those operations existed for.

and, in the same issue:

> the FAQ's "no state file, no backend, no lock" evolves to the stronger
> honest claim: nothing to store, lock, or repair — state format is spoken
> internally and in snapshots; there is no state to OPERATE on. State ops stay
> deleted from the UX: no backend block, no lock management, no state rm /
> moved / import ceremony.

The two halves of that sentence have come apart, and #73 half-saw it: "state
format is spoken internally" already concedes that the objection was never to
the format.

**"No state ops" survives unchanged.** No backend block, no lock, no `state
rm`, no `import` ceremony, no workspace. Those refusals are about a UX in
which the operator repairs a record by hand, and nothing here brings that
back. A cache the tool discards and rebuilds is not something an operator
operates on.

**"Nothing to store" is superseded.** It was doing work it could not justify.
Storing a derived value between runs is not a state op; it is a cache, and it
becomes an op only if losing it costs the operator something. Under the
rebuild-from-markers invariant, losing it costs a read.

## What #109 said, and why it argues the other way

#109 removed observational snapshots. Its ruling is still in the code, at
`internal/configs/live.go:538-545`, and is repeated verbatim to the operator
in the decode-time error at `:559`:

> The live system is authoritative and readable at any time, so a stored
> snapshot was a stale copy of what every run re-derives

Read under this charter, the premise is right and the conclusion does not
follow. "What every run re-derives" is the definition of a value that is
**safe** to cache: one you can always recompute is one you can always check,
so it cannot mislead you for longer than it takes to look. The ruling treats
re-derivability as disqualifying when it is the safety property that makes a
cache admissible at all.

Two things about #109 stay correct and are not reopened here.

- **Nothing reads a snapshot as authority.** That invariant is unchanged and
  extends to anything this charter permits storing.
- **Its removal was the right call on its own facts.** The snapshot was a
  redacted write-only artifact with one load-bearing consumer, guided
  discovery's plan-cost hint, and that consumer moved to the record store.
  Removing an unused thing is not the same decision as forbidding a used one.

What is reopened is the general principle #109's sentence has since been read
as establishing. It does not establish it.

## What the measurements say this is worth

Every figure below was re-derived for this document. Sources are named so the
next reader can re-derive them again.

### The cost gap versus stock is one hook, and it is the estate-wide sweep

From `rfc/20260830-slicing-under-choudoufu.md`, migrated terralith at three
scales, floci pin `sha256:c55d74e1`, commit `cfd0dc58d4`:

| Instances | Tagging leg | Native leg | Sweep | Read pass | Total |
|---|---|---|---|---|---|
| 79 | 1 | 521 | 558 | 148 | 706 |
| 301 | 2 | 521 | 592 | 556 | 1148 |
| 745 | 4 | 521 | 660 | 1372 | 2032 |

The read pass is **exactly** what stock's refresh costs, at every scale
measured: 148, 556 and 1372 on both sides, to the call. That is not close
agreement, it is the same number, and the reason is that the read pass is the
AWS provider's own `Read` implementations, which stock invokes on the same
resources when it refreshes.

So everything choudoufu spends above stock is the sweep, and 521 of it is the
native leg, which is **93% of the sweep at scale 1** (521/558 = 93.4%) and is
`521` in **all thirteen configurations** the slicing work measured: every
scale, both halves of a two-way split, and each of eight slices of an
eight-way split. It does not grow with the estate and it does not shrink when
a configuration declares fewer types.

#611 measures the same thing from the other end. Stateful choudoufu, meaning
this same binary on a configuration with no `live` block, issued **exactly the
same number of API calls as stock**, 150 at 79 instances and 558 at 301, across
three runs each of Terraform, OpenTofu and choudoufu, with no variance
(`rfc/20260830-stateful-equivalence.md`). Fitting the two points:

```
stock                = 1.84N + 5      (150 at N=79, 558 at N=301)
choudoufu, migrated  = 1.99N + 553    (710 at N=79, 1152 at N=301)
```

The fixed term is 553 calls and matches the sweep constant fitted
independently at 545.9. The marginal terms are within 8% of each other, and
choudoufu's is the **higher** one, because a migrated instance is both read by
the projection and counted by the sweep. Two points determine a line exactly,
so that is a description of two measurements rather than a tested model.

### On real AWS the fixed term is nearly the whole plan

#578, real AWS, `us-east-2`, 79 resources, three runs each, every plan empty:
stock 3s / 4s / 3s against choudoufu 203s / 211s / 200s. Stock finishes the
shared read pass in three seconds; the remaining 200 is the sweep, about 0.36s
per sweep call, which is one network round trip apiece.

Two corrections that matter for anyone quoting this later.

**The sweep is no longer sequential.** #605 landed
(`internal/live/discovery/sweepconcurrency.go`, `DefaultSweepParallelism =
10`), and the real-AWS table above predates it. Measured against the emulator,
call counts are identical at parallelism 1, 2, 10 and 20 (558 at 79
instances, 591 at 301), as are scan-row order and diagnostic sequence. The
wall-clock column is milliseconds over loopback and measures the overlap, not
the saving. **The real-AWS run has not been repeated.**

**`521 x 0.39s = 203s` is the arithmetic behind the projected sequential
cost, and `0.367` is not.** `0.367 x 521` is 191s, not 203s, so the two
figures circulating in `internal/live/discovery/sweepconcurrency.go:23` and
in #605's commit message do not multiply out. Quote the product or the
per-call figure, not both.

### #586 removed the alternative

#586 asked whether the native leg could be narrowed and answered no
(`5d55f4aa9f`, no production code changed): `arnJoinReaches` proxies
enumeration rather than placement, and flipping it makes the cross-type marker
guard vacuous, because AWS copies tags onto dependent objects of other types
as ordinary behaviour and the ARN join is what catches it today.

It also found the cost is unbounded by estate size. `scanTypeCloudControl`
issues one `GetResource` per listed object that arrives without tags, so on a
populated account it scales with **the account**, not the estate. The 205s
figure came from a near-empty test account and is the optimistic case.

So there is no third option. Either pay a sweep whose cost tracks the client's
account on every plan, or do not run it on a steady-state plan.

## The bound that does not move

The marker sweep is a **one-sided oracle**. A marker present proves the
resource exists. A marker absent proves nothing, because the tag index lags
writes by minutes. A cached entry can therefore be confirmed cheaply and can
never be refuted cheaply.

Three rules follow, and they are the hard limit on everything this charter
permits:

1. **"Allowed to be stale" is never "allowed to guess."** An entry that cannot
   be confirmed is re-read, or reported unknown. It is never assumed.
2. **Any gate fails toward doing the work.** Where the tool cannot establish
   that a hook is unnecessary, it runs the hook. Absence of evidence never
   selects the cheap path.
3. **The rebuild-from-markers path stays exercised.** It is the invariant that
   makes the file disposable, and an invariant nothing runs is an invariant
   that rots. Whatever caching lands must keep a test that discards the cache
   and rebuilds from markers alone, run on every measurement, not once.

`markerIndex.join`'s three binding gates are not weakened by anything here. A
wrong bind adopts somebody else's resource, which is the one error class with
no recovery. #586 found that one of the three, the `tofu-estate` gate, had no
test at all, so deleting it left the package green, on the argument that
`GetResources` is filtered on that tag, which is a claim about the service
rather than about this package. It is now covered and was proved red.

## The `CollectUnclaimed` decision

`internal/command/live_plan.go:1207-1208` sets, in `statelessDiscoverOne`, at
function-body top level with no branch selecting it:

```go
CollectUnclaimed:  true,
Sweep:             true,
```

**Ruling: `CollectUnclaimed` does not stay unconditional.** It becomes a
capability the run selects, defaulting off for an ordinary `plan` on a
steady-state estate and on where the question it answers is the point of the
command.

The reasoning is that the question it answers is a real one and is not
correctness. `CollectUnclaimed` answers "what is in my account that I do not
know about", which is genuinely useful during a migration, an audit, or a
hunt for
drift somebody else caused, and genuinely irrelevant to an operator changing
one resource in an estate that is already fully adopted. Stock ships without
any equivalent and nobody calls stock broken for it. That is the strongest
available evidence that it is not a correctness requirement.

The gate cannot be inferred, and this is the part worth being exact about.
"Is every declared instance bound?" is cheap: the tagging leg costs
`ceil(tagged/100)` calls, measured at 1, 2 and 4 for 38, 137 and 335 tagged
resources. "Is there anything in the account I do not know about?" is not
answerable by that route at all, because the tagging leg is filtered on this
estate's own tag, so an unclaimed resource is invisible to it by
construction. The native leg exists precisely because of that. **This is a
product decision about how often an operator needs the answer, not an
optimisation that detects when they do.**

What this ruling does not do is pick the mechanism. Opt-in flag, periodic on a
schedule the record store tracks, or on by default for the commands where it
earns its cost (`live-import`, `plan -adoption-only`, an audit command) are
all consistent with this ruling, and choosing between them is design work that
follows it. Rule 2 above binds all of them: whatever the mechanism, an
unanswerable question runs the sweep.

Note that `-adoption-only` (#587) is today a renderer switch and gates
nothing: `internal/command/live_mode.go:179-183` selects a different view and
the help text says so: "the same live reads, the same sweep, the same plan".
It is the obvious first place for the capability to become real, and it is not
that yet.

## The refusals, re-justified one line each

`statelessRejections` is a function at `internal/command/live_mode.go:318`,
called from exactly two places, both inside `if statelessCfg != nil`
(`plan.go:131`, `apply.go:144`). Six refusals, each re-examined under this
charter. The default has inverted: a refusal now earns its place rather than
being assumed.

| Refusal | Verdict | Why |
|---|---|---|
| `-out` (saved plan file) | **Reopen** | Refused because "a saved plan file records the state snapshot the plan was made against" and there was no such record. Under this charter there can be one. What it must not become is an apply that trusts the planfile's prior state; the planfile records what the plan saw, and the apply still confirms against markers. Design work, not a decision this document makes. |
| `apply <planfile>` | **Reopen** | Same reason, same bound. `statelessRejectPlanFile` (`live_mode.go:374`) refuses before reading the file, which stays correct while the refusal stands. |
| `-refresh-only` | **Reopen** | Its stated reason is "both sides of that comparison are the live system". That was true with no stored record and is false with one: comparing a stored record against the live system is exactly what a stale cache plus a marker sweep does. This is the refusal whose justification this charter most directly removes. |
| `-state`, `-state-out`, `-backup` | **Keep, narrowed** | These are state ops in #73's surviving sense: they point the run at an operator-managed file, which is the backend-and-surgery UX this fork removes. That a cache exists internally is not a reason to expose it as a file the operator names. Revisit only alongside a decision to make the cache operator-visible. |
| `-json`, `-json-into` | **Keep for now, and it is debt** | Refused because the live-markers sections "have no JSON representation yet". That is unrelated to state and always was. Nothing about this charter changes it, and the word "yet" is honest: it is unbuilt work, not a principle. |
| `-generate-config-out` | **Keep for now, and it is debt** | Same shape. Refused because the generated form "has not been checked against the live-markers configuration subset yet". Unrelated to state; unbuilt, not forbidden. |

Two refusals live outside that function and are unchanged. Workspaces
(`live_mode.go:145-151`) are a second state file under a different name and
the estate is the unit of ownership instead, squarely "no state ops", which
survives. Non-local backends (`live_mode.go:153-163`) perform operations out
of process, where the marker pipeline cannot reach; that is a mechanism
constraint, not a statelessness one.

`-refresh` is accepted and inert, via `SkipRefresh: true` at
`live_plan.go:724`. That stays right for as long as the projection is built
from live reads immediately beforehand. If a cache lands, `-refresh` acquires
a meaning, discard the cache and re-derive, and should get one.

**One defect found while auditing this.** There are two refusal sets, not
one: `statelessRejections` serves `plan` and `apply`, and
`livePlanRejectUnsupported` (`live_plan.go:2291`) serves `live-plan`. They
have diverged on `-destroy`, which #320 lifted from the first (ruled in #425)
and not the second. `live-plan`'s help text at `live_plan.go:3476` still
lists `-destroy` among the options "rejected rather than ignored", which is
true of `live-plan` and false of `plan` and `apply`. Filed as a follow-up;
not fixed here, because this document changes no code.

## What this does not decide

Nothing is implemented on the strength of this document, and that is
deliberate: the last charter change was settled by a ruling now being
revisited, so this one is written first and built second.

Specifically undecided: the cache's format, where it lives, how staleness is
bounded, what the `CollectUnclaimed` gate's mechanism is, and whether the
three reopened refusals actually land. #579's `live-verify` becomes a special
case of this rather than a separate mode, and should be sequenced after it.

**Standing.** choudoufu has no customers and is experimental. A constraint has
to earn its place on current evidence rather than on the date it was written.
Where one is costing measured performance and its stated reason no longer
holds, it goes. Where a constraint is doing real work, as "no state ops" and
the one-sided-oracle bound both are, it stays on the same terms.

## A Node dependency in a Go repository

This document also records why the repository root now carries a
`package.json`, since that is the kind of thing a later reader will want a
reason for.

`prose-lint.mjs` at the root reports AI-writing tropes in this fork's
hand-written Markdown, using the `sentences` package's `dist-lint` subsystem.
It is **report-only**: it always exits 0, nothing in `just ci` calls it, and
its baseline across 125 hand-written pages is 3226 findings, which is not a
gate anybody could turn on today. Its one runtime dependency is `compromise`,
pure JavaScript, and it reaches no network at lint time. Go has no equivalent
that operates over a constituency parse. Enforcement, if it ever comes, needs
a baseline first, which is what `node prose-lint.mjs -json` produces.

## What was not verified

- **Nothing here was measured against real AWS.** The real-AWS figures quoted
  are #578's, re-read rather than re-run, and they predate #605.
- **The concurrent sweep's real-AWS effect is a projection.** `521 x 0.39s`
  divided by ten is arithmetic. Nobody has run it against an account.
- **No cache exists to measure.** Every claim about what caching would save is
  a claim about what the sweep costs, which is measured, plus an assumption
  that a steady-state plan can skip it, which is this document's ruling rather
  than a measurement.
- **One fixture, one composition, AWS only.** The 521-call native leg is a
  property of the admission table rather than of the estate, but only this
  estate was measured and it declares thirteen types.
- **The refusal verdicts are judgements, not experiments.** No refused option
  was made to work; each was read against its own stated reason.
