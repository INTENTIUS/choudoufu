# Working in this repository

`HANDOFF.md` is the playbook and `bash scripts/pickup.sh` is reality. This
file carries only the handful of rules whose violation has actually cost
real time, so that every agent sees them — including ones that never read
`.claude/agents/gauntlet-worker.md`.

## Nothing wakes a subagent

Task notifications go to the session that spawned an agent, never to the
agent itself. A subagent that backgrounds a command and ends its turn to
"wait for a notification" is not waiting, it is stopped, and it stays
stopped until a human notices. **Six workers died this way on 2026-08-29.**

Long commands block in the foreground. If you must poll, poll
**synchronously in one call**, and pick a condition that cannot match
itself — `while pgrep -f "just ci"` matches its own command line and loops
forever (that happened too). Waiting for `ci.rc` to *exist* is the same
mistake: `ci-gate.sh run` does delete it at start, but a wait launched
alongside the run matches the previous run's file before that delete
happens, and returns a stale green on its first iteration (#1307). Wait on
the gate's identity instead — this blocks until `ci.meta` names the current
HEAD from a run newer than the wait, then prints `check`'s verdict:

```
scripts/ci-gate.sh wait
```

## Never work in the primary checkout

`/Users/alex/Documents/checkouts/intentius/choudoufu` is for reading. All
work happens in a worktree. Run `git rev-parse --show-toplevel` before your
first edit and confirm it is not the primary checkout.

Five agents got this wrong on 2026-08-29, including the one writing the
documentation about it. It does not fail loudly: one run silently exercised
the **unmodified** script and reported `not_run` with **exit 0**, which
reads as a clean pass. If a stage reports `not_run` unexpectedly, suspect
this before suspecting the stage.

Recovery, proven four times: `git status` in the primary checkout,
`git diff > /tmp/x.patch`, `git checkout --` there, `git apply` in the real
worktree, verify the primary is clean, commit immediately.

## The gate is `scripts/ci-gate.sh`, not a bare `ci.rc`

`ci.rc` can read green from a run that never finished — a killed `just ci`
leaves an older run's file sitting there saying `0`. `ci-gate.sh run`
deletes the gate files first and stamps `ci.meta` with the tested sha;
`ci-gate.sh check` refuses a gate written for a different commit. That fix
caught two false greens within hours of landing. Commit before the batch
gate on main: a gate run against an uncommitted tree records the parent's sha.

## Testing is tiered by risk

GitHub CI green on the PR is the merge gate. There is no local
`scripts/ci-gate.sh` run per PR. Docs, data and refactor changes need the
build and the tests of the touched packages. A behaviour change needs one
proof of the behaviour, a test or one local scenario run, never both a local
run and a dispatched smoke for the same thing. The full gate (`ci-gate.sh
run`, then `check`) runs on main once per batch of merges. Red-first is for
a new guard, to show it can fail, not for every row of a table. The paid and
whole-set rule below is unchanged.

## Measured artifacts are never hand-merged

`live/gauntlet.json` holds per-estate rows AND derived aggregates in one
file. Git can auto-merge it with no conflict and still produce a file whose
headline number contradicts its own rows — that happened, reading `3/25`
while four rows read `clear: true`. Take main's artifact wholesale and
re-measure, or use `gauntlet merge-artifact`, which merges at row
granularity and refuses rather than guessing. A refusal means re-run.

Related: a rebase or squash orphans a row's `last_run.commit` even when git
reports no conflict, because the branch's commit graph is discarded. That
is why `automerge-artifact.yml` merges rather than squashes.

## A check that cannot fail is not a check

Prove a new guard red before trusting it green. Three checks written on
2026-08-29 printed "clean" on failure — a `$?` captured from `head` after a
pipe rather than from the command, a shell loop whose error path still
printed its success line, a `t.Skip` that would have left a guard
permanently green in CI on a shallow checkout.

The same rule applies to evidence: read verdict lines, never exit codes. A
printed summary is not proof a run measured anything. A check that fails
once and passes on re-run is a finding, not a flake.

## Heavy and paid runs are the maintainer's, by hand

There is no mechanical gate on a heavy or paid run any more (#1102,
2026-09-14): the allow file, `-confirm`, and the
`LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY` variable are gone. Spend is
bounded where it can be enforced, by the account's own AWS Budgets alarm,
and a dispatched `live-cert.yml` run waits for the maintainer's reviewer
click. A gate an agent can satisfy by exporting a variable constrains only
the maintainer, and a gate that is routinely routed around protects
nothing.

What stays is the rule, and it is about initiative, not ceremony. **An
agent never starts a paid run (`tools/gauntlet live-cert -target aws`, or
any `live/live-cert/*.sh` with `TARGET=aws`) or a whole-set run (`gauntlet
run -set core`, `-set all`, or a bare `gauntlet run` with no names) unless
the maintainer asked for that specific run in the current session.** Not
inferred from a goal, not carried over from an earlier approval, not
"needed to finish the unit".

## Only the maintainer runs the gauntlet

Since 2026-10-10 every `gauntlet run`, `gauntlet live-cert` and estate
`run.sh` asks the person at the terminal to type "run", times out after 60
seconds, and refuses with a DO NOT RETRY banner otherwise. Agents have no
terminal, so an agent never measures an estate, single named ones
included, and never fakes a terminal or file descriptor 3 to get past it.
Build what was asked for, merge it, and tell the maintainer it is ready to
test. The night before, one estate was driven to clear one kind cycle at a
time when the ask was to build many first and test after.

**Three real-AWS certification cycles and two corpus runs went out
overnight on 2026-09-11 on exactly that inferred authorization.** That is
the shape this rule exists to stop, and it is a rule about what an agent
decides, which no environment variable ever stopped.
