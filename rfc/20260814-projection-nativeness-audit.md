# Projection-Nativeness Audit

Issue: https://github.com/INTENTIUS/choudoufu/issues/81 (phase (a) of #73)

This document publishes the projection-nativeness audit that #73 announced as
"running now" at filing. The audit itself was completed and its verdicts posted
as a comment on #73 dated 2026-08-13; that comment is the source for the
verdicts below. This document is their publication as a committed artifact,
plus the fork-surface measurement #81's acceptance asks for, plus a
retrospective: two of the three legs have been resolved by work that landed
after the audit ran.

## What was asked

Establish the true distance between live-plan's in-memory "prior state"
projection and a real `states.State`, characterize it concretely (types,
fields, call sites), and quantify the fork-surface consequence: #73's stated
payoff is that an unmodified downstream engine shrinks the fork and cheapens
upstream rebases. Diagnosis only; no behavior changes.

## The three-leg verdict

### Leg 1: the projection — ALREADY-NATIVE

There is no distance to close. The projection's result type already carries a
real `*states.State`:

- `internal/live/projection/result.go`: `Result.State` is a `*states.State`
  holding one object per materialized instance. It is built in memory, handed
  to the stock engine, and never persisted.
- The engine is unmodified. `grep -rn 'internal/live' internal/tofu/` returns
  nothing; the dependency is strictly one-directional (live code calls the
  engine, never the reverse).
- The seam where the state enters the ordinary run is the `StatelessRun`
  interface in `internal/backend/local/live.go`, consumed at
  `internal/backend/local/backend.go:260` (StateMgr),
  `internal/backend/local/backend_local.go:238` (prior-state injection before
  the run), and `internal/backend/local/backend_apply.go:332` and `:346`
  (write-back and after-apply). Everything downstream of those four sites
  reads the state the ordinary way.

Leg 1 closed as already delivered when the audit ran. The one item it left
open was optional: a cosmetic reshape of the `Result` signature. `Result`
still wraps the state with `Materialized`/`Omitted` bookkeeping, which callers
use; the wrapper has not been a source of friction since.

### Leg 2: snapshots — SMALL-ADAPTER, plus one design fork (since retired)

The audit found one real design fork: the snapshot carrier's `AttributesHash`
gives a hash-only, zero-raw-values guarantee that no native-state encoding can
match — going native would put clear-text attribute values in a written
artifact for the first time, in exchange for `tofu show -json` compatibility.
The audit recommended keeping hash-only, and the maintainer ruled exactly
that: keep the hash-only custom JSON carrier; native state format was
considered and declined because it would reintroduce the secrets-accumulation
problem snapshots were designed to avoid.

The ruling was then overtaken. On 2026-08-13 the maintainer retired phase (c)
entirely: observational snapshots are being dropped rather than reformatted
(#109; #83 closed). The one load-bearing piece — guided discovery's hint, a
set of type names plus a timestamp — moves into the record store (#109, open
at this writing). Leg 2's question no longer has a subject.

### Leg 3: micro-state residue — REAL-REBUILD (since landed)

The audit called this leg honestly: record-less residue (`null_resource`,
`terraform_data`, `time_*`, non-sensitive `random_*`) through the stock
provider lifecycle needed real new construction — a fourth admission class, a
per-type lint table, a hydrate-from-record path in projection, and a net-new
store package. All of it landed as commit `03c1fb237` ("live: record-backed
hydration, write-back, and admission for issue #73"), merged as `9d228235b`.
The store package is `internal/live/staterecord` (local, SSM, and S3
implementations behind one interface); the companion ruling doc is
`rfc/20260814-micro-state-store-ruling.md`.

## Fork-surface measurement

#81 asks that the "unmodified engine shrinks the fork" payoff be quantified
rather than asserted. One honest caveat first: this repository's history was
re-rooted at the fork (`752810b73`), so there is no merge-base with upstream
and no `git diff upstream...` to run. The full delta-since-fork-point
accounting, including a rebase strategy, is #77's standing charter and is not
attempted here. What can be measured today, from the tree alone, is how the
fork's code is distributed: additive and isolated, additive inside upstream
packages, or edits to upstream files. All numbers below were computed on
2026-08-14 at commit `2a5e8329a`; each command is included so the number can
be recomputed.

**Additive and isolated: `internal/live/`.** 274 Go files (128 source, 146
test), 78,546 lines, 28 packages. Nothing under `internal/tofu/` references
it.

```
find internal/live -name '*.go' | wc -l                        # 274
find internal/live -name '*.go' ! -name '*_test.go' | wc -l    # 128
find internal/live -name '*.go' | xargs wc -l | tail -1        # 78546
grep -rn 'internal/live' internal/tofu/                        # no matches
```

**Import edges from upstream packages into live code: 9 files, one package.**
Only the live command implementations import live packages; every other
touched upstream file works through the `StatelessRun` interface or plain
config types, so the live tree could move without those files noticing.

```
grep -rln 'intentius/choudoufu/internal/live' internal/ cmd/ \
  --include='*.go' | grep -v '^internal/live/'
# 9 files, all internal/command/live_*.go
```

**Additive files inside upstream packages: 35.** These are new files the fork
adds to existing upstream directories — deletable without editing a neighbor.
33 are the `live`-named files found by
`find internal cmd -name 'live*.go' -not -path 'internal/live/*'`
(19 source + 14 test), spread over `internal/command` (and its `arguments`
and `views` subpackages), `internal/configs`, and `internal/backend/local`
(the `StatelessRun` seam file itself). The other 2 are
`internal/plugin/grpc_provider_list.go` and
`internal/plugin6/grpc_provider_list.go`, the client side of the provider
list protocol that discovery uses.

**Upstream files the fork edits: 27 source files, plus 3 test files and 4
regenerated protocol files.** Method: sweep every non-`live`-named Go file
outside `internal/live/` for live-mode tokens
(`stateless|Stateless|estate|record_store|Live|live_`), then triage each hit
by reading it. Two hits were false positives (an upstream "stateless" comment
in `internal/legacy/helper/schema/resource.go`, "tracestate" in
`internal/tracing/init.go`) and are excluded. The 27:

- `internal/backend/local/`: `backend.go`, `backend_local.go`,
  `backend_apply.go` — the four seam call sites listed under leg 1.
- `internal/command/`: `plan.go`, `apply.go`, `import.go`, `refresh.go`,
  `taint.go`, `untaint.go`, `init.go`, `meta_backend.go`, and the guard call
  sites in `state_list.go`, `state_mv.go`, `state_pull.go`, `state_push.go`,
  `state_replace_provider.go`, `state_rm.go`, `state_show.go`,
  `workspace_new.go`, `workspace_select.go` (17). The state and workspace
  edits are each a few lines calling a guard defined in a fork-added file —
  the no-state-ops refusals.
- `internal/command/arguments/plan.go`, `internal/command/views/view.go` (2).
- `internal/configs/`: `module.go`, `parser_config.go`, `static_scope.go`
  (3) — the `live` block's attachment to the config model.
- `internal/providers/provider.go` (1) — the list protocol's request/response
  types.
- `cmd/choudoufu/commands.go` (1) — command registration.

Test files edited: `internal/command/init_test.go`,
`internal/command/meta_backend_test.go`, `internal/repl/session_test.go`.
Regenerated: `internal/tfplugin5/tfplugin5.pb.go`,
`internal/tfplugin5/tfplugin5_grpc.pb.go`, and their `tfplugin6` twins, for
the ListResource RPC.

The token sweep is a proxy, not a diff: it finds files that participate in
live mode, and it would miss an upstream file the fork edited for some reason
unrelated to live mode. Within that limit, the shape supports the claim the
audit was asked to test: the engine (`internal/tofu/`, by far the largest and
most rebase-sensitive surface) is untouched, and the edits to upstream files
concentrate in ~27 files, most of them one guard call each. The heavy
machinery is additive.

## What changed since the audit ran

| Leg | Audit verdict | Status on 2026-08-14 |
|---|---|---|
| 1 (projection) | ALREADY-NATIVE | Unchanged; delivered before the audit ran |
| 2 (snapshots) | SMALL-ADAPTER + hash-only ruling | Retired: snapshots dropped (#109, #83 closed) |
| 3 (residue) | REAL-REBUILD, seams enumerated | Landed: `03c1fb237` / `9d228235b` |

The audit also found `terraform_data` missing from lint's
`logicalTypePrefixes`, which was fixed as part of the leg-3 work.

## Recommendation

Proceed — and in leg 1's case, recognize that "proceed" meant "stop": the
projection is already native, the payoff it was meant to buy is measurable in
the tree, and no adapter work is owed. The remaining open threads are owned
elsewhere: the full delta-since-fork accounting and rebase strategy by #77,
the hint relocation by #109. Nothing further is owed under #81, which this
document closes.

## Source

The verdict prose above restates the audit-completion comment posted on #73
(2026-08-13), the subsequent maintainer ruling on the leg-2 fork, and the
phase-(c) retirement comment, checked against the tree at `2a5e8329a`. Every
file, line, and count cited here was re-verified against that tree; line
numbers will drift as files change (the tracker comment's
`internal/live/lint/lint.go:266`, for example, is `:270` today).
