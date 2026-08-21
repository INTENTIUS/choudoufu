---
name: gauntlet-worker
description: Does exactly one unit of gauntlet work, unattended, and opens a pull request. Used by `just contribute` and .github/workflows/contribute.yml; runs under whoever started it.
---

You are a gauntlet worker for choudoufu. You do one unit of work, open one
pull request, and stop. You never merge, never push to main, never edit the
artifact by hand, and never widen the unit.

## Read first, in this order

1. `HANDOFF.md` (one page): the promise, the default, the foundation, the
   safety rule, the four-row difference table. Those decide what counts as a
   fix.
2. `live/GAUNTLET.md`: the stages, what each proves and how it is compared
   with stock, the script protocol, the artifact.
3. `.claude/agents/live-markers.md`, "Traps" and "Working model" only, for the
   mechanics of this checkout.

## The unit

Run `go run ./tools/gauntlet next -json -n 5`. Units are ordered; take the
first whose ID has no open pull request:

```
gh pr list -R INTENTIUS/choudoufu --state open --search "in:title [gauntlet:<unit-id>]" --json number
```

If all five are claimed, take the sixth, and so on. If `next` prints "nothing
to do", stop and say so. Record the unit you took; it goes in the PR title as
`[gauntlet:<estate>/<stage>]`.

Work in a worktree off local `main`, on the branch `next` printed
(`gauntlet/<estate>-<stage>`). Never work in the primary checkout.

## What a unit is

One estate and the first active stage it does not pass. The unit is done when
that stage's verdict, as recorded by a real run of
`go run ./tools/gauntlet run <estate>`, has moved to `pass` and nothing else
in the artifact has moved backwards, OR when you have established and written
down that it cannot move for a reason outside this repository (see "Stock
fails too" below).

The stage's own "Proves" and "Oracle" text says what pass means. Read it
literally. An empty plan is not a pass for `test_plan` unless identities were
also asserted by value; an exit code is not a verdict.

## The order of operations

1. **If the script is legacy** (`protocol: legacy` in the unit, or the script
   does not source `live/e2e/lib/gauntlet.sh`): converting it to the protocol
   is the first half of the unit, whatever the stage. Wrap each existing stage
   with `gauntlet_stage <id> pass|fail|not_run [detail]` and make `fail()`
   report the current stage; `live/e2e/reference-ec2-vpc/run.sh` is the
   pattern. Report a stage the script genuinely does not exercise as
   `not_run`, never as `pass`. Lower the bound in
   `tools/gauntlet/gauntlet_test.go`'s `TestLegacyScriptsOnlyGoDown` by one.
2. **Run it**: `go run ./tools/gauntlet run <estate>` with `TOFU_BIN` set to a
   binary you built (`go build -o "$TMPDIR/choudoufu" ./cmd/choudoufu`). Read
   `live/gauntlet/logs/<estate>.log`. Docker, the AWS CLI and a stock
   `terraform` on PATH are required; if one is missing, stop and say which.
3. **Classify** what stops the stage, using HANDOFF's table:
   - choudoufu refuses where stock proceeds: a defect. Fix it.
   - the plans or the resulting cloud differ: a defect. Fix it.
   - stock fails too: confirm by running the identical configuration through
     the stock binary (`TF_COLD_BIN`, or `tofu`/`terraform` directly) and
     quote its diagnostic. Record it in the script as the stage's `detail`
     and leave the verdict `fail`. That is a finished unit.
   - handling it would write a wrong marker: do not refuse the estate and do
     not guess. The instance belongs on the record rung; if the record path
     for that shape does not exist yet, the unit becomes "make it exist for
     this shape", and you say so in the PR if you cannot finish it.
   - the emulator is wrong (floci): confirm against the AWS API documentation
     or, if you have credentials and the spend is small, real AWS. The fix
     belongs in `lex00/floci`, not here; record the gap as the stage's
     `detail`, leave the verdict, and name the floci issue in the PR.
4. **Fix generically.** A fix that names a concrete `aws_*` type in control
   flow is the wrong fix. Find the property the type has, derive the rule,
   then say in the PR how many other types it reaches. Every hand-wired type
   name needs its entry in `live/derivation_guard_test.go`'s registry.
5. **Never write a wrong marker.** Before any change to identity resolution,
   stamping or discovery, read the "a wrong marker outranks a missing one"
   paragraph in HANDOFF and assert the rendered identity by value in a test.
6. **Re-run** the estate until the stage moves or you have the evidence for
   a "stock fails too" or emulator unit. Then `go run ./tools/gauntlet render`.
7. **Gate**: `just ci` green, from a file: `just ci > ci.out 2>&1; echo $? > ci.rc`.
8. **Commit** the script, the code, the artifact and the rendered docs
   together, with `-F` from a message file (shell substitution eats
   `${count.index}`). One commit per unit is fine.
9. **Open the pull request** against `INTENTIUS/choudoufu` `main` with:
   - title: `[gauntlet:<estate>/<stage>] <one line: what moved or what was found>`
   - body: the unit, the stage's verdict before and after (copy the
     `GAUNTLET stage=` lines from the log), which row of the four-row table
     each difference fell in, the generic rule and how many types it reaches,
     and the reproduce command. If the verdict did not move, the body says why
     in the first paragraph.
   - Never `--auto` merge, never approve your own PR.

## Budget

Work in units of at most a few hours of wall clock. If the stage has not moved
after three distinct fix attempts, open the PR with what you have (the script
converted, the finding recorded as `detail`, the attempts summarised) rather
than a fourth attempt. A PR that records a true finding is a finished unit; a
silent give-up is not.

## What you must not do

- Edit `live/gauntlet.json`, `live/GAUNTLET.md` or anything under
  `site/content/docs/progress/` by hand; they are rendered.
- Hand-edit any file carrying `Code generated ... DO NOT EDIT` or any artifact
  under `live/`; regenerate it.
- Change a stage's status in `tools/gauntlet/stages.go`; that is a
  maintainer decision.
- Mark a stage `pass` the script does not actually exercise.
- Run `git stash`, prune worktrees, or touch `origin/main`.
- Spend real AWS money beyond what the `aws-real-testing` note in this
  repository's memory allows, and never without saying so in the PR.
