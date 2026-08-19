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

1485 rendered identities across 455 configuration directories in under a
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

### 1. Resolved: #313 does not repeat, and it isn't a quick fix anyway

Checked 2026-08-18: #313's `data.aws_availability_zones`/static-context wall
does **not** explain any of the other five `test_plan`-stuck estates. Each
hits its own, distinct, already-tracked cause -
`corpus-lambda-simple` #314 (a new issue: `local_file` needs a fourth
`LogicalClass`, argument-derived identity - real multi-package design work,
not filed as a quick fix), `corpus-rds-complete-postgres` #304
(`count.index` through a nested `lookup()` default), `corpus-ecs-fargate`
#308, `corpus-sumaform-aws` two permanent RULE-classified refusals (#199,
#103, no action needed), `corpus-alb-complete` #309. Evidence and commit
references are in each estate's own entry in
`live/corpus-crossing-manifest.json`. Two of those five checks also found
and fixed stale crossing-script assertions left over from #305 landing
(`corpus-ecs-fargate`, `corpus-alb-complete`), now on main.

**#313 itself is not a bug to derive a rule for - re-read the issue before
assigning it again.** Its own body traces the refusal to `internal/live/
passthrough/refusals.go`'s already-registered "Dynamic value in static
context" class: `live-plan` never calls a provider during plan (statelessness,
#73), so a `for_each`/`count` keyed on a data source's live values, or on
another resource's own attribute, can never be proven statically - the same
family as the CIDR-keyed `for_each` wall CLAUDE.md already documents.
Closing it for real would mean deciding whether `live-plan` may call
read-only provider APIs during plan at all, a real architecture question for
the maintainer, not a derivable rule a background agent should freelance.

Separately, lex00/floci#70 (`CreateCacheSubnetGroup`/`ModifyCacheSubnetGroup`
wrong `SubnetIds` wire param name, found re-verifying `corpus-vpc-complete`
against a freshly published image) is now fixed, independently verified
against AWS's own botocore ElastiCache service model, tested, merged, pushed,
and its CI/GHCR-publish runs are both green (`83c1aa73` on `lex00/floci`
main). The fix that was sitting uncommitted in the shared floci checkout as
another session's in-progress work turned out to be correct and complete;
nothing needs re-implementing.

### 1a. Next up: two generalizing fixes with no design call needed

`#309` (`corpus-alb-complete`): `aws_cognito_user_pool_client` is untaggable
with no native list resource, but the issue's own body already names the
fix shape - a parent-scoped `{user_pool_id}/{client_id}` composite identity,
same pattern as other admitted composite-identity rows, discoverable via
`ListUserPoolClients(UserPoolId=<parent's live id>)`. Needs a generated
admission row plus, probably, a new `internal/live/discovery` leg - scoping
that is explicitly the next slot's work per the issue.

`#308` (`corpus-ecs-fargate`): a child-module `for_each` over a
for-comprehension whose keys are static but whose source collection is a
bare `var.X` chased across a module-call boundary. The issue lays out both
gaps precisely (`internal/live/identity/foreach_keyset.go`'s
`collectStaticForEachKeys` needs a `*hclsyntax.ForExpr` case mirroring
`resolve.go`'s `forEachOverComprehension`, plus a cross-module-call chase
reusing #212/#251's existing machinery) and explains why it generalizes
beyond ECS: any module wrapping a `for_each`'d child on a map-of-configs
variable with one data-sourced value hits this.

### 2. The core set

A small, deliberately chosen set of estates - the plainest reference shape,
an S3 bucket, an IAM role, a security group, a Lambda function - are the ones
worth driving all the way to a genuine five-of-five pass before this project
is shown to anyone outside it. Four of five clear all five stages as of
2026-08-18 (`reference-ec2-vpc`, `corpus-s3-bucket-complete`,
`corpus-iam-policy`, `corpus-iam-read-only-policy`); the security-group one
is the remaining gap, currently on #313 above. `live/corpus-crossing-manifest.json`
says which ones currently clear which stage and why the rest do not; do not
trust a stale count copied here instead.

Not every remaining blocker in that set is a bug. A `local_file` resource
correctly refused (no cloud counterpart to reconcile against; already ruled
on) and a residue gap on two S3 attributes whose provider `Read()` needs
genuinely-remembered prior state a stateless discovery run cannot supply are
both legitimate reasons to scope an estate around one resource rather than
force it through - the same way the OpenTofu-native crossing scoped itself to
the one host role with a real provisioning off switch. That is a documented,
deliberate choice, not a workaround to be embarrassed about, as long as the
script's own header says which resource and why.

### 3. Broaden the OpenTofu-native lane

One estate crossed so far, against a Terraform-popular lane that started with
ten modules already pinned by tag and commit before crossing began. The
OpenTofu-native lane has no equivalent ready-made list - there is no
download-count proxy at OpenTofu's current scale - so sourcing has to stay
active rather than exhaust a prepared queue: GitHub search for real,
maintained projects that describe themselves as built for OpenTofu, plus the
Powered-by-OpenTofu and awesome-opentofu lists.

### Loose ends worth an hour, not a slot

- `lint.worstCaseChildKey` (`internal/live/lint/lint.go:198`) returns
  `addrs.NoKey` for a count'd call, so `checkOverlongAddresses` under-measures
  the address budget inside every count'd module.
- `live/survey-full.json` carries a stale `path` for
  `aws_s3_account_public_access_block` that regeneration moves. It feeds
  row-gen, so it is not a no-op edit.
- `row-gen`'s report still names paste targets that no longer exist
  (`table_cohort_<cohort>.go`, `admission_cohort_<cohort>.go`). The target is
  `tools/row-gen/ratified.json`.
- #263's cure is half done. `ratified.json` holds the rows with a
  byte-identical round-trip proof, and `-emit` still reads `DefaultTable`. The
  flip is three reads - `emittedRows`, `buildConvergence`, `markerlessRoster` -
  and the convergence one is load-bearing.

---

## Rulings worth not relitigating

Kept because each was reached by measurement and each has been re-opened at
least once from prose alone.

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
