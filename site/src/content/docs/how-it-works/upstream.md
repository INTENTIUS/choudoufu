---
title: Upstream OpenTofu
description: The OpenTofu commit choudoufu is built on, the new-runtime opt-ins, what the fork patches in the new runtime, and what the toggle refuses.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/upstream/ and list the
  constructs in this repository's configuration that the new-runtime toggle
  refuses, with the gap-table row for each. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

choudoufu is upstream OpenTofu plus the live backend. This page lists where the
two differ. The base line and the gap tables are generated from the repository
by `go run ./tools/upstreamdoc-gen`, and a test fails when the page and its
sources disagree.

## The base

<!-- upstreamdoc-gen:begin base -->
The base is upstream OpenTofu commit [`aa3eb66a0c`](https://github.com/opentofu/opentofu/commit/aa3eb66a0c1c792b7751e6ffeef7ca0cd8693f66), whose `version/VERSION` reads `1.14.0-dev`. `choudoufu version` prints that version beside the fork's own tag.
<!-- upstreamdoc-gen:end base -->

choudoufu follows upstream `main` between upstream releases, so a release cut
between two upstream tags is built on a `-dev` base. Upstream is merged in every
two weeks while its new-runtime work continues, and at each upstream release
tag, one pull request each time, merged with a merge commit.

[`scripts/upstream-sync.sh`](https://github.com/INTENTIUS/choudoufu/blob/main/scripts/upstream-sync.sh)
does the merge. Upstream's tree is committed unmodified as a graft, and a copy
rewritten to choudoufu's module path is merged. The script then re-pins the base
commit across the repository and regenerates
[`live/fork-surface.json`](https://github.com/INTENTIUS/choudoufu/blob/main/live/fork-surface.json),
the list of every file the fork adds, changes or deletes against that commit.
`scripts/upstream-sync.sh --report` prints the state of every upstream issue
the gap table names. The checklist is
[`.claude/skills/upstream-sync/SKILL.md`](https://github.com/INTENTIUS/choudoufu/blob/main/.claude/skills/upstream-sync/SKILL.md).

## The opt-ins

Upstream's new runtime (`internal/engine`) sits beside the default runtime in
upstream `main`. Upstream split its opt-in in two in
[#4823](https://github.com/opentofu/opentofu/pull/4823). The tracker for the
rewrite is [#3414](https://github.com/opentofu/opentofu/issues/3414).

| Variable | Gates | Upstream | choudoufu |
|---|---|---|---|
| `TOFU_X_EXPERIMENTAL_RUNTIME` | plan and apply | honoured in dev and nightly builds only, decided from the version string | honoured in every build, release binaries included. Each run on it prints a warning, and the gap table's refusals apply. Live mode runs on it |
| `TOFU_X_EXPERIMENTAL_RUNTIME_NEXT` | the command layer: command-level evaluation, module installation, validation | honoured in dev and nightly builds only, decided from the version string | closed in release builds, decided from the release linker flag (`version.dev=no`), since a release built on upstream `main` reports a `-dev` version. Live mode refuses to run under it |
| `CHOUDOUFU_NEW_RUNTIME_NO_FORK_PORTS` | the fork's ports of features upstream's new runtime lacks | none, fork-only | any non-empty value turns every fork port off, and the gap table refuses what the port covered. It arrives with [#2372](https://github.com/INTENTIUS/choudoufu/pull/2372), the in-run estates port |

The code is `experimentalRuntimeEnabled` in
[`internal/tofu/context_temp_runtime.go`](https://github.com/INTENTIUS/choudoufu/blob/main/internal/tofu/context_temp_runtime.go)
and `Meta.NewRuntimeEnabled` in
[`internal/command/temp_new_runtime.go`](https://github.com/INTENTIUS/choudoufu/blob/main/internal/command/temp_new_runtime.go).
Without either variable, plan and apply run on the default runtime, unchanged.
The default changes only after upstream calls the new runtime stable.

A plan saved on the default runtime does not apply with the toggle on, because
upstream's apply engine applies only plans its own planner made. Re-plan with
the toggle on.

## What the fork patches in the new runtime

The new runtime is upstream's code apart from what the live backend's
per-resource ownership and records need. Each patch carries a `choudoufu`
comment at its call site.

| Patch | Where | Replaced by |
|---|---|---|
| Markers at plan time: the configuration adjusters write the estate's marker into planned values | `planning/plan_managed.go`, `configadjust/` ([#2340](https://github.com/INTENTIUS/choudoufu/issues/2340)) | fork-only, no upstream counterpart |
| Markers and records at apply time: the apply engine re-plans each object with the adjusters, writes the marker after a create, and verifies it | `applying/operations_resource_managed.go`, `applying/fork_apply_markers.go` ([#2341](https://github.com/INTENTIUS/choudoufu/issues/2341)) | fork-only, no upstream counterpart |
| Identity resolution: the resolver's answer for an object becomes an import, through upstream's import path ([#4726](https://github.com/opentofu/opentofu/pull/4726), [#4727](https://github.com/opentofu/opentofu/pull/4727)) | `planning/plan_managed_identity.go`, `identityresolve/` ([#2342](https://github.com/INTENTIUS/choudoufu/issues/2342)) | fork-only resolver on upstream's import path |
| The create half of a replace carries markers, in both orderings | no separate engine change: the create half is planned through the create entry point, which carries the marker patches ([#2343](https://github.com/INTENTIUS/choudoufu/issues/2343)) | fork-only, no upstream counterpart |
| Destroy-from-config: configured destroy-time arguments, such as `skip_final_snapshot`, reach the delete for six AWS resource types | `planning/destroy_from_config.go`, `applying/destroy_from_config.go` ([#2344](https://github.com/INTENTIUS/choudoufu/issues/2344)) | [#3472](https://github.com/opentofu/opentofu/issues/3472) |
| Taint on a failed create only. Upstream taints on any apply error; a tainted live object becomes a taint record, so a failed update stays untainted, as on the default runtime | `applying/operations_resource_managed.go` | fork rule, kept on each sync |
| Deferred actions not caused by `-target` or `-exclude` raise an error inside planning | `planning/plan_managed.go` | [#4697](https://github.com/opentofu/opentofu/issues/4697) |
| In-run estates: `terraform_estate_outputs` reads another estate's outputs, including one applied in the same run. One package, `internal/engine/forkport/inrunestates`, switched off by `CHOUDOUFU_NEW_RUNTIME_NO_FORK_PORTS`. Open in [#2372](https://github.com/INTENTIUS/choudoufu/pull/2372) | `forkport/inrunestates`, plus call sites marked for removal | [#4058](https://github.com/opentofu/opentofu/issues/4058), when the package, its call sites and its gap row are removed |

Paths are under `internal/engine/`. When an upstream issue in this list
closes, a sync replaces the fork's patch with upstream's code and drops the gap
row.

## What the toggle refuses or accepts

Generated from
[`internal/live/newruntime/gaps.json`](https://github.com/INTENTIUS/choudoufu/blob/main/internal/live/newruntime/gaps.json),
the only copy of the gap table. The refusal checks and the warning read the
same rows. Numbers link to opentofu/opentofu when the row lists them as
upstream, and to choudoufu otherwise.

<!-- upstreamdoc-gen:begin gaps -->
### Refused

A run that reaches one of these stops with `Not supported on the new runtime yet`, naming the row and what it waits for.

| Row | Gap | Where it stops | What it refuses | Waits for |
|---|---|---|---|---|
| `live` | any live mode on the new runtime | in live mode | refused where NewRuntimeEnabled() is checked (TOFU_X_EXPERIMENTAL_RUNTIME_NEXT, [#2339](https://github.com/INTENTIUS/choudoufu/issues/2339)); the check where the runtime is chosen (any ConfigValueAdjuster set) was lifted by U6 ([#2342](https://github.com/INTENTIUS/choudoufu/issues/2342)), so live mode runs on the new runtime under TOFU_X_EXPERIMENTAL_RUNTIME | U6 ([#2342](https://github.com/INTENTIUS/choudoufu/issues/2342)) lifted the first check; the second stays until upstream calls _NEXT stable |
| `replace-cbd-cycle` | cycle on a replace with newly enabled create_before_destroy | on the plan, before it is shown or saved, outside live mode only | refuse replace actions outside live mode, so a plan cannot half-apply. Live mode does not refuse a replace since U7 ([#2343](https://github.com/INTENTIUS/choudoufu/issues/2343)) | upstream [#4714](https://github.com/opentofu/opentofu/issues/4714) |
| `data-sources` | data sources, and in-run estates | before planning, on the configuration or state | refuse data blocks | upstream [#4058](https://github.com/opentofu/opentofu/issues/4058), [#4740](https://github.com/opentofu/opentofu/issues/4740) |
| `removed` | removed blocks | before planning, on the configuration or state | refuse removed blocks | upstream [#4686](https://github.com/opentofu/opentofu/issues/4686), [#4693](https://github.com/opentofu/opentofu/issues/4693) |
| `moved` | moved blocks (not yet treated as active) | before planning, on the configuration or state | refuse moved blocks | upstream [#4693](https://github.com/opentofu/opentofu/issues/4693) |
| `variable-validation` | variable validation conditions | before planning, on the configuration or state | refuse validation blocks on variables | upstream, not yet filed separately |
| `deposed` | upgrade of orphaned or deposed objects | before planning, on the configuration or state | refuse when state holds deposed objects | upstream, not yet filed separately |
| `lifecycle-destroy` | lifecycle { destroy } | before planning, on the configuration or state | refuse lifecycle destroy arguments | upstream [#4682](https://github.com/opentofu/opentofu/issues/4682) |
| `checks-conditions` | checks and conditions | before planning, on the configuration or state | refuse check blocks and precondition and postcondition blocks | upstream [#4390](https://github.com/opentofu/opentofu/issues/4390) |
| `sensitive` | sensitive values (mark handling) | before planning, on the configuration or state | refuse sensitive = true variables and outputs and the sensitive() function | upstream [#4360](https://github.com/opentofu/opentofu/issues/4360) |
| `deferral` | deferred actions | inside planning | error on any deferral not caused by -target or -exclude; plans have no record of deferral, so a post-plan check cannot see one | upstream [#4697](https://github.com/opentofu/opentofu/issues/4697) |
| `test-command` | tofu test | before planning, on the configuration or state | refuse the test command (its harness still evaluates on the default runtime) | upstream [#4741](https://github.com/opentofu/opentofu/issues/4741) |

### Accepted, and named in the warning

Every run on the new runtime prints `Running on OpenTofu's experimental new runtime` with these lines.

| Row | Warning line | Waits for |
|---|---|---|
| `cancel` | Ctrl-C and stop may not stop an apply cleanly | upstream, not yet filed separately |
| `apply-errors` | state after a failed or partial apply may differ from the default runtime's | upstream, not yet filed separately |
| `uncategorised` | behaviours upstream's own tests still skip without a category (TestContext2Apply_inconsistentWithPlan, TestContext2Apply_multiProviderDestroy and others) | upstream |
| `deprecation-warnings` | deprecation warnings on variables and outputs are not shown | upstream [#4688](https://github.com/opentofu/opentofu/issues/4688) |
| `lint-rules` | built-in lint rules do not run | upstream [#4746](https://github.com/opentofu/opentofu/issues/4746) |
| `tracing-spans` | OpenTelemetry traces do not carry the default runtime's per-resource-instance planning spans | upstream's own tracing in internal/engine |
| `sensitive` | attributes a provider schema marks sensitive may not be handled as sensitive | upstream [#4360](https://github.com/opentofu/opentofu/issues/4360) |

### Runs, with no refusal

| Row | Gap | How | Closed by |
|---|---|---|---|
| `markers-plan` | markers at plan time | none needed: ported by U4 ([#2340](https://github.com/INTENTIUS/choudoufu/issues/2340)) | U4 ([#2340](https://github.com/INTENTIUS/choudoufu/issues/2340)) |
| `markers-apply` | markers and records at apply time | none needed: ported by U5 ([#2341](https://github.com/INTENTIUS/choudoufu/issues/2341)) | U5 ([#2341](https://github.com/INTENTIUS/choudoufu/issues/2341)) |
| `identity` | identity resolution | none needed: ported by U6 ([#2342](https://github.com/INTENTIUS/choudoufu/issues/2342)); the resolver's target becomes an import statement in the planning engine | U6 ([#2342](https://github.com/INTENTIUS/choudoufu/issues/2342)) |
| `replace-live` | markers on the create half of a replace | none needed: U7 ([#2343](https://github.com/INTENTIUS/choudoufu/issues/2343)) ported it; the create half of a replace is planned and final-planned through the create entry point in both orderings, and live mode no longer refuses a replace | U7 ([#2343](https://github.com/INTENTIUS/choudoufu/issues/2343)) |
| `destroy-override` | destroy-from-config overrides ([#2309](https://github.com/INTENTIUS/choudoufu/issues/2309)) | ported by U8 ([#2344](https://github.com/INTENTIUS/choudoufu/issues/2344)): the new runtime's planning engine writes the configured destroy-time arguments into the delete's prior state, and its apply engine sends them from the plan | U8 ([#2344](https://github.com/INTENTIUS/choudoufu/issues/2344)), then upstream; upstream [#3472](https://github.com/opentofu/opentofu/issues/3472) |
| `input-prompts` | interactive input prompts | a missing input fails loudly | upstream [#4695](https://github.com/opentofu/opentofu/issues/4695) |
| `taint-on-create` | taint on create failure | none needed | upstream [#4768](https://github.com/opentofu/opentofu/issues/4768); see [#2336](https://github.com/INTENTIUS/choudoufu/issues/2336) for the taint decision; upstream [#4691](https://github.com/opentofu/opentofu/issues/4691), [#4687](https://github.com/opentofu/opentofu/issues/4687) |
| `imports` | imports, config-driven and plan-option | none needed | upstream [#4726](https://github.com/opentofu/opentofu/issues/4726), [#4727](https://github.com/opentofu/opentofu/issues/4727), [#4745](https://github.com/opentofu/opentofu/issues/4745), [#4683](https://github.com/opentofu/opentofu/issues/4683) |
| `provisioner-self-marks` | provisioner self marks | none needed: ours, already carried | ours |
| `saved-plan-cross-runtime` | a plan saved on the default runtime, applied with the toggle on | apply refuses loudly with "Saved plan contains no execution graph": upstream's apply engine applies only plans its own planner made. Re-plan with the toggle on. | upstream |
<!-- upstreamdoc-gen:end gaps -->

## What differs on the default runtime

These hold on either runtime, and are described on their own pages.

- The live backend, `estate.chdf.hcl`, in place of a state backend ([What it is](../)).
- A marker on each taggable resource names its estate ([Identity](../identity/)).
- One record per managed instance in place of a state file ([Records](../values/)), written with a conditional write per resource ([Resource-level concurrency](../locking/)).
- An ordinary state file kept as a local cache ([The cache](../cache/)).

## How to check it yourself

- The experimental lane,
  [`.github/workflows/experimental-runtime.yml`](https://github.com/INTENTIUS/choudoufu/blob/main/.github/workflows/experimental-runtime.yml),
  runs on every pull request. `make test-experimental-ratchet` runs the
  `internal/tofu`, `internal/command` and `internal/live/...` tests with
  `TOFU_X_EXPERIMENTAL_RUNTIME=1`, checked by `tools/expruntime-ratchet`.
- [`live/experimental-runtime/expected-failures.json`](https://github.com/INTENTIUS/choudoufu/blob/main/live/experimental-runtime/expected-failures.json)
  lists the tests expected to fail under the toggle, each with the gap-table
  row it fails on. Entries come off as rows close and are never added for a
  marker, identity or record test.
- [`internal/live/newruntime/gaps.json`](https://github.com/INTENTIUS/choudoufu/blob/main/internal/live/newruntime/gaps.json)
  is the gap table, and
  [`live/fork-surface.json`](https://github.com/INTENTIUS/choudoufu/blob/main/live/fork-surface.json)
  the fork's file-level diff from the base commit.
- The plan and status of the work are in
  [#2256](https://github.com/INTENTIUS/choudoufu/issues/2256).
