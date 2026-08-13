# Handoff

Written 2026-08-13. Read this before doing anything.

## The goal

**No manual wiring should exist anywhere in this project.** Every per-resource
fact must be derived by a generator from provider schema, the CloudFormation
Registry, or provider docs. Where a generator is wrong, the correction belongs
in a machine-readable ledger, never as a hand-edited entry in a table.

Charter issue: #93. Sites: #94-#100.

## Why this needs saying

`row-gen` computes admission entries and prints them as
`--- paste into internal/live/lint/admission.go ---` blocks that a human copies
in by hand. That has happened roughly 846 times. The generators exist, they
work, and their output is being applied with a clipboard.

## The failure mode, stated plainly

The previous session drifted off this goal four separate times, and the session
before it did the same. The drift is not random. It always takes the same shape:
the manual path has real, measurable problems, and fixing those problems feels
like progress.

What that looked like in practice:

- The best agent of the day was spent splitting the four hand-written tables
  into 113 per-cohort files, so that concurrent batches could hand-append
  without merge conflicts. That work is now scheduled for deletion in #96.
- A runbook was written (`contributing/LIVE-TABLES.md`) documenting where a
  human should paste new entries. It has been deleted.
- Hours went into diagnosing a 47% merge-conflict rate across seven files. That
  contention exists only because those files are hand-maintained. Generated
  files do not conflict; they are regenerated.
- #96 was originally filed as blocked behind a 215-item hand-annotation
  campaign. It was not blocked. That error alone would have cost weeks.

**The test:** if the work makes hand-maintenance faster, safer, more
parallelizable, or better documented, stop. It is the wrong work. The only
acceptable direction is deleting the hand-maintenance.

**Corollary, learned the hard way:** when a subagent reports that something
cannot be generated (`the comments carry the evidence`, `these are closures,
not data`), that is the question to push on, not an answer to accept.
Accepting it once is how an entire session went into optimizing hand-maintenance.

## Where things stand

`main` is at `2fd01e6dd`. CI is green. No branches in flight, no agents running.

The convergence artifact `live/rowgen-convergence.json` measures the gap between
what the generator produces and what humans committed:

```
admitted_total       846
compared             825
adopted_unchanged    610   (73.94%)   generator already agrees
genuine_mismatches   215
  scrape_gap         117              importdocs-gen extraction gaps (#94)
  real disagreement   98              row-gen classifier rules (#95)
annotated              1              the ruling ledger is essentially unused
```

`tools/row-gen -emit` exists as of `2fd01e6dd`. It writes four files carrying
`Code generated ... DO NOT EDIT`: a 610-entry generated partition and a 236/226
entry override ledger for `DefaultTable` and `admittedTypesV0`. Byte-identity is
proven and guarded by `TestEmitFilesMatchCommitted`.

**The generated files are not load-bearing yet.** The 86 per-cohort fragments
still build the tables through `registerCohortTable`/`registerCohortAdmitted`,
which panic on duplicate keys, so wiring the generated files in today would
panic on all 846 types.

Also note: `-emit` currently copies every field verbatim from `DefaultTable`
rather than re-deriving it, because `Reason` strings are human prose by design
(issue #44's non-goal). So the commit proves 610 entries *could* be derived and
builds the harness to prove it. It has not yet made them derived.

## The single most important number

**The irreducible tail is 1.**

Of the 215 mismatches, exactly one type has an identity shape that is genuinely
ambiguous by its own nature: `aws_route`, which the code's own comments already
flag as a deliberate trap. Plausibly 3-5 once unverified rows are checked.

Everything else is mechanical debt in two places. This project can be fully
generated. It is not a 215-item judgment campaign.

## Next actions, in order

1. **Fix `isScrapeGap()` first.** `tools/row-gen/convergence.go:219-269` never
   checks `bucketFoldChild` and checks only GUESSED-notes for
   `bucketNeedsHandSeparator`. The #94/#95 split is therefore mislabeled: 37 of
   the 38 `needs-hand-separator` rows attributed to classifier work are actually
   extraction gaps. Do not quote either count again until this is fixed.

2. **Delete the `CFNType` guard.** `importprecedence.go:113-121` has
   `if p.CFNType == "" { continue }`, which skips every fold row before
   `tryGrammarComposite` and `tryArgumentReferenceValueMatch` run, though neither
   rule references `CFNType`. Simulated against the 49 fold-child mismatches this
   resolves 19 immediately. It is a four-line deletion.

3. **Populate `arguments_in_doc_order` from the identity block.**
   `tryGrammarComposite`'s prose-order fallback (`importprecedence.go:203-222`)
   is already written to consume that field. 13 rows resolve with zero row-gen
   changes. The evidence is already captured in `evidence_excerpt`.

4. **Attack the two big extraction clusters** (#94): 55 rows where
   `composed_of_arguments` never fires on legacy pre-1.12 doc prose, then 24
   rows where the parse captures one of two-plus arguments.

5. **Wire the generated files in and delete the fragments** (#96): remove
   `table.go`/`admission.go`'s core literals, all 56 `*_cohort_*.go` files and
   `table_recordbacked.go`, then have the four generated files build the tables.

6. **Then decide the `Reason` prose question**: emit it from the annotation
   ledger, or accept it as curated data the generator carries.

The four remaining classifier fixes are detailed with counts in #95.

## Working model

No branches. No worktrees. Agents commit directly to `main`, one writer at a
time.

This replaced a branch-and-merge flow that was costing more than it returned:
three integration passes in one session, roughly 2000 seconds of dedicated
model time, plus every instance of silent content loss, which is a merge-only
failure mode.

The contract that replaces what branches provided:

- Run `go build ./...` and the relevant tests **before** committing. Never
  commit red. If it cannot be made green, commit nothing and report.
- Small commits, each independently revertable. `git revert` is the undo.
- Do not push unless asked.

## Traps that cost real time

**Test invocation.** Always run tests as:

```
env -u PWD go test -C /Users/alex/Documents/checkouts/intentius/choudoufu ./...
```

`/Users/alex/checkouts` is a symlink to the real path and `os.Getwd()` honors
`PWD`, so a plain invocation produces 10 false failures in `local-exec` and
`TestFmt*`. They are environmental. Do not chase them.

**CI.** The only lint CI runs is a `gofmt` step over fork-owned packages
(`internal/live cmd site`). It sat red for 20+ consecutive pushes because of one
unformatted file, and nobody noticed. Check it.

**`just lint`** runs the full repo twice, once per GOOS. Both passes are
load-bearing (the windows pass catches real bugs in the 5 `*_windows.go` files).
It takes about 41 seconds, not the 6 minutes an earlier estimate claimed.

**The `admission-pipeline` workflow has never run.** Zero executions, all time,
despite a weekly schedule and `workflow_dispatch`. Running it once is worth
doing; it may well be broken.

## Off the path

These are open and legitimate but are not the goal. Do not let them absorb the
session: the #76 documentation slices (#85-#88), the #73 charter phases
(#81-#84), #79, #77, #70.

Silent-merge-loss findings #89-#92 are real defects; #89 is fixed. They matter,
but they are cleanup, not the goal.

## Residue to clean

26 worktrees and roughly 90 stale branches remain from the retired branch model.
They are safe to prune once nothing references them.
