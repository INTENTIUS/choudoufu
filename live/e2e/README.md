# The e2e harness

`live/e2e/run.sh` is both the end-to-end test for live resource
markers and the feature's live demo
(`website/docs/language/live-markers.mdx`, "Watch It Work"). This README
covers running it in one command, reading its output as a human or a
machine, and what the branch's claim means as a single exit code.

## Quickstart

You need four things.

- Docker, running (`docker info` must succeed). The harness stands up
  Floci (`floci/floci:latest`), a local AWS emulator, on a free port.
- The AWS CLI (`aws`) on `PATH`. It is used directly, alongside
  `choudoufu`, to make assertions no `choudoufu` command should be trusted
  to grade itself on, such as reading a tag straight off a live resource.
- Go, or a prebuilt `choudoufu` binary via `TOFU_BIN` (see below). Without
  `TOFU_BIN`, the harness runs `go build ./cmd/choudoufu` from this
  checkout itself, so it always exercises the code actually on the branch.
- `git` on `PATH`. `drift-observation` gives its phase-local estate copy
  its own throwaway git repository, the carrier the observational
  snapshot's `snapshots = true` branch mode needs.

The one command, from the repository root.

```
bash live/e2e/run.sh
```

It takes about 2 minutes on a warm Go build cache (longer on a cold one,
since the harness builds `choudoufu` from this checkout unless `TOFU_BIN`
is set). It builds `choudoufu`, starts Floci, applies a real estate against
it with plain local state, deletes the state file in front of you, and then
proves each claim below live against the same running emulator. Nothing
here is mocked or simulated.

## What each step proves

Steps whose command surface isn't wired up yet in this build report `NOT
IMPLEMENTED (phase N)` instead of failing. See "Flags" below for the
`--expect` grouping. As of this branch every step is green, including
drift-exact, removal-exact, receipt-cycle, receipt-cycle-existence and
drift-observation (phase 5, P5.1's exactness work, gated additionally on
`LIVE_E2E_EXACTNESS`, see "Env knobs").

| Step | Proves |
|---|---|
| `standup` | Stock `choudoufu init`/`apply` builds a real estate on the emulator with plain local state, the ordinary baseline before any marker work happens. |
| `adopt` | Deleting `terraform.tfstate(.backup)` is the entire migration. Nothing else about the estate or its config changes. |
| `tofu-init` | The state-less work copy resolves providers via `choudoufu` (registry.opentofu.org), never `terraform`. |
| `slot-migration` | The pre-slot `aws_eip.pool` members take their stable `tofu-slot` markers with no create and no destroy, the one-time write an apply would perform. |
| `receipt-adoption` | Both SSM-parameter receipts (`demo_effect` the hash flavor, `demo_existence` the existence flavor, RA.6) take their ownership markers by hand with `aws ssm add-tags-to-resource`, adoption exactly as the docs describe it, a tag you write with your own cloud tools. The emulator drops the inline tags an SSM parameter is created with (floci-gaps #10), so without this both receipts are correctly unowned and every later step would be measuring against a standing diff instead of an owned resource. |
| `empty-plan-named` | `choudoufu live-plan -target=...` reports an empty plan for the client-named/attachment subset, rebuilt from markers alone, with no state file read or written. |
| `empty-plan-full` | The same, full-estate, no `-target`. Every declared instance materializes via discovery plus binding except the one the emulator will not serve tags for, which is reported as an `[UNOWNED]` omission with an adoption hint rather than silently adopted, and gathered in the plan's rendered `Unowned` section as `[ADOPTABLE]` with the exact tag values to write. |
| `drift-exact` | One out-of-band mutation per estate type (VPC, subnet, security group, EIP, bucket, log group tag, and log group retention as the non-tag case) surfaces as *exactly* that resource and that attribute with no broader neighbour noise, is corrected, and reconverges cleanly, all seven cases in one matrix. |
| `foreign-protected` | An unmarked security group created out of band is reported under a `Foreign resources` section and never proposed for deletion. |
| `removal-exact` | Deleting a whole resource block (`aws_security_group.main`) proposes *exactly* one destroy, named in an `Owned and undeclared` section alongside the live resource's ID. Applying it (via the AWS CLI, since no `live-apply` exists) removes exactly that resource and nothing else. |
| `count-scale-down` | Taking `count` from 3 to 2 on `aws_eip.pool` proposes exactly one destroy (the highest slot), zero churn on the survivors, and converges cleanly on re-plan. |
| `rename-no-churn` | `choudoufu live-mv <old> <new>` rewrites the live `tofu-address` tag in place. The following plan is empty. |
| `plain-plan-works` | A `live { estate = "..." }` block in `terraform{}` puts a plain `choudoufu plan`/`apply` on markers, with no `live-`-prefixed subcommand anywhere. `-out` and `refresh` are rejected by name. |
| `receipt-cycle` | The estate's receipt (`aws_ssm_parameter.demo_effect`, the HASH flavor, live/RECEIPTS.md) exists with a 64-char hash after standup. Breaking it out of band (a value overwrite) re-arms the plan's "effect will fire" signal on exactly that resource's value. A corrective write converges it back to clean. |
| `receipt-cycle-existence` | The estate's other receipt (`aws_ssm_parameter.demo_existence`, the EXISTENCE flavor, RA.6, live/RECEIPTS.md's default recommendation) exists with the constant value `"done"`. Breaking it out of band THE EXISTENCE WAY, a genuine `aws ssm delete-parameter` rather than an overwrite, re-arms the plan's "effect will fire" signal as exactly one create on that resource. Recreating it (playing the Op) converges it back to clean. |
| `drift-observation` | A phase-local copy of the standing estate, given its own `live { estate = "stateless-e2e", snapshots = true }` block, adopts the same live resources with no changes at all (no second standup) and enables the observational snapshot's git-branch carrier. Three drifts injected out of band (a log group's retention, a plain tag beside the markers on the VPC, a whole CloudWatch alarm deleted) render on the next plain `choudoufu plan` as exactly two in-place updates and one create, nothing else. A targeted apply (`-target` does not shrink discovery) commits an observational snapshot *while* the estate is still drifted, then a full apply reconverges everything and commits a third snapshot. `git log`/`git diff` on `refs/heads/tofu-snapshots/stateless-e2e` show the drift arriving and departing: the alarm's entry leaving and returning, each changed resource's `attributesHash` changing and reverting. |
| `lint-rejects` | Every directory in `live/e2e/limits/` is refused by the shipped binary, naming exactly the lint rule that fixture exists for and no other. The one construct no lint rule catches yet (duplicate-identity, refused at identity resolution instead) is asserted as unenforced, so the gap is visible in the output rather than hidden by omission. |

Every step that mutates the shared live estate ($MAIN) restores it before
finishing. `count-scale-down` reallocates the EIP it released,
`removal-exact` recreates the security group it destroyed,
`rename-no-churn` rewrites the live tag back to the original address, and
`drift-observation` applies its own phase-local copy of the config to
reconverge the log group, VPC and alarm it drifted, so every later step
still sees the full estate its own config declares.

## Env knobs

| Variable | Default | Meaning |
|---|---|---|
| `FLOCI_PORT` | `4601` | Emulator port. Change if the default is taken on your machine. |
| `FLOCI_NAME` | `tofu-stateless-e2e-$$` | Container name. The default already includes the PID for uniqueness across parallel runs. |
| `TOFU_BIN` | (unset) | Path to a prebuilt `choudoufu` binary. Skips the `go build` step. Must be executable. |
| `LIVE_E2E_EXACTNESS` | `1` | Gates `drift-exact`/`removal-exact`/`receipt-cycle`/`receipt-cycle-existence` on top of their own `-estate`/`live-mv` probes. With the default `1` (since P5.2) all four run for real against P5.1's exactness work, which is merged and green. With `0` all four are forced back to `NOT IMPLEMENTED`, for a pre-exactness bisect or to reproduce what `--expect 4` verified before P5.1/P5.2 landed. |
| `LIVE_E2E_JSON` | `0` | Env-var spelling of `--json` (see below). `1` enables it. |

## Flags

- **`--json`** (or `LIVE_E2E_JSON=1`) emits exactly one JSON object
  as the final line written to stdout.

  ```
  {"steps":[{"name":"standup","status":"pass","phase":0}, ...],"overall":"pass"}
  ```

  `status` is one of `pass`, `not_implemented`, `fail`. `phase` is the
  `--expect` grouping below, not the roadmap task number embedded in a `NOT
  IMPLEMENTED (phase N)` human-readable line (those name which `P<N>.x`
  task unblocks a step, a different scale).

  `overall` is one of four values, independent of `--expect`.

  | `overall` | Meaning |
  |---|---|
  | `pass` | Every recorded step passed or legitimately reported `not_implemented`, and the harness reached its own end. |
  | `fail` | A claim was checked and found false. Some step's `status` is `fail`. |
  | `error` | The harness did not finish and no step reported a failure. A signal, a `set -e` abort, a crash. |
  | `skipped` | The run ended in `SKIP` (missing Docker, `aws` or `go`). Nothing was verified. |

  The last two exist because they used to be reported as `pass`. Every
  `RC=$?` guard after a `choudoufu` invocation was unreachable under `set
  -euo pipefail`, since the command substitution aborts the script first,
  so a genuinely broken run died with no diagnostic and this object still
  said the run had passed. `run_tf` in `run.sh` is what makes those guards
  live, and `error`/`skipped` are what the summary says when there is
  nothing to report a verdict on.

  A design decision worth knowing. With `--json` on, all the normal
  human-readable progress output (`=== N. step ===`, per-step detail
  lines, `FAIL`/`PASS`) stays on stdout exactly as it does without the
  flag. Nothing is redirected to stderr, and nothing about the run is
  quieter. The JSON object is appended as the truly last thing written to
  stdout, from an `EXIT` trap, so it comes out whether the run reaches its
  normal end or exits early via `fail()`. **Parse it by taking the last
  line of stdout.** Do not try to parse the whole stream as one JSON
  document.

- **`--expect <phase>`** exits `0` iff every recorded step at or below
  `<phase>` has status `pass` AND every step above `<phase>` reports
  `not_implemented`. A `fail` anywhere exits nonzero regardless of
  `--expect` (a step that already called `fail()` has already exited the
  script before `--expect` is ever evaluated). On a miss, `run.sh` names
  the blocking step(s), their phase, and their actual status on stderr.

  Phase assignment.

  | Phase | Steps |
  |---|---|
  | 0 | `standup`, `adopt`, `tofu-init`, `slot-migration`, `receipt-adoption` |
  | 1 | `empty-plan-named` |
  | 2 | `empty-plan-full`, `foreign-protected` |
  | 3 | `count-scale-down`, `rename-no-churn` |
  | 4 | `plain-plan-works` |
  | 5 | `drift-exact`, `removal-exact`, `receipt-cycle`, `receipt-cycle-existence`, `drift-observation`, `lint-rejects` |

## Exit-code meanings

| Exit code | Meaning |
|---|---|
| `0` | Every step ran and either passed or legitimately reported `NOT IMPLEMENTED`, or the run was cleanly `SKIP`ped with no `--expect` given, or `--expect <phase>` was given and its condition held. |
| `1` | A step's claim was checked and found false (`FAIL`), or the harness died before finishing, or `--expect <phase>` was given and did not hold. |
| `2` | An argument was invalid, or the run was `SKIP`ped while `--expect` was given. A skipped run verified nothing, so it must not report the expectation as met, distinct from both `0` (met) and `1` (checked and false). |

`SKIP` is reserved for missing tooling. A step that runs and finds its
claim false is always `FAIL`, never `SKIP`. The harness never lets a
broken claim through just because the environment happened to let it
start.

## The reproduction contract

`run.sh --expect <phase>` is how this branch states its own claim, in a
form any agent or human can check without reading the script. Run the
command, read the exit code. Today, that invocation is the following.

```
bash live/e2e/run.sh --expect 5
```

This passes. Every step through phase 5, including `drift-exact`,
`removal-exact`, `receipt-cycle`, `receipt-cycle-existence` and
`drift-observation`, is `pass`, and no step reports `not_implemented`.

```
bash live/e2e/run.sh --expect 4
```

This now FAILS, naming `drift-exact`, `removal-exact`, `receipt-cycle`,
`receipt-cycle-existence` and `drift-observation` as blockers. `--expect 4`
requires every step above phase 4 to be `not_implemented`, and all five are
`pass` now that P5.1's exactness work is merged and P5.2 wired the harness
steps and flipped `LIVE_E2E_EXACTNESS`'s default to `1`. It stays
meaningful as a pre-exactness bisect. Run it with `LIVE_E2E_EXACTNESS=0` to
reproduce the phase-4 claim this branch made before P5.1/P5.2 landed.

Nobody should need to read `run.sh` to know whether this branch backs up
what it says it does. The exit code of `--expect <current phase>` is the
verdict.

## The harness is also the demo

`live/e2e/run.sh` is the "Watch It Work" callout on
`website/docs/language/live-markers.mdx`, the same command described
there in user-facing terms rather than test-harness terms. If you're
changing what a step proves, check that page's claims still match this
README's table above.
