# How to work this repository

This file is the standing playbook. It says what the work is for, what makes
a change acceptable, and how to take a task from the tracker to a merge.

It carries **no ladder table, no site counts and no rankings**. Several
earlier versions did, every one of them went stale within the hour, and one
shipped two rows that were wrong when written. The numbers live in artifacts
that regenerate and in the tracker; this file says how to compute them.

Read `.claude/agents/live-markers.md` next for the operational detail.


**The bar, stated once, plainly: if OpenTofu can run a configuration, choudoufu must run it too. If OpenTofu can't, choudoufu documents that as a limitation and moves on.** Everything below - the standing bar, the decision matrix, every ranked item below - is that one sentence applied to a particular wall. When in doubt whether something is worth fixing, this is the test to run first, before reading anything else in this file. "Parity is the bar," further down, is the same rule with the three-label operational detail (a wall is not automatically a defect; it might be parity, or it might be a question stock OpenTofu never had to answer at all).

---

## What the product is

choudoufu is a fork of OpenTofu that replaces the state file with ordinary
cloud tags. Each resource carries its own ownership record, so AWS itself can
say what an estate contains and your existing IAM decides who may read or
change it. An estate is inherited by being granted access to it: handover is
granting a role, splitting an estate in two is rewriting tags.

Three carriers do the three jobs a state file does. Which real resource an
address refers to is a **marker**, two tags on the resource. Values AWS has
nowhere to put go in a `record_store`. Effects that leave nothing behind to
read back get a **receipt** that tracks staleness.

Everything outside live markers is stock OpenTofu, from fork point
`03743ce6e8`.

**The product's output is a string in a cloud tag.** Not a verdict, not a
count. A marker that is wrong is worse than a marker that was refused,
because a wrong one gets written to a real resource and adopts or displaces
something. Most of this file follows from that.

## The invariant

Read this before anything else here, because every strategy this repository
has abandoned was abandoned for forgetting it.

**A migrated estate is tagged.** `internal/live/stamp` writes `tofu-estate`
and `tofu-address` onto every taggable managed resource, and it reads
taggability off the provider schema rather than off a list of type names. What
carries no tag is the association, attachment and membership family, and those
are admitted precisely because their identity is a composite of parents that
are tagged.

So a migrated estate is **tagged, plus derived-from-tagged. There is no third
bucket.**

Two things follow, and both have cost this project months.

**Untaggability is not an identity problem.** It bounds what an
`aws:ResourceTag` condition can govern, which `live/MARKERS.md`'s "What this
grant cannot reach" states with its own generated figure. It says nothing
about whether an estate can be planned. A wall framed as "untaggable"
measures the marker and reports it as identity.

**A test population that violates the invariant is testing adoption.** An
estate whose parents carry no markers is a half-adopted stranger's estate. It
is a legitimate thing to measure and it is not the product.

## What the campaign is

**The goal is a fully migrated estate**: someone writes ordinary Terraform,
adds a `live` block, applies, and choudoufu manages it from then on with no
state file anywhere, with markers on every taggable resource and derivables
hanging off tagged parents.

The nearer goal, and the one every open task now serves, is that a small,
popularity-weighted set of **real** OSS Terraform and OpenTofu estates - not
synthetic fixtures - runs the full migration cleanly through five stages:
cold deploy with no choudoufu involved, `choudoufu live-import` adoption, an
empty replan with the rendered identities asserted by value, a genuine
no-op apply, and a drift injected out of band that reconverges to exactly
the object mutated and nothing else. `live/corpus-crossing-manifest.json`
is the record of which estates clear which stage and why the rest do not,
yet - read it for current state; nothing here restates it.

This replaced `tools/estate-gen`'s exhaustive synthetic-cohort campaign as
the driving instrument on 2026-08-18, on measured evidence rather than a
preference. The highest-leverage fixes found that night - a live-import
walker that skipped every resource inside a `module` block, a type-admission
check that ran per declared block instead of per resolved instance, a floci
bug that silently replaced a stamped resource's tags instead of merging them
- were all found by crossing real, popular modules, and each one generalized
to every other estate using the same idiom, because popular code reuses a
small number of idioms heavily. estate-gen's own synthetic cohorts, run the
same night against the same floci, failed almost entirely on narrow,
non-generalizing gaps in floci's coverage of exotic AWS services nobody's
real Terraform reaches for. Exhaustive synthetic coverage does not have the
generalizing property; popular real code does.

`tools/estate-gen` keeps a narrower, still-real job: exhaustive **static**
coverage - does every admitted type at least render a schema-valid
configuration - without needing Docker or floci at all. That is cheap and
still catches real things. It is not the number that says whether the
product works.

### Sourcing a real estate

Two lanes. Terraform-popular: the `examples/` directories inside the
most-downloaded modules on the Terraform Registry, `terraform-aws-modules/*`
first, pinned by tag and commit in `live/corpus-manifest.json` -
popularity-weighted by construction, not by which estate happened to be lying
around. OpenTofu-native: real, actively-maintained projects that describe
themselves as built for OpenTofu specifically, not merely compatible with it
- sourced by GitHub search and the Powered-by-OpenTofu and awesome-opentofu
lists, since there is no download-count proxy at OpenTofu's current scale the
way there is for Terraform. This lane exists for a reason narrower than "more
samples": it is the only one that can exercise OpenTofu-only surface -
provider `for_each`, state encryption, `.tofu`-suffixed files, OCI-sourced
modules and providers - which no Terraform-authored estate, however popular,
will ever reach.

### Adoption is a different question, and the offline corpus only measures that one

`choudoufu live-check` reads a configuration directory with no cloud
credentials and says whether an estate could be **adopted**: taken over as it
stands, with no markers on anything. That is the hardest thing this fork ever
does, and every offline instrument here measures it, because every
`live/corpus-manifest.json` entry is somebody else's published configuration
with a backend block and no `live` block.

Adopting cold means deriving an identity for an object nobody has marked. A
migrated estate has the marker already, so a whole family of these refusals
is about a problem the product does not have.

**Do not read an offline corpus figure as a statement about the product
working.** That is what the live crossing pipeline is for instead - it
actually declares the `live` block, actually migrates, actually asserts an
apply and a reconvergence. `live/corpus-manifest.json` (offline, adoption
only) and `live/corpus-crossing-manifest.json` (live, the real pipeline) are
pinned separately and answer different questions. A pass in one is not a pass
in the other.

The corpus is materialized by `tools/corpus-fetch`. Only a subset are
rate-capable published deployments; the rest are fixtures and module
examples. Establish which population a number is over before you use it, and
count it rather than quoting a count.

---

## The standing bar

Four rules. They are the maintainer's, they are not negotiable inside a task,
and a change that violates one gets reverted regardless of how green it is.

### Everything must be derived

This is a generator. Identity tables, admission rulings and import grammars
are produced by `tools/row-gen`, `tools/survey-gen`, `tools/importdocs-gen`,
`tools/mapping-gen` and `tools/estate-gen` from provider schemas, scraped
documentation and CloudFormation metadata.

**A fix that names a concrete `aws_*` type in generator control flow is the
wrong fix.** It buys one estate and leaves the next to be hand-wired by
somebody who no longer knows why the first one was.

The right move is to find the property the type actually has, derive the rule
from it, and then report how many *other* types the rule reaches. If that
number equals the number you set out to fix, you have written a hand-list with
extra steps, and you should say so rather than land it.

The worked example: sibling references between resources were hand-wired per
type pair until they were re-derived as generic `<base>_ids`/`<base>_arns`
arguments, which deleted the hand-wiring and covered pairs nobody had
enumerated. A second one from the live-crossing campaign: a resource block
whose `count`/`for_each` provably resolves to zero instances was refused for
admission per-block rather than per-instance, until `blockHasNoInstances`
read the answer off the same expansion signal identity resolution already
trusted - one generic predicate, not a growing list of count-gated type names.

Where a ruling genuinely cannot be derived it goes into a named ledger with
its evidence and a ratchet, never into a generated file.
`contributing/LIVE-TABLES.md` says which ledger and why.
`live/derivation_guard_test.go` makes this mechanical rather than a rule
someone has to remember: every place a concrete provider type name is
hand-wired in Go carries a registered reason and an exact count
(`TestEveryTypeLiteralSurfaceIsRegistered`), and a name assembled at runtime
to dodge that registry is caught separately
(`TestNoTypeNameIsAssembledFromLiterals`).

### Parity is the bar

Match stock OpenTofu and go no further. If upstream accepts a configuration
that we refuse, that is a defect, and the fix is to accept it rather than to
document the refusal.

The corollary catches people out: **refusing is not automatically the safe
answer.** Before landing a new refusal, run the same configuration through
stock and say what it did.

**A wall is not "parity" by default, and every wall reported needs one of
three labels stated first, before any other detail.** This was gotten wrong
in conversation on 2026-08-20 - a wall was described as choudoufu "inventing
a capability nobody needs," when it was actually an ordinary case the
decision matrix below already names. The three labels:

1. **"OpenTofu fails here too."** Not a defect. Confirm stock refuses the
   identical configuration and stop - this is the `PARITY` row in the
   decision matrix.
2. **"OpenTofu succeeds, choudoufu refuses."** A real parity defect by the
   rule two paragraphs up. Always worth fixing.
3. **"OpenTofu was never asked this question."** The wall needs choudoufu to
   derive, with no human in the loop, something stock OpenTofu only ever
   gets from a human typing a pre-known answer into `terraform import` -
   composite import-ID component order with no wire identity schema is the
   worked example (`#309`'s Cognito wall: the provider's own Go code knows
   the order, it is simply never published as a schema, so a human reading
   the docs and typing the right string is doing the derivation stock
   "solves" this with). There is nothing to run through stock for
   comparison, because stock never attempts the autonomous case at all. This
   is not "parity absent" and it is not invented, out-of-scope work either -
   it is the decision matrix's `DEFER` row ("not knowable at plan time at
   all - read it, record it, or order around it"), same family as `#313`'s
   live-read precedent: try the finite set of candidate answers against the
   provider's own real API and let the provider's own logic - the one thing
   that actually knows the answer - decide, instead of guessing from
   ambiguous prose.

Label 3 is real, buildable product work, not scope creep beyond parity - it
serves choudoufu's own promise (adopt with no human and no state file), which
was never a promise stock OpenTofu made or was measured against.

### A wrong marker outranks a missing one

A refusal is visible and annoying. A fabricated or misdirected marker is
silent, gets written to a real resource, and can adopt another instance's
object or leak one.

**And a wrong identity is invisible to every verdict-level check.** This was
measured twice, five months apart. First: `live/e2e/per-element` was run
against floci with the canonicalising sort in
`internal/live/identity/perelement.go` deliberately removed. The plan stayed
empty, the second apply added nothing, and the foreign sweep came back clean,
because the provider splits that import ID on `/` and puts the tail in a set,
and a set has no order. Only the assertion on the rendered string caught it.
Second, and worse: a floci bug replaced a stamped resource's tags outright on
an incremental update instead of merging them, so `apply` silently dropped
`tofu-address`/`tofu-estate` from the live object while the plan choudoufu
showed, and the exit code, were both clean.
`internal/live/lifecycle/marker_tag_merge_live_test.go`'s
`TestMarkerSurvivesIncrementalTagUpdate` pins the second one; it is gated
behind `TF_FLOCI_TEST`/`TF_ACC` because it needs a real emulator, not because
it matters less.

Convergence is not evidence that an identity is right. Assert the rendered
identity itself.

### No claim without a measurement

A closed issue needs a closing comment naming the number that changed and the
commit it was computed at. Twelve issues were once closed with a figure and
six of those figures were right; the rest argued from a comparison rather than
a run.

---

## Why the offline corpus keeps pulling work the wrong way

`tools/estate-plan` ranks blocked, unmigrated, third-party estates by fewest
remaining blockers. It is a legitimate instrument for the offline adoption
campaign and it is not an assignment for the product. Four reasons, all
structural:

1. It can only measure the PUBLISHED form. No corpus entry declares a `live`
   block or a `record_store`, so nothing it prints describes a migrated
   estate.
2. Fewest-blockers-first is the hard tail by construction. Everything ordinary
   cleared long ago and left the list, so the top line is reliably an exotic
   estate with one exotic thing left.
3. terraform-aws-modules examples are excluded from the rate, correctly, which
   also keeps the code people actually write from reaching the top line.
4. Nothing there measures whether a migrated estate APPLIES.

The live crossing pipeline does not share these four failure modes, and that
is by design rather than luck: it DOES declare a `live` block and actually
migrate; sourcing is popularity-first, not fewest-blockers-first, so the
estate at the top of the list is the one the most people already run, not the
one with the least left to fix; `terraform-aws-modules` examples are the
lane's own primary source rather than excluded from it; and the whole point of
stages four and five is measuring whether a migrated estate applies and
reconverges. Read this section as the reason those five stages exist, not as
an argument against sourcing from real estates at all.

`just onboarding-gap` narrows (1) for the offline corpus and does not close
it. It applies `internal/live/onboard`'s computed edit - a live sidecar
declaring `record_store "local"`, backend or cloud block removed - and
re-analyzes the text. The result still describes an estate where **no
resource carries a marker**. Onboarded form is not migrated form; only an
actual `live-import` against a running cloud does that.

**Never assign by refusal class.** That was tried for a full day: 1570 sites
cleared, the ladder unmoved. The median blocked estate carries about two
blocking classes, so clearing one class across forty estates leaves forty
estates blocked. The refusal-class issues in the tracker are background on a
blocker, never an assignment; the `wall-class` label is retired.

---

## Where the work is

The unit of progress is **a real, popular estate crossed clean**, and
`live/corpus-crossing-manifest.json` is where each one's progress is
recorded - never in this file.

### The loop

1. Pick the next estate. A module several OTHER popular modules depend on
   (security-group, vpc) beats an isolated one, because a fix found there
   reaches every dependent; an estate close to its own five-stage pass beats
   a fresh one with an unexplored blocker.
2. **Cold deploy**: plain `terraform apply` (or `tofu apply` for an
   OpenTofu-native estate), unmodified, no `live` block, no choudoufu
   involved - the honest proof the estate is real and buildable, and the
   source of genuinely unmarked live infrastructure the next stage adopts.
   Onboarding deltas (a provider pin, the emulator's connection flags) are
   expected and asserted; a resource-shape change needs the script's own
   header to say exactly why.
3. **Migrate**: `choudoufu live-import -approve` against the cold state.
4. **Test plan**: delete the state file, `choudoufu live-plan`, and assert
   the plan is EMPTY *and* assert a representative set of rendered identity
   strings against the AWS CLI's own answer - "a wrong marker outranks a
   missing one," above, is why the second half is not optional.
5. **Test apply**: apply the empty plan; assert a genuine no-op by comparing
   the estate's tagged-object count before and after.
6. **Drift and reconverge**: mutate one live object out of band, directly
   against floci, replan, and assert the diff proposes fixing exactly that
   one object and nothing else. `BREAK=1` corrupts the assertion and must
   make the script fail, or the check was never load-bearing.
7. When a stage refuses on a genuine choudoufu gap, fix it generically - the
   decision matrix below still applies. Confirm the fix reaches more than the
   one estate that found it before calling it done; if it does not, say so
   rather than routing around the estate to make a number move.
8. When floci cannot serve what the estate needs, that is a floci work item
   and not a reason to skip the estate. See "Traps" for the specific ways
   this has gone wrong.
9. Record the real result in `live/corpus-crossing-manifest.json` yourself,
   from the crossing's own verified output - not from a report taken at face
   value. That file is orchestrator-maintained on purpose, so concurrent
   crossings never fight over one JSON file's sort order.

### The decision matrix

A refusal is usually a statement about where an identity lives. Which carrier
it should have been is what decides the work.

| Action | The identity is | The fix is | Done when |
|---|---|---|---|
| `ADOPTION-ONLY` | on the resource, as a marker | classify, do not refuse | the estate plans with the marker binding it |
| `DERIVE` | in the configuration, and the analysis does not reach it | extend the static evaluation | the value renders; assert on `ImportID`, never a boolean |
| `ADMIT` | knowable, but the type has no table row | a generator reaches it, or a ruling says it cannot | the row emits and `-convergence` exits 0 |
| `DEFER` | not knowable at plan time at all | read it, record it, or order around it | the estate plans without it, and the marker is right when it lands |
| `RULE` | refused on purpose | a maintainer decision, not code | out of scope; skip the estate |
| `PARITY` | absent for stock too | nothing | confirm stock refuses identically, then stop |

The distinction that decides the row: **the marker names an object, so it
answers an identity-value refusal. It cannot answer an expansion refusal,**
because the marker value *is* the instance address and an unknown key set
means an unknown address. `count` and `for_each` stay analysis or parity work.

### Reading the tracker

`gh issue list -R INTENTIUS/choudoufu`. A bare `gh` in this clone resolves to
`opentofu/opentofu`, silently. Pass `-R INTENTIUS/choudoufu` or run
`gh repo set-default INTENTIUS/choudoufu` once. The same trap exists one
level down: a bare `gh issue create` inside `~/checkouts/floci` resolves to
`floci-io/floci` (upstream) rather than the fork; pass `-R lex00/floci`.

An issue title's figure was honest when written and several populations have
been recomputed since. Never rank off one without recomputing.

---

## Picking up a task

**1. Establish where main actually is.**

```
git log --graph --oneline -15
git status --porcelain
```

`git log --oneline` orders by date, not ancestry. It will happily show a fix
above an artifact that does not contain it. Use `--graph`.

**2. Confirm the tree is green before you touch it.**

```
just ci
```

This is the fast tier, deliberately, not a full-module test run over the
whole module: everything under `internal/live/`, `tools/`, `live/`, `cmd/`
and `internal/command/` is every fork-owned package, and a full-module run
walks several hundred packages inherited from upstream OpenTofu that this
fork's own changes never touch. Every real regression the live-crossing
campaign has actually hit surfaced inside the fast tier's own scope. Reserve
a full-module run for a
periodic wider checkpoint, not for routine post-merge verification.

Read the exit code from a file, never from the tail of a compound command.
One background wrapper reported exit 0 while its log said 1, because the
status belonged to a trailing `echo`.

**3. Scout before you fix.** Re-verify the issue's claim against the code.
Roughly half the briefs written in this repository have been materially wrong.
A report with no commit is a good outcome.

Check the issue is not already fixed:

```
git merge-base --is-ancestor <sha> main
```

`git log --grep` proves only that a commit exists somewhere. An issue was once
closed citing a commit that sat on an unmerged branch whose test file did not
exist on main.

**4. Split anything over about thirty minutes.** Long tasks here are almost
never harder tasks; they are two jobs in one slot. Scouting is a separate job
from fixing.

**5. Work in a worktree.**

```
git worktree add ../wt/<name> -b live/<name> main
```

Base it on **local** `main`, not `origin/main`. This clone's `origin/main` has
gone stale by hundreds of commits at a stretch this session, and a worktree
built from it silently loses every fix landed since - see "Traps."

---

## Measuring

Three instruments, answering different questions. You usually want the one
that matches the estate you are working on, and you should know what none of
them can see.

**Before any of them: the offline corpus measures the PUBLISHED form, and
choudoufu is a thing you migrate to.** Not one `live/corpus-manifest.json`
entry declares a `live` block or a `record_store`. So a refusal that the
onboarding edit clears is not a language wall, it is the estate not having
been onboarded, which is true of all of them - and a refusal that a *marker*
would clear is not one either.

**`live/corpus-crossing-manifest.json` records the live pipeline.** Per
estate, per stage - `pass`, `fail`, or `not_run`, where `not_run` means the
estate's own script does not yet exercise that stage, which is itself a gap
worth closing rather than a silent pass. Update it from a script's own real,
verified output, in an isolated worktree, never by editing it directly from
two concurrent crossings at once.

**`tools/refusal-probe` counts refusals** in the offline corpus.

```
go run ./tools/refusal-probe -out before.json          # ~20s, no schemas
go run ./tools/refusal-probe -schemas -out before.json # ~3min warm
go run ./tools/refusal-probe -diff before.json,after.json
go run ./tools/refusal-probe -entry .corpus/vpc -v
```

It writes where you point it, so several people can measure concurrently in
one tree. `just corpus` cannot.

**Pass `-schemas`.** Without it `LocatedType` fails closed and
`markerless-type` reads as a blocker that a `record_store` already answers.
The default mode is blind to the whole stamp layer and to every rule that
returns false when schemas are nil. Its bound is asymmetric: it over-reports
sites and under-reports the verdict.

A fresh worktree has no `.corpus`; it is gitignored. Get it with
`just corpus-fetch`, or symlink one in - the pattern is in the comment above
`/.corpus` in `.gitignore`. `-diff` refuses any pair whose difference is not
the change under test.

**What the probe cannot tell you today**: which resource type a refusal fired
on, for anything in the identity layer. `check.Site.Type` is populated only by
the type-shaped lint rules, so the cause axis reads `reference:*` or empty for
every identity refusal.

**`TestIdentityGolden` pins the rendered value.**

```
env -u PWD go test ./internal/live/check/ -run TestIdentityGolden
```

1577 rendered identities across 508 configuration directories in under a
second, with no generator, schemas or network. Address, class, `ImportID`,
identity attributes.

This is the only offline instrument that measures what a marker will say
rather than whether something refused. Six defects shipped green because
nothing did that. **If your change moves a line, explain it. Do not run
`-update` to make it quiet** - `TestIdentityGoldenShapeIsPinned` will stop you
anyway.

**A cohort's or a crossing's real verdict is never the test-runner's own
checkmark.** `TestCohortAcceptance`'s per-cohort subtest reports `--- PASS`
for behaving as recorded, which includes a cohort recorded as failing - the
real verdict is the `<name>: pass/fail (phase ...)` line one grep away
(`acceptance_live_test.go:77:`), and the parent test only fails when a
previously-passing cohort regresses. A crossing script is the same shape from
the other direction: read its own `PASS`/`FAIL` output line, never infer it
from an exit code alone, and never trust a stage's "pass" from a script that
has not been re-run since the fix it depends on landed.

### Two numbers, not one

Report **sites and instances** together. Sites falling means the analyzer
stopped complaining. Instances rising means resources that could not be
identified now can.

But instances rising is not by itself good news. Reverting a known conversion
defect fabricated three identities and lost two correct ones, and the instance
count went **up**. Every aggregate this repository records called that
regression an improvement. Only a value assertion separates the two.

**"Entries WORSE must be 0" is not the gate it reads as.** It has twice
flagged a correct fix as a regression the same way: a refusal that fired once
at the block level starts firing per argument, because the block now expands
where it previously refused wholesale. Five honest refusals naming an argument
each are more information than one naming a block.

Maintainer ruling, 2026-08-17: **sites are not the measure.** What a change
must not do is lose an instance or change a rendered identity. Report entries
worse by site count, explain each, and do not revert on that number alone.

### Caveats that travel with any offline figure

Layers come in three lists, not two. Lint, identity, dataread and stamp are
fully checked. **Projection is partly checked**: 2 of its 27 refusals need no
cloud. **Discovery is unchecked**, though 4 of its 25 are computable offline;
the other 21 are verdicts about listed cloud objects.

So `clean` does not mean "this onboards", and it certainly does not mean "this
applies". The corpus does not install registry modules by default, so an entry
with module calls measures a fraction of its refusal surface.

---

## Landing a change

**Assert on rendered identities.** `res.ImportID` and `res.IdentityValues`,
never a predicate boolean. Predicates have been green while markers were wrong
six times; a duplicate-marker bug shipped with a passing analyzer.

**Assert the instance count separately from the key set.** One bug's entire
signature was two instances where OpenTofu makes three.

**Mutation-check every boundary fixture.** Remove the stated obstacle and only
that, and confirm the case then resolves. The same technique applied to a
guard is how you find out the guard is decorative.

**A test nothing runs is not a test.** One harness sat red on main for weeks,
wired to no `just` recipe, no CI step and no README mention. Wire it, then
break it deliberately once and watch it go red.

**Regenerate, never hand-merge, a generated artifact** - with one narrow,
verified exception. Reconciling two branches that each independently added a
digest block to `live/floci-capabilities.json` is safe to splice at the JSON
level (parse both, insert the one new block, re-sort) rather than re-running
the generator against every prior digest again, provided you diff the result
programmatically and confirm every other digest's block is byte-identical
before committing. Doing this with a general-purpose JSON writer instead of
the generator's own has broken the file's escaping twice: the rest of the
file uses Go's default HTML-safe `encoding/json` escaping (`<`, `>`, `&` as
`<`/`>`/`&`), and a writer that does not replicate that
produces a multi-thousand-line spurious diff around every unrelated ASCII
comparison operator already in the file.

**Run a generator twice and diff, but know what that proves.** It catches
nondeterminism. It does **not** prove the artifact is the one its inputs
imply. `row-gen -emit` reads its own previous output, so byte-identical across
two runs means "this is *a* fixed point", not "this is correct". Emptying
`DefaultTable`'s literal and running `-emit` twice yields a 14-row table,
byte-identical, exit 0, 878 rows gone. That is #263, and the restore is
`git checkout --`, never a re-run.

**`marksafe` guards `internal/live`.** A new call to a cty accessor needs a
proof its receiver cannot be marked, `ContainsMarked` before anything that
iterates, and a new package under `internal/live` must be classified.

---

## What is enforced, and what is not

Prose is re-read only by whoever happens to read it. So rules become tests
whenever they can. When you find yourself writing a rule into a document, ask
first whether it can be a test.

| Guard | What it holds |
|---|---|
| `TestCIRunsEveryForkOwnedTestPackage`, `TestCIExclusionsAreReal` | every fork-owned test package is in CI's glob, or excluded with a reason |
| `TestFlociMeasurementsMatchThePinOrSayWhyNot` | a measurement is current, or its exception still applies |
| `TestIdentityGoldenShapeIsPinned` | the golden's shape, so `-update` alone cannot silence a regression |
| `TestBurndownBoundsHold` | every migrated ratchet, each computing its own number and pinning the denominator it is a fraction of |
| `TestEveryToolHasAGitignoreEntry`, `TestNoCompiledBinaryIsTracked` | no multi-megabyte binary lands in a commit again |
| `TestOperationalBriefIsTracked`, `TestLocalAgentStateStaysUntracked` | `.claude/agents/`, `.claude/skills/` and `.claude/scripts/` survive a fresh clone; nothing else under `.claude/` does |
| `TestMarkerlessVetoNeverContradictsClientNaming` | the marker stays delete permission and does not become identity |
| `TestRejectedLedgerIsDisjointFromAdmitted` | a type cannot be both admitted and vetoed |
| `TestCauseCatalogCoversEveryCause` | a new discovery cause appears in the probe's breakdown without an edit |
| `TestEveryTypeLiteralSurfaceIsRegistered`, `TestNoTypeNameIsAssembledFromLiterals` | every hand-wired provider type name in Go carries a registered reason and count, and none is built at runtime to dodge the registry |
| `TestMarkerSurvivesIncrementalTagUpdate` | a stamped resource's markers survive an incremental tag update through a real emulator - gated behind `TF_FLOCI_TEST`/`TF_ACC` |

The bounds those ratchets used to carry as scattered constants live in
`internal/live/harness`, one entry each, computing their number at run time
and naming the denominator they are a fraction of. `live/HARNESS.md` renders
them.

What they have in common is worth copying deliberately. Each is a registry
checked against the tree rather than a hand-list. Each exception is written in
Go and carries its reason. Each fails in **both** directions: when the thing
it guards moves, and when the guard itself stops describing anything real. A
pin on something that no longer exists passes forever.

---

## Traps

Every one of these has been hit, most more than once.

- **`origin/main` is not `main`.** This clone's `origin/main` goes stale by
  hundreds of commits at a stretch, because most sessions push nothing while
  they work. "Fetch main and base a worktree on it" has repeatedly resolved
  to `origin/main` by mistake, silently dropping every fix landed since -
  including, twice, a regression that had already been fixed and merged.
  Base worktrees on local `main` explicitly: `git log --oneline -1 main` in
  the shared checkout, not `origin/main`.
- **The same trap exists one repository over.** `~/checkouts/floci`'s own
  `origin/main` goes stale the same way when a fix is merged locally and not
  pushed. Two independent fixes landed as sibling branches off the same
  stale base more than once, each one silently regressing the other until
  reconciled by hand.
- **A local multi-arch `docker buildx build --push` is the real cost behind
  a "30-minute build," and it is the wrong tool.** Building
  `linux/amd64`+`linux/arm64` together on a single arm64 Mac cross-emulates
  the non-native architecture through QEMU, sequentially, while competing
  with every other agent running concurrently for the same CPU. Re-running
  the full Maven suite locally on top of that makes it worse. floci's own
  CI already does both jobs, off this machine: `ghcr-publish.yml` builds
  each platform natively in parallel, one job per platform on its own
  GitHub-hosted runner, and finishes in minutes; `ci.yml` runs the full
  test suite on a separate runner at the same time. Neither competes with
  local agent work. The right sequence once floci fixes are merged to
  local `main`: fetch `origin/main`, confirm local `main` is a fast-forward
  of it (merge first if it has moved - this is the same staleness check as
  the bullet above, only checked in the other direction before pushing), push,
  then poll `gh run list -R lex00/floci --workflow=ghcr-publish.yml --limit 1`
  and `--workflow=ci.yml` instead of rebuilding or re-testing locally - both
  finish long before a local multi-arch build would. Batch several floci
  fixes into one push rather than pushing per fix; the push itself is now
  cheap, but a CI/publish cycle per single-line fix still isn't free.
- **A capability-manifest block regenerated from a stale worktree fails
  silently plausible-looking tests.** `tools/floci-capability-gen` gained
  a stricter evidence methodology partway through this campaign (self-
  expanding the service watchlist, requiring a create/list round trip
  before a `cloudcontrol-list` row can claim `implemented`); a worktree
  built from a commit before that landed regenerates a digest's block with
  the old, looser methodology, and `TestFlociServiceCapability`,
  `TestCloudControlListRowsRecordAnAnswerNotACall`,
  `TestNoCloudControlListRowClaimsImplementedOnABareCall` and
  `TestCloudControlListGateSkipsForAListThatCannotFindWhatExists`
  (`internal/live/flocitest`) all fail together. This has happened twice.
  The fix is the same as any stale-worktree problem: regenerate the one
  digest's block again from a worktree on current local `main`.
- **Uncommitted changes in the shared floci checkout are not automatically
  garbage.** Multiple sessions can work `~/checkouts/floci` at once with no
  worktree isolation between them (unlike this repo, which sends floci-side
  fixes through an isolated worktree on purpose). Finding modified files
  with no commit behind them there is as likely to be another session's
  real, in-progress fix as it is to be leftover cruft - once, it turned out
  to be the exact correct fix for a gap this campaign hit independently two
  crossings later, still sitting uncommitted. Read the diff before assuming
  either way; never commit, discard, or stash someone else's uncommitted
  work in that checkout without knowing whose it is.
- **A subtest checkmark is not the verdict.** See "Measuring," above -
  `TestCohortAcceptance` and any crossing script both report their real
  result on their own printed line, never on the test runner's summary
  alone.
- **Do not run a full-module test after every merge.** One orchestrating
  session ran a full `go test ./...` - several hundred packages, most of
  them inherited from upstream OpenTofu and untouched by anything this fork
  changes - after nearly every individual merge for an entire night, dozens
  of times over, before the maintainer pointed out the waste directly. `just
  ci`'s fast tier (`internal/live/`, `tools/`, `live/`, `cmd/`,
  `internal/command/`) is the actual pre-push gate CI runs, and it caught
  every real regression that night too, in a fraction of the time.
  `go build ./...` first is still cheap and worth doing every time; a
  full-module `go test ./...` is not routine verification - save it for a
  periodic wider checkpoint, not a reflex after each individual change.
- **`env -u PWD` on every go command.** The checkout is reachable by two
  spellings through a symlink.
- **Read exit codes from a file.**
- **Never pipe a generator into `head`.** SIGPIPE.
- **Never `git stash` here.** The stack is shared across worktrees. Use
  `git show <sha> | git apply -R` and restore afterwards.
- **Never prune a worktree by whether its branch merged.** A branch with no
  commits is trivially an ancestor of main. A prune loop on that predicate
  destroyed five running agents' work in one command.
- **`.gitignore` needs `/.corpus`, not `/.corpus/`.** Agents symlink the
  corpus into worktrees, and a directory pattern does not match a symlink.
- **A new tool binary needs a `.gitignore` entry.**
- **Cohort ownership is split**: `GENERATED.md` and `.tf` belong to
  `estate-gen`; `README.md` is hand-owned.
- **`just estate-plan` is not a recipe.** Only `just estate-plan-from <sweep>`
  is. Run the tool directly for a fresh plan.
- **Two branches can merge cleanly in text and be semantically
  incompatible.** Run the tests on the merge result, not on the branch. Two
  branches that each independently extend the same constructor's parameter
  list are worse: git resolves the conflict by position, and the wrong
  resolution still compiles - only the tests that actually exercise the
  newly-added parameters catch a field wired to the wrong position.
- **Shell substitution in a `-m` commit message** will eat things like
  `${count.index}`. Use `-F` with a message file.

---

## Where the estates stand

Measured at commit `7e511a8c11` (2026-08-21), off `live/corpus-crossing-manifest.json`'s own `totals` field: **12 of 24** estates clear all five stages. Recompute with `jq '.totals' live/corpus-crossing-manifest.json` before trusting this number tomorrow - the "no ladder table" rule at the top of this file exists because a number like this goes stale within the hour, and this section is not an exception to it. What *is* durable, and the actual reason this section exists, is which of three buckets each non-5/5 estate is in - that decides whether it is live work, parked work, or already done.

**Being actively pursued** - a real "OpenTofu succeeds, choudoufu refuses" gap, or a floci emulator bug blocking the estate from ever reaching the point where parity can be measured at all:

- `corpus-autoscaling-complete` - re-crossed for real 2026-08-21 at `7aea0eef95`: stages 1 and 2 pass (68 added, 41 stamped, 27 skipped), stage 3 fails. `#353` removed its sole stage-3 diagnostic (the `provisioner "local-exec"` on `aws_iam_service_linked_role.autoscaling`) and did **not** move its stage outcome, which stays 2 of 5 - two walls stand behind it, both confirmed by running the script both ways in one session. The first is `#346`-shaped (`aws_autoscaling_traffic_source_attachment.this["ex-alb"].identifier` reading a sibling's ARN through a module output), so this estate now joins the three below in waiting on that design decision. The second is `Component.SoleElement` on a zero-element `prefix_list_ids`, deliberate per its own registry text. Numbers and both diagnostics in full: `live/corpus-crossing-manifest.json`.
- `corpus-eks-basic` - `#326`'s four hand-ratified Kubernetes rows (`kubernetes_config_map` and three siblings) are landed; a final re-cross and manifest update is what's left.
- `corpus-dynamodb-table-basic` - sourced (`terraform-aws-modules/terraform-aws-dynamodb-table` v5.5.1), not yet in the manifest because it can't clear stage 1: `lex00/floci#86`, `PutResourcePolicy`/`GetResourcePolicy`/`DeleteResourcePolicy` entirely unimplemented. A real emulator gap, not a choudoufu one - fix in progress directly on the fork.
- `corpus-leynos-monitoring` - stages 1-2 pass for real; `migrate` blocks on `lex00/floci#88` (`CloudWatch::TagResource` returns a bare `{}` body instead of a `TagResourceResult` wrapper, breaking the AWS Go SDK v2 - confirmed against real stock OpenTofu too, so this is floci's bug, not this fork's). Fix in progress on the fork; stages 3-5 are written and will run the moment it clears.
- `corpus-lambda-simple` - `#348`/`#349` cut the estate's unresolved root-output diagnostics from 23 to 2 (measured, not estimated: `d039b43db8` had 10, `88d7e3961e` has 2). The remaining 2 need a live data-source read before plan - a smaller, cheaper cousin of `#346`'s design question, not yet decided. See "What to do next."

**Blocked on a design decision, and not being worked until one is made:**

- `corpus-vpc-complete`, `corpus-rds-complete-postgres`, `corpus-ecs-fargate` - all three block on the identical `#346` wall (an identity argument reading a sibling's non-identity Computed attribute through a module output). Three design passes, each refuting the last, are on record; a named soundness hazard sits unresolved. Needs an opus-level session with the maintainer before any code is written. Full history preserved below - read it in full before touching this again.

**Already at the bar, correctly labeled, and not being chased** - OpenTofu itself either fails the identical configuration, or was never asked the question at all, so choudoufu is already running these "at least as well" as OpenTofu can:

- `corpus-security-group-complete` - `#335`. Real `hashicorp/aws` 6.59.0 provider bug (`Provider produced invalid plan`, a prefix-list ingress rule). Label 1, "OpenTofu fails here too" - confirmed against stock, filed with full evidence, nothing in this fork's own code to fix. This was the last gap in the small, deliberately-chosen "core set" of estates (the plainest reference shapes: an S3 bucket, an IAM role, a security group, a Lambda function) meant to reach five-of-five before this project is shown to anyone outside it - see the `#332` entry in the archive below for how it got here. It stops at `#335` by design, not by neglect.
- `corpus-alb-complete` - `#309`'s Cognito wall (`aws_cognito_user_pool_client`). Label 3, "OpenTofu was never asked this question" - stock `terraform import` gets this identity from a human typing a pre-known string, never autonomously. Maintainer ruling, 2026-08-21: **this needs the limitation stated plainly, not more code.** The crossing script's own header and this file both need to say so in as many words - check `live/e2e/corpus-alb-complete/run.sh`'s header for whether it already does before treating this as done.
- `corpus-overture-tiles` - `#249`, an already-ruled admission gap (`aws_cloudfront_origin_access_control`). The estate's real wall (`#345`, floci's `localhost.floci.io` DNS routing) is fixed; this residual is accepted, not open.
- `corpus-xancloud-iac` - `#347` fixed (the wall looked like `#327`'s ForceNew-reads-null shape and wasn't; the real cause is `lex00/floci#87`). The crossing's remaining diagnostics are the accepted, by-design residue `#347`'s own fix documents in the script's stage-3 assertions.
- `corpus-sumaform-aws` - two permanent RULE-classified refusals (`#199`, `#103`). No action needed, by design, since before this file existed in its current form.

---

## What to do next

Genuinely open items only. Everything landed lives in "Archive: landed work" below, compressed - full evidentiary detail stays there rather than here, so a reader can see what's actually still open without wading through resolved history first.

### 1. Not fixed: `#346` - three successive passes have each corrected the last; read all three before touching this again

`corpus-vpc-complete`, `corpus-rds-complete-postgres`, `corpus-ecs-fargate` are all blocked on the identical wall: an identity argument reads a `ClassNeedsDiscovery` sibling's own non-identity, schema-Computed attribute through a **module output** (`module.vpc.vpc_cidr_block` -> `aws_vpc.this[0].cidr_block`, or `module.ecs_cluster.arn` -> `aws_ecs_cluster.this.arn`). This is DEFER-class per the decision matrix above ("OpenTofu was never asked this question" - autonomous derivation with no human/state file, not a parity gap), and the maintainer has approved building a live-read mechanism for it. Three passes tried, in order, each finding the previous pass's design targeted the wrong code path:

**Pass 1** proposed widening `internal/live/identity/resolve.go`'s `parentPart` (`:2301`) to defer a `ParentRef`/Formula for a `ClassNeedsDiscovery` sibling, not just `ClassRecordBacked`/`ClassConcrete`. **Refuted by Pass 2**: none of the three estates' failures reach `parentPart` at all - the read is through a module output, evaluated by `moduleoutputvalue.go`'s `moduleOutputValue` via a strict child-module evaluator that refuses silently (no diagnostic, no demand recorded), never through the direct sibling-traversal path `parentPart` guards.

**Pass 2** found `internal/live/identity/resolve.go`'s `Context.ManagedResults` already wired into every static evaluator including `moduleOutputValue`'s, and `internal/live/projection/read.go`'s `ReadInstances` already built with zero callers - concluding that populating `ManagedResults` with the sibling's live object would resolve the Computed attribute through existing plumbing, needing only new demand-recording plumbing plus a third resolve/discover/project pass in `live_plan.go`. **Refuted by Pass 3**, empirically: built a fixture reproducing #346's exact shape (both spellings, `lookup(each.value, ...)` and `each.value.cidr_blocks`), supplied `ManagedResults` with the sibling's real object by hand, and got byte-identical diagnostics with or without it. Pass 2's load-bearing claim was false as stated.

**Pass 3's own finding, not yet acted on**: the identity argument is **symbolic** (`isSymbolic`, `resolve.go:2555`, true whenever `scope.eachValueExpr != nil`), so it never reaches `tolerantPart`/`moduleOutputValues` (`partialargs.go:261` is that function's sole non-test call site) at all. What actually runs is `resolveTraversal` -> `eachValuePart` (`eachvalue.go:162`) -> `eachValueSelect` -> `selectStatic` over the *element expression*, whose reference pre-scan (`internal/configs/static_scope.go`) refuses a module-output reference unconditionally - `managedCovered`/`dataLookupFor` (`dataresults.go:191`/`:164`) can't see a module output no matter what `ManagedResults` holds, because they only recognize resource subjects. **Two independent walls, both must clear**: (1) nothing routes a module-output reference reached through `eachValueSelect` into `moduleOutputValue` in the first place; (2) the managed Computed attribute inside the child module still needs either the configuration fold `siblingLiteralExpr`'s Computed gate refuses (`resolve.go:2414`, itself its own maintainer ruling), or a genuine live read.

Two further blockers named for whoever picks this up: `projection.PlanInstances`'s own doc says it "plans only resources with no count and no for_each" - `aws_vpc.this`/`aws_ecs_cluster.this` are both `count`-gated, so the existing second pass can't serve any of these three estates even once demand recording exists. And a **named soundness hazard** already sits in the code as a comment: `internal/live/projection/read.go:78-81` states a third-pass read is unsound today only because a resolution error is fatal in `live_plan.go` before discovery runs - `classifyOrphans` withholds a marker-carrying live resource from Removal only when its block still has an unbound declared instance, and a block that failed to resolve contributes none, so making resolution non-fatal risks silently reclassifying live objects as orphans. Any design has to answer this before writing code.

**Pass 3's measurement ran with no provider schemas** (`siblingLiteralExpr` returns `applicable=false` when `r.schemas == nil`, so wall (2) was never actually exercised - a real `live-plan` always has schemas). Re-running the fixture with schemas populated is a five-minute check and the first thing the next slot should do; it could change wall (2)'s shape. The fixture itself was built and then deliberately deleted rather than committed, since committing it would have regenerated `TestIdentityGolden`'s shape mid-wind-down - recreating it is described in full in the pass's own report and is a couple of minutes' work, worth doing with a golden update as the first act of the next slot, since it pins #346 offline in under a second.

No code has been written across any of the three passes. Worktree `../wt/two-pass-resolve-discover` (branch name live/two-pass-resolve-discover) is clean, HEAD equal to main, nothing to clean up and nothing to pick up except this analysis.

### 2. `corpus-alb-complete` needs `#309`'s limitation stated, not more code

Maintainer ruling, 2026-08-21, correcting a framing this file previously carried: `#309`'s Cognito wall was described here as something to keep chasing toward five-of-five. It isn't - it's label 3, "OpenTofu was never asked this question," and per the standing bar that means read it, record it, or order around it, not force it into a fix. The remaining work is documentation: confirm `live/e2e/corpus-alb-complete/run.sh`'s own header states the limitation plainly (what the wall is, which label, why it stops there), and that this file's own "Where the estates stand" entry above is what a reader sees first - not a still-open investigation.

### 3. The credential-material opt-in - ruled, not built; mechanism still undecided

`identity.CredentialMaterial` (`internal/live/identity/located.go`) blocks record-location for any markerless type whose schema carries *any* `Sensitive`-and-not-`Deprecated` attribute, regardless of whether that attribute is the one actually recorded - the located mechanism only ever writes `obj.id`. Measured 2026-08-21 against real `hashicorp/aws` 6.59.0: **11 types** currently excluded - `aws_appconfig_hosted_configuration_version`, `aws_appsync_api_key`, `aws_codebuild_source_credential`, `aws_cognito_user_pool_client`, `aws_iam_access_key`, `aws_iam_service_specific_credential`, `aws_iot_certificate`, `aws_kms_grant`, `aws_ssm_maintenance_window_task`, `aws_wafv2_api_key`, `aws_workmail_user`.

**Ruling (2026-08-21):** the blanket refusal stays the default - unchanged behavior - but must become explicitly overridable. An operator can deliberately turn record-location on for a specific type they've decided to accept the risk for, the same way OpenTofu state already routinely holds real sensitive values and the operator is expected to secure it. A refusal is loud and reversible (one flag away); a leaked secret is silent and irreversible once the record store has been synced or committed - so the default stays fail-safe, but the choice is the operator's, not the tool's to permanently withhold. This does not, by itself, move `corpus-alb-complete` - `aws_cognito_user_pool_client` is refused by a *second*, independent wall too (`id` unproven whole, per `#329`'s own scoping), so building this opt-in would not reach five-of-five on that estate regardless.

**Not yet decided:** the opt-in's mechanism. Two shapes were posed and neither chosen - a per-type allowlist declared in the estate's `live {}` config block (versioned with config, visible in review), or an env var/CLI flag (operator-local, not committed, easier to forget is set). Ask before building either.

### 4. floci emulator fixes in progress: `#86`, `#88`

Both are real gaps in `lex00/floci` (the fork, never upstream), each blocking a real estate from reaching the point where choudoufu-vs-OpenTofu parity can even be measured - not choudoufu defects. See "Where the estates stand" above for which estate each unblocks. Once merged on the floci side: re-pin `live/floci-image`, regenerate the capability manifest, and re-cross the affected estate(s) for real before marking anything done - see "Traps," above, on why a local multi-arch build is the wrong way to verify a floci-side fix.

### Loose ends worth an hour, not a slot

- `internal/live/mv`'s `checkAddresses` still cites the same retired premise `markers.UnescapeAddress` did before 2026-08-18's fix - see `#317` in the archive below.
- The shared `TF_PLUGIN_CACHE_DIR` records no checksums, so any crossing script running more than one `terraform init` pays a real ~320s tax per init after the first (measured; filed as `#339`). Seeding the second directory's lock file from the first init's takes that init from 320s to 1s.

---

## Archive: landed work, evidence preserved

Everything here is fixed and merged. Kept in compressed form rather than deleted, per this file's own "no claim without a measurement" standard - the number that changed and the commit it was computed at, not the full investigation narrative that guided whoever landed it. Ranked newest-first, matching how this list has always read.

- **`#337` (2026-08-21, `7e511a8c11`):** a located identity can now compose from the provider's own documented import grammar, not only its wire schema - 18 previously-refused markerless types now record-located instead of refusing. Measured against real `hashicorp/aws` 6.59.0: `markerless=158 located(string id)=97 located(composite object)=8 located(composed string)=18 credential=11 unprovenID=21 noID=3`. Mutation-checked (emptying the new grammar route reverts to `composed=0 unprovenID=39`, nothing else moves). No estate-crossing impact yet - none of the 18 appears in any estate declaring a `record_store` today.
- **`#309`/`#329`/`#337` chain (2026-08-19 through 2026-08-21):** the full story of why `aws_cognito_user_pool_client` stays refused, and what it took to get the other 17 similarly-shaped types unstuck. Three re-scopings each corrected the last (a third discovery transport was never needed; the real missing piece was `#270`'s existing `ClassRecordLocated` mechanism plus a composite located payload, `#329`; then a documented-import-grammar route, `#337`, to tell a proven-whole `id` from an unproven one without needing component order at all). `MarkerlessTypes` 140 -> 158, `IDNotProvenWholeTypes` derived generically over all 317 import-grammar rows. See "What to do next" item 2 for the one thing still open on this thread (documenting the limitation, not more code) and item 3 for the separate credential-material ruling it surfaced.
- **`#332` (2026-08-19, `859c1ad747`/`ff1f6bcdea`/`c1197befc7`):** `aws_default_route_table` was ratified to import by its own `rtb-...` id; the real provider imports it by the VPC's id. Fixed by correcting the ratified row and splitting a conflated predicate (`defaultAdopterSiblings` vs. `sameRatifiedIdentity`). Reach: one type today, generically derived so any future `aws_default_X`/`aws_X` divergence needs no code change. Took `corpus-security-group-complete`'s stage-3 diagnostics from 239 to 1 - the 1 remaining is `#335`, a real AWS provider bug (see "Where the estates stand").
- **The "core set" formula-carrying fix (2026-08-19, `312acbbb61`/`75ef0a6a78`):** `tolerantVariables` now carries a formula, not a bare value, across a module-call argument boundary - `#191`'s own closing recommendation, landed two years-in-repo-time after two prior naive attempts each made the loss worse (`-16` then `-49` instances). `TestIdentityGolden` 0 changed, 6 added; over `.corpus`, +26 instances, 0 removed, 0 modified.
- **`#325` (2026-08-19, `bf1ad64982`/`92f8fb7b55`):** discovery's marker type-equality check treated `aws_default_route_table`/`aws_route_table` and `aws_default_security_group`/`aws_security_group` as mismatched types. Fixed generically off the `aws_default_` prefix cross-checked against shared ratified identity fields.
- **`#308` (2026-08-18, `a9ac6d06e7`/`b2bb59585d`):** `foreach_keyset.go` gained the `*hclsyntax.ForExpr` case and a cross-module-call `var`/`local` chase, generically. Fixing it revealed `corpus-ecs-fargate` also hits `#313` underneath - see below.
- **`#313` (2026-08-18/19, `c636ab20f7`/`0284d8c408`):** re-scoped from "architecture question" to "one resolver gate" after scouting found `live-plan` already makes real `ReadDataSource` calls (since `#179`) - the maintainer's belief that it didn't was the false premise, not a design gap. `resolver.callerVariables` now also rebuilds when a strictly-ancestral module instance carries read coverage. Measured over 204 `.corpus` directories: configurations the read phase changes 22->71, instances +1->+80, error diagnostics cleared 1225->10712. `corpus-security-group-complete` alone: 239->19 diagnostics, revealing `#321` (a derivable splat-through-`element()` gap, since fixed) as the next real blocker.
- **`#304` crash (2026-08-19):** `TestNoUnregisteredRefusalsInTheTree` was genuinely red on main. Root cause: `normalizeRefValue` built an ill-typed `cty.UnknownVal` on certain error paths, and `#304`'s own `EvaluateStructural` was the first caller to ask that value's type. Fixed at the construction site (`cty.DynamicVal` instead). A second, independent bug found in the same episode: `filepath.WalkDir` doesn't descend a symlink, so a symlinked `.corpus` silently walked 1667 directories in 1.4s instead of 7649 in 325s and reported a plausible-looking pass - fixed with `filepath.EvalSymlinks` before the walk, plus a floor that only applies when `.corpus` exists on disk.
- **`#342` (2026-08-20):** all nineteen remaining crossing scripts updated to assert `#340`'s six-column summary line; three of them needed a genuinely different assertion, not just a longer one, because `#340` also moved record-backed instances out of the skipped bucket. Guarded by `live/summary_line_guard_test.go`, mutation-checked in both directions.
- **`#341` (2026-08-20, `c73a6e4617`/`78c92ad64a`):** an untaggable admitted instance's residue write was structurally unreachable - a single `*eligible` boolean was gating both "write the marker" and "record what we sent." Split into a third carrier, `residuable`. `corpus-mastino-dns` went from 2 of 5 to 5 of 5 the same day.
- **`#340` (2026-08-20):** `live-import -approve` had one write path (the tag), and a record-backed instance has nothing to tag - so migrating an estate with `random_pet`/`local_file`/etc. reported success while writing the generated value nowhere. `projection.SeedRecordForInstance` gives `Approve` a second write path, reusing the same encode/key machinery `WriteBack` uses. Reaches 15 types across four providers, generically, off `RecordBacked`.
- **`#314` (2026-08-19):** a fourth `LogicalClass`, `ClassExternalAdmitted`, admits `local_file` - reached exactly one type today, and that's stated as the honest number rather than dressed up. Revealed a genuinely different wall on `corpus-lambda-simple` (an identity resolver declining to read a record-backed carrier the run already has), filed as `#336` and since fixed - the resolver's `coalesce()` handling was the real gap, confirmed by a live mutation check (disabling the fix's call site reproduces the exact "Dynamic value in static context" failure `#336` described, then reverts clean).
- **`#316` (2026-08-18, `a30cb152f8`/`4d02f05d30`):** the rename-withholding guard - `classifyOrphans`'s own safety mechanism - never fired for a module-qualified address at all. A real silent destroy-recreate hazard, found while tracing a smaller fix. Both root causes reproduced with real values before any code changed.
- **`corpus-giantswarm-crossplane` and `corpus-evoteum-modules`** (2026-08-19): the seventh and eighth OpenTofu-native estates, both five-of-five. The first is the lane's first estate from a commercial vendor's own production repository rather than a registry module; `#334` ratified two `*_exclusive` rows it needed (a ledger decision, not a generator gap - 166 more types sit unratified under the identical rule, a maintainer-scale backlog, not a defect). The second is the lane's first estate with zero `.tf` files anywhere in its pinned tree and the first whose `for_each` keys fall outside the AWS tag-value charset, which exercised `EscapeAddress` for real for the first time.
- **`corpus-leynos-monitoring`, stages 1-2** (2026-08-20): the ninth OpenTofu-native estate, `leynos/df12-www`'s `modules/monitoring`. Cold-deploys for real; migrate blocks on `lex00/floci#88` - see "Where the estates stand" and "What to do next" item 4.

## Rulings worth not relitigating

Kept because each was reached by measurement and each has been re-opened at
least once from prose alone.

- **`identity.CredentialMaterial` stays default-refuse; the opt-in mechanism is
  a separate, still-open decision.** Ruled 2026-08-21 (see "What to do next"
  item 3 for the full context and the 11-type population): the blanket
  exclusion is not being narrowed, and the question of whether it should be
  is settled - it stays coarse by design, so a type drops out automatically
  the day its schema grows a secret, with no per-release re-audit. What's
  still open, and should not be re-litigated as "should the default change,"
  is only the shape of the operator opt-in (config block vs. env var).
- **`#309`'s Cognito wall is documentation work, not a fix to chase.** Ruled
  2026-08-21, correcting this file's own prior framing: it is label 3,
  "OpenTofu was never asked this question," and the standing bar's own
  words for that ("read it, record it, or order around it") mean state the
  limitation plainly and stop, not keep landing code toward five-of-five.
  Do not re-open this as an active gap without new evidence that changes the
  label.
- **#263's cure is COMPLETE, not half done.** This list carried "the flip is
  three reads" as an open item for a day after it had landed at `52596938c8`,
  and a slot was briefed from it. `-emit` reads `tools/row-gen/ratified.json`;
  `emittedRows`, `markerlessRoster` and `buildConvergence` all moved, and
  `TestEmitDoesNotReadTheTableItWrites` empties `identity.DefaultTable` and
  still requires byte-identical output. `retraction.go` deliberately did not
  move. The residual is in `importprecedence.go`, which reads `DefaultTable`
  inside the fresh classifier - classifier self-agreement, a different debt,
  and it cannot lose a row. #263's closing comment locates that at `:699` and
  as a single site; both are off as of `1e06f2d485`. `:699` is where
  `tryCloudSingletonID` is declared, not where it reads, and there are TWO
  reads, not one: `tryCloudSingletonID` at `:715` and `tryLiteralSingletonID`
  at `:849`, each refusing to let fresh evidence defeat a standing
  `ServerAssigned` claim. Verify against the code before re-opening.
- **row-gen's report names `tools/row-gen/ratified.json`, and prints JSON.**
  The dead `table_cohort_<cohort>.go` / `admission_cohort_<cohort>.go` targets
  are gone from `render.go`. Note the shape of the fix, because "retarget the
  string" was the obvious wrong move: the blocks used to render Go literals,
  so pointing them at a JSON file would have told an operator to paste Go into
  JSON. They now render the type's `ratified.json` member through
  `renderRatified`, the same function `TestRatifiedJSONIsCanonical` holds the
  committed file equal to. There is no admission line to paste any more -
  `admittedTypesV0` is derived from the emitted table's key set.
- **The parent-derived widening of the markerless veto: REFUTED, population
  zero.** Three independent checks, on 2026-08-17. Do not re-open without new
  evidence.
- **`list + content match` was never an admission path.** The token named a
  mechanism this fork does not have and is now `enumerable, unbindable`.
  `internal/live/discovery` binds by reading marker tags and by nothing else.
- **The record store MAY hold an identity for an object that carries no
  marker, because an ID is not a permission.** "May I delete this" stays with
  IAM, scoped by ARN or resource policy. "Which object is this" is what the
  record answers, and only that.
- **cty marks cannot carry provenance.** `IsMarked()` guards roughly 70
  panicking accessors across `internal/live`, and `marksafe` is a static
  prover requiring exactly that guard shape. A second mark kind would mean
  rewriting every guard.
- **A blanket "defer every unknown" was measured and rejected.** It frees five
  rate-capable estates, all of them govuk-aws unset-variable estates that #183
  rules must stay blocked, and moves rate-capable instances not at all.
- **Receipts never migrate onto the record store.** A receipt's value must
  stay readable with `aws ssm get-parameter` by someone with no binary.
  `live/RECEIPTS.md` has the four guards.
- **choudoufu's own stamp/apply seam was traced end to end and found
  correct.** A silent marker-loss defect looked structurally like exactly the
  risk `internal/live/stamp/doc.go` names - `apply`'s own re-plan throwing
  away the marker-injection seam. It was traced through
  `internal/live/stamp`, `internal/live/projection/build.go`'s
  `configuredTagsSeed`, and `NodeApplyableResourceInstance`'s re-plan, and
  none of them were it. The real cause was a floci bug: S3 Control's
  `TagResource` did a full tag replace where real AWS merges. Do not re-open
  this seam from a marker-loss symptom alone without a fresh, real
  reproduction first - the first reproduction attempt for this exact defect
  also failed to trigger it, because it avoided the one code path the bug
  actually lived in.

## Session-perishable state

Anything that rots faster than this file lives elsewhere on purpose.

- Work items and their current figures: the tracker.
- Which real estate clears which stage and why the rest do not:
  `live/corpus-crossing-manifest.json`.
- Refusal figures over the offline corpus: regenerate with `just corpus`, or
  measure with `refusal-probe` against the tree you are on.
- Pinned floci image and provider pins: `live/floci-image` and the tests in
  `live/pins_drift_test.go` and `live/flociimage_test.go`.
- Whether a background subagent is still working or has stalled:
  `.claude/scripts/agent-progress.sh <task-id>...` (or `just agent-progress`)
  reports write-age and the last few real commands without pulling a full
  transcript into context.
- Adversarial audits have found real defects in work that was green,
  committed and believed finished, and CI caught none of them. An extra audit
  pass buys more than an extra CI run. Treat that as a standing option.
