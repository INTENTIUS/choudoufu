# Upstream Sync: the Measured Delta and the Procedure

Issue: https://github.com/INTENTIUS/choudoufu/issues/77

Companion to `rfc/20260814-projection-nativeness-audit.md`, which measured
the fork surface from this side. This document measures the delta from the
upstream side and derives the sync procedure the numbers support. Every
figure below was computed on 2026-08-14 against `upstream/main` at
`3561785c4`; the commands are included so the measurement can be rerun the
day a sync is actually attempted.

## The situation

- Fork point: `03743ce6e8`, an upstream commit of 2026-08-11 — the fork is
  **three days old** at the time of writing.
- `main` does not contain the fork point (`git merge-base --is-ancestor
  03743ce6e8 main` fails): the history was re-rooted, so `git merge` and
  `git rebase` against upstream have no common ancestor to work from.
  Whatever the procedure is, it is diff transplantation, not a merge.
- The module path was renamed throughout (`github.com/opentofu/opentofu` →
  `github.com/intentius/choudoufu`), so **889** non-live, non-`live_*`,
  non-test files differ from the fork point
  (`git diff --name-only 03743ce6e8 HEAD -- 'internal/*' 'cmd/*'` minus
  those filters). Almost all of that difference is import lines.

## The delta, measured

Upstream since the fork point: **30 commits, 85 files, +4548/−562**
(`git rev-list --count 03743ce6e8..upstream/main`;
`git diff --stat 03743ce6e8 upstream/main`).

The number that decides the sync cost is the overlap between what upstream
changed and what the fork changed, classified by whether the fork's change
is real logic or only the rename:

- Files upstream changed that also differ in the fork, over ALL paths:
  **33**. Restricted to non-test Go files under `internal/` and `cmd/` —
  the filter the first version of this document applied without stating
  it, which a wave-2 audit called out — **20**.
- Of those 20, differing from the fork point **only by the module-path
  rename** (verified by normalizing the path with `sed` and diffing): **19**.
- Go files with real fork logic AND upstream churn: **2**, not the one
  the first version claimed. `internal/command/init.go` (the fork's
  `case rootModEarly.Live != nil` arm vs upstream's unrelated 32-bit
  deprecation warning, `97a20581b` — different lines, trivial) and
  `internal/command/init_test.go`, which the non-test filter had hidden:
  a ~150-line fork-authored section with upstream appending a test to the
  same trailing region — a real merge conflict, the largest single item in
  a sync performed today.
- The remaining 13 of the 33 are the always-both-sides files: `go.mod`,
  `go.sum`, `CHANGELOG.md`, `.goreleaser.yaml` and the workflow files.
  Each carries fork changes and upstream churn on every sync forever;
  they are resolved by policy (fork's module path and release identity
  win; upstream's dependency bumps win), not by inspection.

The seam files the nativeness audit named (`internal/backend/local/*.go`,
`internal/configs/module.go`, `cmd/tofu/commands.go`) have **zero** upstream
churn since the fork point.

## The procedure these numbers support

Because there is no merge base and the rename touches every file, the sync
is a normalized-tree diff transplant:

1. Check out the upstream target (a release tag, not `upstream/main`) in a
   scratch worktree and apply the deterministic rename transformation to it
   (module path, `cmd/tofu` → `cmd/choudoufu`; the retired
   `tools/rename-phase/rename.sh` in git history at `492490cc2` records the
   transformation, minus its references to since-deleted files).
2. Diff the previous normalized upstream tree (the fork point, normalized
   the same way) against the new one. That diff is upstream's changes in
   the fork's own vocabulary.
3. Apply it to `main`. With today's numbers, 83 of 85 files apply clean;
   `init_test.go` needs a real merge and `init.go` a look that is not
   currently even a conflict, plus the policy-resolved metadata files.
4. Run the fork's own instruments before committing: the full suite, `just
   corpus` (upstream `internal/configs` changes can move the pass-through
   refusal set — those diagnostics are ranked 1, 3 and 7 in the corpus),
   `just limits`, and the drift tests. A sync that changes any refusal
   surface shows up as a registry/scan failure, which is the intended
   tripwire, not an obstacle.
5. Record the new fork point in `version/VERSION` and the release notes,
   the existing convention.

## Cadence recommendation

Sync on upstream **minor releases**, not on `upstream/main`. The conflict
surface is two files today (one of them a test) and grows with both clocks; the instruments above
make each sync's blast radius measurable, so smaller, regular transplants
are strictly cheaper than a big-bang catch-up. The first sync should happen
while the conflict surface is still this small — it doubles as the
procedure's shakeout at the lowest possible stakes.

## What this document does not decide

Whether to re-root history again to restore a merge base. The transplant
procedure above works without one; restoring one would rewrite published
history (#75's cleanup touches the same question). Defer until #75 is
decided, and revisit only if transplants start conflicting in ways `git
merge` machinery would resolve better.
