# stateless/e2e — the harness, agent-drivable

`stateless/e2e/run.sh` is both the E2E test for stateless mode and the
feature's live demo (`website/docs/language/stateless-mode.mdx`, "Watch It
Work"). This README covers running it in one command, reading its output —
human or machine — and what "the branch's claim" means as a single exit
code.

## Quickstart

Prerequisites:

- Docker, running (`docker info` must succeed) — the harness stands up
  Floci (`floci/floci:latest`), a local AWS emulator, on a free port.
- AWS CLI (`aws`) on `PATH` — used directly, alongside `choudoufu`, to make
  assertions no `choudoufu` command should be trusted to grade itself on (e.g.
  reading a tag straight off a live resource).
- Go, OR a prebuilt `choudoufu` binary via `TOFU_BIN` (see below) — without
  `TOFU_BIN`, the harness runs `go build ./cmd/choudoufu` from this checkout
  itself, so it always exercises the code actually on the branch.

The one command, from the repository root:

```
bash stateless/e2e/run.sh
```

Takes about 2 minutes on a warm Go build cache (longer on a cold one — the
harness builds `choudoufu` from this checkout unless `TOFU_BIN` is set). It builds
`choudoufu`, starts Floci, applies a real estate against it with plain local
state, deletes the state file in front of you, and then proves each claim
below live against the same running emulator — nothing here is mocked or
simulated.

## What each step proves

Steps whose command surface isn't wired up yet in this build report `NOT
IMPLEMENTED (phase N)` instead of failing — see "Flags" below for the
`--expect` grouping. As of this branch every step is green, including
drift-exact, removal-exact, receipt-cycle and receipt-cycle-existence (phase
5, P5.1's exactness work, gated additionally on `STATELESS_E2E_EXACTNESS` —
see "Env knobs").

| Step | Proves |
|---|---|
| `standup` | Stock `choudoufu init`/`apply` builds a real estate on the emulator with plain local state — the ordinary baseline before anything "stateless" happens. |
| `adopt` | Deleting `terraform.tfstate(.backup)` is the entire migration; nothing else about the estate or its config changes. |
| `tofu-init` | The state-less work copy resolves providers via `choudoufu` (registry.opentofu.org), never `terraform`. |
| `slot-migration` | The pre-slot `aws_eip.pool` members take their stable `tofu-slot` markers with no create and no destroy — the one-time write an apply would perform. |
| `receipt-adoption` | Both SSM-parameter receipts (`demo_effect`, the hash flavor; `demo_existence`, the existence flavor — RA.6) take their ownership markers by hand, with `aws ssm add-tags-to-resource` — adoption exactly as the docs describe it, a tag you write with your own cloud tools. The emulator drops the inline tags an SSM parameter is created with (floci-gaps #10), so without this both receipts are correctly unowned and every later step would be measuring against a standing diff instead of an owned resource. |
| `empty-plan-named` | `choudoufu live-plan -target=...` reports an empty plan for the client-named/attachment subset, rebuilt from markers alone — no state file read or written. |
| `empty-plan-full` | The same, full-estate, no `-target`: every declared instance materializes via discovery + binding except the one the emulator will not serve tags for, which is reported as an `[UNOWNED]` omission with an adoption hint rather than silently adopted. |
| `drift-exact` | One out-of-band mutation per estate type (VPC, subnet, security group, EIP, bucket, log group tag; log group retention as the non-tag case) surfaces as *exactly* that resource and that attribute — never broader neighbour noise — is corrected, and reconverges cleanly, all seven cases in one matrix. |
| `foreign-protected` | An unmarked security group created out of band is reported under a `Foreign resources` section and never proposed for deletion. |
| `removal-exact` | Deleting a whole resource block (`aws_security_group.main`) proposes *exactly* one destroy, named in an `Owned and undeclared` section alongside the live resource's ID; applying it (via the AWS CLI — no `live-apply` exists) removes exactly that resource and nothing else. |
| `count-scale-down` | `count` 3 → 2 on `aws_eip.pool` proposes exactly one destroy (the highest slot), zero churn on the survivors, and converges cleanly on re-plan. |
| `rename-no-churn` | `choudoufu live-mv <old> <new>` rewrites the live `tofu-address` tag in place; the following plan is empty. |
| `plain-plan-works` | A `live { estate = "..." }` block in `terraform{}` makes a plain `choudoufu plan`/`apply` stateless — no `live-`-prefixed subcommand anywhere; `-out` and `refresh` are rejected by name. |
| `receipt-cycle` | The estate's receipt (`aws_ssm_parameter.demo_effect`, the HASH flavor — stateless/RECEIPTS.md) exists with a 64-char hash after standup; breaking it out of band (an out-of-band value overwrite) re-arms the plan's "effect will fire" signal on exactly that resource's value; a corrective write converges it back to clean. |
| `receipt-cycle-existence` | The estate's other receipt (`aws_ssm_parameter.demo_existence`, the EXISTENCE flavor, RA.6 — stateless/RECEIPTS.md's default recommendation) exists with the constant value `"done"`; breaking it out of band THE EXISTENCE WAY — a genuine `aws ssm delete-parameter`, not an overwrite — re-arms the plan's "effect will fire" signal as exactly one create on that resource; recreating it (playing the Op) converges it back to clean. |
| `lint-rejects` | Every directory in `stateless/e2e/limits/` is refused by the shipped binary, naming exactly the lint rule that fixture exists for and no other. The two constructs no lint rule catches yet are asserted as unenforced, so the gap is visible in the output rather than hidden by omission. |

Every step that mutates the shared live estate ($MAIN) restores it before
finishing — `count-scale-down` reallocates the EIP it released,
`removal-exact` recreates the security group it destroyed, `rename-no-churn`
rewrites the live tag back to the original address — so every later step
still sees the full estate its own config declares.

## Env knobs

| Variable | Default | Meaning |
|---|---|---|
| `FLOCI_PORT` | `4601` | Emulator port. Change if the default is taken on your machine. |
| `FLOCI_NAME` | `tofu-stateless-e2e-$$` | Container name; the default already includes the PID for uniqueness across parallel runs. |
| `TOFU_BIN` | (unset) | Path to a prebuilt `choudoufu` binary. Skips the `go build` step. Must be executable. |
| `STATELESS_E2E_EXACTNESS` | `1` | Gates `drift-exact`/`removal-exact`/`receipt-cycle`/`receipt-cycle-existence` on top of their own `-estate`/`live-mv` probes. `1` (default since P5.2): all four run for real against P5.1's exactness work, which is merged and green. `0`: forces all four back to `NOT IMPLEMENTED`, for a pre-exactness bisect or to reproduce what `--expect 4` verified before P5.1/P5.2 landed. |
| `STATELESS_E2E_JSON` | `0` | Env-var spelling of `--json` (see below). `1` enables it. |

## Flags

- **`--json`** (or `STATELESS_E2E_JSON=1`) — emit exactly one JSON object as
  the final line written to stdout:

  ```
  {"steps":[{"name":"standup","status":"pass","phase":0}, ...],"overall":"pass"}
  ```

  `status` is one of `pass`, `not_implemented`, `fail`. `phase` is the
  `--expect` grouping below, not the roadmap task number embedded in a `NOT
  IMPLEMENTED (phase N)` human-readable line (those name which `P<N>.x` task
  unblocks a step — a different scale).

  `overall` is one of four values, independent of `--expect`:

  | `overall` | Meaning |
  |---|---|
  | `pass` | Every recorded step passed or legitimately reported `not_implemented`, and the harness reached its own end. |
  | `fail` | A claim was checked and found false — some step's `status` is `fail`. |
  | `error` | The harness did not finish and no step reported a failure: a signal, a `set -e` abort, a crash. |
  | `skipped` | The run ended in `SKIP` (missing Docker, `aws` or `go`). Nothing was verified. |

  The last two exist because they used to be reported as `pass`. Every
  `RC=$?` guard after a `choudoufu` invocation was unreachable under `set -euo
  pipefail` — the command substitution aborts the script first — so a
  genuinely broken run died with no diagnostic and this object still said
  the run had passed. `run_tf` in `run.sh` is what makes those guards live,
  and `error`/`skipped` are what the summary says when there is nothing to
  report a verdict on.

  **Design decision**: with `--json` on, all the normal human-readable
  progress output (`=== N. step ===`, per-step detail lines, `FAIL`/`PASS`)
  stays on stdout exactly as it does without the flag — nothing is
  redirected to stderr, and nothing about the run is quieter. The JSON
  object is appended as the truly last thing written to stdout, from an
  `EXIT` trap, so it comes out whether the run reaches its normal end or
  exits early via `fail()`. **Parse it by taking the last line of stdout** —
  do not try to parse the whole stream as one JSON document.

- **`--expect <phase>`** — exit `0` iff every recorded step at or below
  `<phase>` has status `pass` AND every step above `<phase>` reports
  `not_implemented`. A `fail` anywhere exits nonzero regardless of
  `--expect` (a step that already called `fail()` has already exited the
  script before `--expect` is ever evaluated). On a miss, `run.sh` names the
  blocking step(s), their phase, and their actual status on stderr.

  Phase assignment:

  | Phase | Steps |
  |---|---|
  | 0 | `standup`, `adopt`, `tofu-init`, `slot-migration`, `receipt-adoption` |
  | 1 | `empty-plan-named` |
  | 2 | `empty-plan-full`, `foreign-protected` |
  | 3 | `count-scale-down`, `rename-no-churn` |
  | 4 | `plain-plan-works` |
  | 5 | `drift-exact`, `removal-exact`, `receipt-cycle`, `receipt-cycle-existence`, `lint-rejects` |

## Exit-code meanings

| Exit code | Meaning |
|---|---|
| `0` | Every step ran and either passed or (legitimately) reported `NOT IMPLEMENTED`; or the run was cleanly `SKIP`ped with no `--expect` given; or `--expect <phase>` was given and its condition held. |
| `1` | A step's claim was checked and found false (`FAIL`), or the harness died before finishing, or `--expect <phase>` was given and did not hold. |
| `2` | An argument was invalid, or the run was `SKIP`ped while `--expect` was given. A skipped run verified nothing, so it must not report the expectation as met — distinct from both `0` (met) and `1` (checked and false). |

`SKIP` is reserved for missing tooling. A step that runs and finds its claim
false is always `FAIL`, never `SKIP` — the harness never lets a broken claim
through just because the environment happened to let it start.

## The reproduction contract

`run.sh --expect <phase>` is how this branch states its own claim, in a form
any agent or human can check without reading the script: run the command,
read the exit code. Today, that invocation is:

```
bash stateless/e2e/run.sh --expect 5
```

This passes: every step through phase 5 — including `drift-exact`,
`removal-exact`, `receipt-cycle` and `receipt-cycle-existence` — is `pass`,
and no step reports `not_implemented`.

```
bash stateless/e2e/run.sh --expect 4
```

This now FAILS, naming `drift-exact`, `removal-exact`, `receipt-cycle` and
`receipt-cycle-existence` as blockers — `--expect 4` requires every step
above phase 4 to be `not_implemented`, and all four are `pass` now that
P5.1's exactness work is merged and P5.2 wired the harness steps and flipped
`STATELESS_E2E_EXACTNESS`'s default to `1`. It stays meaningful as a
pre-exactness bisect: run it with `STATELESS_E2E_EXACTNESS=0` to reproduce
the phase-4 claim this branch made before P5.1/P5.2 landed.

Nobody should need to read `run.sh` to know whether this branch backs up
what it says it does — the exit code of `--expect <current phase>` is the
verdict.

## The harness is also the demo

`stateless/e2e/run.sh` is the "Watch It Work" callout on
`website/docs/language/stateless-mode.mdx` — the same command, described
there in user-facing terms rather than test-harness terms. If you're
changing what a step proves, check that page's claims still match this
README's table above.
