---
name: upstream-sync
description: The routine sync of upstream OpenTofu into choudoufu, run by hand about every two weeks and at every upstream release tag. Use when doing an upstream sync, following upstream main, the two-weekly sync, picking up a new upstream tag, grafting upstream, or bumping the OpenTofu base. Covers the merge, the new runtime's gap table and toggles, the experimental lane, the repo guards a sync trips, and the PR.
---

# Syncing upstream OpenTofu

Epic #2256 decided that choudoufu follows upstream `main` continuously: a
sync every two weeks while upstream's runtime work is active, and one at
every upstream release tag. Each sync is one PR. The merge is the easy part.
The rest of this checklist is there because the new runtime (`internal/engine`)
and its toggles can break silently on a sync that merges clean.

Upstream (`opentofu/opentofu`) is read-only from here. Never push to it,
comment on it, react on it or link it. Upstream issue and PR numbers go in
code spans (`#4823`), in code, commits, PRs and this file alike, so GitHub
creates no cross-reference events on upstream's tracker (#2309).

## 1. Pick up, and a worktree

```
cd /Users/alex/Documents/checkouts/intentius/choudoufu
bash scripts/pickup.sh
git fetch -q origin
git fetch upstream main            # read-only
short=$(git rev-parse --short=10 upstream/main)
git worktree add -b sync/upstream-$short ../wt/sync-upstream-$short origin/main
cd ../wt/sync-upstream-$short
git rev-parse --show-toplevel      # must NOT be the primary checkout
```

For a release tag, use the tag in place of `upstream/main` (`git fetch
upstream tag v1.14.0`), and still name the branch `sync/upstream-<shortsha>`.

The primary checkout is for reading. A sync run there exercises the
unmodified tree and can report success about the wrong code (CLAUDE.md).

## 2. Sync

```
scripts/upstream-sync.sh --materialize main     # or a tag, or a sha
git status                                      # conflicts, as in a normal merge
# resolve, then: git add <paths>
scripts/upstream-sync.sh --finish
```

`--finish` commits the staged tree as a merge of HEAD and the raw graft, then
makes a second commit that re-pins `forkPointCommit`, `forkSurfaceForkPoint`,
`upstreamBaseCommit`, `changelogPastBases` and the README's base lines, and
regenerates `live/fork-surface.json`. `--abort` drops a materialized sync.
A ref that is already merged changes nothing and exits 0.

Rules the script enforces, and you must not work around:

- The merge is a real merge commit whose parents are HEAD and the raw graft.
  Never squash it, rebase it, or merge the PR any way but a merge commit.
- `version/VERSION` arrives only through the merge. Never hand-edit it.
- The graft chain is raw upstream trees (`graft: OpenTofu ...` subjects).
  Never commit anything onto a graft.

What it refuses with (stderr `upstream-sync: error E_...`):

| Error | Meaning, and what to do |
|---|---|
| `E_ON_MAIN` | on main or detached; switch to the sync branch |
| `E_DIRTY_WORKTREE` | commit or stash first |
| `E_NO_UPSTREAM_REMOTE` | add `upstream` as `https://github.com/opentofu/opentofu` |
| `E_BAD_REF`, `E_UPSTREAM_MISSING` | `git fetch upstream`, then retry |
| `E_NO_PREV_GRAFT`, `E_WRONG_PREV_GRAFT` | the previous graft is missing, not a graft, not on HEAD, not the latest, or its tree is not the upstream commit it claims. Don't pass an older `--prev-graft` to get past it |
| `E_REWRITE_INCOMPLETE` | a twin still carries `opentofu/opentofu` imports, `cmd/tofu`, `website/`, `rfc/` or `.github/workflows/`. A script bug: stop and report |
| `E_CONFLICTS` (exit 2) | the plain mode found conflicts; rerun with `--materialize` |
| `E_SYNC_IN_PROGRESS`, `E_NO_SYNC_IN_PROGRESS`, `E_HEAD_MOVED` | `--finish` or `--abort` the pending one; don't commit between materialize and finish |
| `E_UNRESOLVED`, `E_MARKERS_LEFT`, `E_UNSTAGED` | unmerged paths, leftover markers, or unstaged resolutions |
| `E_VERSION_EDITED` | your resolution changed `version/VERSION`; take upstream's |
| `E_REPIN_PATTERN`, `E_REGENERATE` | a pin moved or `tools/forkdiff-gen` failed; fix the pin file, not the check |

`scripts/upstream-sync-test.sh` is the script's own red-case suite. Run it
only when the script itself changed, and read its `PASS (x)` / `FAIL (x)`
lines.

## 3. Resolving conflicts

The fork's hooks in `internal/tofu` (conflicted here at U1, and will again):
`context.go`, `eval_context.go`, `eval_context_builtin.go`,
`eval_context_mock.go`, `node_resource_abstract_instance.go` (the
destroy-from-config hook in `planDestroy`), `update_state_hook.go`. Keep the
fork's hook calls and take upstream's surrounding change.

The fork's patches in `internal/engine`:

- `applying/operations_resource_managed.go`: U4's adjuster calls in
  `ManagedFinalPlan`, U5's applied-marker writer and verifier, and the
  create-only taint rule. Upstream taints on any apply error. The fork taints
  a failed create only, because a tainted object becomes a taint record
  (#353). Keep the fork's rule unless the maintainer rules otherwise.
- `planning/plan_managed.go`: the U2 deferral refusal
  (`newruntime.DeferralRefusal` in `planDesiredManagedResourceInstance`),
  the U4 adjuster calls, U6's identity resolver feeding the import path,
  and U8's destroy-from-config.
- `configadjust/` and `identityresolve/`: fork-only packages. They conflict
  only through the interfaces they call; see section 4.

`internal/command`'s live entry points: `plan.go` and `apply.go` keep the
#894 live-plan alias in `Run` and the `liveRefusesNewRuntime()` call. Most
other `internal/command` conflicts come from upstream reshaping CLI plumbing
(the `mitchellh/cli` removal at U1) against the fork's relabelled `Help()`
and `Run()`.

After resolving, before `--finish`:

```
go build ./...
go vet ./internal/tofu ./internal/engine/... ./internal/command
```

`internal/tofu/context_temp_runtime_adjust.go` carries compile-time drift
assertions between `internal/tofu`'s adjuster interfaces and
`internal/engine/configadjust`'s. If the build fails there, upstream
reshaped something the adjusters depend on: fix the adapter, never delete
the assertion. `go vet` also compiles the test files, which hold more.

Breaks `git merge-tree` cannot show, because they are in files only one side
touched. Look for each on every sync:

- A dependency upstream dropped from `go.mod` that fork-only files still
  import (U1: `mitchellh/cli` in `joint_cmd.go`, `joint_join_test.go`,
  `live_mv_test.go`). `go build ./... && go vet ./...` finds them.
- An `ExperimentalFeature*` flag upstream removed that a fork test passes to
  `SkipExperimental` (U1: `ExperimentalFeatureIgnoreChanges` in
  `resource_identity_test.go`).
- A method a fork type assertion calls that upstream removed or renamed. The
  assertion still compiles and silently takes the fallback branch (U1:
  `ReadDataSourceEncrypted` in `internal/live/dataread`). Grep the fork's
  `.(interface{` assertions against each method upstream deleted:
  `git diff <prev-graft> <new-graft> -- '*.go' | grep '^-func'`.
- Upstream moving test helpers. `internal/provider-simple` and
  `-v6` moved to `internal/command/e2etest/provider-simple*`; a fork test
  that `go build`s the old path fails at test time, not compile time (#2360).
- Upstream changing a lifecycle contract. `Context.Eval` now returns a `done`
  that shuts down the whole provider manager; the fork's root-output path
  deferred it and every later schema read failed (#2359). Read the diff of
  any upstream function the fork calls whose return values grew.

## 4. The new runtime and its toggles

Do this on every sync, even a clean one.

Gap table. `scripts/upstream-sync.sh --report` prints the state of every
upstream issue in `internal/live/newruntime/gaps.json`, read-only. That file
is the only copy of the table (#2256 links to it). For each row whose
upstream issue closed or merged:

- swap in upstream's code for the fork's interim (refusal or port), in this
  PR if small, otherwise in a follow-up unit issue under #2256;
- change the row's `check`, `interim` and `closed_by`, or drop it. The
  refusal checks are generated from the file, so the row is the switch.

Toggle semantics. Confirm these still hold after the merge:

| Variable | Gates | Builds |
|---|---|---|
| `TOFU_X_EXPERIMENTAL_RUNTIME` | plan and apply (`experimentalRuntimeEnabled` in `internal/tofu/context_temp_runtime.go`) | every build, release included (U2) |
| `TOFU_X_EXPERIMENTAL_RUNTIME_NEXT` | the command layer (`Meta.NewRuntimeEnabled` in `internal/command/temp_new_runtime.go`) | closed when `version.IsReleaseBuild()` |

Live mode runs under the first since U6, and is still refused under the
second (`liveRefusesNewRuntime` in `internal/command/live_new_runtime_guard.go`).
The warning comes only from `newEnginePlan` and `newEngineApply`, never from
`newEngineShim`; the post-plan check is `newRuntimePostPlan`.

```
git diff <prev-graft> <new-graft> -- internal/tofu/context_temp_runtime.go internal/command/temp_new_runtime.go internal/command/meta.go
git grep -n 'TOFU_X_EXPERIMENTAL' -- '*.go' ':!*_test.go'
```

If upstream renamed, split or defaulted a variable (as `#4823` did when it
split `_NEXT` out), follow upstream's name and semantics. Update
`newruntime.Variable`, the guard, the release gate and their tests in the
same PR, and say so in the PR description. Never add a choudoufu-specific
name (ruling 4).

Interfaces. Check upstream hasn't reshaped what the fork's engine patches
call: `planning.PlanOpts`, the planning glue, `ValidateFinalPlan`, the
import path (`#4726`, `#4727`), `PersistingState` (`#4767`), and per-object
metadata (`#4655`). `#4696` is the known risk for U8's destroy-from-config.

```
git diff <prev-graft> <new-graft> --stat -- internal/engine internal/lang/eval
```

## 5. Tests, tiered by CLAUDE.md

Read verdict lines, never exit codes.

1. Build and vet: `go build ./... && go vet ./...`
2. The touched packages' tests with the toggle off, e.g.
   `go test ./internal/tofu ./internal/engine/... ./internal/command ./internal/live/...`
3. The experimental lane:

   ```
   make test-experimental-ratchet     # last line: RATCHET: PASS ... or RATCHET: FAIL ...
   ```

   Then update `live/experimental-runtime/expected-failures.json`. It only
   shrinks: take off what now passes, and add only an unlisted failure that
   upstream caused, with a reason. Every entry needs a reason. Never put a
   marker, identity or record test on it; one failing is a bug to fix in
   this PR. Commit, then `scripts/upstream-sync.sh --report` prints the
   list's diff against main for the PR description.
4. The named guard tests, with the toggle off (each sets what it needs):

   ```
   go test ./internal/command -run 'TestLiveRefusesNewRuntime_|TestNewRuntimeEnabledClosedInReleaseBuild|TestLiveModeCreateCycleOnNewRuntime'
   go test ./internal/tofu -run 'TestContextLiveOnNewRuntime|TestExperimentalRuntimeToggleInReleaseBuild|TestNewRuntimeToggle_|TestNewRuntimeAdjuster|TestNewRuntimeApply_appliedMarker|TestNewRuntimeReplace|TestContextDestroyFromConfigNewRuntime|TestNewRuntimeResolver'
   go test ./internal/live/newruntime ./tools/expruntime-ratchet
   ```

   G is `TestLiveRefusesNewRuntime_*` and `TestContextLiveOnNewRuntime*`;
   the release gate is `TestExperimentalRuntimeToggleInReleaseBuild` and
   `TestNewRuntimeEnabledClosedInReleaseBuild`; U2 is `TestNewRuntimeToggle_*`;
   U4 to U8 are the rest. Use `-v` and check each ran (`--- PASS`), since a
   `-run` pattern that matches nothing also prints `ok`.
5. After the PR merges: the full gate on main, in the foreground.

   ```
   scripts/ci-gate.sh run
   scripts/ci-gate.sh check
   ```

## 6. Repo guards a sync trips

Each of these has fired on a past sync. Fix the list, not the guard.

- `live/forkdiff_test.go` `otherAllowlist`: upstream moved or deleted a path
  an entry names. `live/fork-surface.json` is regenerated by `--finish`;
  rerun `go run ./tools/forkdiff-gen && go run ./tools/forkdiff-gen -render`
  after any later conflict fix.
- `live/ci_coverage_test.go` `forkOwnedMixedRoots`, and the gofmt and
  `go test` package lists in both `.github/workflows/ci.yml` and `justfile`.
  `TestJustCIMirrorsTheWorkflow` fails when one is updated without the other
  (#2358 and #2361 were exactly that).
- `internal/live/check` `notAStage`, and `internal/live/marksafe`'s
  `guardedPackages` and `deferredPackages`, when a sync adds or moves a
  package they classify.
- `TestEveryToolHasAGitignoreEntry` (`live/toolbinaries_test.go`), when a
  new `tools/` directory arrives.
- `TestRenderedDocsAreCurrent` (`tools/gauntlet`): run
  `go run ./tools/gauntlet render` after the re-pin; the base version string
  shows up in rendered docs.

`render` is the only tool that may write `live/gauntlet.json`. Never
hand-edit or hand-merge it; on a conflict take main's copy and re-render.

## 7. The PR

Push the branch and open the PR against `main`. Merge it with a merge
commit, never squash or rebase, or the graft parent is lost. The description
carries:

- `Part of #2256`
- the upstream sha (and tag, if any) and the previous graft's sha
- the conflict counts the script printed: content, modify/delete,
  rename/delete, and which were fork hooks
- the merge-tree-invisible breaks found and how each was fixed
- gap-table rows changed or dropped, and toggle or interface changes
- the expected-failures diff from `--report`
- a `CHANGELOG.md` line under `(Unreleased)`: the new base, e.g.
  "Built on OpenTofu `1.14.0-dev` at `<sha>`"
- the light re-measure set (ruling 3)

Light re-measure. Pick a small set of estates for distinct product coverage,
not count, and name the surface each drives. Only the maintainer runs the
gauntlet: every run asks for "run" at the terminal. Give the maintainer two
lines, one per runtime:

```
cd <measure worktree> && gauntlet run -parallel 3 <names>
cd <measure worktree> && TOFU_X_EXPERIMENTAL_RUNTIME=1 gauntlet run -parallel 3 <names>
```

Then read `live/gauntlet/logs`. Under the toggle, the data-source preflight
refuses any estate with a `data` block, by design (gap row `data-sources`,
waiting on `#4058` and `#4740`). That refusal is the expected result for
such an estate, not a regression. Pick at least one estate without data
sources so the toggle-on run measures something.

## 8. Hard rules

- An agent never starts a gauntlet, estate, live-cert, AWS or other paid run.
  It builds, merges, and hands the maintainer the command.
- Upstream numbers only in code spans. Nothing is written upstream.
- Commit subjects never start with `#NNN` (git strips them on a rebase or
  amend). Check with `git log -1 --format=%s`.
- Grafts and syncs land as merge commits only.
- Never work in the primary checkout.
