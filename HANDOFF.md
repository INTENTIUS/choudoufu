# Handoff

Rewritten 2026-08-13. Phases 3 and 4 folded in 2026-08-14, with phase 5
half-done. This replaces a version organized around the wrong goal; if you
find a copy that opens with "no manual wiring", it is superseded.

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

This is not a formality. The phase-3 session read the scoreboard table below,
copied its "three largest blockers" into five source files, and only found
out from an adversarial audit that the three rank 1, 3 and 7 with a lint rule
between the first two. The table was right; the sentence under it was not.

```sh
# 1. Effects are admitted behind record_store.
grep -n 'ClassRecordAdmitted && recordStoreConfigured' internal/live/lint/lint.go
# expect one hit; the line number drifts (267 -> 269 when #103's rule landed),
# so the hit is the claim and the number is not

# 2. The tier-2 connective-tissue types are genuinely not admitted.
for t in aws_ecs_service aws_lambda_permission aws_cloudwatch_event_target; do
  printf "%-32s %s\n" "$t" "$(grep -c "^	\"$t\":" internal/live/identity/table_generated.go)"
done
# expect 0 for all three

# 3. The cohort corpus size, which two agents disagreed on (657 vs 649).
grep -rhoE '^resource "aws_[a-z0-9_]+"' live/e2e/estates --include="*.tf" | sort -u | wc -l
# expect 649; if you get something else, this document's corpus numbers are wrong

# 4. The whole live path's refusal count, which two audits guessed at (77, 128)
#    before it was enumerable.
go run ./tools/limits-gen   # expect "163 refusals", and no diff afterwards
```

If any of them disagrees with what is written here, trust the code and fix this
file before doing anything else.

One thing in here is weaker than it reads. Every count in the "what to
measure" section came from an agent; two of them were later recomputed and
disagreed with the original, which is why they are hedged there rather than
quoted. Everything in the scoreboard and phase sections below is recomputed
from a committed artifact and reproducible by the commands above.

The phase order used to carry the same warning, because phases 3 onward were a
prediction about what an instrument that did not exist yet would say. **That
instrument now exists** and the phase order below is measured rather than
guessed. The guess it replaced was wrong in a useful way: `-out` plus `apply
<planfile>` was named as the likely top refusal, and it does not appear in the
top twelve.

## The invariant: no state ops (#73)

The user never configures a backend, manages a lock, or performs state
surgery. State is progressively emptied:

- identity moves to cloud tags (markers)
- effects with no cloud twin move to per-estate micro-state records
  (`record_store`, local / SSM / S3): `null_resource`, `terraform_data`,
  `time_*`, non-secret `random_*`, run through the completely stock provider
  lifecycle exactly as upstream

**Receipts are not part of that.** They never move onto the record store, and
the boundary is enforced: a `record_store` `key_prefix` whose first segment is
`tofu-receipts` is a decode error. A receipt stays an ordinary declared
resource, deliberately AWS-shaped so its value reads with a plain `aws ssm
get-parameter` by someone with read-only IAM and no `choudoufu` binary. A
`staterecord` payload is a tool-internal ctyjson envelope. An earlier version of
this file said receipts move to the record store; that was wrong, and
`live/RECEIPTS.md`'s "Boundary" section is the authority.

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
- the live path carries a lot of hard refusals. Two audits counted 77 and
  128 and neither defined "hard refusal" precisely enough to reproduce.
  There is a reproducible answer now: `check.AllRefusals()` returns **159**,
  assembled from a registry per stage plus the pass-through class, and every
  one of them has an entry in `live/LIMITATIONS.md`. The split is 16 lint,
  87 identity (34 its own plus 53 passed through), 26 projection, 23
  discovery, 7 stamp

**Type coverage is not the binding constraint.** A user at 100% type coverage
still fails on `backend "s3"`, `-out` plus `apply <planfile>` (how CI runs
Terraform), a non-default workspace, a CIDR-keyed `for_each`, `count.index` in
a name, or an identity argument read from a data source or a module output. The
binding rule is **static evaluability**: every `count`, every `for_each`, and
every identity-bearing argument must be computable from `var` / `local` /
`path` / `terraform` alone.

## The scoreboard, which now exists

`live/corpus-refusals.json` is the artifact to read. #102 built it: lint plus
identity resolution over 105 configurations, no cloud. 74 of them are the
`examples/` root modules of ten `terraform-aws-modules` repositories pinned to
exact commits; 31 are this repo's own fixtures. Regenerate with `just corpus`
after `just corpus-fetch`.

Top blockers, by configurations blocked out of 105:

| Configs | Sites | Layer | Refusal | Raised by |
|---|---|---|---|---|
| 66 | 3536 | identity | Unable to compute static value | `internal/configs` |
| 58 | 845 | lint | `unadmitted-type` | `internal/live/lint` |
| 57 | 1953 | identity | Dynamic value in static context | `internal/configs` |
| 49 | 415 | lint | `logical-resource` | `internal/live/lint` |
| 37 | 121 | identity | Unresolvable identity | `internal/live/identity` |
| 35 | 4254 | lint | `count-index` | `internal/live/lint` |
| 33 | 239 | identity | Module output not supported in static context | `internal/configs` |
| 27 | 75 | lint | `provisioner` | `internal/live/lint` |
| 6 | 11 | lint | `module-providers` | `internal/live/lint` |

Four things this settles.

**Static evaluability is the binding constraint, measured rather than
asserted.** Four of the top seven are static-evaluation failures.

**The largest single blocker is a diagnostic this fork does not write.**
Ranks 1, 3 and 7 are static-evaluator diagnostics passed through identity
resolution. When this table was first written they were in no registry at
all, so a `LIMITATIONS.md` generated from `lint.Rules()` and
`identity.Refusals()` would have omitted the top of its own list. #110 closed
that: `totals.refusals_unregistered` is 0, and `internal/live/passthrough`
is the registry that holds them.

An earlier version of this paragraph called those three "the top three".
They are not, and the error propagated into five source files before an
audit recomputed it: `unadmitted-type` at 58 sits between the first and the
second. Rank 1 is the part that is true.

**Admission moves a refusal downstream; it does not remove it.** This is
the finding phase 5 produced and the one most likely to mislead the next
session. #105 admitted six resource types, and `unadmitted-type` fell from
961 sites to 845. `totals.blocked` did not move at all - the six appear in
configurations already blocked by something else - and three identity
refusals went **up**, because a type admitted at lint now reaches identity
resolution and is refused there instead when its arguments are not
statically evaluable. Compare the rows above against
`git show 5d4a78d8c:live/corpus-refusals.json` to see it.

So "types admitted" is not a proxy for progress, and neither is a falling
`unadmitted-type`. The number that would mean something is `totals.blocked`,
and nothing has moved it yet.

**Read `totals.blocked` (81 of 105) as a ranking, not a rate.** Module
`examples/` lean far harder on variables, conditionals and `dynamic` blocks
than an ordinary estate, so this corpus reports worse than typical user code.
It is third-party, which is what it was missing; it is not estate-shaped, which
is #118. Do not quote the figure as a compatibility number, and do not let it
onto the docs site.

Two caveats to carry. The run covers **two of five layers** — `lint` and
`identity`; `discovery`, `projection` and `stamp` are unchecked, and the
artifact says so. All five now have refusal registries (#110), so what those
three can refuse is documented even though no corpus run reaches it. And it was measured against provider **6.58.0** while
survey-gen pins 6.59.0 (#117).

Regenerate it with `just corpus`, which now passes the provider-schema flags
the committed artifact was actually produced with. It did not, so the
documented command produced a worse artifact than the one in the tree: with
no schemas every type outside the admission table reads as refused, and
`unadmitted-type` tops the ranking for a reason belonging to the run.

## What phases 3-5 built, so you do not rebuild it

Five things landed on 2026-08-14 that later work should use rather than
reinvent.

**Every refusal the live path can produce is enumerable.** `check.AllRefusals()`
returns 163, from a registry per stage - lint 18, identity 35, passthrough
53, projection 26, discovery 24, stamp 7. The pass-through registry holds
the diagnostics this fork shows without having written them, from
`internal/configs`, `internal/addrs` and HCL; they surface during identity
resolution, so `check.Layer` files them under `identity` (88 rows) while
`Refusal.RaisedBy` names who actually wrote each one.
`check.Catalog()` is the narrower set the corpus ranks: the two passes that
run without a cloud. Do not conflate them - a zero in the corpus means
"measured and blocked nothing", and a stamping refusal has never been
measured by anything.

**A refusal cannot be added without documenting it.** `internal/live/refusalscan`
parses each package's source and fails on a summary with no registry entry.
It is strict: anything it cannot resolve to a literal is an error, not a
skip. If you add a diagnostic and the test complains, name the string - a
`Summary`-prefixed constant, or an entry in a declared summary map - rather
than working around it.

**`live/LIMITATIONS.md` is generated.** `just limits`. Two spans; the
narrative sections stay hand-written. Every refusal's `DocsRef` is derived
from its own summary, so adding a registry row adds a document heading, and
`TestEveryRefusalDocsRefIsResolvable` fails if the generator has not run.

**`live/identity-sources.json` compares the identity sources.** `just
identity-sources`. The finding that matters: the provider's identity schema
and the scraped documentation describe 438 types between them and agree on
every one, so a future disagreement is a scraper bug and there is a ratchet
holding it at zero.

**Three generators, one convention.** `internal/live/mdspan` owns the
span-marker mechanics `tools/survey-gen` and `tools/limits-gen` both write
with. A third generator writing into a shipped document should use it rather
than copy it.

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

Every open issue carries a phase label, so the tracker and this list cannot
drift apart. `gh issue list -R INTENTIUS/choudoufu --label phase-5-coverage`
is the front of the queue. Work outside the ladder is labelled `standing`.

**1. `phase-1-messages` — stop misreporting what ships (#101). Done.**
Every refusal message in lint, identity, stamp, discovery, projection and the
command layer was audited and corrected. It also spawned #115 and #116, which
are behaviour bugs the audit found underneath the messages it was fixing.

**2. `phase-2-scoreboard` — build the instrument (#102). Done.**
`live/corpus-refusals.json` ranks which refusals fire over 105 configurations.
See "The scoreboard" above. Phases 3-5 are ordered by it rather than by
judgement, which is the whole reason this ladder can be trusted now and could
not before.

**3. `phase-3-documentable` — make the top blockers documentable (#110).
Done.**
`live/LIMITATIONS.md` has a generated section holding all 163 refusals the
live path can produce, from a registry per stage plus
`internal/live/passthrough` for the diagnostics this fork does not write.
Every one carries a resolvable reference to its own entry, and a scan per
package fails when a refusal exists in code with none. `tools/limits-gen`
writes it; `just limits` runs it.

The estimate in the line above this one was wrong in a way worth keeping.
Criterion 2's generator was said to need three inputs; it needed six. The
two extra registries nobody had counted were `projection` (26 refusals) and
the pass-through class turning out to be 53 rather than the 3 the corpus
had seen fire, and both were found by adversarial audits rather than by the
work itself. A count of what
a codebase refuses is not something to estimate.

**4. `phase-4-silent-hazards` — correctness bugs with no diagnostic
(#103, #104, #115, #116). Done.**

All four closed. They never appeared in the scoreboard and never would have,
because a silent failure produces no refusal to count - which is why they
were done on principle rather than by rank.

Two of them turned out to be measurable after the fact, which is the useful
surprise. `module-providers` (#104) fires on **6 of the 105 corpus
configurations**, 11 sites: real cross-region `providers` mappings in the
rds, s3-bucket and lambda examples that live mode was silently planning
against the wrong region. The refusal made a hazard visible that no
instrument could see while it was silent.

#123 is the one thing left open from this phase, and it is a question rather
than a task: whether a root resource naming an undeclared provider alias
reaches the same empty-body fallback, or whether upstream validation already
refuses it. Establish that by running it before building anything.

**5. `phase-5-coverage` — the top measured refusals themselves
(#105, #106, #107, #108). Half done, and this is the front of the queue.**

#107 is closed. #105 and #106 are open with one acceptance criterion each
left; #108 is untouched. Read the closing comments on #105, #106 and #107
before starting - each records a correction to its own issue text, and two
of them narrow what is left considerably.

**What is left, smallest first.**

*#106's third criterion: derive the `arn` identity mappings.* The issue says
"468 components use SameNameIdentity and 95 carry an explicit name... 76
identical values is a rule nobody has written down". Those are component
**sites**, not rows. Per row it is 17: eleven `arn`, four `id`, one `url`,
one `member_account_id`. Every one of the eleven `arn` rows has a first
literal beginning `arn:aws:` - no exceptions, verified against
`table_generated.go`. `url` is the same rule with `https://`. So twelve of
the seventeen derive from the leading literal, and five rows need hand
evidence rather than ninety-five. Row-gen does not propose `IdentityAttr` at
all today (convergence calls it "identity-attrs-not-proposed (issue #44
non-goal)"), so this is a new proposal rule, not a change to an existing one.

*#105's third criterion: an end-to-end test.* Admit, plan, apply, replan
empty, against a cloud. It needs the acceptance tier #108 builds, so do #108
first or do them together.

*#108: the corpus acceptance tier.* Untouched. Its four criteria are
substantial and independent; read the issue.

**Two measurements from this phase that should shape how #108 is judged.**

Admitting six types (#105) moved `unadmitted-type` from 961 sites to 845,
moved `totals.blocked` by zero, and moved three identity refusals **up**.
See "Admission moves a refusal downstream" in the scoreboard section. An
acceptance tier that counts admitted types will report progress that nobody
experiences.

And #107's silence turned out to be one path's, not both. The estate-wide
tag sweep was already reporting those resources - under a message about ARN
parsing that described something else entirely. Check which paths a claim
covers before sizing work against it.

Note #107 argued in its own text that it belonged in phase 4 ("silence is
the one unacceptable outcome"). Working it first, ahead of #105 and #106,
was right for a reason worth reusing: #105's acceptance asks that
synthesized types either join the sweep or take #107's decision explicitly,
so #107 had to have a decision before #105 could cite one.

**6. `phase-6-onboarding` — finish #73's charter and the onboarding surface**
(#72, #73, #74, #81, #82, #109).

#84 closed with the docs-site work.

### Standing work, outside the ladder

Labelled `standing`; sixteen issues. The ones that bear on the ladder:

- **#118** — the corpus measures module examples, not estates, so its rate does
  not mean what a reader assumes. Until this lands, quote the ranking and never
  the percentage.
- **#117** — the corpus used provider 6.58.0; survey-gen pins 6.59.0. Two
  artifacts describing different providers.
- **#79** — the docs site's last two hand-written numbers want generated spans.
- **#92** and its three instances (#89, #90, #91) — silent merge loss. Not a
  phase, but the reason every merge in this repo is verified rather than
  trusted.

## How to slice the work

Fan out on **rules and stages**, never on resources. A resource-shaped slice
rewards hand-writing the row, and it is genuinely faster for the agent holding
it: fixing the extractor costs ten times more and only pays off across the
other types that agent cannot see. That much of the old handoff was right.

The diagnosis usually lives a layer above the slice. When an agent reports that
something "can't be generated", treat it as the start of the investigation.
Verify claims by recomputing them, not by reading the summary line.

## Run an adversarial audit after each phase

Two ran on 2026-08-14, one per phase, and both found defects in work that was
green, committed, and believed finished. This is not optional polish; it is
the step that made the difference between the two phases shipping what they
claimed and shipping something weaker.

What they caught, as a guide to what to ask for:

- **A completeness test that could see almost nothing.** The registry
  scanner recorded the shapes it recognised and silently skipped the rest,
  so it reported everything registered *because* it was blind. Discovery had
  2 refusals registered and 23 real ones - 24 now, after #107 added one;
  projection had no registry at all and 26 refusals. Ask an auditor to
  *defeat* a test, not to review it.
- **A claim copied without recomputing.** "The three largest blockers" came
  from this document into five source files. Ranks 1, 3 and 7. Ask an
  auditor to recompute every number in the diff.
- **A fix that made things worse.** #115's per-instance comparison bound
  `each.value` to the key on both sides, so a wrong marker over a `for_each`
  map verified silently where it had previously warned. Ask specifically:
  did this change turn any warning into silence?
- **A rule that refused working configurations.** #103's first version fired
  on types with no markers at all, explained as losing tags they do not
  have. Ask: what does this newly refuse that used to work?

Give the auditor the environment notes below, tell it to run rather than
read, and tell it that a claim surviving attack is a one-line answer, not a
paragraph.

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

Two mechanical traps that cost time on 2026-08-14:

- **Do not pipe a generator into `head`.** `go run ./tools/corpus-gen | head`
  kills it with SIGPIPE before it writes the artifact, and it looks exactly
  like a run that produced no change. Redirect to a file and `tail` that.
- **A regenerated artifact is the measurement.** After changing anything the
  generators read, regenerate and diff rather than reasoning about what
  should have moved. `just corpus` needs the provider-schema flags it now
  passes by default; `just limits` and `just identity-sources` need neither
  network nor provider.

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
