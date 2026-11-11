---
title: Upstream OpenTofu
description: OpenTofu base, new-runtime opt-ins, fork patches.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/upstream/ and its gap
  table, and list what the toggle refuses here. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

<!-- upstreamdoc-gen:begin base -->
Built on upstream OpenTofu [`aa3eb66a0c`](https://github.com/opentofu/opentofu/commit/aa3eb66a0c1c792b7751e6ffeef7ca0cd8693f66), version `1.14.0-dev`.
<!-- upstreamdoc-gen:end base -->
`scripts/upstream-sync.sh` merges upstream `main` fortnightly and per release.

| Opt-in | Upstream | choudoufu |
|---|---|---|
| `TOFU_X_EXPERIMENTAL_RUNTIME` | dev, nightly | every build, live mode included |
| `TOFU_X_EXPERIMENTAL_RUNTIME_NEXT` | dev, nightly | non-release builds, by linker flag |
| `CHOUDOUFU_NEW_RUNTIME_NO_FORK_PORTS` | none | ports off ([#2372](https://github.com/INTENTIUS/choudoufu/pull/2372)) |

| Patch | Replaced by |
|---|---|
| Markers, identity, replace create half | fork-only |
| Destroy-from-config | [#3472](https://github.com/opentofu/opentofu/issues/3472) |
| Taint failed creates only | fork rule |
| Deferral error | [#4697](https://github.com/opentofu/opentofu/issues/4697) |
| In-run estates ([#2372](https://github.com/INTENTIUS/choudoufu/pull/2372)) | [#4058](https://github.com/opentofu/opentofu/issues/4058) |

## Already done by choudoufu

- Records: one per resource, a [conditional write](../locking/), written per instance from upstream's whole-snapshot `PersistingState` ([#4767](https://github.com/opentofu/opentofu/issues/4767)).
- Refresh: prior state is read live; plans skip refresh ([Identity](../identity/)).
- `moved`: found by its old-address marker, applied as a tag update ([#2374](https://github.com/INTENTIUS/choudoufu/issues/2374)).

Gaps: [`live/NEW-RUNTIME.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/NEW-RUNTIME.md).
