# How to work this repository

One page. Everything longer than this is generated from code or lives in the
tracker, and the tests say which.

## Pick up here

Every session, new or resumed or picking up after a crash, starts with one
read-only command and reads all of it before touching anything:

```
bash scripts/pickup.sh
```

It prints, from git, `gh` and the tree rather than from anyone's memory: the
checkout and whether `main` has diverged from `origin/main`; the artifact's
commit, the two bars and which estates are not clear; the next units; open
pull requests with their CI state; every `gauntlet/*` and `live/*` branch
with its worktree, commits ahead of `main`, last commit, the gate file
(`ci.rc`) and last `GAUNTLET stage=` line found in that worktree, and a
disposition; leftover Agent-tool worktrees; running workers and emulator
containers; and the open foundation and ruling issues.

The dispositions are rules, not suggestions, and they are the whole answer
to "where was the last session":

| pickup says | it means | do |
|---|---|---|
| `ACTIVE?` | files in that worktree were written in the last 15 minutes; an Agent-tool worker shows no process of its own, so this is the only liveness signal | leave it alone; `.claude/scripts/agent-progress.sh` or wait |
| `UNCOMMITTED` | changed paths in the worktree and no recent write: a worker stopped before committing | read the diff, commit it on that branch with the unit ID, then treat as `COMMITS, NO PR` |
| `MERGED/EMPTY` | the branch is an ancestor of `main` with nothing ahead, nothing uncommitted, no recent write: landed, or a worker that never committed | delete the branch and its worktree |
| `PR OPEN #N` | a worker finished and reported | the orchestrator verifies (reads `ci.rc`, the `GAUNTLET` lines, the artifact diff) and merges on green |
| `COMMITS, NO PR` | a worker was mid-unit when its session ended | resume in that worktree from the last commit; never start the unit over in a new branch |
| Agent-tool worktree, ahead > 0 | an agent's unreported work | read the commits before pruning |
| `dirty` paths in the primary checkout | someone worked in the main tree | read them first; the main tree is never where work happens |

Branches are named `gauntlet/<estate>-<stage>` for a unit (what `next`
prints) and `live/<topic>` for anything else, so a branch name alone says
what it was for; a worker commits early and often on its branch with the
unit ID in the message, so a crash loses minutes, not the unit. The state
of the work is the artifact, the tracker, the branches and the pull
requests. Nothing about it lives in chat or in a session's memory.

Then read the rest of this page, and the brief for your role:
`.claude/agents/gauntlet-orchestrator.md` to run the loop,
`.claude/agents/gauntlet-worker.md` to do one unit,
`.claude/agents/live-markers.md` for the mechanics and traps of this
checkout.

## The promise

**If OpenTofu runs an estate, choudoufu runs it too.** Migration from a stock
state file is lossless, a greenfield apply is equivalent, and day-2
operations behave the way stock's do. That is the whole bar, and it is
measured, not argued: `live/GAUNTLET.md` is the test that decides it.

Cold adoption, taking over live infrastructure that nothing has ever marked,
is a feature with its own ladder. It is not part of the promise, and a number
about it is not a number about the product.

## The default, and the principles

**Compatible out of the box.** A configuration that works on stock OpenTofu
works here with a `live` block added and nothing else. That means a local
record store is implied when none is declared, the way stock implies local
state; secrets the configuration generates are stored there the way stock
stores them; markers are written on every taggable resource; marker repair is
on. The principles this fork exists for are toggles, and turning them on is
the setup step:

- no secrets stored by the tool (secret-generating types refused, sensitive
  settable arguments never recorded);
- markers never repaired out of band (for estates where something else owns
  the tags, with `ignore_changes` honoured exactly as stock honours it);
- per-type or per-address `markers = record`, for tag budgets and tag
  policies, trading IAM governability for a record-held identity.

The toggles live in the live configuration schema, each with a default, a
doc string and a fixture that proves it refuses exactly what it names. The
reference page is rendered from the schema.

## The foundation

Every managed instance has a **record**: identity, the arguments the provider
never echoes back, sensitivity marks, taint, deposed key. One per resource,
namespaced per estate, under IAM, written with compare-and-swap. **Markers**
stay what they are: the authoritative, recoverable identity on every taggable
type, the inventory any cloud tool can list, the attribute IAM conditions on.
Binding reads the record and verifies it against the marker; a lost record is
rebuilt from tags where tags exist, and where they do not, the estate is
exactly where stock is.

Ruled 2026-08-23 (`rfc/20260823-foundation-order-ruling.md`): the record
holds the identity of **every** instance, written by `live-import` and by
every apply, and a plan reads it first. The marker sweep and derivation
from configuration are the recovery paths, for when there is no record or
the record and the marker disagree. Nothing about ownership moves: a record
is never read as permission to delete, and the marker decides. What it
changes is that the path a migrated estate takes on every plan no longer
depends on re-deriving what the state file already said.

So every type stock supports is admitted. What varies per instance is its
**rung**: tag-governable, derived from configuration, or record-only. That
is a metric that goes up, never a gate that refuses.

## The safety rule

**Never write a wrong marker.** A refusal is loud and reversible; a wrong
marker is silent and adopts or displaces a real object. When a construct
stock accepts cannot yet be handled without risking that, the instance drops
to the record rung and the run proceeds. Refusing an estate is never the end
state, and convergence is never evidence an identity is right: assert the
rendered identity by value.

## The engine

**Stock is the oracle.** The gauntlet (`tools/gauntlet`, contract in
`live/GAUNTLET.md`) runs real, popular estates through fixed stages side by
side with stock OpenTofu against the pinned emulator, and diffs the plans and
the cloud. Every difference is one of five things, each with a fixed action
and no ruling:

| Difference | Action |
|---|---|
| choudoufu refuses where stock proceeds | defect; fix it |
| the plans or the resulting cloud differ | defect; fix it |
| stock fails too | the estate still has to clear: either choudoufu handles what stock cannot, or the stage's oracle is wrong and the oracle is what gets fixed. Matching stock is not the promise — supporting an estate that already works in OpenTofu is (maintainer ruling, 2026-08-24) — so matching stock's failure is never the finish line, and check the masquerade first: both runners share the emulator, so "stock fails identically" can be row 4 wearing row 3's clothes (corpus-ecs-fargate's task-def wall was exactly this) |
| the emulator is wrong | fix it in the floci fork, file the issue there, publish the image, repin and re-measure |
| handling it would write a wrong marker | drop the instance to the record rung, proceed, open a rung ticket |

None of those five rows ends the work. Stock failing is not a ceiling:
`cold_deploy` passes for every estate in the manifest, so stock runs them
all, and what it fails at is a later stage. Where stock cannot replan an
estate that it applied, choudoufu handling it anyway is a feature, not a
divergence to apologise for; `plan_approval` already commits to being
stricter than stock, and this is the same licence pointed the other way. An
emulator gap is not a ceiling either, because the emulator is ours to fix.

The unit of progress is **an estate clearing every active stage**, and that
is the only thing a unit may end on. A finding written down is a note on an
unfinished unit, never a finished one. An estate stays on the list until it
clears. The two numbers on the site, core estates clear and all estates
clear, are read from `live/gauntlet.json`, which only the runner writes.

## What a measurement is worth

The oracle only counts when it is independent. Stock and choudoufu talk to
the same emulator, so stock agreeing is evidence the two share a code path,
never evidence a defect is upstream. An estate was recorded for weeks as
blocked on a hashicorp/aws bug because plain terraform reproduced its
diagnostic byte for byte; the provider was choking on a rule the emulator
returned with no source, and fixing the emulator made it vanish. Before
calling anything upstream, read the API directly, with no terraform in the
loop, and compare against what AWS documents. Real AWS is the oracle for the
emulator; it is not the runner, and one call settles what a night of
inference cannot.

A check written from the implementation passes forever and proves nothing.
Three of those surfaced in one night: emulator tests that encoded the same
wrong wire key as the code they covered, crossing scripts asserting the
broken behaviour they were written beside, and a `BREAK=1` control that
tampered a resource carrying `ignore_changes = [tags]` and so could never
fail. Write a check from what the API promises, and prove it is load-bearing
by making it fail on purpose.

So a fixed wall makes stale scripts fail. When an assertion breaks right
after a fix lands, read it as the assertion being stale before reading it as
a regression; the estate usually got better and the script did not.

## The loop

0. `bash scripts/pickup.sh`, always, and act on its dispositions first.
1. `go run ./tools/gauntlet next` names the unit: the core estate closest to
   clear and the first active stage it does not pass, else the next growing
   one. The first act on a unit is to re-read its recorded failure against
   the service API directly, on the current emulator image, with no tofu in
   the loop; the recorded detail is a lead. Say which of the five rows it
   is before fixing anything. New estates enter with `go run ./tools/gauntlet add` (the procedure is
   `site/content/docs/progress/add-an-estate.md`; `live/GAUNTLET.md` is the
   definition). `just contribute` runs one unit unattended under
   your own key, in a worktree, and opens the PR; that is also what
   contributors run from their forks. `.claude/agents/gauntlet-orchestrator.md`
   is the brief for a session that keeps the loop running unattended: pick,
   spawn, verify, merge on green, and the short list of things to stop and
   ask about.
2. `go run ./tools/gauntlet run <name>`; read the verdict lines, not the exit
   code.
3. Every difference from stock gets its row from the table above. Fix
   generically: a fix that names a concrete `aws_*` type in control flow is
   the wrong fix; find the property and derive the rule, then say how many
   other types it reached.
4. `go run ./tools/gauntlet render`; commit the script, the artifact and the
   rendered docs together. `just ci` must be green.
5. When a planned stage is implemented for enough estates to be honest, flip
   its status to active in `tools/gauntlet/stages.go`. The bars drop; that is
   the point. **A headline flip is half a unit.** A PR that flips a stage's
   `Status` to active while `Headline: true` must either land as part of a
   series with the catch-up queue already dispatched, or state the resulting
   board number in its own body and name the catch-up tracking issue, which
   must exist before merge. Sections-first-flip-last is the default ordering
   for future stage activations (#491 is the working example); flip-early
   requires the catch-up tracking issue in hand. #480 flipped `day2_count` on
   two estates of evidence, correctly citing this precedent and correctly
   saying the bars would drop, and the session stopped there: the board went
   to core 2/25 clear, all 2/26 clear with nothing dispatched behind it, and
   #488 had to be opened after the fact to carry it back to clear.

## The order

Units continue from `next` at all times. Underneath them, the foundation
lands in a fixed order, ruled 2026-08-23 in
`rfc/20260823-foundation-order-ruling.md`, which carries the measurements
each item rests on and the commits they were computed at:

1. **#364 and record-primary identity**: every instance's identity in the
   record, written by `live-import` and apply (the write half), read first by
   the plan and verified against the marker (the read half). Moves the
   migrated population off the static path.
2. **Schema-first table (#387)**: the provider's identity schema wins over a
   ratified row wherever it reproduces it; the ledger keeps the exceptions.
   Independent; may run alongside 1.
3. **The plan-node seam (#388)**: identity resolved, and markers stamped, at the
   plan-instance node from stock's own evaluated values: record, then marker
   index, then identity schema over the evaluated configuration. One hook
   inside the engine. The HCL-rewriting stamp, `module_prefix`, and
   LayerStamp's refusals retire when the gauntlet holds without them; the
   static evaluator does NOT retire - it is the estate-wide demand
   computation live-import, live-mv, live-check, discovery and the
   instruments all consume (measured 2026-08-25, #388's retirement-scope
   comment). After 1 holds.
4. **Toggles (#365)**: a no-source instance (no record, no marker, nothing
   derivable) refuses by default and plans a create under a toggle;
   `aws_iam_access_key` is stored by default and refused under
   `strict { secrets }`.

A foundation item is a design pass until its issue names files and changes;
then it is a unit like any other and a worker lands it. "This is foundation
work" describes scope, never a reason to stop.

Evidence for the order, in one line each: 97 of 212 refusal kinds are the
static-evaluation stage; about 40% of the migrated gauntlet population is
re-derived from configuration every plan and five of six open failures sit
on that path; the schema reproduces 134 of 575 config-identified rows today;
696 of 1699 types can be held only by a record, and the corpus names twelve
of them people actually write. Re-derive these before quoting them; the
ruling document says how each was computed.

## What is enforced

Rules are tests. The ones that hold this document to the tree:

| Guard | What it holds |
|---|---|
| `internal/live/check`: `TestIdentityGolden`, `TestIdentityGoldenShapeIsPinned` | 1739 rendered identities across 629 configuration directories, pinned by value; if your change moves a line, explain it, and `-update` alone cannot silence it |
| `tools/gauntlet`: `TestRenderedDocsAreCurrent`, `TestManifestIsCanonical`, `TestArtifactAgreesWithManifest` | the spec, the site pages and the artifact are what the code says |
| `tools/gauntlet`: `TestLegacyScriptsOnlyGoDown` | crossing scripts move onto the protocol and never back |
| `live/derivation_guard_test.go`: `TestEveryTypeLiteralSurfaceIsRegistered`, `TestNoTypeNameIsAssembledFromLiterals` | every hand-wired provider type name carries a registered reason and count, and none is assembled at runtime to dodge the registry |
| `live/ci_coverage_test.go` | every fork-owned test package is in CI's glob |
| `live/brief_tracked_test.go` | the briefs, the skill, `scripts/pickup.sh` and `.claude/scripts/agent-progress.sh` are tracked, so a fresh clone has the procedure |
| `internal/live/harness` | every ratchet pins its denominator |
| `live/flociimage_test.go`, `live/pins_drift_test.go` | the emulator and provider pins are current |
| `internal/live/lifecycle/marker_tag_merge_live_test.go` | markers survive an incremental tag update through a real emulator |

## Working here

- Worktree off **local** `main` (`git worktree add ../wt/<name> -b
  live/<name> main`); `origin/main` goes stale. A unit's branch is the one
  `next` prints; commit early with the unit ID in the message.
- `env -u PWD` on every go command; read exit codes from a file; never
  `git stash`; never prune a worktree by whether its branch merged.
- `just ci` is the gate; a full-module `go test ./...` is a periodic
  checkpoint, not a reflex.
- Regenerate artifacts, never hand-edit them; a generator run twice and
  diffed proves determinism, not correctness.
- A brief is a lead, not a fact: re-verify against the code before fixing.
- `gh` defaults to upstream in this clone; pass `-R INTENTIUS/choudoufu`.
- Status and completion reports lead with `bash scripts/pickup.sh`'s board
  line (clear counts, stale-evidence count, queue depth), never with an
  issue count; an issue count may follow, it may not lead. Issue state and
  board state are allowed to disagree here by design (the queue is the
  tracker for gauntlet work), so a report built from the tracker alone can
  call itself finished while the board does not agree: read the board before
  writing the report, not the other way round.
- The primary checkout is guarded, not just documented: a `PreToolUse` hook
  in `~/.claude/settings.json` (user scope, deliberately not committed here
  — see #517) denies `Edit`/`Write`/`NotebookEdit` calls whose target
  resolves to the primary checkout's git toplevel, so a worker or subagent
  that lands there by mistake is hard-blocked rather than merely warned.
  `ask` was tried first and rejected: a live pipe-test showed spawned
  subagent sessions run under `permission_mode: "bypassPermissions"`, which
  silently auto-approves an `ask` decision with no prompt — `deny` is the
  only decision that actually holds under that mode. The maintainer's own
  direct edit still works: set `CHOUDOUFU_ALLOW_PRIMARY_EDIT=1` in the shell,
  or in a personal, uncommitted local settings file's `env` block, before
  editing.

## Retired

The old stock-comparison score and its three labels, the decision matrix and its `RULE` row,
admission as a gate, "no memory" as a goal, the offline corpus and
refusal-site counts as progress instruments (`live-check` stays as a user
tool), the wall taxonomy, the rulings list. Their history is in git before
this file's rewrite on 2026-08-21; the reasoning for retiring them is in the
tracker's design thread of the same date.

Retired 2026-08-23: "the engine is unmodified" as a rule (it stays a
measured cost: `rfc/20260814-projection-nativeness-audit.md`); the hand
exclusion of `aws_iam_access_key` outside the toggles; the config-language
subset as a permanent property of the mode rather than of the static
evaluator. Reasoning in `rfc/20260823-foundation-order-ruling.md`.
