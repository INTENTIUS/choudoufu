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
forever (that happened too). `scripts/ci-gate.sh run` deletes `ci.rc` at
start and writes it only on completion, so this is a correct wait:

```
while [ ! -f ci.rc ]; do sleep 15; done; echo "ci.rc=$(cat ci.rc)"
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
caught two false greens within hours of landing. Commit before gating: a
gate run against an uncommitted tree records the parent's sha.

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

Prove every guard red before trusting it green. Three checks written on
2026-08-29 printed "clean" on failure — a `$?` captured from `head` after a
pipe rather than from the command, a shell loop whose error path still
printed its success line, a `t.Skip` that would have left a guard
permanently green in CI on a shallow checkout.

The same rule applies to evidence: read verdict lines, never exit codes. A
printed summary is not proof a run measured anything. A check that fails
once and passes on re-run is a finding, not a flake.
