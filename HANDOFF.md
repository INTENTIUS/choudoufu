# How to work this repository

This file is the standing playbook. It says what the work is for, what makes
a change acceptable, and how to take a task from the tracker to a merge.

It deliberately carries **no ladder table, no site counts and no rankings**.
Three earlier versions did. Each was stale within the hour, and one carried
two rows that were wrong at the moment they were written. Every number in
this project has been wrong at least once while being quoted confidently, so
the numbers live in artifacts that regenerate and in the tracker, and this
file tells you how to compute them.

Read `.claude/agents/live-markers.md` next for the operational detail, and
`.claude/skills/measuring-the-wall/SKILL.md` before producing any figure.

---

## What the product is

choudoufu is a fork of OpenTofu that replaces the state file with ordinary
cloud tags. Each resource carries its own ownership record, so AWS itself can
say what an estate contains and your existing IAM decides who may read or
change it. An estate is inherited by being granted access to it: handover is
granting a role, splitting an estate in two is rewriting tags.

Three concrete pieces do the three jobs a state file does. Which real
resource an address refers to is a **marker**, a tag on the resource. Values
AWS has nowhere to put go in a `record_store`. Effects that leave nothing
behind to read back get a **receipt** that tracks staleness.

Everything outside live markers is stock OpenTofu, from fork point
`03743ce6e8`.

**The thing to hold on to: the product's output is a string in a cloud tag.**
Not a verdict, not a count. A marker that is wrong is worse than a marker
that was refused, because a wrong one gets written to a real resource and
adopts or displaces something. Keep that straight and most of the rest of
this file follows from it.

## What the campaign is

**The primary goal is a fully migrated estate**: someone writes ordinary
Terraform, adds a `live` block, applies, and choudoufu manages it from then on
with no state file anywhere. That is the product. Everything else is secondary
to it, including everything the rest of this section describes.

Read that twice, because this document has spent most of its life pointing at
something else, and every session that followed it drifted the same way. See
"Why work drifts to the edges" below.

### The secondary campaign, and why it dominates the tooling

`choudoufu live-check` reads a configuration directory with no cloud
credentials and says whether an estate could be **adopted** — taken over
as it stands, with no markers on anything yet. Type coverage is rarely what
stops one. A `count.index` in a resource name, a `for_each` keyed by CIDRs, an
identity argument read from a data source, a module output used in a static
context — each of those stops one first.

That set of refusals is the **language wall**. Burning it down matters, and it
is what the corpus measures, because every corpus entry is somebody else's
published configuration with a backend block and no `live` block.

But adoption is the hardest thing choudoufu ever does, not the thing it is
for. Adopting cold means deriving an identity for an object nobody has marked.
A migrated estate has the marker already — choudoufu wrote it at create time —
so a whole family of these refusals is about a problem the primary path does
not have.

**Do not read a language-wall figure as a statement about the product
working.** It is a statement about how much of a stranger's configuration
could be taken over without editing it.

The corpus is a set of third-party configurations pinned in
`live/corpus-manifest.json` and materialized by `tools/corpus-fetch`. Only a
subset are rate-capable published deployments; the rest are fixtures.
Ranking against the wrong denominator has happened repeatedly, so establish
which population a number is over before you use it, and count it rather than
quoting a count from a document. The manifest holds globs, not entries, so
even the corpus size is a measurement.

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
wrong fix.** It buys one cohort and leaves the next one to be hand-wired
again by somebody who no longer knows why the first one was.

Hand-wiring is cheating even when it works. The right move is to find the
property the type actually has, derive the rule from it, and then report how
many *other* types the rule reaches. If the answer equals the number you set
out to fix, you have written a hand-list with extra steps, and you should say
so rather than land it.

A worked example of the shape: sibling references between resources were
hand-wired per type pair until they were re-derived as generic
`<base>_ids`/`<base>_arns` arguments, which deleted the hand-wiring and
covered pairs nobody had enumerated.

### Parity is the bar

Match stock OpenTofu and go no further. If upstream accepts a configuration
that we refuse, that is a defect, and the fix is to accept it rather than to
document the refusal.

The corollary catches people out: **refusing is not automatically the safe
answer.** A refusal that stock does not make is its own defect. Before
landing a new refusal, run the same configuration through stock and say what
it did.

This rule has already killed work in flight. Invented values in a corpus
generator were removed under it, on the ground that stock produces nothing
there either.

### A wrong marker outranks a missing one

Ordering for triage. A refusal is visible and annoying. A fabricated or
misdirected marker is silent, gets written to a real resource, and can adopt
another instance's object or leak one.

So a defect that produces a wrong rendered identity outranks a whole class of
refusals by count, every time.

**And a wrong identity is invisible to every verdict-level check.** This was
measured, not reasoned: `live/e2e/per-element` was run against floci with the
canonicalising sort in `internal/live/identity/perelement.go` deliberately
removed. The plan stayed empty, the second apply added nothing, and the
foreign sweep came back clean — because the provider splits that import ID on
`/` and puts the tail in a set, and a set has no order, so nothing on the wire
objected. Only the assertion on the rendered string caught it.

Convergence is therefore not evidence that an identity is right. An e2e step
that stops at "plan is empty" has proved the run terminates, not that it
addressed the correct object. Assert the rendered identity itself.

### No claim without a measurement

A closed issue needs a closing comment naming the number that changed and the
commit it was computed at. Twelve issues were once closed with a figure and
six of those figures were right; the rest argued from a comparison rather
than a run.

---

## Why work drifts to the edges

Read this before `just estate-plan`, because that command has pulled every
session so far toward work almost nobody would care about, and it does it
structurally rather than by anyone being careless.

`estate-plan` ranks **blocked, unmigrated, third-party estates by fewest
remaining blockers.** Four things follow, and all four point away from the
primary goal:

1. It can only measure the PUBLISHED form. No corpus entry declares a `live`
   block or a `record_store`, so nothing it prints says anything about a
   migrated estate.
2. Fewest-blockers-first is the hard tail by construction. Everything ordinary
   cleared long ago and left the list, so the top line is reliably an exotic
   estate with one exotic thing left. On 2026-08-17 the three estates at the
   head cost a whole new mechanism each.
3. terraform-aws-modules examples are excluded from the rate - correctly, since
   onboarding an example onboards nobody's infrastructure - which also keeps
   the code people actually write from ever reaching the top line. 74 of them
   are in the corpus and 71 are blocked.
4. Nothing measures whether a migrated estate works. The end-to-end crossings
   under `live/e2e/` are the only evidence that exists, and they are written
   one at a time by hand.

So: the top line of `estate-plan` is a legitimate assignment for the ADOPTION
campaign, and it is not the assignment for the product. Decide which you are
doing before you run it, and say which in your report.

**The gap worth closing is (4).** There is no instrument for "someone wrote
ordinary Terraform, added a `live` block, applied, and it kept working." Until
there is, every claim about the primary goal rests on a handful of shell
scripts.

## Where the work is: one estate at a time

**For the adoption campaign, run this and take the first line.**

```
just estate-plan -schemas
```

It sweeps the corpus and prints the blocked rate-capable deployments, fewest
blockers first, each with the action class every blocker implies. Re-plan from
a sweep you already have with `just estate-plan-from <file>`.

**Pass `-schemas`, and check that it was passed.** Without it `LocatedType`
fails closed and `markerless-type` reads as a blocker that a `record_store`
already answers - which is a four-estate difference on its own, and three
agents in one day drew wrong conclusions from a schema-less sweep.

**Never assign by refusal class.** That was tried for a full day: 1570 sites
cleared, the ladder unmoved at 26. It could not have gone otherwise. The median
blocked estate carries about two blocking classes, so clearing one class across
forty estates leaves forty estates blocked. **An estate onboards when its LAST
blocker clears**, which makes the estate the only unit that measures progress
and therefore the only unit worth assigning.

The shape of the work, as of the last sweep: **44 of 90 blocked deployments
are one blocker from clean**, and several of those are one blocker at one site.
Recompute rather than quoting that.

**That list is ranked by fewest remaining blockers, which is "closest to done"
and NOT "least complex".** The two get confused, including by people who have
been working the list for a day. Everything ordinary cleared long ago and left
it, so what remains is the hard tail by construction: an estate showing one
blocker can be large and exotic, and simply have one thing left. On 2026-08-17
the last three estates at the head of that list cost a whole new mechanism
each - a record-carried identity, a name-binding discovery leg, and a managed
read that is still unbuilt.

**And a blocked estate is not the only work.** 28 real third-party estates pass
`live-check` with zero refused sites, and as of 2026-08-17 exactly one of them
had ever been run against a cloud. "live-check says clean" and "applies, loses
its state file, and replans empty" are different claims, and only the second one
is the product. The one crossing that had been done needed four deltas no
offline instrument predicted, two of which became defects (#268, #269).
Compute the passing set the same way you compute the blocked one, and treat it
as a queue - see #274.

Note what is *not* counted. `Resolves at plan time via a data-source read` is
not a refusal — `dataread`'s own declaration says so, and `ClassifyOnboarding`
lands such an estate on the data-read-eligible rung. It is printed against the
estate because the read still has to succeed against a real cloud at step 6,
but it does not order the queue. Counting it read 118 blocked and 56 one-away,
and put a class no fix removes at the top of the board.

### The loop

1. `just estate-plan`.
2. Take the top estate. Selection is **by fewest blockers and nothing else** —
   not by which estate the emulator happens to support. If it is marked with
   unresolved modules its blockers are a floor, so run `just corpus-fetch`
   first or take the next one.
3. For each blocker, the matrix below says what kind of work it is.
4. Drive **every** blocker on that estate to zero. A partial estate is worth
   nothing.
5. **Offline gate**: `go run ./tools/refusal-probe -entry <path> -v` reads
   `blocked=false`.
6. **The real gate: make it run.** Stand the estate up against floci and
   assert the product's own claims — `live-plan` is exact, `apply` succeeds,
   a second plan is empty, and the markers land on the right objects.
   `live/e2e/tagging-sweep/run.sh` and `live/e2e/create-over/run.sh` are the
   working shape; each is wired to a `just` recipe and each fails for a stated
   reason rather than by exit code alone.
7. Regenerate, commit, go to 1.

**Step 5 is not step 6, and the gap between them is where the worst defects
live.** `blocked=false` means four fully-checked layers plus 2 of projection's
27 refusals. Discovery is unchecked and 21 of its 25 refusals need a cloud. An
estate can read clean and still be wrong: #266 was exactly that — every
offline check passed while `live-plan` proposed creating a resource the estate
already owned, once per run, forever.

**When floci cannot serve what the estate needs, that is a floci work item and
not a reason to skip the estate.** The lane exists and was exercised today:
fix it in the fork (`lex00/floci`), publish to ghcr, re-pin `live/floci-image`,
and re-verify from this side rather than trusting the fix. #229 went through it
end to end — `floci-capability-gen -mode=tagging` 0/7 to 7/7, with the tagging
sweep's bind asserted afterwards instead of skipped.

**If a capability is genuinely beyond the emulator, the estate goes to live
AWS.** Ask first, naming the estate and what it will create; that is real
infrastructure and real spend, and it is not standing authorization.

An estate whose blockers are all `RULE` is **not driveable**. Say so, skip it,
and do not spend a slot proving it again.

### The decision matrix

The product has three places to *carry* an identity: a **tag** on the
resource, a **record_store** entry, or a **receipt**. A refusal is usually a
statement that none of them applies yet, and *which* one it should have been
is what decides the work.

Usually, not always. Read the next section before concluding a refusal is one
of these, because the most common wrong answer is to reach for a carrier when
the identity needed no carrier at all.

| Action | The identity is | The fix is | Done when |
|---|---|---|---|
| `DERIVE` | in the configuration, and the analysis does not reach it | extend the static evaluation | the value renders; assert on `ImportID`, never a boolean |
| `ADMIT` | knowable, but the type has no table row | a generator reaches it, or a ruling says it cannot | the row emits and `-convergence` exits 0 |
| `DEFER` | not knowable at plan time at all | read it, record it, or order around it | the estate plans without it, and the marker is right when it lands |
| `RULE` | refused on purpose | a maintainer decision, not code | out of scope; skip the estate |
| `PARITY` | absent for stock too | nothing | confirm stock refuses identically, then stop |

`tools/estate-plan`'s `blockerAction` holds the per-refusal classification with
its reason. Both directions are enforced against `live/corpus-refusals.json`,
which a different generator produces, so a renamed refusal fails rather than
silently dropping out of the plan.

### Two questions, not one

**The marker answers "may I delete this". It does not answer "which object is
this".** Admission has drifted toward refusing a type because no marker can be
written on it, and that is a different claim from "nothing says which instance
this is".

A resource can be perfectly identified by its own declaration and still have
nowhere to hang a tag. Every association, attachment and membership in the
provider is that shape. The identity table already carries such rows:
`aws_iam_group_policy_attachment` is untaggable, has no ARN, and is admitted
with a composite of `{group}` `/` `{policy_arn}`, both client-supplied. Count
the admitted types on the `parent-derived` survey path, none of which is
taggable, before arguing that untaggability decides anything.

So a fourth answer belongs beside the three carriers: **the identity needs no
carrier, because it re-derives from the declaration on every run.** Nothing is
persisted and nothing is looked up. That is what the `client-named`,
`parent-derived` and `account-derived` survey paths already mean, and it is the
answer for an edge: an association's identity is its endpoints, which the
configuration holds in full.

Four consequences, in the order they are worth doing. Recompute every
population before acting on one; the figures that motivated this are in the
issue, not here.

1. **Client-naming is provable without the provider's identity schema.**
   `identity.Derivable` accepts only that schema as proof, and the provider
   publishes one for well under half its types. A **required, non-computed**
   argument cannot be a value the provider fills in, so the configuration
   schema settles it alone. The `aws_vpc` and `aws_s3_bucket` caution in
   `Derivable`'s doc comment is about Optional+Computed and stays excluded.
   This is the widest of the four by a large margin.
2. **When the provider ships no identity schema, the identity candidates are
   in `live/import-grammar.json`**, scraped from the provider's own import
   documentation. That artifact already exists and already carries the
   composition for types the schema says nothing about.
3. **A set-valued component serializes in canonical sorted order, all or
   nothing.** Canonicalize only when *every* element yields a key; otherwise
   leave the order alone rather than refusing the type. The objection that an
   unordered set cannot produce an ordered import ID does not hold where the
   provider parses that ID straight back into a set, which makes any order
   round-trip and sorting merely deterministic. Check that it does before
   relying on it.
4. **Split "confirmed absent" from "never looked".** These collapse into one
   verdict today, so a resource nobody could look at reads the same as one
   confirmed missing, and declared-plus-absent proposes a create. A third
   outcome with a total, exhaustive reason is what lets an unmarkable resource
   stay inside the model instead of leaving it.

`live/marker_identity_split_test.go` holds the part of this a reader cannot
skip: no type may be vetoed as markerless while the survey classifies it
client-named, because those are contradictory claims about the same fact and
the veto currently wins in silence.

The sibling project at `lex00/chant` reached the same wall and did not stop.
Its ownership module is delete-permission only, carrying no identifier and no
lookup; identity is recomputed from the declaration's own properties every
run. Its untaggable share of the provider is roughly ours. Worth reading
before redesigning any of this from scratch.

**`DEFER` is the big column and it is where the campaign actually is.** The two
largest sole-blockers, `Resolves at plan time via a data-source read` (28
estates) and `markerless-type`, are both deferral questions, not analysis
questions. `unadmitted-type` (20 estates) is the largest `ADMIT`.

### Reading the tracker

`gh issue list -R INTENTIUS/choudoufu`. A bare `gh` in this clone resolves to
`opentofu/opentofu`, silently. Pass `-R INTENTIUS/choudoufu` or run
`gh repo set-default INTENTIUS/choudoufu` once.

**Most wall issues are named after a refusal class**, which is the old frame.
Read them as background on a blocker, not as an assignment. An issue title's
figure was honest when written and the population has been recomputed twice —
never rank off one without recomputing.

Two ranking mistakes both already made here: counting estates that *carry* a
refusal and calling it sole-blocker count, and using sole-blocker count where
marginal cover was the question.

---

## Picking up a task

The sequence below works from a cold start with no memory of prior sessions.

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
Roughly half the briefs written in this repo have been materially wrong, and
several agents found out only after committing to an approach. A report with
no commit is a good outcome.

In particular, check the issue is not already fixed:

```
git merge-base --is-ancestor <sha> main
```

`git log --grep` proves only that a commit exists somewhere. An issue was
once closed citing a commit that sat on an unmerged branch whose test file
did not exist on main.

**4. Split anything over about thirty minutes.** Long tasks here are almost
never harder tasks; they are two jobs in one slot. Scouting is a separate job
from fixing. Hand back the half you did with enough detail that the next slot
starts from a correct brief.

**5. Work in a worktree.**

```
git worktree add ../wt/<name> -b wall/<name> main
```

---

## Measuring

Read `.claude/skills/measuring-the-wall/SKILL.md` first. It is the catalogue
of every way a number here has been wrong.

Two instruments, answering different questions. You usually want both.

**Before either: the corpus measures the PUBLISHED form of every estate, and
choudoufu is a thing you migrate to.** Not one of the 145 entries declares a
`live` block or a `record_store`; each still carries the backend it was
published with, and a module may declare one or the other but never both. So a
refusal that the onboarding edit clears is not a language wall - it is the
estate not having been onboarded, which is true of all of them.

Two consequences, and both have already cost a slot here.

Choudoufu does not care whether a type is taggable. Taggability is about the
MARKER, which answers "may I delete this". What decides whether an estate can
run is whether the identity is DERIVABLE, and for an object choudoufu created
it is, because choudoufu minted the ID. Framing a wall as "untaggable" measures
the marker and reports it as an identity problem.

And when a finding is cleared by an operator edit rather than by a change in
this repository, measure the ONBOARDED form: copy the estate out of `.corpus`,
swap the published backend for a live block, and run the probe on the copy.
`5e6cf9c86f` did exactly this - `blocked=0, sites=0, 13 instances` on an estate
that reads blocked in published form - and it is the only measurement that
answers the question. An agent that measured the published form and reported
"nothing cleared" was measuring an unmigrated config against a migrated-platform
capability.

The demotion that follows is only safe when the promise is enforced downstream.
`5e6cf9c86f` could demote because `internal/live/projection` raises
"Record-backed instance with no record store" as an ERROR at plan time, so an
operator who writes the live block and forgets the store is stopped by name.
Without that guard the demotion trades a refusal for a silent failure.

**`tools/refusal-probe` counts refusals.**

```
go run ./tools/refusal-probe -out before.json          # ~20s, all 250 entries
go run ./tools/refusal-probe -diff before.json,after.json
go run ./tools/refusal-probe -entry .corpus/vpc -v
go run ./tools/refusal-probe -schemas -out before.json # ~2.5min warm
```

It writes where you point it, so several people can measure concurrently in
one tree. `just corpus` cannot.

A fresh worktree has no `.corpus` - it is gitignored - and a sweep there used
to report 31 in-repo fixtures with exit 0 and nothing else said. The probe now
refuses unless every manifest source expands to something on disk and every
fetched source sits at the commit the manifest pins. Get the corpus with `just
corpus-fetch`, or symlink one in from a checkout that already has it.
`-allow-partial-corpus` measures anyway and stamps the sweep, and `-diff` will
not compare a stamped sweep against a full one.

`-diff` refuses any pair whose difference is not the change under test: two
trees, two manifests, one side schema-backed, two provider versions, or two
different sets of entries. It reports - without refusing - the inputs that are
allowed to move and still change the meaning: module install state, var files,
and per-provider acquisition.

The default mode runs without provider schemas. It is blind to the whole
stamp layer, to every rule that returns false when schemas are nil, and to
non-AWS estates. **Its bound is asymmetric**: it over-reports sites and
under-reports the verdict, because blocked configurations rise once schemas
are present. A fix validated only against the default mode can look like it
unblocked something it did not.

**`TestIdentityGolden` pins the rendered value.**

```
env -u PWD go test ./internal/live/check/ -run TestIdentityGolden
```

1407 rendered identities across 426 configuration directories in under a
second, with no generator, schemas or network. Address, class, `ImportID`,
identity attributes.

This is the only instrument here that measures what a marker will say rather
than whether something refused. Six defects shipped green because nothing did
that. **If your change moves a line, explain it. Do not run `-update` to make
it quiet** — and the shape pin in `live/identity_golden_pin_test.go` will
stop you anyway.

### Two numbers, not one

Report **sites and instances** together. Sites falling means the analyzer
stopped complaining. Instances rising means resources that could not be
identified now can.

But instances rising is not by itself good news, and this is the sharpest
lesson available. Reverting a known conversion defect fabricated three
identities and lost two correct ones, and the instance count went **up**.
Every aggregate this repository records called that regression an
improvement. Only a value assertion separates the two.

**And "entries WORSE must be 0" is not the gate it reads as.** It has now
twice flagged a correct fix as a regression, both times the same way: a
refusal that fired once at the block level starts firing per argument or per
instance, because the block now expands where it previously refused wholesale.
Five honest refusals naming an argument each are more information than one
naming a block, and expanding is what stock does.

Maintainer ruling, 2026-08-17: **sites are not the measure.** What a change
must not do is lose an instance or change a rendered identity. Report entries
worse by site count, explain each, and do not revert a fix on that number
alone - the campaign counts estates onboarded. The module-argument fix landed
with 24 entries improved and 11 worse by sites, no instance lost anywhere, no
golden line changed, and blocked 194 -> 193.

### Caveats that travel with any ladder figure

Layers come in three lists, not two. Lint, identity, dataread and stamp are
fully checked. **Projection is partly checked**: 2 of its 27 refusals need
no cloud, and the rest do. **Discovery is unchecked**, though 4 of its 25 are
computable offline and wiring them is #261; the other 21 are verdicts about
listed cloud objects and genuinely cannot be reached without one.

So `clean` does not mean "this onboards". The supportable sentence names the
share: *"N of the rate-capable deployments pass the offline checks this
instrument runs, which is four full passes plus 2 of projection's 27."*

The corpus does not install registry modules by default, so an entry with
module calls measures a fraction of its refusal surface and every per-entry
number for it is a floor.

---

## Landing a change

**Assert on rendered identities.** `res.ImportID` and `res.IdentityValues`,
never a predicate boolean. Predicates have been green while markers were
wrong six times; a duplicate-marker bug shipped with a passing analyzer.

**Assert the instance count separately from the key set.** One bug's entire
signature was two instances where OpenTofu makes three.

**Mutation-check every boundary fixture.** Remove the stated obstacle and
only that, and confirm the case then resolves. Otherwise you have proved the
case refuses, not that it refuses for the reason you claimed. The same
technique applied to a guard is how you find out the guard is decorative: one
compatibility leg turned out to be untested at the layer where it mattered,
and only a deliberate mutation revealed it.

**A test nothing runs is not a test.** One harness sat red on main for weeks,
wired to no `just` recipe, no CI step and no README mention. Wire it, then
break it deliberately once and watch it go red.

**Regenerate, never hand-merge, a generated artifact.** Two have conflicted
on merge and both had to be regenerated.

**Run a generator twice and diff, but know what that proves.** It catches
nondeterminism, and one generator was silently nondeterministic through
sporadic subprocess handshake failures. It does **not** prove the artifact is
the one its inputs imply.

`row-gen -emit` reads its own previous output — `markerlessRoster` looks up
`identity.DefaultTable`, which is `table_generated.go` — and has more than
one fixed point. A mutation retracted 217 rows; reverting the mutation and
re-running did not restore them, and the wrong state survived a second run
while exiting 0 and converging. Only `git checkout --` brought it back. So
byte-identical across two runs means "this is *a* fixed point", not "this is
correct". That is #263, and it matters because the two-run diff has been
cited as the acceptance bar on several table changes.

**`marksafe` guards `internal/live`.** A new call to a cty accessor needs a
proof its receiver cannot be marked, `ContainsMarked` before anything that
iterates, and a new package under `internal/live` must be classified. Note
the asymmetry it exists for: a marked element hoists its mark to the
container only for a set.

---

## What is enforced, and what is not

Prose is re-read only by whoever happens to read it, and this project keeps
discovering that its prose was stale. So rules get converted into tests
whenever they can be. When you find yourself writing a rule into a document,
ask first whether it can be a test.

The pattern to copy, in `live/`:

| Guard | What it holds |
|---|---|
| `TestCIRunsEveryForkOwnedTestPackage`, `TestCIExclusionsAreReal` | every fork-owned test package is in CI's glob, or excluded with a reason |
| `TestFlociMeasurementsMatchThePinOrSayWhyNot` | a measurement is current, or its exception still applies |
| `TestIdentityGoldenShapeIsPinned` | the golden's shape, so `-update` alone cannot silence a regression |
| `TestBurndownBoundsHold` | every migrated ratchet, each computing its own number and pinning the denominator it is a fraction of |
| `TestEveryToolHasAGitignoreEntry`, `TestNoCompiledBinaryIsTracked` | no multi-megabyte binary lands in a commit again |
| `TestOperationalBriefIsTracked` | the brief cannot go back to being untracked local state |
| `TestMarkerlessVetoNeverContradictsClientNaming` | the marker stays delete permission and does not become identity: no type is vetoed as markerless while the survey calls it client-named, and a resolved exception has to be deleted |

The bounds those ratchets used to carry as scattered constants now live in
`internal/live/harness`, one entry each, computing their number at run time
and naming the denominator they are a fraction of. `live/HARNESS.md` renders
them. Migrating them found one that had stopped bounding anything.

What they have in common is worth copying deliberately. Each is a registry
checked against the tree rather than a hand-list. Each exception is written
in Go and carries its reason. Each fails in **both** directions: when the
thing it guards moves, and when the guard itself stops describing anything
real. A pin on something that no longer exists passes forever.

Some rules stay prose because they are about judgment rather than about the
tree: parity, splitting long work, and the worktree hygiene below. Those
depend on being read.

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
  corpus into worktrees rather than refetching 250 repositories, and a
  directory pattern does not match a symlink.
- **A new tool binary needs a `.gitignore` entry.**
- **Cohort ownership is split**: `GENERATED.md` and `.tf` belong to
  `estate-gen`; `README.md` is hand-owned.
- **Two branches can merge cleanly in text and be semantically
  incompatible.** One wrote a test against a signature another had changed.
  Run the tests on the merge result, not on the branch.
- **Shell substitution in a `-m` commit message** will eat things like
  `${count.index}`. Use `-F` with a message file.

---

## Mid-flight, as of this handoff

Nothing is blocked on a decision. These are the loose ends a fresh session
would otherwise rediscover.

- **The corpus now has its modules, and that moved the ladder.** `corpus-fetch`
  and `corpus` have both been run and committed, so the module hole is closed.
  Two published deployments that read unblocked did so only because their
  modules were absent, and refusal sites roughly doubled. Nothing regressed:
  the committed ladder had been measured with a hole. Four entries still report
  install errors and stay a floor, named in the fetch log.
- **Two refusals fire that no corpus run had ever reached**, because both live
  only inside installed modules. `Null identity argument` is `firstPresent`
  choosing an alternation member by syntactic presence while a sibling holds
  the value. `moved-block` is `declaresSubject` collapsing an instance address
  to its resource, so every keyed rename terraform-aws-modules ships reads as
  un-vacated. Both are classified `DERIVE` in `blockerAction` with the
  reasoning; neither is any estate's sole blocker, so neither reorders the
  queue. Neither is fixed.
- **Ratification has a safe home now.** `-emit` reads
  `tools/row-gen/ratified.json` rather than the table it writes, so adding a
  row is an edit to a hand-owned input followed by a regeneration. Verified by
  mutation: delete rows from `table_generated.go`, re-emit, and they come back
  - where before they stayed deleted while the run exited 0 and "converged".
  `aws_accessanalyzer_analyzer` went through that path and cleared an estate.
  Two things a ratifier still has to do that nothing tells them up front: pin
  the type's taggability in the right `internal/live/stamp` cohort, and expect
  the lint fixtures that used the type *because* it was unadmitted to need a
  new one.
- **`row-gen`'s report still names paste targets that do not exist**
  (`table_cohort_<cohort>.go`, `admission_cohort_<cohort>.go`). They went when
  `-emit` took ownership. The target is `ratified.json`; the report has not
  been told.
- **Most of the queue's head is not language-wall work, and that is now
  measured rather than suspected.** Of the 28 blocked estates carrying any
  unset-variable site, 13 go to zero blockers when values are supplied and 15
  keep real ones - all 13 govuk-aws. Of the estates one blocker from clean, a
  large share are non-AWS types, which #5 rules out of scope. `estate-plan`
  annotates both and sorts them behind driveable work; it does not reclassify
  or drop them, because #183 rules they stay blocked honestly.
- **RULED AND LANDED 2026-08-17: the `list + content match` survey path
  named a mechanism this fork does not have, and is now `enumerable,
  unbindable`.** The rest of this entry is the case that produced the
  ruling; the token it names no longer appears in any artifact.
  `tools/survey-gen` assigned it from
  "untaggable + Cloud-Control-enumerable", which is an ENUMERATION fact
  labelled as an ADMISSION fact. `internal/live/discovery` binds by reading
  the marker tags and by nothing else: the Cloud Control leg lists an object,
  refines it with GetResource, and discards it when neither carried tags
  (`cloudcontrol.go`'s `ProblemNoTags`, severity error). `internal/live/foreign`
  says the same in words - a content match is "surfaced for explicit adoption
  and never bound automatically", because "inferring it from a content match
  would be exactly the guess the marker spec exists to forbid".
  `internal/live/doc.go` states both the promise and the denial within twenty
  lines, and `lint.go` offers the path to a user as one of four options.
  So the markerless veto is RIGHT and its stated reason is WRONG: the accurate
  reason is that marker discovery is the only binding mechanism there is.
  Narrowing the veto on this signal was investigated and rejected - it would
  release 62 vetoed types onto an unimplemented path and turn a lint refusal
  that names the type into a per-plan discovery error. The question was
  whether path 4 was aspirational or simply wrong. The maintainer ruled it
  wrong: the token was renamed rather than the capability built, moving 142
  rows in `live/survey-full.json` and 9 in `live/survey.json`, with no
  verdict changed. `aws_eip` came off the exception list in the same pass -
  its hand row had claimed the same non-existent wiring while the thing that
  binds an eip is the `tofu-slot` tag.
- **The ACM cluster needs the key set AND the each.value path. My earlier
  "one fix, not two" note here was wrong, and this is the correction.**
  Wiring the provider plan through end to end removes the `for_each` refusal
  on rust-lang-org exactly as intended, and then reads 3 sites/5 instances
  before against 4 sites/4 instances after: two refusals appear one layer down
  (a data source inside the for_each body, and an identity argument carried
  through `each.value` that is unknown until apply) and one resolved instance
  is LOST. The experiment that produced the earlier note substituted a static
  key set and set the record's name from a direct resource attribute
  reference, which defers; the real configuration carries it through
  `each.value`, which refuses. The library half is landed and tested
  (projection.PlanInstances, pluginschema.AcquireSession,
  check.Context.ManagedResults); the probe wiring is not, because a change
  that clears a refusal and loses a marker is the wrong trade.
- **The ACM gap and the lost instance are ONE defect, diagnosed: an unknown
  REFUSES where a deferral CLASSIFIES.** Planning rust-lang-org loses
  `module.certificate.aws_acm_certificate_validation.cert`, which resolved
  PARENT_DERIVED with an empty ImportID and afterwards does not resolve at
  all. The cause is not the plan: before it, a reference to the unexpanded
  record block defers; after it, the block expands and its unknown-until-apply
  values refuse. More information made the answer worse. The two refusals that
  appear one layer down are the same thing seen from the other side.
  So one fix covers all of it - treat an identity value that is unknown
  BECAUSE it comes from a resource this run planned the way a direct reference
  to that resource is already treated.
  The obstacle was thought to be provenance: an unset required variable also
  evaluates to an unknown, and deferring THAT would reverse #183's honesty.
- **RESOLVED 2026-08-17: cty marks cannot carry that provenance, and half the
  problem needed no carrier at all.** Marks are out on evidence, not taste.
  `IsMarked()` is not a policy test in this fork - it guards panicking
  accessors, roughly 70 of them across `identity`, `lint`, `stamp`,
  `dataread`, `check`, `foreign` and `projection`, and `internal/live/marksafe`
  is a static prover that requires exactly that guard shape
  (`marksafe.go:285`, `ProofGuarded`). The fork's guards also spell it
  `IsMarked()` where stock spells it `HasMark(marks.Sensitive)`, so they
  cannot tell two mark kinds apart. A second kind would mean rewriting every
  guard and teaching marksafe a new proof form, with a missed site producing
  either a wrong refusal or a panic.
  The key-set half then turned out to be plain parity. Stock asks a `for_each`
  two different questions - `IsKnown()` for a map or object, `IsWhollyKnown`
  only for a set, because a set's elements ARE its keys while a map's values
  never enter an address (`internal/lang/evalchecks/eval_for_each.go:144`).
  This package asked `IsWhollyKnown` of all three and so refused a map with
  literal keys and apply-time values that stock plans. `forEachKeysKnown`
  fixes that and needs no provenance: stock's own rule already separates "I
  cannot say which instances exist" from "I cannot say what is inside them".
  #183's cohort stays refused, but NOT for free, and an earlier version of this
  entry got that wrong in a way that would have talked the next agent into a
  silent reclassification. It said an unset required root variable "does not
  evaluate to an unknown at all" because `configs.StaticEvaluator` refuses it
  outright. That is true through the identity package's OWN loader, which is
  what `TestForEachUnsetVariableStillRefuses` uses, and false through
  `internal/live/check`, which is what `refusal-probe`, `just corpus` and
  `live-check` use: `check/load.go:266` substitutes
  `cty.UnknownVal(variable.ConstraintType)`.
  Measured both ways on the same fixture,
  `internal/live/identity/testdata/foreach-unset-var-map`: identity's own loader
  gives `No value for required variable`; `check.Dir` gives `Non-static for_each
  expression` with 0 instances. The cohort agrees -
  `.corpus/govuk-aws/terraform/projects/app-elasticsearch6` carries `Non-static
  identity argument`, 7 sites, `unset_var_only: true`.
  So the #183 guard is weaker than it reads, and the provenance discriminator is
  genuinely required IN THE CHECK PATH and genuinely unnecessary in `live-plan`,
  which has no substitute loader. A floci run cannot prove #183 stays refused,
  because floci exercises the path where the problem does not exist.
  **The second half is still open and no estate has cleared.** An identity
  argument left unknown by the provider's plan must classify as
  `ClassNeedsDiscovery`, not refuse at `resolve.go:2352`. The carrier for that
  is the expansion rather than the value: leave a not-wholly-known element out
  of `eachValues` and build the expansion `keyOnly`, so `expansion.scope`
  leaves `each.value` unbound and the structural route that already produces
  PARENT_DERIVED/NEEDS_DISCOVERY takes over. Unbuilt because it was not
  verified that the structural route terminates correctly for a comprehension
  loop variable rather than a resource instance address.
- **RULED 2026-08-17: a type whose identity is fully determined by parents
  choudoufu already tags and admits needs no marker of its own.** The
  markerless veto reads two facts - untaggable, provider-minted identity - and
  never asks the third: does the configuration already state enough to name
  the object. `tools/survey-gen/classify.go:303` reasons from carrier signals,
  and checks for a parent reference only inside the already-`derivable`
  branch, over the provider's IDENTITY-SCHEMA attributes. A type with no
  identity schema never reaches that check whatever its configuration says.
  `internal/live/discovery/parent_read.go:48 parentReadSweep` is the mechanism
  and it is already wired. Roughly a third of the 148-type veto has a required
  argument pointing at a taggable, admitted parent - recomputed outside the
  generator, so treat the figure as unverified until the generator says it.
  Ruled in the same pass: **widen the derivability rule to read
  mutually-exclusive argument groups.** An argument the schema marks Optional
  only because it is one member of an `ExactlyOneOf` group, where the
  configuration states exactly one member, counts as stated. That is what
  `aws_eip_association` needs - its `allocation_id` / `instance_id` /
  `network_interface_id` are all Optional for that reason alone.
  Open under this ruling: the three CloudFront policy types have a required
  client-supplied `name` and no identity schema at all, so nothing routes them
  anywhere. Whether the name identifies the object is an evidence question -
  if AWS does not enforce uniqueness, matching on name could adopt an object
  the operator made by hand.
- **REFUTED 2026-08-17, same day, by measurement: the parent-derived ruling
  has a qualifying population of ZERO.** Do not re-open it without new
  evidence. Three independent checks:
  (1) The intersection of `MarkerlessTypes` with `tools/row-gen/ratified.json`
  is 0 of 148, and `emittedRows` ships only ratified rows - so sparing a type
  from the veto does not put it in `DefaultTable`. It falls through to
  `SynthesizeTypeIdentity`, which refuses it anyway, and the refusal merely
  changes from `markerless-type` to `unadmitted-type`. Same estates, same
  sites.
  (2) The wire protocol carries no exclusion groups at all.
  `docs/plugin-protocol/tfplugin6.9.proto`'s `Attribute` has eleven fields and
  none of them is `ExactlyOneOf` / `ConflictsWith` / `AtLeastOneOf` /
  `RequiredWith`. Those are enforced provider-side at `ValidateResourceConfig`
  and never reach this fork. So the ExactlyOneOf half of the ruling cannot be
  implemented as stated, and would not have reached `aws_eip_association`
  anyway, which has no identity schema at all.
  (3) CloudFormation's own model refutes the parent story per type.
  `AWS::GlobalAccelerator::Listener` has primary identifier `[ListenerArn]`
  with `ListenerArn` read-only; `AcceleratorArn` is a create-only INPUT, not
  part of the identifier, and an accelerator carries many listeners.
  `AWS::EC2::EIPAssociation` is primary `[Id]`, read-only. Contrast
  `AWS::S3::BucketPolicy`, primary `[Bucket]` with no read-only properties -
  that is the shape where a parent genuinely is the identity, and those types
  are already admitted. Of the 148 markerless types, 4 have an identifier free
  of read-only properties and 0 of those link to an eligible parent.
  The "51 types with a required argument pointing at a taggable admitted
  parent" figure is real but measures the wrong side: a required ARGUMENT is
  about "may I delete this", and the ruling was stated over "which object is
  this".
- **RULED 2026-08-17: the record store MAY hold an identity for an object
  that carries no marker, because an ID is not a permission.** The reasoning,
  because it is the part worth keeping. `live/MARKERS.md` claimed the marker
  is an ordinary tag "so IAM can condition on it directly through
  `aws:ResourceTag`, with no second permission model to keep in sync". For an
  UNTAGGABLE type that condition can never match, so the published estate
  grant already conveys nothing on such an object - the governance claim is
  not available for these types today, and storing an ID takes nothing away.
  That claim has since been scoped where it is made: see MARKERS.md's "What
  this grant cannot reach", which carries the derived figure - 221 of the 884
  admitted AWS types are untaggable, across 77 CloudFormation services, and
  the grant governs the other 663.
  This holds for governance WITHIN an estate as well as across estates, and
  the within-estate half is the one to check first, because it is the finer
  claim: granting a principal rights over one declared address means
  conditioning on `aws:ResourceTag/tofu-address`, and an untaggable object
  carries that tag no more than it carries `tofu-estate`. Both grants are
  unavailable for it, for the same reason, and MARKERS.md publishes both
  without saying so.
  The split is therefore: "may I delete this" stays with IAM, scoped by ARN or
  resource policy, and was never choudoufu's to give for an untaggable type;
  "which object is this" is what the record answers, and only that. A record
  entry must never be read as delete authority.
  The failure mode is better than a state file's and that is why the trade is
  acceptable. Lose the record and the declared instance reads unbound, finds
  nothing, and a CREATE is proposed, while `internal/live/foreign` surfaces
  the existing object as unclaimed - and by construction an unclaimed resource
  "can never enter the prior state and the plan engine has nothing to propose
  destroying". A lost record risks an announced duplicate. It does not risk a
  silent deletion.
  Note what this does NOT license: the guided-discovery hint written to the
  same store stays non-authoritative (`guided.go:21-24`, a bad hint "never
  changes what the sweep does ... it only changes cost",
  `TestGuided_equivalence`). A record that carries identity is a different
  class from a hint and must not be conflated with one.
- **Framing for that design, from the maintainer: choudoufu has to have an
  ANSWER here, and the answer may be a toggle rather than a rule.** Worth
  writing down because it changes what the fix is aiming at.
  The unknown-vs-unknown distinction is not really about provenance. It maps
  onto the parity rule exactly. An unset required variable evaluates to an
  unknown AND stock refuses the same configuration, so refusing is
  parity-correct. A resource attribute that is unknown until apply evaluates
  to an unknown AND stock plans it without complaint, so refusing is a parity
  DEFECT. Same value, opposite correct answers, and what separates them is
  whether stock would proceed - not anything about the value itself.
  That suggests the refusal is in the wrong PLACE rather than being the wrong
  rule. At the point of use the two unknowns are indistinguishable; at the
  source they are not, and an unset required variable is knowable exactly
  where the variable is read. Refusing there and deferring everywhere else is
  the shape that lets an ordinary estate onboard, and it is not what the
  resolver does today.
  A marker does not have to exist at plan time. It has to exist after apply,
  which is when the value does - that is what ClassNeedsDiscovery and
  ClassParentDerived already mean, and the ACM records are exactly that shape.
  And a strict mode that refuses anything it cannot name up front is a
  legitimate thing to want, because it is what makes live-check a gate worth
  running. It cannot be the ONLY mode, however, since the same setting decides
  whether an ordinary estate is onboardable at all. Do not assume which way
  round the default goes; that is the maintainer's, and the two modes need
  separate measurements before it is chosen.
- **MEASURED, and it argues against the general form.** Before choosing a
  default, the cost of each mode was measured rather than guessed. Suppressing
  the point-of-use `Non-static identity argument` refusal across the whole
  corpus frees exactly FIVE rate-capable estates - app-licensify-documentdb,
  infra-content-data-admin, infra-public-wafs, infra-specialist-publisher,
  infra-stack-dns-zones - and every one of them is a govuk-aws
  unset-variable estate, which is precisely the cohort #183 rules must stay
  blocked. No ACM estate is freed, because in the unplanned state they are
  blocked on the for_each, not on this. And rate-capable instances do not move
  at all: 2584 before, 2584 after.
  So a blanket "defer every unknown" would reverse #183, free nothing the
  campaign wants, and resolve not one additional identity. It would hide a
  refusal rather than answer it - which is the failure mode a rising site
  count normally reveals and this one would not, because the sites simply
  disappear.
  That is a strong argument for the source-versus-point-of-use split above
  rather than for a mode switch on its own. The point-of-use refusal is doing
  exactly one job today, and that job is refusing unset-variable estates; the
  apply-time unknowns it would also need to defer are not reached until a plan
  supplies the values. Any toggle has to be built on top of that distinction,
  not instead of it.
- **Superseded, kept for the reasoning:** A scout concluded that deriving the `for_each` key set would only
  reveal the identity refusal underneath, because the record's `name` and
  `type` come from `resource_record_name`/`_type` and are known only after
  apply. That is wrong. Substituting a statically-knowable key set into
  `.corpus/simpleinfra/terraform/shared/modules/acm-certificate/main.tf` while
  leaving an identity argument genuinely computed
  (`name = aws_acm_certificate.cert.arn`) takes the estate to ZERO blockers -
  four informational data-read findings and nothing else. An apply-time
  identity argument defers; it does not refuse. So the whole cluster turns on
  the key set alone, and whoever picks it up should not budget for the second
  half. The corpus was restored after the experiment.
- **Three shapes remain, each with its next step named.** The ACM
  DNS-validation `for_each` blocks four estates at one shared module line and
  is a confirmed parity defect - stock plans it; the machinery to resolve the
  adoption case is `projection.ReadInstances`, which is landed and has no
  non-test caller. `markerless-type` blocks five, and those types really are
  server-minted and untaggable, so they need the edge-identity idea in "Two
  questions, not one", not a veto correction.
  `simpleinfra/team-members-access` needs a variadic-tail component before
  its row can be written at all.
- **The typed-variable half of Shape B** was in progress and stopped. Its
  fixture `shapeb-absent-typed` pins today's behaviour, so the debt cannot go
  quiet; #260's closing comment has the design.
- **#263's cure is half done.** `tools/row-gen/ratified.json` holds the 878
  rows with a byte-identical round-trip proof, and `-emit` still reads
  `DefaultTable`. The flip is three reads, not one — `emittedRows`,
  `buildConvergence` and `markerlessRoster` — and the convergence one is
  load-bearing.
- **Worktrees under `../wt/` are live.** Never prune one by whether its branch
  merged: a branch with no commits is trivially an ancestor of main, and a
  prune loop on that predicate destroyed five agents' work in one command.

## Session-perishable state

Anything that rots faster than this file lives elsewhere on purpose.

- Work items and their current figures: the tracker.
- Ladder and refusal figures: regenerate with `just corpus`, or measure with
  `refusal-probe` against the tree you are on.
- Pinned floci image, provider pins and their drift guards: `live/floci-image`
  and the tests in `live/pins_drift_test.go` and `live/flociimage_test.go`,
  which fail if a measurement outlives its stated reason.
- Adversarial audits have found real defects in work that was green,
  committed and believed finished, and CI caught none of them. An extra audit
  pass buys more than an extra CI run. Treat that as a standing option, not a
  one-off.
