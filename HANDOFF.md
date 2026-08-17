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

`choudoufu live-check` reads a configuration directory with no cloud
credentials and says whether the estate can be onboarded. Type coverage is
rarely what stops a configuration. A `count.index` in a resource name, a
`for_each` keyed by CIDRs, an identity argument read from a data source, a
module output used in a static context — each of those stops one first.

That set of refusals is the **language wall**, and burning it down is the
campaign. The measure is how many real published deployments go from refused
to onboardable, not how many refusal sites disappear.

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

### No claim without a measurement

A closed issue needs a closing comment naming the number that changed and the
commit it was computed at. Twelve issues were once closed with a figure and
six of those figures were right; the rest argued from a comparison rather
than a run.

---

## Where the work is

**The tracker, not this file.** `gh issue list -R INTENTIUS/choudoufu`.

A bare `gh` in this clone resolves to `opentofu/opentofu`, silently and
without an error. Either pass `-R INTENTIUS/choudoufu` every time or run
`gh repo set-default INTENTIUS/choudoufu` once.

Issue titles carry figures that were honest when written and have since
moved. Populations have been recomputed twice. **Do not read a title as a
current number**, and do not rank off one without recomputing.

Two ranking mistakes that have both been made here:

- Ranking by how many estates *carry* a refusal, and calling it the number it
  is a sole blocker on. Those differ by an order of magnitude.
- Ranking by sole-blocker count when marginal cover was the question. One
  class freed one estate alone and nine more once other classes were cleared.
  Use the greedy marginal ordering.

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

**`tools/refusal-probe` counts refusals.**

```
go run ./tools/refusal-probe -out before.json          # ~20s, all 250 entries
go run ./tools/refusal-probe -diff before.json,after.json
go run ./tools/refusal-probe -entry .corpus/vpc -v
go run ./tools/refusal-probe -schemas -out before.json # ~2.5min warm
```

It writes where you point it, so several people can measure concurrently in
one tree. `just corpus` cannot.

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

1323 rendered identities across 376 configuration directories in under a
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

### Caveats that travel with any ladder figure

`checked_layers` is lint, identity, dataread and stamp. `discovery` and
`projection` are unchecked, so `clean` means "nothing in four of six layers
refused it". The supportable sentence is *"N of the rate-capable
deployments pass the four offline checks this instrument runs"*.

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

**Run a generator twice and diff before trusting its output.** One was
silently nondeterministic through sporadic subprocess handshake failures.

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
