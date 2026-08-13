# Handoff

Rewritten 2026-08-13. Read this before doing anything.

## The goal

**No manual wiring should exist anywhere in this project.** Every per-resource
fact must be derived by a generator from provider schema, the CloudFormation
Registry, or provider docs. Where a generator is wrong, the correction belongs
in a machine-readable ledger, never as a hand-edited entry in a table.

A generator's output contains its own wiring. A hand-written line that
assembles generated pieces into a table is the same paste cycle in a smaller
font, and it fails this charter exactly as a pasted block does.

Charter issue: #93. Sites: #94-#100.

## The test

**If the work makes hand-maintenance faster, safer, more parallelisable, or
better documented, stop. It is the wrong work.** The only acceptable direction
is deleting the hand-maintenance.

This test exists because the wrong work is indistinguishable from the right
work by ordinary engineering instinct. An agent-day once went into splitting
the tables into 113 per-cohort files so batches could hand-append without
conflicts. The 47% conflict rate it fixed was a real, measured number. All 113
files are now deleted. That contention existed only because the files were
hand-maintained.

## What will mislead you

Four things in this repo currently read as more finished, or more blocked, than
they are. Each one has already cost time.

**1. The tables are generated in *form*, not in *content*.** `-emit` copies
every field verbatim from the live table. 194 of the 846 rows are values no
fresh classifier run reproduces, so they remain human judgments the generator
merely carries. The hand-*wiring* is gone; roughly a quarter of the *data* is
still not derived. Commit `d0cb7c59c` reads like "the table is generated now"
and it is easy to stop there.

**2. The 117 "scrape gaps" are close to one defect, not 117 problems.** The
widened scrape emits nothing at all — see Next Action 2. Sizing this as 117
separate extraction fixes overestimates it by a wide margin.

**3. The 194 mismatches are not a 194-item judgment campaign.** Exactly one
type is believed genuinely ambiguous by its own nature (`aws_route`, which the
code's own comments already flag as a deliberate trap; plausibly 3-5 once
unverified rows are checked). Everything else is mechanical debt in a rule or
an extractor. Issue #95 was originally titled "move the 215 unannotated
mismatches into annotations.json", which is that campaign written down as a
plan; it has been retitled. One rule fix retired 21 rows at once. That is the
shape the work takes.

**4. The published scrape-gap/classifier split is wrong at the source.**
`isScrapeGap` cannot label a `bucketFoldChild` row, so the attribution between
#94 and #95 is mislabelled. Do not quote a split from the artifact until Next
Action 1 lands.

## Where things stand

`main` is at `b04f3f13b`. Build green, tests green, `gofmt` clean. No agents
running, no worktrees in flight. CI is deprioritised — keep work local.

**The tables are generated.** `DefaultTable` (846 rows) and `admittedTypesV0`
(836) are each declared in full by a file `tools/row-gen -emit` owns end to
end. No fragment, no `init()`, no core literal, no assembly line.
`table.go`/`admission.go` hold only resolution behaviour. `-emit` is a fixed
point: running it twice with no source change is a byte-for-byte no-op.

Convergence, measured at `b04f3f13b`:

```
admitted_total       846
compared             825
not_in_mapped_set     21
adopted_unchanged    631   (76.48%)   generator reproduces these
genuine_mismatches   194
  scrape_gap         117              but see "What will mislead you" #4
annotated              0              the ruling ledger is EMPTY
```

`tools/row-gen/annotations.json` has zero rulings. `tools/row-gen/rejected.json`
has 147 entries.

## Why this keeps failing, and how to slice it so it does not

The drift is not a discipline problem. It is what the obvious decomposition
rewards. The previous session drifted four times; the one before it did the
same; and the session that wrote this file reproduced it within twenty minutes
of reading the warning, by starting to write a hand-maintained
`var DefaultTable = mergeTables(...)`.

**846 rows look like 846 parallel tasks. They are not.** The work is about six
classifier rules plus two extraction stages. Hand an agent a slice of
*resources* and the fastest way for it to finish is to hand-write the rows, and
it is right that this is fastest: fixing the extractor for one resource costs
ten times more and only pays off across the other 845 that agent cannot see.
Every resource-shaped slice points at hand-wiring.

**The diagnosis lives above the slice.** The Next Action 2 root cause needed
four facts held at once: an artifact header date, `omitempty` on three struct
tags, the contents of the doc cache, and a section parser's boundary rule.
Nobody working a 13-row slice holds those. From inside such a slice, "the
evidence is prose, this needs judgment" is a *reasonable* conclusion. Locally
true, globally false. That is why "can't be generated" keeps being reported,
and why accepting it once cost an entire session.

**The measuring instrument is broken.** See Next Action 1. When measurement
lies, agent self-reports cannot be checked and progress gets accepted on
narrative.

So:

- Fan out on **rules and extraction stages**, roughly six wide. Never on
  resources.
- The orchestrator holds the measurement and does the cross-layer diagnosis.
  That part cannot be sliced.
- Require **computed set differences** (matched-before vs matched-after over
  the full compared set), never a quoted summary line. A fix and a regression
  cancel out in a total.
- One regression outweighs many fixes.
- Verify agent claims by recomputing them. The one agent result landed this
  session reported 21 rows; recomputing confirmed 21 and 0 regressions, but the
  estimate it was checked against said 19.

Subagents get all of this as a system prompt via
`.claude/agents/generator-work.md`. Use that agent type for anything touching
generators or per-resource facts, so the briefing cannot be forgotten.

## Next actions, in order

1. **Fix `isScrapeGap`** (`tools/row-gen/convergence.go`). Its final gate reads
   `if p.Bucket != bucketEvidenceOnly && p.Bucket != bucketNeedsHandSeparator
   { return false }`, so a `bucketFoldChild` row can never be labelled a scrape
   gap whatever its notes say. Note the gate has two parts — bucket AND notes —
   so adding the bucket may be a no-op if fold rows carry no `GUESSED` /
   `argument-composed ID` note. Determine that empirically, in-process:
   `proposed_notes` is serialised for 0 of 825 rows, so the artifact cannot
   answer it. Partial work on branch `worktree-agent-a7297e2ab0fc69764`
   (unverified); it had confirmed the single remaining non-scrape-gap
   `needs-hand-separator` row is `aws_route`.

2. **Fix the widened scrape (#94).** Biggest single lever, and it is one defect
   rather than 117. `arguments_in_doc_order`, `identity_schema_required` and
   `identity_schema_optional` are absent from **all 1600 rows** of
   `live/import-grammar.json`. The extraction is already implemented and wired
   (`tools/importdocs-gen/artifact.go:162-164`, `parse.go:543`); it runs and
   returns nothing. The artifact is not stale (6.59.0, regenerated the same
   day). The input is present (923 cached docs carry an `Identity Schema`
   block). The obvious sectioning bug is ruled out: `importSection`
   (`parse.go:43`) terminates only on `"## "`, so a `### Identity Schema`
   heading does not cut the section short.

   Both consumers of these fields have therefore been **dead code**:
   `applyIdentitySchemaAttrsCorrection` (the ARN-vs-short-id rule) and
   `tryGrammarComposite`'s prose-order fallback
   (`importprecedence.go:217-222`). Candidate population: 66 unmatched
   `server-assigned` rows plus 36 whose only complaint is `identity-attrs`.

   Fastest first step: a throwaway test in `tools/importdocs-gen` running
   `buildRow` against the real cached `batch_compute_environment.html.markdown`,
   printing what each field returns. That says more in one run than reading the
   parser will. Partial work on branch `worktree-agent-ab534ae5121942cfe`
   (unverified).

3. **Then the remaining classifier rules (#95)**, worked as rules, not as rows.
   28 of the 49 fold-child mismatches remain after the precedence fix.

4. **Finish #96's out-of-scope half**: `typeOverrides`
   (`tools/estate-gen/overrides.go` `Apply` closures) and the stamp tables.

5. **Then decide the `Reason` prose question** — see Decisions below.

## Decisions waiting on a human

None of these should be made by an agent on its own initiative.

- **`Reason` prose.** Emit it from a ledger, or accept it as curated data the
  generator carries? Currently deferred and carried verbatim, which is why
  `-emit` copies rather than re-derives. This gates how far #96 can go.
- **14 branches carry real unmerged commits.** Keep or discard? Notable:
  `iampolicy-gen` (a whole `tools/iampolicy-gen`, 10k+ lines added),
  `issue-74-plan-fingerprint`, `issue-72-sidecar`, `issue-79-docs-redesign`,
  `generated-merge-strategy`, `branding-tagline`, `changelog-upstream`,
  `getting-started-tutorial`, `lint-toolchain`, `floci-image-bump`.
- **Two dirty worktrees.** `agent-a645c07bacae418aa` has 6 uncommitted files.
  `agent-ab7119058a89f8f63` is **mid-merge-conflict** with unresolved `UU`
  files across `internal/stateless/{identity,lint,projection}`. Both need
  resolution or an explicit abort, not a blind removal.
- **Three branches are safe to force-delete.** `floci-capabilities`,
  `fragment-hot-files`, `pipeline-admission-endgame` are genuinely merged;
  `git branch -d` refuses only because `origin/main` is behind local `main`.
  `-D` them, or fetch first.
- **`admission-pipeline` has never run.** Zero executions, all time, despite a
  weekly schedule and `workflow_dispatch`. Running it once is cheap and it may
  well be broken.

## Do not re-derive these

- **The generated/override split is gone from the source, deliberately.** It
  measured how well the classifier was doing, and the table's shape should not
  encode that. Counts live in `live/rowgen-convergence.json` and the `-emit`
  summary. Payoff: `b67189ce5` improved the classifier by 21 rows and
  `table_generated.go` came out byte-unchanged.
- **Do not hand-list struct fields in a renderer.** `renderTypeIdentity` used
  to, so adding a field to `TypeIdentity` meant editing the generator in
  lockstep and silently dropping the field until someone noticed. Rows render
  by reflection now.
- **Do not generate the type definitions.** Considered and rejected: there is
  no upstream source to derive `TypeIdentity`'s shape from, so a model JSON
  would be exactly as hand-maintained as the struct, minus compiler checking,
  plus a codegen path and a drift test to keep in sync. That is relocating
  hand-maintenance. The real coupling was the renderer's field list, and
  reflection removed it.
- **The generator once read hand-written comment prose as input.**
  `scanFileForRejected` globbed the table sources and grepped Go comments for
  the word `Rejected`, harvesting every `aws_*` token within 60 lines as a veto
  set. Deleting the fragments would have emptied it silently and let PROPOSE
  re-propose 147 already-declined types. Now `tools/row-gen/rejected.json`,
  with the loader failing closed on an empty or missing ledger. **Watch for
  other instances of this shape** — a generator whose input is someone's prose.

## Working model

No branches for review. Agents work in isolated worktrees when they must build
concurrently; the orchestrator verifies and lands to `main` as single writer.

- Run `go build ./...` and the relevant tests **before** committing. Never
  commit red. If it cannot be made green, commit nothing and report.
- Small commits, each independently revertable. `git revert` is the undo.
- Do not push unless asked.
- When stopping an agent mid-flight, commit its work to its own branch first.
  Removing the worktree discards uncommitted changes.

## Traps that cost real time

**Test invocation.** Always:

```
env -u PWD go test -C /Users/alex/Documents/checkouts/intentius/choudoufu ./...
```

`/Users/alex/checkouts` is a symlink to the real path and `os.Getwd()` honours
`PWD`, so a plain invocation produces 10 false failures in `local-exec` and
`TestFmt*`. They are environmental. Do not chase them.

**`gh` defaults to the wrong repo.** This checkout has `upstream` pointing at
`opentofu/opentofu`, so a bare `gh issue view 93` shows an unrelated upstream
PR. Always pass `-R INTENTIUS/choudoufu`.

**The doc cache is offline and complete.**
`~/Library/Caches/choudoufu/importdocs-gen/6.59.0/`, 1699 files, matching the
artifact's own `types_considered`. Re-running a doc sweep needs no network; use
it as a measurement loop.

**`just lint`** runs the full repo twice, once per GOOS, in about 41 seconds.
Six issues are currently outstanding and all predate this work
(`statelessOwnership` unused, five staticcheck). Do not attribute them to your
change without checking.

## Off the path

Open and legitimate, but not the goal: the #76 documentation slices (#85-#88),
the #73 charter phases (#81-#84), #79, #77, #70. Silent-merge-loss findings
#89-#92 are real defects (#89 fixed) but are cleanup.

## Residue already cleaned

20 worktrees removed and 85 fully-merged branches deleted this session (103
branches down to 18). What remains needs the decisions listed above. Two WIP
branches from this session, `worktree-agent-a7297e2ab0fc69764` and
`worktree-agent-ab534ae5121942cfe`, hold the unverified partial work referenced
in Next Actions 1 and 2.
