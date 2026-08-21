# How to work this repository

One page. Everything longer than this is generated from code or lives in the
tracker, and the tests say which.

## The promise

**If OpenTofu runs an estate, choudoufu runs it too.** Migration from a stock
state file is lossless, a greenfield apply is equivalent, and day-2
operations behave the way stock's do. That is the whole bar, and it is
measured, not argued: `live/GAUNTLET.md` is the test that decides it.

Cold adoption, taking over live infrastructure that nothing has ever marked,
is a feature with its own ladder. It is not part of the promise, and a number
about it is not a number about the product.

## The default, and the principles

**Compatible out of the box.** A configuration that works on stock OpenTofu
works here with a `live` block added and nothing else. That means a local
record store is implied when none is declared, the way stock implies local
state; secrets the configuration generates are stored there the way stock
stores them; markers are written on every taggable resource; marker repair is
on. The principles this fork exists for are toggles, and turning them on is
the setup step:

- no secrets stored by the tool (secret-generating types refused, sensitive
  settable arguments never recorded);
- markers never repaired out of band (for estates where something else owns
  the tags, with `ignore_changes` honoured exactly as stock honours it);
- per-type or per-address `markers = record`, for tag budgets and tag
  policies, trading IAM governability for a record-held identity.

The toggles live in the live configuration schema, each with a default, a
doc string and a fixture that proves it refuses exactly what it names. The
reference page is rendered from the schema.

## The foundation

Every managed instance has a **record**: identity, the arguments the provider
never echoes back, sensitivity marks, taint, deposed key. One per resource,
namespaced per estate, under IAM, written with compare-and-swap. **Markers**
stay what they are: the authoritative, recoverable identity on every taggable
type, the inventory any cloud tool can list, the attribute IAM conditions on.
Binding reads the record and verifies it against the marker; a lost record is
rebuilt from tags where tags exist, and where they do not, the estate is
exactly where stock is.

So every type stock supports is admitted. What varies per instance is its
**rung**: tag-governable, derived from configuration, or record-only. That
is a metric that goes up, never a gate that refuses.

## The safety rule

**Never write a wrong marker.** A refusal is loud and reversible; a wrong
marker is silent and adopts or displaces a real object. When a construct
stock accepts cannot yet be handled without risking that, the instance drops
to the record rung and the run proceeds. Refusing an estate is never the end
state, and convergence is never evidence an identity is right: assert the
rendered identity by value.

## The engine

**Stock is the oracle.** The gauntlet (`tools/gauntlet`, contract in
`live/GAUNTLET.md`) runs real, popular estates through fixed stages side by
side with stock OpenTofu against the pinned emulator, and diffs the plans and
the cloud. Every difference is one of four things, each with a fixed action
and no ruling:

| Difference | Action |
|---|---|
| choudoufu refuses where stock proceeds | defect; fix it |
| the plans or the resulting cloud differ | defect; fix it |
| stock fails too | record it against stock and move on |
| handling it would write a wrong marker | drop the instance to the record rung, proceed, open a rung ticket |

The unit of progress is **an estate clearing every active stage**. The two
numbers on the site, core estates clear and all estates clear, are read from
`live/gauntlet.json`, which only the runner writes.

## The loop

1. `go run ./tools/gauntlet next` names the unit: the core estate closest to
   clear and the first active stage it does not pass, else the next growing
   one. New estates enter with `go run ./tools/gauntlet add` (see
   `live/GAUNTLET.md`). `just contribute` runs one unit unattended under
   your own key, in a worktree, and opens the PR; that is also what
   contributors run from their forks.
2. `go run ./tools/gauntlet run <name>`; read the verdict lines, not the exit
   code.
3. Every difference from stock gets its row from the table above. Fix
   generically: a fix that names a concrete `aws_*` type in control flow is
   the wrong fix; find the property and derive the rule, then say how many
   other types it reached.
4. `go run ./tools/gauntlet render`; commit the script, the artifact and the
   rendered docs together. `just ci` must be green.
5. When a planned stage is implemented for enough estates to be honest, flip
   its status to active in `tools/gauntlet/stages.go`. The bars drop; that is
   the point.

## What is enforced

Rules are tests. The ones that hold this document to the tree:

| Guard | What it holds |
|---|---|
| `tools/gauntlet`: `TestRenderedDocsAreCurrent`, `TestManifestIsCanonical`, `TestArtifactAgreesWithManifest` | the spec, the site pages and the artifact are what the code says |
| `tools/gauntlet`: `TestLegacyScriptsOnlyGoDown` | crossing scripts move onto the protocol and never back |
| `live/derivation_guard_test.go`: `TestEveryTypeLiteralSurfaceIsRegistered`, `TestNoTypeNameIsAssembledFromLiterals` | every hand-wired provider type name carries a registered reason and count, and none is assembled at runtime to dodge the registry |
| `internal/live/check`: `TestIdentityGolden`, `TestIdentityGoldenShapeIsPinned` | 1589 rendered identities across 521 configuration directories, pinned by value; if your change moves a line, explain it, and `-update` alone cannot silence it |
| `live/ci_coverage_test.go` | every fork-owned test package is in CI's glob |
| `internal/live/harness` | every ratchet pins its denominator |
| `live/flociimage_test.go`, `live/pins_drift_test.go` | the emulator and provider pins are current |
| `internal/live/lifecycle/marker_tag_merge_live_test.go` | markers survive an incremental tag update through a real emulator |

## Working here

- Worktree off **local** `main` (`git worktree add ../wt/<name> -b
  live/<name> main`); `origin/main` goes stale.
- `env -u PWD` on every go command; read exit codes from a file; never
  `git stash`; never prune a worktree by whether its branch merged.
- `just ci` is the gate; a full-module `go test ./...` is a periodic
  checkpoint, not a reflex.
- Regenerate artifacts, never hand-edit them; a generator run twice and
  diffed proves determinism, not correctness.
- A brief is a lead, not a fact: re-verify against the code before fixing.
- `gh` defaults to upstream in this clone; pass `-R INTENTIUS/choudoufu`.

## Retired

"Parity" and its three labels, the decision matrix and its `RULE` row,
admission as a gate, "no memory" as a goal, the offline corpus and
refusal-site counts as progress instruments (`live-check` stays as a user
tool), the wall taxonomy, the rulings list. Their history is in git before
this file's rewrite on 2026-08-21; the reasoning for retiring them is in the
tracker's design thread of the same date.
