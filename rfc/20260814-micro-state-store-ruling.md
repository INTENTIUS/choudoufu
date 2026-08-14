# Micro-State Store Ruling

Issue: https://github.com/INTENTIUS/choudoufu/issues/82 (phase (b) of #73)

#73 asked the maintainer to rule between SSM (zero-infrastructure) and S3
conditional-write (CAS-correct) as the backend for per-estate cloud records.
#82's task is to determine whether that ruling was made de facto by the
phase-(d) work and, if so, document it as settled. It was. This document
records the ruling as confirmed; it cites the implementation rather than
restating it.

## The ruling

Support all three, behind one interface, chosen per estate:

- **local file** — the zero-configuration default: solo development, tests,
  air-gapped runs, mirroring plain local state's "just works" shape.
- **SSM** (AWS Systems Manager Parameter Store) — the zero-infrastructure
  team default: nothing beyond IAM to provision.
- **S3** — true conditional-write CAS end-to-end, for teams that want it.

The "SSM or S3" question dissolved rather than resolved: with no users yet
there is no compatibility burden, and a use case for each is reason enough
for each. The store choice is estate configuration (`record_store` block in
the `live` block); all three carry the same loud-CAS-failure semantics and
the same redaction rules, which live in the caller, never the store. The
concurrency answer is unchanged from #73: optimistic conditional writes, no
locks; a race is a loud failure naming both versions
(`*VersionConflictError`).

## Where it is implemented

- `internal/live/staterecord/` — the store package: `store.go` (the `Store`
  interface: Get, PutIfVersion, PutIfAbsent, Delete, List), `local.go`,
  `ssm.go`, `s3.go`, and a shared `conformance_test.go`. Landed in
  `03c1fb237`, merged as `9d228235b`.
- `internal/live/staterecord/doc.go` (84 lines) — the contract in full: opaque
  keys and payloads, `""` as the universal no-record version sentinel, the
  upstream-adoptability constraints, and the design rule that a fourth store
  is a new file implementing `Store`, never a refactor.
- `internal/configs/live.go` — the `record_store` block (`LiveRecordStore`,
  block schema at line 324).
- `internal/live/lint/lint.go:270` — the admission consequence: a
  record-admitted logical type flips from refused to admitted when a
  `record_store` is declared.

## The CAS asymmetry, plainly

The three stores do not offer equal conditional-write strength, and the
package documents this rather than hiding it:

- **LocalStore** and **S3Store**: real compare-and-swap with no
  read-compare-write window (O_EXCL/rename atomicity; S3 conditional
  writes). A conflicting write never reaches the stored state.
- **SSMStore**: real CAS for **create only** (`PutIfAbsent` via PutParameter
  `Overwrite: false`, which fails atomically on an existing parameter).
  Update (`PutIfVersion` on an existing key) is a read-compare-write with
  best-effort detection after the fact — a conflict is reported via
  `*VersionConflictError`, but the overwrite has already happened. Delete is
  weaker still: SSM's DeleteParameter takes no version and returns nothing
  to check, so a racing write in the window is invisible.

The full analysis of SSM's API surface is the package-level doc comment in
`internal/live/staterecord/ssm.go`. The asymmetry is acceptable because of
the micro-state's tiny blast radius — a lost record means an effect re-runs
or a random id regenerates — and because a caller who needs prevented rather
than reported update races has two stores that provide it.

## What would reopen this

- A fourth backend. By design that is one new file implementing `Store`; the
  ruling's lineup would grow, not change shape.
- A workflow that requires prevented (not merely reported) update races on
  SSM specifically. The answer on file is "use S3 or local", not "strengthen
  SSM" — Parameter Store's API has no versioned-overwrite primitive to build
  on, so any strengthening would be a redesign, and that would be a genuine
  reopening.
- Upstream adoption talks (the package is built to be proposable to OpenTofu
  verbatim), if upstream conventions demanded interface changes.

## Verdict

Confirmed. The ruling exists, is implemented, is documented at the
implementation, and nothing about it is undecided. #82 closes with this
document; the maintainer prose behind it is the four store comments on #73
(2026-08-13).
