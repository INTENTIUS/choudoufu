# choudoufu

Working in the INTENTIUS workspace, `../agents/shared.md` applies too.

An OpenTofu fork that runs existing configurations against live cloud
resources, with cloud tags as ownership markers and a per-instance record
for what the cloud cannot hold. `HANDOFF.md` has the promise, the safety
rule and the five-row difference table; read it before touching identity,
stamping or discovery. `bash scripts/pickup.sh` prints the work's state.

## Where to work

The primary checkout is read-only (a user-scope hook denies edits there).
`pickup.sh` parses branch names: `gauntlet/<estate>-<stage>` for a gauntlet
unit, `live/<topic>` for anything else.

`upstream` is opentofu/opentofu. Its LLM-contribution ban applies there, not
to this fork. If `gh repo set-default --view` is not INTENTIUS/choudoufu,
pass `-R INTENTIUS/choudoufu` to every `gh` command, or it silently reads
upstream.

There is no merge queue on `main`. Open the pull request and stop; the
maintainer merges.

## Runs only the maintainer starts

- `gauntlet run` (`just gauntlet`, `just gauntlet-run`), `gauntlet live-cert`
  and estate `run.sh` scripts prompt for "run" on a terminal and refuse
  otherwise. Do not retry, fake a tty, or call `run.sh` directly. Build, then
  say it is ready to test.
- No real-AWS spend (`-target aws`, `TARGET=aws`) or whole-set run unless the
  maintainer asked for that exact run in this session.
- No `just ci`, `scripts/ci-gate.sh` or `just corpus` for a change. Here
  `just ci` is the full gate, not a workflow render; `.github/workflows/` is
  hand-written YAML.

## Commands

- Prefix go commands with `env -u PWD`; a symlinked path gives false failures
  in `local-exec` and `TestFmt*`. `go build ./tools/<name>` needs `-o`, or
  the binary lands in the repo root.
- The local gate: `gofmt -l` on touched Go files (CI checks fmt, tests do
  not), `env -u PWD go build ./...`, and the touched packages' tests.
  Tree-wide guards can fail on files you never opened; run the ones your
  change can reach: `./internal/live/check/`, `./tools/gauntlet/`, `./live/`,
  `./internal/live/marksafe/`.
- Never pipe a generator into `head` (SIGPIPE kills it before it writes).
  Send output to a file and read the file; read exit codes the same way.
- Refusal counts: `go run ./tools/refusal-probe -schemas -out before.json`,
  change, `-out after.json`, then `-diff before.json,after.json`. Read
  `.claude/skills/measuring-choudoufu/SKILL.md` before quoting any number.
- `go test ./internal/live/check/ -run TestIdentityGolden` pins rendered
  identities. Explain every line your change moves; never `-update` to
  quiet it.

## Generated files

Artifacts under `live/` are generated. `just gauntlet-render` regenerates
`live/gauntlet.json`, `live/GAUNTLET.md` and the board pages.
`pkg/registry/registry.json` and `mapping.json` embed the `live/` copies;
re-copy after regenerating. Run `just merge-drivers` once per clone. Never
hand-merge `live/gauntlet.json`; use `gauntlet merge-artifact` or take main's.

## Product rules

- Never write a wrong marker. A plan that creates something that already
  exists is that failure. Drop to the record rung instead of refusing the
  estate, and ask before adding any new refusal.
- Fix generically: a concrete `aws_*` type in control flow is the wrong fix.
  Hand-wired type names need an entry in `live/derivation_guard_test.go`.
- Stage status in `tools/gauntlet/stages.go` and the principles in
  `HANDOFF.md` are the maintainer's to change.
