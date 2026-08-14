# Handoff

Rewritten 2026-08-13. This replaces a version organized around the wrong goal;
if you find a copy that opens with "no manual wiring", it is superseded.

## What this is

choudoufu is an OpenTofu fork that runs a user's **existing** OpenTofu
configuration against live cloud resources, using cloud tags as ownership
markers instead of a state file.

## The goal

**People's existing OpenTofu should work under live markers, with extremely
narrow exceptions.** Onboarding them from regular OpenTofu to live markers is
the product. Every piece of work is judged against that.

## Before you act on this document, test it

This file is a set of claims, not a briefing to absorb. The version it replaced
was absorbed whole by three sessions, and four of its load-bearing statements
were false. Nothing about this rewrite makes that less likely; it only points
somewhere better. So start by checking it.

The numbers below are mixed-confidence and the prose does not distinguish them.
Some were verified to a file and line, some are a single agent's count that was
never recomputed. Run these three first. They take under a minute and each one
tests a different claim this document depends on.

```sh
# 1. Effects are admitted behind record_store.
grep -n 'ClassRecordAdmitted && recordStoreConfigured' internal/live/lint/lint.go
# expect a hit at internal/live/lint/lint.go:267

# 2. The tier-2 connective-tissue types are genuinely not admitted.
for t in aws_ecs_service aws_lambda_permission aws_cloudwatch_event_target; do
  printf "%-32s %s\n" "$t" "$(grep -c "^	\"$t\":" internal/live/identity/table_generated.go)"
done
# expect 0 for all three

# 3. The cohort corpus size, which two agents disagreed on (657 vs 649).
grep -rhoE '^resource "aws_[a-z0-9_]+"' live/e2e/estates --include="*.tf" | sort -u | wc -l
# expect 649; if you get something else, this document's corpus numbers are wrong
```

If any of them disagrees with what is written here, trust the code and fix this
file before doing anything else.

Two other things in here are weaker than they read. The phase order after phase
2 is a prediction about what an instrument that does not exist yet will say;
`-out` plus `apply <planfile>` being the top refusal is a guess, not a finding.
And every count in the next section came from an agent; two of them were later
recomputed and disagreed with the original, which is why they are hedged there
rather than quoted.

## The invariant: no state ops (#73)

The user never configures a backend, manages a lock, or performs state
surgery. State is progressively emptied:

- identity moves to cloud tags (markers)
- receipts move to the live system, as per-estate cloud records (`record_store`,
  SSM or S3)
- what remains is **effects only**: `null_resource`, `terraform_data`,
  `time_*`, non-secret `random_*`, run through the completely stock provider
  lifecycle exactly as upstream

That gives a definition of done a user can check for themselves. After a live
apply, the plan rebuilt from markers alone is empty and the residual state file
holds nothing but effects. Anything else in there is a defect with a name.

## What to measure, and what not to

**Do not treat `live/rowgen-convergence.json` as a coverage metric.** It
measures whether row-gen's fresh proposal agrees with a human-ratified table
row. The ratified row is what ships, because `tools/row-gen/emit.go:41` copies
every field verbatim. A mismatch is generator-autonomy debt, not a failure any
user experiences, and driving it to 100% would mean the generator had memorised
a human's judgments rather than become correct. Three sessions were organized
around that number. Once it is the scoreboard, resource-shaped work is the only
thing it rewards.

The gate users actually hit is **admission**, and above that the
**config-language subset**. Measured 2026-08-13:

- of a 36-type "most commonly used" list, 35 are admitted; of the next 73, 17
  are not, and they are connective tissue: `aws_lambda_permission`,
  `aws_cloudwatch_event_rule` and `_target`, `aws_api_gateway_deployment` and
  `_resource`, `aws_ecs_service`. **Those six are verified unadmitted; the
  ratios are not reproducible** - the 36/73 list exists nowhere in the repo and
  was one agent's judgement of real-world usage. Treat them as a shape, not a
  measurement, until #102 produces one.
- the live path carries a lot of hard refusals - one audit enumerated 77,
  a later recount of distinct error-severity summaries over
  `internal/live/**` plus `internal/command/live_*.go` found 128. Neither
  defined "hard refusal" precisely enough to reproduce. The enumerated
  inventory is on #101; the count is not load-bearing for anything

**Type coverage is not the binding constraint.** A user at 100% type coverage
still fails on `backend "s3"`, `-out` plus `apply <planfile>` (how CI runs
Terraform), a non-default workspace, a CIDR-keyed `for_each`, `count.index` in
a name, or an identity argument read from a data source or a module output. The
binding rule is **static evaluability**: every `count`, every `for_each`, and
every identity-bearing argument must be computable from `var` / `local` /
`path` / `terraform` alone.

#102 builds the instrument that measures this. Until it exists, no number in
this repo predicts onboarding success.

## What is already true, and reads as unfinished

Each of these cost time in a previous session.

**Provider identity schemas are plumbed and load-bearing** (#22, closed
completed). They are read from unconfigured plugins early
(`live_plan.go:1595`), threaded into identity resolution, and
`internal/live/lint/admission.go:26` admits a type the hand table does not
cover when the schema settles it. 479 types carry one at provider 6.59.0.

**Effects already work.** `null_resource` and friends are admitted the moment a
`live` block declares a `record_store` (`lint.go:267`). The refusal message
used to claim the support "does not exist yet"; that was fixed in `05d52b319`
and now names `record_store` and shows the block to write.

**The product layer is further along than any generator document suggests.**
`live-plan`, `live-mv`, `live-import` and plain `plan`/`apply` under a `live`
block all ship. So do the configurable ownership-policy matrix (#67), provider
version-skew detection (#63), bulk migration off a state file (#61), and
resource-level `count` and `for_each`.

**The doc-scrape fields are populated.** `arguments_in_doc_order`,
`identity_schema_required` and `identity_schema_optional` sit at 43, 441 and
299 rows of `live/import-grammar.json`, and have since `d8cf48ef1`. A previous
handoff asserted all three were absent from all 1600 rows and sized a phase
around it.

## Phase order

1. ~~**Stop misreporting what ships** (#101)~~ - **done.** Every refusal message
   in lint, identity, stamp, discovery, projection and the command layer was
   audited and corrected. Its second half, generating `LIMITATIONS.md` from an
   enumerable rule table, is **#110** and is half-landed: the identity registry
   exists (`refusals.go`), the renderer does not.
2. **Build the scoreboard** (#102). Real OpenTofu configs, lint plus identity
   resolution, no cloud. Output is a ranked table of which refusals fire.
3. **Close the silent hazards, then the top measured refusals** (#103, #104).
   Silence is worse than a refusal; both of these are correctness bugs.
4. **Type coverage, correctly scoped** (#105, #106, #107, verified by #108).
5. **Finish #73 and the onboarding surface** (#81, #82, #84, #109, #72).

## How to slice the work

Fan out on **rules and stages**, never on resources. A resource-shaped slice
rewards hand-writing the row, and it is genuinely faster for the agent holding
it: fixing the extractor costs ten times more and only pays off across the
other types that agent cannot see. That much of the old handoff was right.

The diagnosis usually lives a layer above the slice. When an agent reports that
something "can't be generated", treat it as the start of the investigation.
Verify claims by recomputing them, not by reading the summary line.

## Traps that cost real time

Tests must run as:

```
env -u PWD go test -C /Users/alex/Documents/checkouts/intentius/choudoufu ./...
```

`/Users/alex/checkouts` is a symlink and `os.Getwd()` honours `PWD`, so a plain
invocation produces 10 false failures in `local-exec` and `TestFmt*`.

`gh` defaults to the wrong repo: `upstream` points at `opentofu/opentofu`.
Always pass `-R INTENTIUS/choudoufu`.

The doc cache is offline and complete at
`~/Library/Caches/choudoufu/importdocs-gen/6.59.0/`, 1699 files. Re-running a
doc sweep needs no network.

`just lint` does NOT complete: the `GOOS=windows` pass fails, `make` exits 2,
and the darwin pass never runs. About 23 seconds to that point. Six issues are
outstanding (5 staticcheck, 1 unused) and all predate this work.

## Working model

Agents work in isolated worktrees when they must build concurrently; the
orchestrator verifies and lands to `main` as single writer.

- Run `go build ./...` and the relevant tests before committing. Never commit
  red. If it cannot be made green, commit nothing and report.
- Small commits, each independently revertable.
- Do not push unless asked. CI is deprioritised; keep work local.
- When stopping an agent mid-flight, commit its work to its own branch first.

## Decisions taken 2026-08-13

Do not reopen these without a reason that did not exist on the day.

**Observational snapshots are being dropped**, not reformatted (#109; #83
closed). The live system is authoritative and readable at any time, so a stored
snapshot is a stale copy of something always re-derivable. The one load-bearing
part was #64's guided-discovery hint, which is a set of type names plus a
timestamp (`projection.Hint.Types` and `WrittenAt`; `guidedSweepUniverse` reads
nothing else). It moves into the per-estate `record_store` that #73 phase (d)
already ships, so plan cost at estate scale is unaffected. What is traded away
is the git-branch drift narrative. `snapshots` and `snapshot_path` leave the
live block and the sidecar.

**The sidecar is adopted** (#72). Docs lead with it; the in-`terraform{}` form
stays supported; both present remains an error. The reason the live block is a
block and not a flag survives the move, because a sidecar is still checked in
and reviewed. Implementation constraint: it must load at the decoder's
lifecycle point, which is the earlier wall that refuses a `backend` alongside
it.

**All 14 branches with unmerged commits are discarded**, and the two dirty
worktrees are committed to their own branches before removal. The
`admission-pipeline` workflow is deleted; its regeneration logic belongs in
#108's acceptance tier, run locally.

## Still open, but not a blocker

**`floci-capabilities.json` and the image digest** (#99, #98). The digest has
five copies, and `Makefile:237` has already drifted to upstream
`floci/floci:latest` rather than the `ghcr.io/lex00/floci` fork, so `make
test-floci-clean` cleans nothing.
