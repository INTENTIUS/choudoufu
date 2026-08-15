# Handoff

Written 2026-08-15, at `7036d117cb`. Working tree clean, `just ci` green,
nothing in flight.

This is a session handoff, not a second work queue. The work lives in the
issue tracker (`gh issue list -R INTENTIUS/choudoufu`) and the operational
brief lives in `.claude/agents/live-markers.md`, which is tracked now
(#165) and is the file to read before touching the live-marker path. A
previous HANDOFF.md accumulated four false load-bearing claims across three
sessions and was retired into the tracker for that reason; this one records
session state and the things that are true but written down nowhere else.
If you find yourself copying issue content into here, stop.

## What landed this session

Seven issues closed: #142 (estate grant policy), #171 (CI coverage of
`internal/command`), #165 (the brief is tracked), #152 (generated SCP action
list), #161 (live-check variable attribution), #155 (CFN schema facts),
#145 (floci pin drift), #135 (separator scraping). #136 is part-done and
open.

## The repository's history was rewritten

An 8.9MB arm64 binary, `iamref-gen`, was committed in `d7ec51f6ec` and
purged with `git-filter-repo` across all 34,268 commits. Every commit from
that point forward has a new hash. `backup-pre-iamref-blob-purge` is the
local pre-purge branch.

Two consequences that are not obvious:

- **The old objects are unreachable on GitHub but not gone.** They survive
  until GitHub GCs them and stay fetchable by SHA until then. If the binary
  genuinely must be unrecoverable, that needs GitHub Support.
- **Any clone or worktree taken before 2026-08-15 has the old history.** A
  rebase from one of those against the rewritten `main` will look like a
  divergence of hundreds of commits. Re-clone or hard-reset rather than
  merging your way out of it; that exact mistake put eight ancient Terraform
  commits onto `main` mid-session.

`backup-pre-blob-purge-reroot`, from the earlier 2026-08-14 rewrite, does
not contain the purged commit and was untouched.

## Where #136 stands

Two commits in: `ded965af19` extracts each type's documented example into
`live/import-grammar.json`, `d425a161c7` seeds `estate-gen` from it and
retires the first override.

- **585 override entries remain**, 229 of which have a clean documented
  example and are the candidate pool.
- **The retirement test is regenerate-and-diff, not validate.** An override
  is safe to delete when the regenerated cohort is byte-identical apart from
  its `# overrides:` provenance line. `terraform validate` passing is weaker:
  it does not see apply-time requirements, and the acceptance tier cannot
  discriminate either while #149 stands.
- The seed is suppressed for any type that still has an override, so
  retirement is one type at a time and each step is checkable.

Run the drift and validate tiers with:

```
TF_FLOCI_TEST=1 env -u PWD go test -C "$(git rev-parse --show-toplevel)" ./tools/estate-gen/ -timeout 40m
```

Both were green at `d425a161c7`, with `knownDrift` and `regenGaps` empty.

## Regenerating cohorts in bulk

Use the drift test's own `recordedRegenTypes`, which prefers `GENERATED.md`
over `README.md` and handles shell continuations. Do not grep `-types` out
of the READMEs: doing that cut `compute-platforms` from 28 resources to 4,
and the only reason it was caught is that the net diff was −431 lines for a
pass that only adds. There is no `-regenerate-all` mode; the way to do it is
a temporary in-package test that reuses that function, which is how the last
sweep ran.

## Things measured this session that contradict what was written down

Each of these was believed, is false, and would otherwise be believed again.

- **`identity_schema_required` cannot disambiguate separators** for the rows
  #135 was about. It is empty on 22 of those 23. The issue proposed it as
  the mechanism.
- **`additionalIdentifiers` was never discarded** by `registry-gen`. #155's
  table listed it as one of four missing fields; it has been parsed and
  emitted all along, on the 208 types the table itself counts.
- **No upstream file is gofmt-dirty.** The CI comment said "a handful of
  upstream OpenTofu files are gofmt-dirty as inherited" and used it to
  justify a narrow gofmt scope. The measurement is one dirty file
  repo-wide, and it was fork-added.
- **`survey-gen` does not use `mdspan`.** The brief said it did. The four
  that do are `estate-gen`, `iamref-gen`, `limits-gen`, `tagverbs-gen`.
- **`live/plan-budget.json` was measured against a different emulator image**
  than the current pin, and nothing said so. It is now a recorded exception
  in `live/flociimage_test.go` rather than a silent one.

## The one thing worth generalising

Four separate defects this session were the same shape: a check whose unit
was the directory guarding a fork whose unit is the file (#156, #164, #171),
a caveat attached to a whole report rather than to the refusals it explained
(#161), an override "winning" only on the arguments it happened to set
(#136), and an ARN's internal `:` read as a join (#135). In each case the
rule was correct about the case its author had in mind and silently wrong
one level out.

The habit that caught all four was the same: regenerate, then diff, then
read the diff — and when a number moves the wrong way, find out why before
explaining it. The ARN defect surfaced because `rowgen-convergence.json`
flagged three rows, an artifact whose headline number is explicitly not
worth optimising for. It is still a good change detector.

## What I would pick up next

1. **#136's retirement batches** — the setup is done and the verification
   loop is defined above. Highest value per unit of risk.
2. **#139**, COVERAGE.md's generated spans. Bounded, and its PROPOSE section
   is separately wrong today.
3. **#162**, the `live/*.md` brevity pass, now that no other agent holds it.
4. **#132** is the large one and is unchanged: 82 extraction gaps and 102
   ratification judgments behind the emit gate.

#170 is deliberately untouched — the user asked for it to be left alone.
