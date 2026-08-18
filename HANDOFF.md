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

The nearer goal, and the one every open task now serves, is that
`tools/estate-gen` produces **properly marked estates of varying complexity,
all of which work** - plan exact, apply succeeds, second plan empty, markers
on the right objects.

### Adoption is a different question, and the corpus only measures that one

`choudoufu live-check` reads a configuration directory with no cloud
credentials and says whether an estate could be **adopted**: taken over as it
stands, with no markers on anything. That is the hardest thing this fork ever
does, and every offline instrument here measures it, because every corpus
entry is somebody else's published configuration with a backend block and no
`live` block.

Adopting cold means deriving an identity for an object nobody has marked. A
migrated estate has the marker already, so a whole family of these refusals is
about a problem the product does not have.

**Do not read an offline corpus figure as a statement about the product
working.**

The corpus is pinned in `live/corpus-manifest.json` and materialized by
`tools/corpus-fetch`. Only a subset are rate-capable published deployments;
the rest are fixtures and module examples. Establish which population a number
is over before you use it, and count it rather than quoting a count.

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
wrong fix.** It buys one cohort and leaves the next to be hand-wired by
somebody who no longer knows why the first one was.

The right move is to find the property the type actually has, derive the rule
from it, and then report how many *other* types the rule reaches. If that
number equals the number you set out to fix, you have written a hand-list with
extra steps, and you should say so rather than land it.

The worked example: sibling references between resources were hand-wired per
type pair until they were re-derived as generic `<base>_ids`/`<base>_arns`
arguments, which deleted the hand-wiring and covered pairs nobody had
enumerated.

Where a ruling genuinely cannot be derived it goes into a named ledger with
its evidence and a ratchet, never into a generated file.
`contributing/LIVE-TABLES.md` says which ledger and why.

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
measured: `live/e2e/per-element` was run against floci with the canonicalising
sort in `internal/live/identity/perelement.go` deliberately removed. The plan
stayed empty, the second apply added nothing, and the foreign sweep came back
clean, because the provider splits that import ID on `/` and puts the tail in
a set, and a set has no order. Only the assertion on the rendered string
caught it.

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
remaining blockers. It is a legitimate instrument for the adoption campaign
and it is not an assignment for the product. Four reasons, all structural:

1. It can only measure the PUBLISHED form. No corpus entry declares a `live`
   block or a `record_store`, so nothing it prints describes a migrated
   estate.
2. Fewest-blockers-first is the hard tail by construction. Everything ordinary
   cleared long ago and left the list, so the top line is reliably an exotic
   estate with one exotic thing left.
3. terraform-aws-modules examples are excluded from the rate, correctly, which
   also keeps the code people actually write from reaching the top line.
4. Nothing there measures whether a migrated estate APPLIES.

`just onboarding-gap` narrows (1) and does not close it. It applies
`internal/live/onboard`'s computed edit - a live sidecar declaring
`record_store "local"`, backend or cloud block removed - and re-analyzes the
text. The result still describes an estate where **no resource carries a
marker**. Onboarded form is not migrated form.

**Never assign by refusal class.** That was tried for a full day: 1570 sites
cleared, the ladder unmoved. The median blocked estate carries about two
blocking classes, so clearing one class across forty estates leaves forty
estates blocked. The refusal-class issues in the tracker are background on a
blocker, never an assignment; the `wall-class` label is retired.

---

## Where the work is

The unit of progress is **an estate that works**, and the cheapest supply of
those is the generator.

`tools/estate-gen` already writes marker tag values into every taggable
resource of its 32 cohorts. It does not yet emit a `live` block, a sidecar or
a `record_store`, so not one cohort is a live configuration and the fork's own
path never runs over them as estates. That is issue #291 and it is the head of
the queue.

### The loop

1. Take the next cohort or estate from #291's list, in increasing complexity.
2. Make it a real estate: sidecar, `record_store`, markers on every taggable
   resource, derivables hanging off tagged parents.
3. **Offline gate**: `go run ./tools/refusal-probe -entry <path> -v` reads
   `blocked=false`.
4. **The real gate: make it run.** Stand it up against floci and assert the
   product's own claims - `live-plan` is exact, `apply` succeeds, a second
   plan is empty, and the markers land on the right objects.
   `live/e2e/tagging-sweep/run.sh` and `live/e2e/create-over/run.sh` are the
   working shape; each is wired to a `just` recipe and each fails for a stated
   reason rather than by exit code alone.
5. When a refusal fires on a resource that carries a marker, that is #289, not
   an analysis gap. Read it before writing a derivation.
6. Regenerate, commit, next cohort.

**Step 3 is not step 4, and the gap between them is where the worst defects
live.** `blocked=false` means four fully-checked layers plus 2 of projection's
27 refusals. Discovery is unchecked and 21 of its 25 refusals need a cloud.
#266 was exactly that: every offline check passed while `live-plan` proposed
creating a resource the estate already owned, once per run, forever.

**When floci cannot serve what an estate needs, that is a floci work item and
not a reason to skip the estate.** Fix it in the fork (`lex00/floci`), publish
to ghcr, re-pin `live/floci-image`, and re-verify from this side rather than
trusting the fix. #229 went through it end to end.

**If a capability is genuinely beyond the emulator, the estate goes to live
AWS.** Ask first, naming the estate and what it will create. That is real
infrastructure and real spend, and it is not standing authorization.

### The decision matrix

A refusal is usually a statement about where an identity lives. Which carrier
it should have been is what decides the work.

| Action | The identity is | The fix is | Done when |
|---|---|---|---|
| `ADOPTION-ONLY` | on the resource, as a marker | classify, do not refuse (#289) | the estate plans with the marker binding it |
| `DERIVE` | in the configuration, and the analysis does not reach it | extend the static evaluation | the value renders; assert on `ImportID`, never a boolean |
| `ADMIT` | knowable, but the type has no table row | a generator reaches it, or a ruling says it cannot | the row emits and `-convergence` exits 0 |
| `DEFER` | not knowable at plan time at all | read it, record it, or order around it | the estate plans without it, and the marker is right when it lands |
| `RULE` | refused on purpose | a maintainer decision, not code | out of scope; skip the estate |
| `PARITY` | absent for stock too | nothing | confirm stock refuses identically, then stop |

`ADOPTION-ONLY` is the row that is new and the row that is largest.
`tools/estate-plan`'s `blockerAction` does not carry it yet; #289 says which
refusals belong in it and which do not.

The distinction that decides the row: **the marker names an object, so it
answers an identity-value refusal. It cannot answer an expansion refusal,**
because the marker value *is* the instance address and an unknown key set
means an unknown address. `count` and `for_each` stay analysis or parity work.

### Reading the tracker

`gh issue list -R INTENTIUS/choudoufu`. A bare `gh` in this clone resolves to
`opentofu/opentofu`, silently. Pass `-R INTENTIUS/choudoufu` or run
`gh repo set-default INTENTIUS/choudoufu` once.

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

---

## Measuring

Two instruments, answering different questions. You usually want both, and
you should know what neither of them can see.

**Before either: the corpus measures the PUBLISHED form, and choudoufu is a
thing you migrate to.** Not one entry declares a `live` block or a
`record_store`. So a refusal that the onboarding edit clears is not a language
wall, it is the estate not having been onboarded, which is true of all of
them - and a refusal that a *marker* would clear is not one either, which is
what nothing currently measures.

**`tools/refusal-probe` counts refusals.**

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
`just corpus-fetch`, or symlink one in. `-diff` refuses any pair whose
difference is not the change under test.

**What the probe cannot tell you today**: which resource type a refusal fired
on, for anything in the identity layer. `check.Site.Type` is populated only by
the type-shaped lint rules, so the cause axis reads `reference:*` or empty for
every identity refusal. That is #290, and it is why #289 is priced in types
rather than in sites.

**`TestIdentityGolden` pins the rendered value.**

```
env -u PWD go test ./internal/live/check/ -run TestIdentityGolden
```

1454 rendered identities across 445 configuration directories in under a
second, with no generator, schemas or network. Address, class, `ImportID`,
identity attributes.

This is the only instrument here that measures what a marker will say rather
than whether something refused. Six defects shipped green because nothing did
that. **If your change moves a line, explain it. Do not run `-update` to make
it quiet** - `TestIdentityGoldenShapeIsPinned` will stop you anyway.

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
cloud. **Discovery is unchecked**, though 4 of its 25 are computable offline
(#261); the other 21 are verdicts about listed cloud objects.

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

**Regenerate, never hand-merge, a generated artifact.**

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
| `TestOperationalBriefIsTracked` | the brief cannot go back to being untracked local state |
| `TestMarkerlessVetoNeverContradictsClientNaming` | the marker stays delete permission and does not become identity |
| `TestRejectedLedgerIsDisjointFromAdmitted` | a type cannot be both admitted and vetoed |
| `TestCauseCatalogCoversEveryCause` | a new discovery cause appears in the probe's breakdown without an edit |

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
  incompatible.** Run the tests on the merge result, not on the branch.
- **Shell substitution in a `-m` commit message** will eat things like
  `${count.index}`. Use `-F` with a message file.

---

## What to do next

Ranked. Every item is filed, so the tracker carries the evidence and this list
carries only the reason and the order.

### 1. #291 - estate-gen must produce estates, not fixtures

32 cohorts, 685 resource blocks, 522 already carrying marker tags, and not one
cohort declaring a `live` block or a `record_store`. Adding the sidecar and
the store turns the generator's whole output into estates the fork's own path
can run, at a scale hand-written crossings will never reach.

This is the head of the queue because it supplies the test bed everything else
needs, and because "properly marked estates of varying complexity, all of
which work" is the goal state.

### 2. #289 - the resolver ignores the marker it stamps

221 taggable admitted types have their identity composed from configuration,
and every one of them carries a marker in a migrated estate. `identity`
consults the marker on one condition only, `entry.ServerAssigned`, so the
other 221 are refused for a cold identity they do not need. 198 of them are
enumerable, which is the gate; the remaining 23 must keep the refusal.

#290 is its measurement prerequisite and is small: identity refusals carry no
resource type, so the change cannot be priced in sites until they do.

### 3. #274 - cross the estates that have never run

Twelve of the 28 passing estates still have not touched a cloud. Every
crossing so far has found something no offline instrument could see, including
all three of the wrong-marker defects fixed on 2026-08-17.

### 4. #245 - admission, which migration does not fix

A type with no row is unadmitted whether or not the resource carries a marker,
because nothing sweeps for a type the configuration cannot declare. 669 AWS
types sit in neither the identity table nor the veto ledger.

### 5. #284, then #288, then #287

#284 is the `managedCovered` fallback, and without it the second pass is a net
loss: it demotes `aws_acm_certificate_validation` from PARENT_DERIVED to
NEEDS_DISCOVERY, and that type is untaggable, so the demotion becomes a hard
stamp refusal.

#288 is `aws_wafv2_web_acl`, which has no list operation and no Cloud Control
fallback, and may be a class rather than a type - nobody has counted admitted
types with neither route. #287 is what keeps #272's unique-name binding
unverifiable against a cloud.

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
  rules must stay blocked, and moves rate-capable instances not at all: 2584
  before, 2584 after.
- **Receipts never migrate onto the record store.** A receipt's value must
  stay readable with `aws ssm get-parameter` by someone with no binary.
  `live/RECEIPTS.md` has the four guards.

## Session-perishable state

Anything that rots faster than this file lives elsewhere on purpose.

- Work items and their current figures: the tracker.
- Refusal figures: regenerate with `just corpus`, or measure with
  `refusal-probe` against the tree you are on.
- Pinned floci image and provider pins: `live/floci-image` and the tests in
  `live/pins_drift_test.go` and `live/flociimage_test.go`.
- Adversarial audits have found real defects in work that was green,
  committed and believed finished, and CI caught none of them. An extra audit
  pass buys more than an extra CI run. Treat that as a standing option.
