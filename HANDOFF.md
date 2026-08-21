# How to work this repository

This file is the standing playbook. It says what the work is for, what makes
a change acceptable, and how to take a task from the tracker to a merge.

It carries **no ladder table, no site counts and no rankings**. Several
earlier versions did, every one of them went stale within the hour, and one
shipped two rows that were wrong when written. The numbers live in artifacts
that regenerate and in the tracker; this file says how to compute them.

Read `.claude/agents/live-markers.md` next for the operational detail.

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

1571 rendered identities across 504 configuration directories in under a
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

## What to do next

Ranked. Every item is filed, so the tracker carries the evidence and this list
carries only the reason and the order.

### -5. Fixed: `#342`, and its own "one-line fix each" premise was wrong

All nineteen crossing scripts now assert `#340`'s six-column summary line,
and `just ci` can see the next widening. The issue's premise - insert
`0 newly recorded, 0 already recorded` into each - holds for **sixteen** of
them and not for three. `#340` did not only widen the line; it moved
record-backed instances out of the SKIPPED bucket into a new RECORDED one, so
an estate carrying one has a **different skipped count**, and not only a
longer line. Three do: `corpus-alb-complete` (`null_resource.download_package`, 29
-> 28 skipped), `corpus-ecs-fargate` (`time_sleep.this[0]`, 16 -> 15) and
`corpus-rds-complete-postgres` (`random_id.snapshot_identifier`, 13 -> 12).
The dry run's own UNTAGGABLE count is unmoved in all three -
`ratifyRecordBacked` still answers `StatusUntaggable` - so only the
`-approve` line splits, and each script now carries both numbers separately
with the reason next to them.

Two predicates decide which group a script is in, and neither is a type list:
the estate's `live` block must declare a `record_store` at all
(`ratify.go:369` gates on `req.RecordStore != nil`), and the migrated state
must actually contain an instance of a `RecordBacked` type. Seven of the
nineteen declare no store, and nine declare one but reach no such instance -
`corpus-eks-basic` is the interesting near-miss, carrying four record-backed
resources and no `record_store`, so its 29 skipped is unchanged;
`corpus-s3-bucket-complete`'s DELTA 3 deletes the estate's only `random_pet`
before migrating.

The guard is `live/summary_line_guard_test.go`, and it is in `live/`
deliberately: `just ci`'s fast tier runs `./internal/command/`, the package,
not that package's whole subtree, so a test beside the view would not have
run -
the per-file/per-package check-unit trap for a fourth time.
`TestApproveSummaryLineIsPinned` renders the real line through
`views.StatelessImportHuman.Stamped` with a distinct prime per bucket and
pins it; `TestCrossingScriptsAssertTheCurrentSummaryLine` recovers the column
labels from that render and requires every whole-line assertion in
`live/e2e/*/run.sh` to carry all of them in order. Both mutation-checked:
stripping the two columns from one script names that script and line, and
inserting a seventh column into the view fails the pin and names all 23
script lines it costs.

**Four crossings run for real against floci to verify it**, rather than read
on paper: `corpus-rds-complete-postgres` (the count-moving case - stage 2 PASS,
`26 stamped, 1 recorded (random_id.snapshot_identifier), 0 failed, 12
skipped`, stage 3 still BLOCKED at exactly 2 sites as recorded),
`corpus-giantswarm-crossplane` and `corpus-hongbomiao-storage` and
`corpus-iam-read-only-policy`, all three 5 of 5. The other fifteen are
pattern-matched against the shape those four proved.

### -4. Fixed: `#341`, and `corpus-mastino-dns` is 5 of 5

`ratify.go`'s taggability check returned before the one carrier `Approve`'s
residue call took, so `recordResidueFor` was structurally unreachable for
every admitted resource whose provider schema has no `tags` argument.
`*eligible` was gating two unrelated jobs - "write the marker" and "record
what we sent" - and untaggability only disqualifies the first. A migrate
reported success and the first cold `live-plan` proposed a phantom update for
every argument the provider's own `Read` can never give back, forever.

Fixed 2026-08-20 (`c73a6e4617`/`78c92ad64a`) with a third carrier beside
`*eligible` and #340's `*recordable`: a `residuable`, which `*eligible` now
embeds, so a stampable instance and an untaggable one reach the same call
through the same argument type. It keys on `taggable(schema.Block)` over the
schema the provider served this run and on nothing else - **342 untaggable
admitted types**, and any future one the moment a provider grows it.

Two deliberate non-changes, both asserted: **the verdict does not move**
(`StatusUntaggable`, `OutcomeSkipped` - what was skipped is the marker
write), and **no `ReadResource` is added at ratify time**. The object handed
forward is the state's own, which is where a residue value lives by
definition; reaching a read would let an untaggable instance come back
MISSING or DRIFTED and move counts eighteen crossing scripts assert.

`live/e2e/corpus-mastino-dns` run for real against floci, 2026-08-20:
**all five stages pass**, where it landed at 2 of 5 the day before. 14
residue records written at migrate time, every one read back out of the
store's own files as `allow_overwrite = true`; the cold replan EMPTY with all
63 rendered identities asserted by value; a no-op apply; and a TTL drift on
one of the fourteen - untaggable *and* residue-carrying - reconverging to
exactly that instance and exactly the `ttl` attribute, which is the check
that a residue fill has not started masking real live drift.
`BREAK_STAGE5=1` verified.

**Two things found doing it, neither of them this issue.** First, stages 4
and 5 had to be **written**: that script's header said they lived in git
history and they do not - `e74b6e5c01` is the file's only commit. Second, and
worth a slot: **#340's summary-line change broke twenty other crossing
scripts on main.** `N resource(s) newly stamped, ...` gained two columns and
only `corpus-lambda-simple` was updated; `corpus-mastino-dns` is fixed here,
the other nineteen still assert the pre-#340 shape by exact string and will
fail at stage 2 the moment they are re-run. Nothing in `just ci` can see it -
no crossing script runs there. **Filed as `#342`**, which names all nineteen
and the one-line fix each needs.

Recomputed while writing it, since it travelled into the issue and a source
comment: `identity.DefaultTable` holds **1040** rows, not 1025.
`live/survey-full.json`'s taggable signal calls 683 taggable and 342
untaggable and does not cover the remaining 15 at all - so the untaggable
numerator was right and the denominator had dropped the uncovered rows. The
runtime gate settles the 15 anyway, which is the point of gating on the
schema rather than the artifact.

### -3. Fixed: `#340`, a migration that wrote no record at all, and the sixth wall under it

`live-import -approve` had one write path, the tag, and a record-backed
instance has nothing to tag. So it was reported `SKIPPED` and its generated
value went nowhere: the run said success, and the next `live-plan` proposed
creating `random_pet`, `null_resource`, `terraform_data` and `local_file`
from scratch, taking every identity derived from one of them down with it.

`Approve` now has a second write path beside the first.
`projection.SeedRecordForInstance` writes the state's own object into the
record store, reusing `encodeRecordPayload` and `RecordKey` so a migration's
write is byte-identical to `WriteBack`'s and readable by
`materializeRecord`. It reads before it writes: an identical record is an
idempotent no-op, a **different** one is a refusal - the store's value can
legitimately be newer than the tfstate a migration was pointed at, and
clobbering there produces an empty plan built on a stale value, which
nothing downstream can see.

Derived, not hand-wired: it keys on `identity.TypeIdentity.RecordBacked`,
the same property `WriteBack` reads after an apply, which row-gen derives
from `live/logical-schemas.json`'s per-provider `store_only` gate. **Fifteen
types across four providers today**, and a type row-gen newly classifies
`store_only` is migrated the moment the table regenerates.

**The sixth wall it reached is worth a slot, and it is two unrelated
things.** `corpus-lambda-simple` against floci, 2026-08-20: `STAGE 1 PASS`,
`STAGE 2 PASS` (3 stamped, 4 recorded, 0 failed, 1 skipped, the store's own
files grepped for the pet name), `STAGE 3` blocked with **zero
diagnostics**, every identity resolved, and `Plan: 0 to add, 2 to change, 0
to destroy`:

- `aws_lambda_function.this[0]`: `- environment {}` and
  `+ logging_config { log_format = "Text" }`, plus the computed
  re-derivation those force. A nested-block round-trip between floci's
  Lambda read and the module's config - neither an identity nor a record
  question.
- `local_file.archive_plan[0]`: `~ content = (sensitive value)` with
  OpenTofu's own renderer saying *"The value is unchanged"*. A
  **sensitivity-only** diff. hashicorp/local marks `content` sensitive,
  `ResourceInstanceObjectSrc.Decode` re-applies that mark from
  `AttrSensitivePaths`, and `projection.recordPayload` has nowhere to put a
  sensitivity path - so the migrate unmarks before `ctyjson` can encode.
  `WriteBack` shares the hole and has it worse: with no unmark of its own it
  would panic on the same object.
  `TestApprove_RecordsAnObjectCarryingASensitiveAttribute` pins the
  limitation in both directions.

### -2. Fixed: `#314`, a fourth `LogicalClass`, and the wall it revealed

`local_file` is admitted. `ClassExternalAdmitted` ("EXTERNAL_ADMITTED") is
the class #237 and #238 both correctly declined to force and neither left
behind: a `record_store` admits it exactly as it admits RECORD_ADMITTED, it
resolves `identity.ClassRecordBacked` through the same projection path, and
what it does **not** get is `countIndexScopeForType`'s skip - because one of
its own arguments names an object the record does not bound.
`TestLocalFileKeepsItsCountIndexCheck` passes unchanged, which is the whole
reason the boundary is drawn there rather than at RECORD_ADMITTED.

**Two claims in the issue were false, and both are why the class is not
spelled "argument-derived identity" the way #314 proposed.** First,
hashicorp/local 2.9.0 implements no `ImportState` for `local_file` at all -
`tofu import local_file.f <path>` answers "Resource Import Not Implemented" -
so a resolution carrying the filename as `ImportID` would hand projection a
string the provider refuses, trading a lint refusal for a "Cannot import for
projection" hard error. Second, this estate's own `filename` is
`data.external.archive_prepare[0].result.build_plan_filename`, so there was
never a static value to fold. The record is the only carrier there is; the
argument's job is to say why the count.index walk stays.

Derived, not hand-wired: `live/logical-schemas.json`'s per-provider
`store_only` used to gate whether a type derived a row **at all**, and now
selects between the two admitted classes instead. That also retired the
hand-written `local_sensitive_file` exception `ClassifyLogicalType` carried.
**The new class reaches exactly one type today** - stated plainly rather than
dressed up; hashicorp/local serves two and the other is secret-bearing. What
generalizes is the rule.

The `RecordBacked` leg of `countIndexScopeForType` is **gone**, not bypassed.
It was a second door onto the same skip, safe only while the RecordBacked set
and the RECORD_ADMITTED set were equal, and this change ends that equality on
purpose. Both mutations were checked in place.

`TestIdentityGolden` 1565 -> 1567: 0 changed, 2 added, 0 removed. Both added
rows are the two `local_file` fixtures that already existed, both
RECORD_BACKED with an **empty** value - the honest answer for a type nothing
can import.

**What the crossing then reached is a genuinely different wall, newly reached
rather than caused, and it is worth a slot.** `corpus-lambda-simple` run for
real against floci: `STAGE 1 PASS`, `STAGE 2 PASS`, `STAGE 3 BLOCKED at 5
diagnostics`. `local_file` appears nowhere in `live-plan`'s output any more,
and the script now asserts that **by absence** alongside #303's two types.
All five that remain trace to one expression in the estate's own `main.tf`:

    function_name = "${random_pet.this.id}-lambda-simple"

`random_pet.this` is RECORD_ADMITTED, so its `id` lives in the record store
and nowhere else, and three of the module's real AWS resources take their
identity from it (`aws_iam_role.lambda.name` via a `local`,
`aws_iam_role_policy.logs.name`, `aws_lambda_function.this.function_name`,
`aws_cloudwatch_log_group.lambda.name` through a `coalesce`), plus one
cascade. The diagnostics are `Non-static identity argument` and
`Unresolvable identity`.

Read it before assigning it: **choudoufu holds that value already** - the
record store has `random_pet.this` and `internal/live/projection` already
reads records to hydrate record-backed instances - and **all three affected
resources are already marked**, stamped and CLI-verified by stage 2 of the
same run. So it is an identity resolver declining to read a carrier the run
already has, over resources whose markers already say which object they are.
Whether the fix is the record or the marker is a real design question. Full
chain in `live/e2e/corpus-lambda-simple/run.sh`'s own header.

One methodology fix landed with it: that script said "no version pin needed"
and silently followed whatever `hashicorp/aws` had published most recently
(`>= 6.28`, resolving 6.61.0 today). It now pins `= 6.59.0` as
corpus-cloudfront does - the release this fork's tables are actually derived
at. The crossing was neither reproducible nor measuring the right provider
before.

### -1. Resolved: `../wt/security-group-formula-carrying` was a real fix, now merged

An orchestrator session ran out of budget 2026-08-19 mid-dispatch on the
formula-carrying `tolerantVariables` fix item 2 below calls for, leaving a
subagent's work uncommitted, unverified and unmerged in
`../wt/security-group-formula-carrying`. It was read, held to the same bar
every other fix this session passed, and merged 2026-08-19
(`312acbbb61`/`75ef0a6a78`) - see item 2, whose first "what the next slot
needs" bullet it closes.

The evidence that decided it, all measured rather than read off the diff:
the eleven `partialargs` unit tests pass, three of them adversarial
refusals (the dynamic leaf still refusing two calls down, a dynamic key SET
refusing the whole expansion, `composedArgument` declining to reconstruct a
call); `TestIdentityGolden` is 0 changed, 6 added, 0 removed, the zero
being the load-bearing half for a change that makes an earlier evaluation
succeed; and `corpus-security-group-complete/run.sh` was run for real
against floci on the branch, printing `STAGE 1 (cold deploy): PASS`,
`STAGE 2 (migrate): PASS` and `STAGE 3 (test_plan): BLOCKED for real, at 4
sites`, with `BREAK=1` exiting 1 at stage 3.

One honest caveat for whoever touches that script next: `BREAK=1` fails
fast on the FIRST corrupted assertion (unadmitted-type), so the new
`#332` projection-import and empty-result counts are asserted at exact
values but are not themselves reached by the negative control. Their
values are real - the `BREAK=1` run's own diagnostic dump prints the
plan's entire `^Error:` surface and it is exactly those four lines.

### 0. Fixed: the #304 crash that had `just ci` red on main

`TestNoUnregisteredRefusalsInTheTree` (`internal/live/check`) was genuinely
failing on `main` from 2026-08-18 until the fix below. It crashed rather
than refused on 32 directories, all of them
terraform-aws-modules/security-group's vendored `modules/_templates` and
`wrappers/_templates`, materialized under `.terraform/modules/` by
`terraform init` across the `autoscaling`, `ecs`, `lambda` and `rds`
examples. Those are code-generation TEMPLATES, so they genuinely reference
`var.auto_*` names no `variable` block declares.

The cause was older than #304 and was only ever reachable through it.
`normalizeRefValue` (`internal/lang/eval.go`) is the one funnel every
`lang.Data` lookup passes through into an `hcl.EvalContext`, and on the
error path it built `cty.UnknownVal(val.Type())`. A `Data` method may report
an error and return NO value - `staticScopeData.GetInputVariable` answers an
undeclared variable with `cty.NilVal`, and upstream's own
`evaluationStateData.GetCheckBlock` does the same - and `cty.NilVal.Type()`
is the zero `cty.Type`, whose `typeImpl` is nil. An unknown OF that type is
not equal to `cty.NilVal`, so every `== cty.NilVal` guard passes it, and it
panics inside `cty` the moment anything asks its type a question.
`Scope.EvalExpr` had always returned at its own `diags.HasErrors()` gate
before calling `expr.Value`, so nothing had ever asked; #304's
`EvaluateStructural` evaluates against such a context on purpose, and
`hclsyntax`'s `BinaryOpExpr` asks immediately in `convert.Convert`.

Fixed 2026-08-19 at the point the ill-formed value is constructed: an absent
type becomes `cty.DynamicVal`. Full sweep 7646 loadable configurations of
7745, 324.71s, 0 panics; the same commit reverted in place gives 7613
loadable and 33 panics, the 33rd being the fix's own fixture.
`internal/live/lint/testdata/count-index-undeclared-var` is that fixture and
`TestCountIndexSurvivesAnUndeclaredVariableInAModuleArgument` the gate.
#304's own improvement is intact and was checked rather than assumed:
count-index sites are unmoved at 554 across the corpus with this change, and
reverting #304's call sites on top of it takes them to 1032.

**Two things worth keeping from the episode.** First, `.corpus` is not a
build input Go's test cache can see, so a cached `ok` for
`internal/live/check` is not evidence that a crossing's `terraform init`
has not, moments earlier, materialized a directory that breaks it. Second, the
reason this survived #304's own verification: **`filepath.WalkDir` does not
descend a symlink**, and `sweepRoots` includes `.corpus`, which most
worktrees symlink in. The sweep then walked `live` and `internal` only,
finished in 1.4 seconds instead of 325, reported a perfectly plausible
"analyzed 1667 loadable configurations", and passed.

**Fixed 2026-08-19** (`internal/live/check/sweep_test.go`'s `configDirs`):
every sweep root is now resolved with `filepath.EvalSymlinks` before the
walk, so a symlinked `.corpus` is walked exactly like a real directory - no
manual workaround needed any more. `TestNoUnregisteredRefusalsInTheTree`
also gained a second, tighter floor (`>= 3000` directories, not just
`>= 500`) that only applies when `.corpus` exists on disk, so this exact
blind spot fails loudly rather than passing quietly if it ever recurs.
Verified against a real symlinked `.corpus` in a fresh worktree: the sweep
now runs 319.21s and reports "analyzed 7649 loadable configurations of 7748
directories" - a real run, not the symlink-blind 1667/1.4s one.
`tools/refusal-probe` never had this bug: it reads explicit entries from
`live/corpus-manifest.json` via `check.ReadManifest` rather than walking
`.corpus` with `filepath.WalkDir`, so it never depended on the root's own
directory-ness. If you are verifying anything that sweeps `.corpus` from a
worktree on a tree older than this fix, either check the elapsed time and
the analyzed count against a real run or materialize a real directory
(`cp -Rl` the shared corpus - hard links, 15 seconds, no data copied).

### 1. Resolved: #313 does not repeat, and it isn't a quick fix anyway

Checked 2026-08-18: #313's `data.aws_availability_zones`/static-context wall
does **not** explain any of the other five `test_plan`-stuck estates. Each
hits its own, distinct, already-tracked cause -
`corpus-lambda-simple` #314 (`local_file` needs a fourth `LogicalClass` -
**fixed 2026-08-19, see item -2 above**, and the estate now blocks on a
different wall entirely), `corpus-rds-complete-postgres` #304
(`count.index` through a nested `lookup()` default), `corpus-ecs-fargate`
#308, `corpus-sumaform-aws` two permanent RULE-classified refusals (#199,
#103, no action needed), `corpus-alb-complete` #309. Evidence and commit
references are in each estate's own entry in
`live/corpus-crossing-manifest.json`. Two of those five checks also found
and fixed stale crossing-script assertions left over from #305 landing
(`corpus-ecs-fargate`, `corpus-alb-complete`), now on main.

**#313 was first read as an architecture question - it wasn't one, and that
premise was wrong. Re-read this before assigning the area again.** The
maintainer ruled 2026-08-18/19 that `live-plan` may call a provider's own
read-only data-source APIs during plan to prove a `for_each`/`count` key
set, on the belief that `live-plan` never calls a provider at all today
(statelessness, #73). Scoping the actual implementation found that belief
false: `live-plan` has made real `ReadDataSource` provider calls since
#179 - `internal/live/dataread` (`Analyze` offline, `Read` against
`statelessProviders.ConfiguredProvider`), wired into `live_plan.go`'s
`statelessDataReads` between the subset check and resolution. Nothing
needed building. `live-check`'s fully-offline, credential-optional
guarantee was never at risk, because that capability was never touched.

**The real defect was one gate in the resolver, fixed 2026-08-19
(`c636ab20f7`/`0284d8c408`).** A read value could not cross a plain
(non-repeated) module call: `internal/live/identity/modulevars.go`'s
`resolver.callerVariables` only rebuilt a module instance's `var.*` closure
when a call on the path carried its own `count`/`for_each` (`pathRepeats`,
scoped for #252, whose doc claimed a non-repeating tree "cannot need" a
data read - false). `frozenClosureIsStale` now also rebuilds when a
strictly-ancestral module instance carries read coverage
(`ancestorCarriesResults`). One predicate, no type or data-source names.
Measured over 204 `.corpus` directories with an eligible demanded source:
configurations the read phase changes 22→71, instances +1→+80, error
diagnostics cleared 1225→10712, 57 entries improved, 0 worse.
`corpus-security-group-complete` itself: `test_plan` diagnostics 239→19 -
#313's canonical cause (50 sites) fully gone, its resource-attribute
variant (2 sites) still correctly refuses (genuinely out of the ruling's
own scope), the 187-site cascade collapsed to 5, and 12 sites are a
NEWLY-REACHED class (previously masked behind #313's hard refusal) filed
as `#321` - `element(<resource>[*].id, count.index)` over a splat of
tagged resources, a derivable gap with no design call needed. `#321` is
now the estate's, and the core set's, real remaining blocker - see below.

Read this as the general lesson beyond this one issue's own story: the standing
bar's "scout before you fix" applies to architecture questions as much as
to bugs. An assumption about what the codebase does NOT do is exactly the
kind of claim scouting the actual code disproves before a design
conversation is even needed.

Separately, lex00/floci#70 (`CreateCacheSubnetGroup`/`ModifyCacheSubnetGroup`
wrong `SubnetIds` wire param name, found re-verifying `corpus-vpc-complete`
against a freshly published image) is now fixed, independently verified
against AWS's own botocore ElastiCache service model, tested, merged, pushed,
and its CI/GHCR-publish runs are both green (`83c1aa73` on `lex00/floci`
main). The fix that was sitting uncommitted in the shared floci checkout as
another session's in-progress work turned out to be correct and complete;
nothing needs re-implementing.

### 1a. #308 fixed; #309 turned out to need a design call after all

`#308` (`corpus-ecs-fargate`) is fixed and merged (`a9ac6d06e7`/`b2bb59585d`,
2026-08-18): `internal/live/identity/foreach_keyset.go` gained the
`*hclsyntax.ForExpr` case and the cross-module-call `var`/`local` chase the
issue laid out, generically - no type names, reaches every module-call
`for_each` proof in the shared `identity` package, reaching more than the
one estate that found it.
Verified against the real crossing (0 occurrences of #308's diagnostic, was
1) and mutation-checked. One side effect worth knowing: #308 had been firing
*first* in `corpus-ecs-fargate`'s `live-plan` output, masking #313
underneath it - fixing #308 didn't unblock the estate, it revealed that
this estate hits #313 too (see 1, above: not a quick fix). The crossing
script's stage-3 assertions/header are now stale about this and need a
follow-up pass, not attempted here.

`#309` (`corpus-alb-complete`, `aws_cognito_user_pool_client`) looked like
the same shape as #308 - scoped, generalizing, no design call - and wasn't,
on first scouting 2026-08-18. **Re-scoped 2026-08-19 with fresh evidence,
and the first scouting's own central conclusion was wrong: this does not
need a new discovery transport.** The re-scope refuted a specific claim
too - that `unique_name_property`/`declared_unique` is "for client-named
binding, a different problem." It is precisely the mechanism admitting
every untaggable `ServerAssigned` type that already works (four of them,
`stamp.go:940`'s `mustStamp` exempts exactly those). It also corrected the
generalization estimate: not "roughly 40" - a tighter count is **18**
similarly-shaped types (the prior filter swept in already-admitted ones).

**The real missing piece is #270's existing `ClassRecordLocated`
mechanism** (already shipped, admitting 124 types at aws 6.59.0) - by
`test_plan` the estate is already migrated, so this was never an
enumeration problem, it's an ownership one, and a scoped Cloud Control
listing couldn't answer it anyway (Cognito doesn't document `ClientName`
as unique within a pool). Three concrete, code-verified predicates
currently block it: (1) the type's primary identifier is only partly
read-only (`ClientId` alone, not the composite), so `markerless()`'s
wholly-read-only requirement excludes it from `MarkerlessTypes`; (2)
`credentialMaterial` correctly fires on `client_secret` (Sensitive); (3)
`locatedImportIDAttr`'s assumption that `id` is the import identity is
wrong for this type (the provider's own docs distinguish the two). Of the
18-type class, only 2 would work today if the veto widened blindly - the
other 16 would move from an honest refusal to a deferred import failure,
so sequencing matters: a composite located payload (filed as `#329`) has
to land before the veto widens, and `credentialMaterial`'s own breadth is
a separate maintainer call. Real, staged, well-precedented work now - not
"a third transport with no precedent to copy." Full evidence on the issue
and `#329`. `corpus-alb-complete`'s stage 3 is unchanged - nothing
implemented yet, this was scoping only.

**`#329` is now built, and its own "not reachable today" premise was
refuted while building it.** Landed 2026-08-19 (`250fd46952`). A located
record now carries an identity OBJECT when the provider's own identity
schema says the string is not the whole identity, and a composite whose
components cannot all be read off an applied object is REFUSED rather than
recorded in part. **It admits no new type and does not widen row-gen's
markerless veto** - `LocatedType`'s first condition is still membership in
`MarkerlessTypes`, untouched - so #309's veto-widening is still its own,
still-unstarted work, now with something safe to land on.

The premise this file repeated above, that the hole is unreachable because
every type of this shape has a partly read-only CFN primary identifier and
so is outside `MarkerlessTypes`, holds for the 16 types the issue
enumerated and **not for the class**. Computed at `334bd26a44` from
`markerless_generated.go` + `live/import-grammar.json` + the offline doc
cache: all 140 `MarkerlessTypes` are outside `DefaultTable` and so reach
`LocatedType`; **43 of them have a non-null documented import separator**;
and of those 43 the docs describe `id` as a bare LEAF, not the composite,
for at least fourteen (`aws_apigatewayv2_route`, `aws_backup_selection`,
the five `aws_datazone_*`, both `aws_emr_instance_*`,
`aws_ec2_client_vpn_network_association`, `aws_glue_partition`,
`aws_route53_traffic_policy`, `aws_ssm_maintenance_window_task`,
`aws_s3outposts_endpoint`), while seven document `id` AS the composite and
were always fine. So this was a live defect in an estate declaring a
`record_store`, not only a sequencing constraint. Two honest bounds on that
figure: it is docs-and-artifact evidence, and `credentialMaterial` and
`hasLocatedImportID` are schema facts not re-checked per type here, so a
type failing either is refused before the defect can bite.

The fix reaches the 16 of the 43 the provider serves an identity schema
for. **The other 27 are the remaining debt and are named as such**: their
composite import is documented but not in any schema, so nothing at run
time can tell a leaf `id` from a whole one, and today's rule still admits
them on the string. Closing it means row-gen emitting a derived
composite-import roster the way it already emits `MarkerlessTypes` - the
grammar it would read from is `live/import-grammar.json`'s `separator`,
which it already parses. Not attempted: it is generator work, and
`tools/row-gen` was held by another agent.

**The roster is built** (`#337`, 2026-08-19), and the 43/16/27 split above is
confirmed by an independent recomputation. `just composite-import` writes
`live/composite-import-roster.json`. One correction to the paragraph above,
though: the fix does **not** reach all 16 of the schema-backed types. Only
**8** of them have a wire identity schema that requires `id` alongside
another attribute, which is what `compositeIdentity` gates on; the other 8
require two non-`id` attributes and fall through to the same bare-`id` rule
as the 27. Whether that fallback is right for them is a separate,
unmeasured question.

The new evidence is a doc section nothing here had read: the Attribute
Reference's own `id` bullet, which is the only account of what the provider
puts in `id` after a create - every other grammar field describes the import
STRING. `tools/importdocs-gen/idattribute.go` scrapes it, and the roster's
verdict is that sentence's stated separator **agreeing** with the Import
section's independently-scraped one. No component order is read, from prose
or anywhere else: "is `id` the whole import string" is a yes/no question and
a yes needs no grammar, which is what keeps this clear of the order
counterexamples #309 documented. Validated provider-wide rather than on the
27: of 1693 pages, 968 carry an `id` bullet, 123 state a composite, 120 are
corroborated by the Import section and 3 have nothing to check against -
**zero contradictions**.

Result on the 27: **6 proven whole, 21 unproven** (12 whose page documents no
`id` attribute at all, 9 whose `id` bullet states no composite form). The 9
are deliberately left unproven rather than called leaves - "The EMR Instance
ID" under a composite documented import is what a leaf looks like *and* what
an incuriously written page says about a whole one, and weak evidence of a
leaf is not proof of one.

This is classification only. Wiring it into `identity.LocatedType` - which
would move the 21 from a silent wrong record to an honest refusal - is
#309's next step and is sequenced after this on purpose.

**That step landed 2026-08-19, and it is BOTH halves rather than the one the
brief for it named.** `tools/row-gen/markerless.go`'s
`primaryIdentifierPartlyReadOnly` widens the veto's registry evidence from
"the primary identifier is WHOLLY read-only" to "some component is minted and
some is supplied", which is the question the veto actually asks -
`identity.Resolve` cannot compute an identity with one unknown component any
more than one with all of them unknown. `classify.go`'s bucket is deliberately
**untouched**: rule 1 decides whether a pastable `serverAssigned(...)` row
describes the type, and for a server-minted leaf under a config-known parent
it does not. Conflating those two questions is what #309 spent three scouting
passes recovering from, and widening the bucket would have re-made the error
in the other direction.

`MarkerlessTypes` 140 -> **158**. Every one of the 18 is untaggable with a
mixed primary identifier, asserted per type against the committed inputs by
`TestWidenedVetoReachesOnlyUntaggableMixedIdentifiers`, and the leg was
mutation-checked in place (removed, the test reports the delta is empty).

**The other half is what makes the first half safe, and it had to land in the
same commit.** #337's verdict is now a fact the runtime can read:
`internal/live/identity/idnotwhole_generated.go`'s `IDNotProvenWholeTypes`,
emitted by `tools/row-gen/idnotwhole.go` from the same
`classifyCompositeImport` rule the roster report uses. It is derived over
**every** row in `live/import-grammar.json` (317 types) and not over
`MarkerlessTypes`, on purpose: `-emit` rewrites `MarkerlessTypes` in the same
run, so a set derived from it would be derived from the previous run's answer
- #263's failure mode with a different name. `LocatedIdentityComponents` now
takes the resource type and refuses a member in its bare-`id` fallback; the
wire-schema composite branch still wins where it applies, pinned by
`TestLocatedIdentityComponentsPrefersTheWireSchemaOverTheDocs`.

**Measured against real hashicorp/aws 6.59.0 schemas** (`CHOUDOUFU_LIVE_SCHEMAS=1
go test -run TestLocatedTypePopulation`, 288s): of the 158, **97 located by
string id, 8 by composite object, 11 credential material, 39 refused as
unproven `id`, 3 with no string id**. So 105 types are record-located, of
which **4 are new** - `aws_api_gateway_resource` through #329's composite
branch, and `aws_elastic_beanstalk_configuration_template`,
`aws_lexv2models_bot_version`, `aws_vpn_gateway_attachment` through the
bare-`id` rule their own docs support.

**This is a support change and the direction is deliberate.** 26 types that
were markerless before now refuse where they previously recorded a bare `id`:
19 of the 21 #337 named (the other 2, `aws_kms_grant` and `aws_wafv2_api_key`,
were already refused as credential material), plus 7 with a wire identity
schema requiring two non-`id` attributes, which fall through to the same bet
and which #337 left as "a separate, unmeasured question". How many of those 26 were actually
located-admitted before (rather than already refused for having no string
`id`) is **not measured** - the before/after run needed a second provider
install and the machine was carrying 91 concurrent terraform processes; two
attempts timed out at ten minutes each. 26 is the upper bound, not the figure.

Two things the brief for this was wrong about, both checked rather than
inherited. First, #329's 8 and #337's 6 are **not** the types the widening
admits: both figures are over `MarkerlessTypes`, so they describe types
already in the roster. The widening's own population is #309's 18, and its
gain is the 4 above. Second, all 18 already carried an entry in
`tools/row-gen/rejected.json`, so this is not the "third bucket with no ledger
entry" #309's scoping comment described and `unreached-types` does not move
(462, unchanged).

Residue worth knowing before touching the area: for the 14 of the 18 that stay
refused, the refusal ID changes from `unadmitted-type` to `markerless-type`,
whose standing wording ends "no future batch reaches it". That is true of the
carrier and not of the evidence - a page that proved its `id` shape would
reach them. Differentiating it needs `LocatedType` to return a reason rather
than a bool, which touches every markerless type's refusal text and is its own
change. `aws_cognito_user_pool_client`, #309's own motivating type, is refused
twice over: `client_secret` is Sensitive, and its `id` is unproven.

### 1b. `#316` fixed: the rename-withholding guard now fires for module-qualified addresses

Was a real silent destroy-recreate hazard, found scouting a smaller loose
end (`markers.UnescapeAddress` decoding a count'd module step's key as a
string instead of an int, fixed - `f6c6541748`/`11a8178c52`). While tracing
that fix's reach, the same session found `classifyOrphans`'s
rename-withholding guard - the property its own doc comment calls the whole
safety mechanism - never fired for a module-qualified address at all, and
`internal/live/foreign/classify.go`'s `removals()` had the mirror-image bug.
Fixed 2026-08-18 (`a30cb152f8`/`4d02f05d30`, merged `f1567a63b8`), with the
same rigor the marker fix got: both root causes reproduced with real values
before any code changed, both fixes mutation-checked (revert in place,
confirm the new tests fail exactly as expected, re-apply, confirm green).

One deliberate departure from the filed issue, worth knowing before touching
this area again: the issue said "module-qualify both sides," but the
declared side keys on type-and-name only (`blockKey`, not the full
instance) - **a strict superset of the guard's prior safety**, so no
configuration that planned no destroy before plans one now. The reasoning
is a resource block moved *out* of a module and into the root is exactly as
safe to withhold as the reverse, and the old code accidentally already
withheld that direction; module-qualifying the declared side naively would
have started destroying it. `TestClassifyOrphans_aBlockMovedAcrossModulesStaysWithheld`
pins both directions.

`TestIdentityGolden`: 0 changed, 4 added (the new fixture), 0 removed - the
zero is the load-bearing half, since this sweep renders identities without
ever classifying an orphan.

New, smaller follow-up this fix exposed rather than caused: a
module-qualified withhold is now correctly computed but invisible in the
plan output - `internal/live/foreign/rename.go`'s pairing logic is
root-only (`// v0 declares no modules`), so the "Renamed keys?" section a
user would read to understand *why* nothing was destroyed never mentions a
module-qualified withhold. Pre-existing, newly reachable, connects to
`#317`'s same root-only assumption. Worth a scouting slot, not attempted.

Separately, `#317` (design, not a bug): `live-mv`'s `checkAddresses` refuses
a rename through a count-keyed module step on the exact retired premise
`markers.UnescapeAddress` carried before today's fix - `live/LIMITATIONS.md` now
documents the idiom as admitted, so an estate using it can never `live-mv`
into the root the way `mv.go`'s own package doc advertises. Left alone on
purpose: whether to admit the rename is a maintainer call, not a comment fix.

### 2. The core set

A small, deliberately chosen set of estates - the plainest reference shape,
an S3 bucket, an IAM role, a security group, a Lambda function - are the ones
worth driving all the way to a genuine five-of-five pass before this project
is shown to anyone outside it. Four of five clear all five stages as of
2026-08-18 (`reference-ec2-vpc`, `corpus-s3-bucket-complete`,
`corpus-iam-policy`, `corpus-iam-read-only-policy`); the security-group one
is the remaining gap, and as of 2026-08-19 every wall left in it belongs to
the AWS provider rather than to this fork - see the #332 entry below. `corpus-rds-complete-postgres` (outside the core set,
same `security-group`/`vpc` module family) reached the equivalent state
2026-08-19 after #321/#324/#323 cleared every other derivable wall it had.

**"Root cause B" was the wrong name for this - and so was this file's own
first correction. Read this whole entry, including its own retraction,
before touching the area again; two people have now been wrong about it
in different directions and the third pass is the one that measured
instead of reasoning.** It was first recorded as "a `for_each`/`count`
keyed on another managed resource's own LIVE attribute", judged too risky
for a quick follow-on to #313's data-source ruling. A scouting pass
2026-08-19 (evidence only, no code) reported that framing wrong on both
estates - "neither needs a live read, every value is a literal" - and this
file repeated that claim. **A second, implementation pass 2026-08-19
checked the scouting claim by actually building and mutation-testing a
fix, and found it right about the values but wrong about the mechanism.**
`corpus-rds-complete-postgres`'s chain genuinely does cross a managed
resource - `module.vpc.vpc_cidr_block` is `try(aws_vpc.this[0].
cidr_block, null)` - and `aws_vpc.cidr_block` is **Optional+Computed** in
the real AWS provider schema (verified via `terraform providers schema
-json`), so even a perfect structural walk lands on a deliberate Computed
gate this repo already has (`internal/live/identity/resolve.go:2414`,
`siblingLiteralExpr`). All 58 corpus sites carrying this diagnostic follow
`terraform-aws-modules`' own house style, `try(<managed resource
attribute>, fallback)` - none of them are the pure-literal shape the
scouting pass's one hand-verified substitution happened to produce.

**The #191 regression is not fixed, and this file's claim that it was is
retracted.** #191 ("wall: Module output not supported in static context -
blocks 12 of 79 estates", closed on process not merit, prior art worth
reading in full) measured a naive permissive-evaluator fix at **-16
instances, 0 configs unblocked**. This file previously claimed `36757b988e`
fixed that loss mode. **Re-measured against current main 2026-08-19: the
naive fix now costs -49 instances, three times worse, still 0 configs
unblocked, 18 corpus entries regressed.** `36757b988e` fixed a *different*
loss mode - a managed reference dropping out of the symbolic path - not
the module-output structural-walk gap #191 and this wall both need. Do
not re-cite that commit as the fix for this; it isn't.

**A real, narrower fix landed anyway, correctly scoped, and it reaches
neither estate today.** `7b10c0ef25`/`0dafd48b63`
(`internal/live/identity/moduleoutputvalue.go`, new) resolves a module
output referenced inside a module-call argument when the output's own
expression evaluates to a wholly-known, non-null, unmarked, non-sensitive
value through the child module's pure evaluator - genuinely safe, proven
by six mutation-tested fixtures (one resolving case with a value that
could only come from the real source, five adversarial refusals: an
Optional+Computed managed attribute, a plain-Optional one, `uuid()`,
`sensitive = true`, an ambiguous multi-element list). Corpus generalization
from this specific fix: **zero** - the current corpus has no site of the
pure-configuration-literal shape it handles; every real site is the
Computed-attribute shape it correctly declines. `corpus-rds-complete-
postgres` stage 3 stays at exactly 2 diagnostics, unchanged, confirmed by
byte-identical site lists before and after. This is real, correct,
foundational work with zero current payoff - land it for what it is, not
for what it doesn't yet reach.

**What the next slot actually needs, per the implementation pass's own
closing analysis, is two different design changes, not two more scoped
fixes:**
1. **LANDED 2026-08-19** (`312acbbb61`/`75ef0a6a78`).
   `corpus-security-group-complete` needed `tolerantVariables` to carry a
   **formula**, not a `cty.Value`, across a module-call argument boundary
   - #191's own closing recommendation. `internal/live/identity/
   partialargs.go` now builds the tolerant evaluator with a tolerant
   `var.*` closure for the PARENT module instance, recursively, so a
   substitution survives more than one module call; and
   `composedArgument` evaluates a whole argument through that evaluator
   when the caller wrote `merge()` rather than a constructor, taking the
   function's own answer about an unknown instead of reconstructing the
   call. No provider type name anywhere in it. Eleven unit tests, three
   of them adversarial refusals. `TestIdentityGolden` 0 changed, 6 added,
   0 removed; over `.corpus`, 22398 -> 22424 instances, 0 removed, 0
   modified, all 26 added rows `NEEDS_DISCOVERY` with an empty value.
   The estate's own crossing, run for real against floci, printed
   `STAGE 3 (test_plan): BLOCKED for real, at 4 sites` (was 239, then 19,
   then 7): **every analysis-layer refusal is now zero and each zero is
   asserted by absence** - #305/#307's unadmitted-type, #313 root causes
   A and B, and #321. The 4 remaining are the plan's entire `^Error:`
   surface and all of them are `#332` (2 `Cannot import for projection` +
   2 `empty result`, both `aws_default_route_table`, one per nested vpc
   call), so **#332 alone is what now blocks the estate**, newly reached
   rather than caused - the plan only gets as far as importing all 67
   resources because the analysis walls are gone. Nothing here decided
   the managed-attribute question in (2) below: `aws_security_group.app.
   id` is still not resolved from configuration, and the estate turned
   out never to need its VALUE, only the key set it travels with.

   **#332 is now FIXED too, and it moved the estate to 1 diagnostic -
   which is not a choudoufu one.** Landed 2026-08-19
   (`859c1ad747`/`ff1f6bcdea`/`c1197befc7`). The ratified row claimed the
   route table's own `rtb-…` id on the reasoning that the provider's
   Import section could not be followed literally, since the schema has no
   `vpc_id` argument. It has none, but `vpc_id` is a computed **attribute**
   and that is all the importer needs; stock terraform 1.15.8 with
   hashicorp/aws 6.59.0 answers `Error: empty result` for the `rtb-…` id
   and `Import successful!` for the VPC's. `live/import-grammar.json` had
   already extracted this correctly (`sole_id_part {"token": "vpc_id",
   "source": "attribute"}`) - the scrape was right and the ratification
   overrode it, so the row and the convergence annotation repeating the
   claim are both corrected.

   The code half split one predicate that had conflated two facts.
   `defaultAdopterSiblings` proved "these two names are one live object" by
   requiring the two rows to name the same import identity, which is the
   separate question of whether the id already read carries forward. Now
   `defaultAdopterSiblings` keeps only the object-identity proof and
   `sameRatifiedIdentity` is its own predicate; when they disagree,
   `importIdentityFromResource` recomposes off the listed object, driven by
   the bind type's own `IdentityAttrs` rather than a hard-coded `"arn"`, so
   one code path now serves #302's `aws_iam_service_linked_role` (`arn`)
   and this (`vpc_id`) with **no provider type name in it**. A third piece
   was found only by running the estate: `scanTypeMarkerFallback` composed
   a second, `rtb-…` claimant from the object's ARN, and post-fix that
   collided with the correct `vpc-…` one as `ProblemCollision`. It now
   declines for such a type - a route table's ARN carries no VPC id, so
   composing there succeeds with the WRONG string - staying silent when the
   plain sibling is declared and refusing explicitly when it is not.

   **Reach, stated honestly: one type today.** The derivation is generic
   and picks up any future `aws_default_X`/`aws_X` pair whose rows diverge
   with no code change, but `aws_default_route_table` is the only member of
   that set at aws 6.59.0. `#302`'s pair reuses the same recomposition and
   is unaffected because its identity is its ARN.

   **The crossing, run for real against floci** (stage 3's whole `^Error:`
   surface, greped from the run's own log): **239 -> 19 -> 7 -> 4 -> 1**.
   `aws_default_route_table` is named by **no diagnostic at all**, the
   collision is gone, and every choudoufu wall of every layer is at zero.
   The 1 remaining is the AWS provider failing on itself:
   `Provider produced invalid plan` - `"requires replacement"` on
   `module.security_group.aws_vpc_security_group_ingress_rule.this
   ["dns-from-prefix-list"]` for the non-existent attribute path
   `cty.Path{cty.GetAttrStep{Name:""}}`, the provider's own message ending
   "This is a bug in the provider, which should be reported in the
   provider's own issue tracker." It is the one rule in the estate sourced
   from a prefix list rather than a CIDR or a referenced security group.
   **Filed as #335** - it is the last thing between this estate and
   five-of-five, and it is genuinely outside this fork's own code (the
   diagnostic names no choudoufu path).

   **The script itself needed a real update pass too, found only by
   actually re-running it clean.** Three separate belt-and-suspenders
   assertions (`aws_default_network_acl`/etc.'s code-frame loop, the
   `aws_default_route_table`-named-nowhere check, and #313's
   `aws_availability_zones`-absent check) were all written back when the
   plan always errored out before reaching real execution, as bare
   substring matches over the whole plan output. Once #332 let the plan
   reach PROJECTION for real, each one started matching its own type's
   ORDINARY, non-error plan-diff or data-source-refresh output instead of
   an actual diagnostic - three separate false failures, found one at a
   time by dumping the real plan text and reading it rather than trusting
   the assertion's own error message. Fixed the same way each time: match
   only the diagnostic's own numbered source-line echo (the pattern
   `corpus-giantswarm-crossplane`'s own script already used), never a bare
   substring. Confirmed clean: real run, `NORMAL_EXIT:0`, blocked at
   exactly 1 site (the #335 provider bug); `BREAK=1` correctly fails.

   One honest gap remains worth knowing. `refusal-probe` over 250 corpus
   configurations (same tree both runs) shows sites 16068 -> 16074 (+6) and
   **instances 4417 -> 4413 (-4)**, blocked 194 -> 194. The -4 is the point
   rather than a regression: `.corpus/cyhy-amis` and `.corpus/cool-
   assessment` write `aws_route.route_table_id = aws_default_route_table.X.
   id`, and `.id` is no longer one of this type's identity attributes, so
   those children refuse instead of silently taking a VPC id for a route
   table id. Recovering them needs per-attribute values for a *discovered*
   parent, which `discovery.Binding` does not carry - a real follow-up, not
   filed. Stage 3's rewritten assertions (including step 3a, which
   re-derives each default route table's import identity from AWS itself
   and asserts it BY VALUE) HAVE now been executed end to end for real, on
   a clean run with the three assertion fixes above - see that entry.
2. `corpus-rds-complete-postgres` needs (1) plus an actual, currently
   unmade ruling: **may this fork ever resolve a managed resource's own
   Computed attribute off configuration alone** (not read the cloud,
   read what the provider's own defaulting/normalization would produce)?
   `resolve.go:2414`'s gate refusing this is deliberate, not an oversight
   - revisiting it is squarely the maintainer's call, closer to the
   original architecture question than either prior framing landed on.

**A second, real defect sits immediately behind this wall on both
estates, filed as `#325` - and already fixed, independently of the
module-output question.** `internal/live/discovery/discovery.go`'s marker
type-equality check treated `aws_default_route_table`/`aws_route_table`
and `aws_default_security_group`/`aws_security_group` as mismatched types,
calling a correct marker malformed. Fixed 2026-08-19 (`bf1ad64982`/
`92f8fb7b55`), derived generically from the `aws_default_` prefix cross-
checked against each pair's shared ratified identity fields rather than a
hand list, so it also covers `aws_default_vpc`/`aws_default_subnet`/
`aws_default_vpc_dhcp_options` automatically whenever a future issue
admits them. Neither estate reaches five-of-five without this, but it
alone doesn't move either past the module-output wall above it.

`live/corpus-crossing-manifest.json` says which ones currently clear which
stage and why the rest do not; do not trust a stale count copied here
instead.

Not every remaining blocker in that set is a bug - though the example this
paragraph used to lead with was wrong and is retracted. A `local_file`
resource was described here as "correctly refused (no cloud counterpart to
reconcile against; already ruled on)"; the local filesystem is the
counterpart, and #314 admitted the type (item -2 above). A residue gap on
two S3 attributes whose provider `Read()` needs
genuinely-remembered prior state a stateless discovery run cannot supply are
both legitimate reasons to scope an estate around one resource rather than
force it through - the same way the OpenTofu-native crossing scoped itself to
the one host role with a real provisioning off switch. That is a documented,
deliberate choice, not a workaround to be embarrassed about, as long as the
script's own header says which resource and why.

### 3. Broaden the OpenTofu-native lane

Eight estates crossed now. The first six: `corpus-sumaform-aws`; three disjoint slices of
`hongbo-miao/hongbomiao.com` (`corpus-hongbomiao-labelbox`, landed
2026-08-18, the first OpenTofu-native estate to clear all five stages and
stronger evidence than sumaform - genuine `.tofu` files throughout, not a
`.tf` template that merely describes itself as OpenTofu-built;
`corpus-hongbomiao-storage`, landed 2026-08-18, reusing the same pin;
`corpus-hongbomiao-harbor`, landed 2026-08-19, also reusing the same pin -
the Harbor S3-bucket/IAM-user section, the one part of `kubernetes/main.tofu`
that needs no EKS cluster or remote state, everything else in that file
being 15 IAM modules all reading a real cluster's OIDC provider, the same
scope/risk class as the terraform-popular lane's already-blocked
terraform-aws-eks crossing); and `corpus-overture-tiles`,
`corpus-xancloud-iac` from fresh sourcing searches. **Three** of the six
clear all five stages for real, not four - that figure said four here from
2026-08-18 until 2026-08-19, and `live/corpus-crossing-manifest.json`, which
is the source, has never agreed with it: of those six, `corpus-hongbomiao-
labelbox`, `-storage` and `-harbor` are all-pass, while `corpus-sumaform-aws`
and `corpus-xancloud-iac` are `test_plan: fail` and `corpus-overture-tiles`
is `migrate: fail`. Count it off the manifest rather than off this
paragraph. With the seventh and eighth below, **five of eight** do.

Seventh, landed 2026-08-19 from a fresh sourcing search:
`corpus-giantswarm-crossplane`, the `crossplane/` module of
`giantswarm/giantswarm-aws-account-prerequisites` (pinned by tag v8.2.2 AND
commit). It is the first estate in this lane from a **commercial vendor's
own production customer-onboarding repository** rather than a module
registry, a personal monorepo or a single-maintainer accelerator, and the
sourcing evidence is worth copying: three independent kinds at once - a
README whose opening sentence is "This repository contains OpenTofu
configuration" with no compatibility claim anywhere, a workflow of its own
named "OpenTofu checks" that runs `tofu` through `opentofu/setup-opentofu`
with the string "terraform" appearing nowhere in it, and genuinely
`.tofu`-suffixed files in the crossed directory. That last
one is the standard only `corpus-hongbomiao-*` had met before;
`corpus-overture-tiles` and `corpus-xancloud-iac` are both plain `.tf`.
**It clears all five stages as of 2026-08-19**, so five of the seven do.
`test_plan` was BLOCKED at exactly 2 sites, both `unadmitted-type` on
`aws_iam_role_policies_exclusive` and `aws_iam_role_policy_attachments_exclusive`
(`#334`), with a control stage that cut those two blocks and nothing else out
to prove they were the whole block. They were; `#334` ratified both rows and
the control retired with the block it controlled for.

**`#334` guessed at a generator gap and there was none, which is the part
worth carrying forward.** `go run ./tools/row-gen -service '(no CFN model)'`
proposed both rows all along, client-named, under the same rule that produced
the `aws_vpc_security_group_rules_exclusive` row `#307` ratified - "import-grammar
precedence: composed_of_arguments, single argument, arity confirmed against the
example string" - resolving `role_name` off the provider's own Import
documentation. Nobody had ratified the proposal. The `force_new` difference the
issue flagged as a possible gate is not one; that branch reads no `force_new`
field. `-convergence` now scores both rows `"matched": true`.

So the reach is exactly two types, and that is the honest number: this is a
ledger decision, not a rule. **The population behind it is the finding.** 316
types row-gen proposes today sit unratified, 166 of them under this exact
rule, including every other member of the same `*_exclusive` family -
`aws_iam_group_policies_exclusive`, `aws_iam_group_policy_attachments_exclusive`,
`aws_iam_user_policies_exclusive`, `aws_iam_user_policy_attachments_exclusive`,
`aws_ram_resource_share_associations_exclusive`, `aws_route53_records_exclusive`,
`aws_cloudfrontkeyvaluestore_keys_exclusive`. That is a ratification backlog
rather than a generator defect, and working it is a maintainer-scale decision:
every row a human ratifies is a claim that touches live infrastructure, which
is why `-emit` copies them verbatim and no generator writes that file.

One thing found running it, worth knowing before any crossing script is timed
again: the shared plugin cache records no checksums, so `init` in a directory
with no `.terraform.lock.hcl` re-downloads the whole ~600MB AWS provider purely
to compute them, even when the cache already holds that exact version. Measured
here at 320s, twice per run, which is most of a crossing's wall time and is what
put three attempts past a ten-minute cap. Seeding the second directory's lock
file from the first init's takes that init 320s -> 1s and a full five-stage run
to 250s. Every crossing script that runs more than one `init` has this.

Eighth, landed 2026-08-19 from a fresh sourcing search:
`corpus-evoteum-modules`, the `aws/networking` and `aws/dynamodb` modules of
`evoteum/tofu-modules` (pinned by commit alone - the repository publishes no
tags, and its own README says why: "As these are only small modules, we have
chosen not to publish them to the Tofu registry"). Evoteum Ltd is the second
commercial vendor in this lane, and the OpenTofu-native evidence is of four
independent kinds, all asserted by the script rather than described in its
header: a self-description as OpenTofu's with no compatibility claim; **not
one `.tf` file anywhere in the pinned tree** (109 `.tofu`, 0 `.tf`, checked
over the whole clone rather than the crossed directories);
`.pre-commit-config.yaml` running `tofuutils/pre-commit-opentofu`'s
`tofu_validate`/`tofu_fmt` with no Terraform hook configured at all; and
`.tofutest.hcl` unit tests, **the first piece of evidence in this lane that
Terraform could not even parse**. It is production code rather than a
showcase: the same organisation's `estate-config` repository calls
`aws/bucket` from it over a `setproduct()`-built `for_each` map.
**All five stages pass** - 10 instances cold-deployed, 7 stamped, 3
`aws_route_table_association` correctly UNTAGGABLE and re-derived from
tagged parents, an empty replan, a genuine no-op apply, and drift on the
VPC's `Name` tag reconverging without touching anything else. Both negative
controls (`BREAK=1` on stage 2's marker, `BREAK_STAGE5=1` on stage 5's
one-object assertion) verified in real runs.

**What that crossing found by getting it wrong first is worth carrying.** The
script's first version asserted the subnet markers as their addresses
verbatim, `module.networking.aws_subnet.public["10.0.101.0/24"]`, and found
nothing. An AWS tag value admits only `[A-Za-z0-9 _.:/=+@-]`, so
`internal/live/markers`' `EscapeAddress` writes
`module.networking.aws_subnet.public:10@d0@d101@d0/24` instead. This is the
first estate in **either** lane whose `for_each` keys fall outside that
charset, so that layer is load-bearing here where every earlier crossing's
keys were absent or already legal. The three expected strings are now
written out by hand from the rule at `internal/live/markers/markers.go:196`
rather than computed by the function under test, and each is asserted to be
inside the charset - an escaping that emitted an illegal value would fail on
real AWS while passing against a lenient emulator. Two things follow for
whoever writes the next crossing script: a marker assertion on any
`for_each`-expanded resource must expect the escaped form, and the AWS CLI's
`Name=,Values=` filter shorthand is the wrong tool for a marker lookup - it
splits on `,` and `=`, both legal tag-value characters - so use the JSON
`--filters` form.

The scoping was two of the repository's eleven AWS modules; the other nine
are excluded with a stated reason each in the script's header. One is worth
repeating because it is a known wall rather than an unknown: `aws/bucket`
names its bucket from `random_password.bucket_suffix.result`, which is the
secret-bearing twin of the `random_pet` identity argument item -2 records for
`corpus-lambda-simple`. Crossing it would re-find that, not measure anything.

The monorepo hongbomiao was sourced from has now
been surveyed in full at the pinned commit - `network/main.tofu` is pure
data sources (nothing to migrate), `kubernetes/main.tofu`'s IAM modules all
need a live EKS cluster's OIDC provider (out of scope, same reason as
above), and Nebius/Cloudflare/Snowflake target clouds floci cannot emulate
at all - so this repo has nothing further to offer without a new sourcing
search. Against a Terraform-popular lane that started with ten modules
already pinned by tag and commit before crossing began, this lane still has
no equivalent ready-made list - there is no download-count proxy at
OpenTofu's current scale - so sourcing has to stay active: GitHub search
for real, maintained projects that describe themselves as built for
OpenTofu, plus the Powered-by-OpenTofu and awesome-opentofu lists.

What has actually worked, three times now, is **GitHub code search on
`extension:tofu` crossed with AWS resource type names** - `extension:tofu
aws_iam_role`, `extension:tofu aws_vpc`, and so on, then ranking the
repositories that recur by whether they are real and maintained. That is
what found `corpus-giantswarm-crossplane` and `corpus-evoteum-modules`.
Two things to know before repeating it. The code-search rate limit is
**10 queries per minute**, separate from the 5000/hour core limit, so batch
the queries and expect 403s; and the result set is dominated by course
material, homelabs and scaffolds - of roughly forty distinct repositories
surfaced, three were worth reading and one was worth pinning. The evoteum
search reproduced that shape almost exactly: 24 queries in three batches,
**41** distinct repositories, of which one course repo and one policy-test
fixture repo accounted for the two largest hit counts, and one organisation
was worth pinning.

Two search axes were tried on that pass and produced nothing pinnable, which
is worth recording so the next search does not re-run them. **OCI module
sources** (`extension:tofu "oci://"`) returned exactly one repository,
`V3RO/homelab`, and its OCI source is a Flux/Kubernetes chart, not an AWS
estate. And a **licence filter is doing more work than expected**: three
otherwise-plausible AWS-targeting `.tofu` estates - `harik8/awsing`,
`Akatama/website`, `datarockets/infrastructure` - carry no licence at all,
and every existing pin in `live/corpus-manifest.json` records a checked
licence. `awesome-opentofu` and
Powered-by-OpenTofu were checked again and are still what
`corpus-overture-tiles`'s own sourcing found them to be: tooling and adopter
lists with no deployable estates in them.

The other productive axis is **state encryption**, which is genuine
OpenTofu-only surface: `extension:tofu "encryption {"` returns real,
maintained projects (osinfra-io's `pt-*` estates, `vehagn/homelab`,
`brettcurtis/backstage`, `Five-Colleges-Incorporated/library-infrastructure`).
Almost none of them target AWS - they are GCP, Proxmox, Talos, Hetzner and
TrueNAS - so none was pinnable here, and one caveat is worth recording
before someone spends a slot on it: the crossing pipeline strips the backend
block, so an estate's state-encryption configuration is not exercised by any
of the five stages anyway. Provider `for_each` and OCI-sourced modules are
the OpenTofu-only surface that would actually be exercised, and neither has
turned up in a real AWS-targeting estate yet.

### Loose ends worth an hour, not a slot

- `internal/live/mv`'s `checkAddresses` still cites the same retired premise
  `markers.UnescapeAddress` did before 2026-08-18's fix - see `#317`, above.
- `#324` item 1 (`coalescelist()`) and #325's own follow-ups are done; #315,
  `lint.worstCaseChildKey`, and `live/survey-full.json`'s stale `path` are
  all fixed too and recorded in "Rulings worth not relitigating" rather
  than left here, since more than one has already been re-opened from this
  list after landing.

---

## Rulings worth not relitigating

Kept because each was reached by measurement and each has been re-opened at
least once from prose alone.

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
