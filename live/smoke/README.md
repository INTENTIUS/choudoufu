# The smoke stack

Paste this to a coding agent (Claude Code or similar) and it will run the
whole thing for you:

```
Clone https://github.com/INTENTIUS/choudoufu, then do the following.

1. Confirm Docker is running (`docker info` must succeed) and the AWS CLI
   is installed (`aws --version`).
2. If Go is installed, skip this step. Otherwise pick the latest release
   tag from https://github.com/INTENTIUS/choudoufu/releases and
   export CHOUDOUFU_VERSION=<that tag> so the smoke runs a prebuilt binary.
3. From the repo root, run: just smoke import
4. Then run: just smoke greenfield
5. Report each step's verdict line as it prints, and each scenario's final
   PASS or FAIL line.

Exit code 0 means every claim held: an estate stood up by stock OpenTofu
survived losing its state file, and a brand-new estate carried its
ownership markers from the first create call. Non-zero names the step
that failed.
```

## What this is

One `docker compose` stack - the pinned floci emulator and the pinned
stock OpenTofu oracle - and one scenario per invocation, each tracing a
real user path with a verdict line per step. The harness is versioned
(`VERSION`, printed in every banner) so a report can name what measured it.

```
just smoke                # list scenarios
just smoke greenfield     # a new estate from nothing
just smoke import         # stock estate -> delete the state file -> adopt
just smoke full           # the comprehensive 15-step harness (~6 minutes)
```

## Scenarios

- **greenfield** - a live-block configuration, one plain apply: markers
  ride the create calls, the replan is empty, the state cache exists and
  is disposable, and `apply -destroy` removes exactly what was made.
- **import** - the migration path: the stock oracle (in its container)
  stands the estate up with a plain `terraform.tfstate`; the state file is
  deleted; the receipts are adopted with two CLI tag writes; the count
  pool takes its slot markers by the values the plan names; the estate
  plans empty from markers alone; one identity is asserted by value with
  the AWS CLI and no choudoufu in the loop.
- **full** - wraps `live/e2e/run.sh --expect 5`, the 15-step harness.

## Knobs

| Variable | Effect |
|---|---|
| `CHOUDOUFU_VERSION=v0.8.0` | run a pinned release binary instead of building from source |
| `CHOUDOUFU_BIN=/path` | run an explicit binary |
| `FLOCI_IMAGE=...` | override the pinned emulator image (default: `live/floci-image`) |
| `FLOCI_PORT=4650` | pin the emulator host port; unset, the kernel assigns a free one, so concurrent runs never collide |
| `OPENTOFU_IMAGE=...` | override the stock oracle (default: `live/oracle-versions.json`'s tofu) |
| `SMOKE_INSTRUMENT=1` | capture every request (choudoufu's own clients included, per #682) and print request/retry counts with a top-operations table |
| `BREAK=1` | corrupt one expected fact mid-scenario; the scenario passes only by CATCHING it - proof its assertions are load-bearing |

choudoufu builds from source by default and supports pinning; floci is
always the pinned image, never built here - that split is deliberate
(issue #713).

## Reading a run

Every step prints a `=== N. name ===` banner and an indented verdict
line. Trust the verdict lines, never the exit code alone; the exit code is
the summary, the lines are the evidence. A scenario that cannot fail is
not a check, which is what `BREAK=1` exists to disprove on demand.
