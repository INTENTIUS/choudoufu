---
name: measuring-choudoufu
description: How to produce a number about choudoufu that survives contact with the next reader. Use when measuring refusals, ranking work, claiming a fix moved something, or reading a figure someone else produced.
---

# Measuring choudoufu

Every number in this project has been wrong at least once, usually while
being quoted confidently. This is the accumulated list of how, and what to
do instead.

The failure is never arithmetic. It is always that the number answered a
different question than the one it was quoted for.

## The first question: which estate are you measuring

A migrated estate is tagged. `internal/live/stamp` writes `tofu-estate` and
`tofu-address` onto every taggable managed resource, and what carries no tag
is the association family, whose identity derives from tagged parents. Tagged
plus derived-from-tagged, and no third bucket.

**Every offline instrument here measures configurations where that invariant
does not hold.** No corpus entry declares a `live` block or a `record_store`,
and no resource in one carries a marker. So a corpus figure is a statement
about adopting a stranger's infrastructure cold, which is the hardest thing
this fork does and is not what the product is for.

Say which population your number is over, before anything else:

- **published form** - the corpus as fetched. Adoption.
- **onboarded form** - `internal/live/onboard`'s computed edit applied
  (`refusal-probe -onboarded`). Still unmarked, still adoption.
- **migrated form** - markers present. Only `live/e2e/` has any, and they are
  hand-written one estate at a time.

A figure that does not name one of those three has not been read yet.

## Before you produce a number

**Check the artifact is at HEAD.**

    git log -1 --format=%h -- live/corpus-refusals.json
    git log -1 --format=%h

If those differ, the committed artifact predates the tree. It has been
behind by up to four behaviour-changing commits in a single afternoon, and
an agent quoted from it and had to be corrected. Say which commit your
number came from, every time.

**Check the instrument saw the whole corpus.** `.corpus` is gitignored, so a
fresh worktree has none, and a sweep there measured the 31 in-repo fixtures
and reported them under a summary line indistinguishable from a full run.
`refusal-probe` refuses that now, and names the sources it could not find;
`just corpus-fetch`, or symlink a `.corpus` in from a checkout that has one.
A number quoted from a partial corpus is about a different population than
every other number in this project.

**Check which instrument answers your question.** `tools/refusal-probe`
default mode is schema-less and runs in ~20s. `-schemas` takes ~2.5min warm
and sees things the default cannot. They are not the same measurement and
`-diff` refuses to compare them.

The default mode is blind to:

- the **whole stamp layer** — every resource reads as "schema not available"
- **any rule returning false when `schemas == nil`** — its zero is not
  evidence of anything, it is the absence of evidence
- **non-AWS estates** — a `google_*` config measured with no google schema
  reports `unadmitted-type` for every resource, which is a property of the
  run and not the configuration

**And the bound is asymmetric.** Schema-less *over*-reports sites and
*under*-reports the verdict: blocked configurations **rise** by thirteen
with schemas. So "upper bound" is true of sites and false of blocked. A fix
validated only against the default mode can look like it unblocked
something it did not.

**`-schemas` is not deterministic across machines.** Provider acquisition
fails for some requirements — 8 of 75 in one run, 35 of 250 entries with a
partial set. Two agents ran "the same" schema-backed sweep and got different
site totals for that reason. **Report what you acquired**, and treat any
class whose provider failed as a floor.

## Ways a number has actually been wrong here

**Quoted from a branch whose base predates a merge.** A site total was
measured on a branch, propagated into three committed files, and was off by
exactly the size of a class a later merge had emptied. The tool's own copy
was correct because it *labelled its commit*; the prose copies said "one
commit" and outlived it.

**Counted touches and called them sole blockers.** An issue title said
"sole blocker on 12 of 51"; 12 was how many estates *carried* the refusal.
It was sole blocker on 1. Those numbers differ by an order of magnitude and
rank work completely differently.

**Ranked by the wrong denominator.** Corpus-wide config counts include 105
fixtures that are not rate-capable published deployments. `rejected.json`'s
size was used as the admission debt for months; the real denominator is
1699 and the ledger is a veto set, not a coverage ledger.

**Ranked by sole-blocker count when marginal cover was the question.** One
class freed 1 estate alone and +10 at greedy step 10. A sole-blocker table
buries it. A different class inverted from step 1 to step 4 in four hours
because a merge moved where its refusal fires.

**Measured a class the corpus cannot see.** An entry with registry module
calls whose modules are not installed is measuring roughly a sixth of its
refusal surface. One went 59 sites to 394 with modules installed. `just
corpus-fetch` installs them now (#254), for 58 of the 250 entries — but a
`.corpus` fetched before that landed, or symlinked in from a checkout that
was, still has the hole, and the two produce site totals that cannot be
subtracted. `refusal-probe`'s second summary line says how many entries it
found with modules installed, and its `-diff` names any entry whose install
state moved between two sweeps. Every per-entry number for an entry with the
hole is a floor.

**Reported a total that concealed two opposite movements.** Sites can hold
steady while one entry improves and another regresses. Always diff
per-entry; `refusal-probe -diff` prints an "entries WORSE" section for
exactly this.

**Reported sites when instances was the question.** Sites falling means the
analyzer stopped complaining. **Instances rising means resources that could
not be identified now can.** A relabelling moves thousands of sites and zero
instances — real work, but not the same work. Report both.

**Reported instances rising as if it were good news.** This corrects the
paragraph above, which was written this morning and is not safe on its own.
`TestIdentityGolden` was validated by reverting #251's conversion; the
revert made three fabricated identities appear and two correct ones vanish,
and the instance count went **1320 → 1321, up**. Every aggregate this
repository records says the defect was an improvement.

An instance is a marker this tool will write into a cloud tag. Fabricating
one is worse than refusing to make one, so a count that cannot tell a real
instance from an invented one ranks a regression above the fix. **Only a
value assertion separates them** — which is why `Report.Identities` and the
golden exist, and why an instance delta is now a supporting number rather
than a verdict.

**Reported a gain that was our own fixture.** A change dropped blocked
configurations by ten; every one was a cohort estate whose only refusal was
a resource `estate-gen` had just removed. No third-party configuration
changed. The agent said so rather than banking it.

## The claims that must be checked, not assumed

**"This is on main."** Use `git merge-base --is-ancestor <sha> main`, never
`git log --grep`. An issue was closed citing a commit that matched a grep
and sat on an unmerged branch; its test file did not exist on main at all.

**`git log --oneline` orders by date, not ancestry.** It will show a fix
above an artifact regeneration that does not contain it. Use `--graph`
before concluding an artifact is stale.

**"The generator converged, so the artifact is right."** `row-gen -emit`
reads its own previous output and has at least two fixed points. A wrong
retraction of 217 rows survived reverting the mutation and re-running twice,
exiting 0 and converging each time; only `git checkout --` restored it. A
two-run diff proves the generator is deterministic, not that its output is
implied by its inputs. See #263 before quoting fixed-point-ness as evidence.

**"Nothing moved."** Zero is also what a dead code path looks like.
Instrument and confirm your code is reached before reporting no change — one
agent found 63 hops reaching its function, 13 of them typed, which is what
made its zero meaningful.

**"The closure was measured."** Twelve wall issues closed with a figure;
six were right. The rest argued from a *comparison* rather than a run, and
three compared the wrong thing: a function upstream never calls on that
path, a greenfield plan rather than a post-apply one, the fallback's
boundary rather than the one that fires.

## What makes a number defensible

- It names the commit it was computed at.
- It says which instrument, and what that instrument cannot see.
- It reports **per-entry**, not only aggregate.
- It reports **instances alongside sites**.
- Its verifier was **independent** — re-implementing `ClassifyOnboarding`
  in Python and asserting 0 mismatches against the Go classifier is the
  local idiom, and it is what makes a ranking credible.
- It distinguishes what you **computed** from what you **read**. Say both.

## Assertions, not just measurements

The same discipline applies to tests, and this is where silent wrongness
lives:

**Assert on rendered identities — `res.ImportID`, `res.IdentityValues` —
never on a predicate boolean.** Predicates have been green while markers
were wrong six times. A duplicate-marker bug had a passing analyzer.

There is now one instrument that does this at scale.
`internal/live/check.TestIdentityGolden` pins the rendered identity —
address, class, `ImportID`, identity attributes — of every managed resource
instance the in-repo fixtures resolve, in under a second with no generator,
schemas or network. **If your change moves a line in
`testdata/identity-golden.txt`, explain it. Do not run `-update` to make it
quiet.** A moved line is the only signal here that distinguishes a fix from
a plausible-looking regression.

Its current size is in the `# shape:` block at the top of the golden and in
`live/identity_golden_pin_test.go`. Do not repeat it here — this paragraph
carried 1320 instances over 375 directories for a day after it was 1335
over 382, which is this document's own subject.

Its bound is worth knowing and is stable in shape rather than in count:
roughly two rows in five render an empty value, because their identity
needs a live account or a server-assigned ID, so the file covers the
CONCRETE rows and the symbolic PARENT_DERIVED formulas and nothing else.
Eight of the eleven classified defect shapes fail it automatically; three
present as an *added* line and are only surfaced to somebody reading the
diff.

Two legs hold it. The counts are pinned in
`live/identity_golden_pin_test.go`, and so is a sha256 over the rows. The
digest exists because the counts alone were defeated: an audit rewrote 35
rendered `ImportID`s, ran `-update`, and every count came back
byte-identical.

**Assert the instance count separately from the key set.** One bug's whole
signature was two instances where OpenTofu makes three.

**Mutation-check every boundary fixture.** Remove the stated obstacle and
only that, and confirm the case then resolves. Otherwise you have proved
the case refuses, not that it refuses *for the reason claimed*.

**A test nothing runs is not a test.** A harness sat red on main for weeks,
wired to no `just` recipe, no CI step and no README mention.

## Strategy: what the measurements have actually shown

Re-derive these rather than trusting them. They are leads, not facts, and
several have already inverted once.

- **Refusal-class burndown does not move estates.** 1570 sites cleared in a
  day, the ladder unmoved. The median blocked estate carries about two
  blocking classes, so clearing one class across forty estates leaves forty
  estates blocked. The estate is the only unit that measures progress.
- **A large slice is stock-refuses-too, not defect.** Whole classes are 100%
  unset-required-variable artifacts that stock OpenTofu refuses identically.
  Those gate nothing an operator with tfvars would see.
- **A large slice is adoption-only.** A refusal that fires because the
  configuration cannot yield an identity, on a resource that would carry a
  marker naming it, is answering a question a migrated estate has already
  answered. Which refusals those are is in the tracker; the population is
  measured in types because nothing can yet measure it in sites.
- **Untaggability is not an identity fact.** It bounds what an
  `aws:ResourceTag` condition governs. A wall framed as untaggable measures
  the marker and reports it as identity, and that substitution has cost this
  repository more slots than any other single error.
- **Adversarial audit finds what CI does not** - 33 defects to zero, and
  read-only auditors finish in half an implementer's time because they run no
  generators.
