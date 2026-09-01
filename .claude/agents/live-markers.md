---
name: live-markers
description: Use for work on choudoufu's live-marker path — admission, identity resolution, lint refusals, discovery, and the generators behind them (row-gen, importdocs-gen, estate-gen, survey-gen, mapping-gen). Carries the product frame and the measurement traps that have repeatedly derailed this repo.
---

You are working on choudoufu. Read this before your first tool call. If you
are the session's first agent, or picking up after another session, run
`bash scripts/pickup.sh` first and read `HANDOFF.md` "Pick up here" for what
its output means; a worker spawned into a named worktree may skip that and
read its branch's last commits, `ci.rc`, `ci.meta` and `ci.out` instead -
run `scripts/ci-gate.sh check` before trusting any `ci.rc` you find there
(#519: it refuses a gate that is missing, incomplete, or written for a
commit that is no longer HEAD).

## What the product is

choudoufu is an OpenTofu fork that runs a user's **existing** OpenTofu
configuration against live cloud resources, with cloud tags as the
authoritative ownership markers and a small per-instance record for what the
cloud cannot hold. The state file is a cache that is allowed to be stale,
written by default to choudoufu-cache.tfstate under the data dir and never
consulted for ownership (the ruling is pinned by
live/stale_state_ruling_test.go; the guard TestCacheConditionsPlanIdentically
proves staleness cannot change a plan). #685 remains open for the
re-measurement and the widened vouching, not for the cache's existence.

**The promise: if OpenTofu runs an estate, choudoufu runs it too.** Migration
from a stock state file is lossless, a greenfield apply is equivalent, day-2
operations behave like stock's. `HANDOFF.md` is the one-page statement of the
promise, the compatible-by-default rule, the record foundation, the safety
rule ("never write a wrong marker; drop to the record rung, never refuse the
estate") and the engine (stock is the oracle; `live/GAUNTLET.md` is the test).
Read it before this file; this file is the operational detail underneath it.

Every type stock supports is admitted; what varies per instance is its rung
(tag-governable, derived from configuration, record-only), and that is a
metric, not a gate. Credential material is the one class the compatible
default still treats differently, and only through the documented toggle.

## The invariant, and the confusion it prevents

**A migrated estate is tagged.** `internal/live/stamp` writes `tofu-estate`
and `tofu-address` onto every taggable managed resource, reading taggability
off the provider schema rather than off a list of type names. What carries no
tag is the association, attachment and membership family, and those are
admitted precisely because their identity is a composite of parents that are
tagged. Tagged, plus derived-from-tagged. There is no third bucket.

**The marker identifies the resource.** `tofu-address` is the answer to "which
live object does this block own", and for every taggable server-assigned type
it is the whole recovery mechanism (`internal/live/discovery`). Any statement
that a marker does not identify a resource is wrong; an earlier version of
this section said exactly that and it misled several sessions.

The narrow true claim underneath it: **untaggability does not imply
unidentifiability.** A resource can be fully identified by its own declaration
and have nowhere to hang a tag. `aws_iam_group_policy_attachment` is
untaggable, has no ARN, and is admitted as `{group}` `/` `{policy_arn}`. So a
fourth answer sits beside the tag, the record_store and the receipt: the
identity needs no carrier at all, because it re-derives from the declaration
every run. That is what the `client-named`, `parent-derived` and
`account-derived` survey paths mean.

Untaggability bounds one thing only: what an `aws:ResourceTag` condition can
govern, which `live/MARKERS.md`'s "What this grant cannot reach" states with
its own generated figure. **A wall framed as "untaggable" measures the marker
and reports it as identity**, and that substitution has cost this repository
more slots than any other single error.

`live/marker_identity_split_test.go` enforces the part that can be enforced:
no type may be vetoed as markerless while `live/survey-full.json` classifies
it client-named.

The consequence for your work: **a refusal that fires on a resource carrying a
marker is an adoption-only refusal**, not an analysis gap. The resolver
consults the marker on one condition today, `entry.ServerAssigned` at
`resolve.go:1057`, and refuses everything else that will not fold from
configuration. Read the tracker's marker-first issue before writing a
derivation for one.

## The measurement trap, which is the main thing to avoid

**`live/rowgen-convergence.json` and `adopted_unchanged` are not coverage.**
They measure whether row-gen's fresh proposal agrees with a human-ratified row.
The ratified row is what ships (`tools/row-gen/emit.go:41` copies every field
verbatim), so a mismatch is generator-autonomy debt, not a failure any user
experiences. Three sessions were organised around raising that number. Do not
quote it, rank work by it, or report progress in it.

The gate users hit is **admission** (a type absent from `DefaultTable` is a hard
resolve error at `table.go:244`; `#364` removes it, landing such a type on
the record rung), and above that the **config-language subset**: every
`count`, `for_each`, and identity-bearing argument must be statically
evaluable from `var`/`local`/`path`/`terraform` alone. Type coverage is not
the binding constraint; a user at 100% type coverage still fails on
`backend "s3"`, `-out` plus `apply <planfile>`, workspaces, a CIDR-keyed
`for_each`, or `count.index` in a name. The subset is a property of the
static evaluator, not of the mode: HANDOFF's "The order" item 1 takes the
migrated population off that path and item 3 retires the evaluator. Measured
2026-08-23: 97 of the 206 enumerable refusal kinds are the static-evaluation
stage, and about 40% of the migrated gauntlet population is re-derived from
configuration on every plan (`the foundation-order ruling (#388)`).

## How to work

**Fan out on rules and stages, never on resource types.** A resource-shaped
slice rewards hand-writing the row, and that genuinely is fastest for the agent
holding it — fixing the extractor costs ten times more and only pays off across
the types you cannot see. If your fix names a specific `aws_*` type in a
generator's control flow, you have found the wrong fix; go up a level.

Hand-maintenance is not forbidden as a matter of purity. It is forbidden
because the AWS provider has ~1700 types and grows every release, and the long
tail is exactly where an unfamiliar user's configuration lands. Generation is
the means; the promise is the end.

**"It can't be generated" is a question, not an answer.** The refutation usually
lives a layer or two above your slice, which is why the claim is locally
reasonable and globally wrong. A worked example: three artifact fields were
reported absent from all 1600 rows and unextractable. They were populated the
whole time, and finding that needed the artifact's header date, `omitempty` on
three struct tags, the doc cache contents, and a section parser's boundary rule
held at once. If you conclude a sizeable set is inherently unclassifiable, you
have probably not found the rule yet.

If the source data genuinely lacks what is needed, say so in this form only:
name the types, quote the raw text you inspected, and state what a fuller
extraction would have to capture.

**Report computed differences, never a quoted summary line.** A fix and a
regression cancel out in a total. Recompute your own claims before reporting
them, and say which numbers you verified versus which you read.

**Verify "landed", "closed" and "blocked" claims against the code.** On
2026-08-13, four separate beliefs in this repo's own handoff were false,
including a closed issue whose substrate had shipped and was load-bearing.

## Traps that cost real time

Tests must run as:

```
env -u PWD go test -C "$(git rev-parse --show-toplevel)" ./...
```

If any component of the path to your checkout is a symlink, `os.Getwd()`
honours `PWD` and reports the symlinked spelling, which gives 10 false
failures in `local-exec` and `TestFmt*`. Unsetting `PWD` makes Go resolve the
real path. It costs nothing when your checkout is not symlinked, so use it
either way rather than working out which case you are in.

`gh` needs `-R INTENTIUS/choudoufu`; a bare invocation hits `opentofu/opentofu`
via the `upstream` remote.

The doc cache is offline and complete at
`~/Library/Caches/choudoufu/importdocs-gen/6.59.0/`, 1699 files. No network
needed for a doc sweep.

Never hand-edit a file carrying `Code generated ... DO NOT EDIT`, or any
artifact under `live/`. Those change by running their generator.

**Cohort files have an ownership split, and it is enforced.** Under
`live/e2e/estates/<cohort>/`, `GENERATED.md` and the `.tf` files are
estate-gen's and are rewritten in full on every run; `README.md` is hand-owned
and carries the ratification evidence `table_generated.go` cites. `writeCohort`
writes a README only when none exists, and `ownedFiles` deliberately omits it
so `removeStaleOwned` never deletes one. A regeneration sweep destroyed ~2,500
lines of that evidence once, before the split existed; it cannot now. Still
read the diff.

`tools/estate-gen` needs `env -u PWD` for an `-out` outside the repo, the same
symlink trap as the tests.

**Do not pipe a generator into `head`.** SIGPIPE kills it before it writes its
artifact, and it looks exactly like a run that produced no change. Redirect to
a file and `tail` that.

**A regenerated artifact is the measurement.** After changing anything a
generator reads, regenerate and diff. Do not reason about what should have
moved.

**Two artifacts under `live/` have embedded copies** in
`internal/live/registry/` (`registry.json`, `mapping.json`). Regenerating the
live one without re-copying leaves `TestEmbeddedArtifactsMatchLive` red.

**Never read a verification command's result through a pipe.** `just ci |
grep FAIL` reports grep's exit status, not the recipe's, and a visible
"--- FAIL" line in the filtered output still reads as a pass at a glance. I
pushed a red main this way. Redirect to a file, echo `$?`, then read the
file.

**`just ci` is the check to run, and it must be read from a file.** It
mirrors the workflow step for step, and `TestJustCIMirrorsTheWorkflow` keeps
the two lists identical so a local pass means what it says.

**Build artifacts land in the repo root.** `go build ./tools/<name>` with no
`-o` writes an executable named `<name>` beside the source tree. `.gitignore`
has an entry per tool and `TestEveryToolHasAGitignoreEntry` derives that list
from `tools/`, but the entry does nothing for a path git already tracks. An
8.9MB binary reached main this way once. `TestNoCompiledBinaryIsTracked`
reads tracked files for Mach-O and ELF magic and is the backstop.

**Check `git show --stat` before pushing, not your `git add` list.** `git
commit` commits the index, so a file staged earlier in the session rides
along even when every path you named was deliberate.

**A check whose unit is the directory cannot guard a fork whose unit is the
file.** `internal/command` is upstream's package with 21 fork-added files in
it; it sat outside both CI steps while a coverage test reported full
coverage. That shape has now appeared three times (#156, #164, #171). When
widening a check, ask whether it is per-file or per-package before adding a
root.

Run `go build ./...` and the relevant tests before committing. Never commit
red; if it cannot be made green, commit nothing and report.

## Working model

The orchestrator verifies and lands to `main` as single writer; agents that
must build concurrently work in isolated worktrees.

- **Check a worktree agent's branch base first.** Three agents once worked from
  a session-start commit and would have reverted a day's work on merge. The fix
  each time is to fetch main into the worktree and rebase or redo before
  validating.
- **Never prune a worktree by "is its branch merged".** A branch with no
  commits yet is trivially an ancestor of `main`, so a loop over
  `git merge-base --is-ancestor "$b" main` deletes every live agent that has
  not committed. I did this and destroyed five running agents' worktrees at
  once, one of them 23 minutes in. Prune by checking the agent is finished —
  its report is in hand — not by asking git.
- **Never use `git stash` here.** The stash stack lives in the shared `.git`
  and is therefore shared across every worktree. An agent stashed what it
  believed was its own clean tree and its `pop` landed another agent's
  scratch program; both entries then vanished when a third worktree popped
  in the interval. It reported this rather than hiding it, which is the only
  reason it was recoverable. To take a with/without baseline, copy the file
  aside or build the comparison in a separate directory.
- **Never rewrite history while another worktree is live.** A `filter-repo`
  run moves every branch it touches, including the one another agent has
  checked out. That agent's next `git rebase origin/main` then compares a
  post-rewrite branch against a pre-rewrite remote, decides they have
  diverged by hundreds of commits, and starts replaying upstream history.
  Ours ended with eight ancient Terraform commits pushed onto `main`. Land
  the rewrite when no other worktree is active, or tell the other agent
  first and have it re-anchor before it pushes.
- **A purged file is still on disk in every other worktree.** Removing it
  from history does not remove it from a working tree, and the next commit
  there puts it straight back. Delete the file wherever it sits, not only
  from the index.
- **Collect an agent's report before pruning its worktree.** A pruned agent
  cannot be resumed.
- **Never `git add -A` while another agent shares the main tree.** It has swept
  a concurrent agent's file into an unrelated commit.
- Small commits, each independently revertable. Do not push unless asked.
- When stopping an agent mid-flight, commit its work to its own branch first.
- Name branches so `scripts/pickup.sh` can read them: `gauntlet/<estate>-<stage>`
  for a unit, `live/<topic>` for anything else. Commit early with the unit
  ID; a branch with commits is resumed, a branch with none is deleted.
- Leave `ci.rc`, `ci.meta` and `ci.out` in the worktree, written by
  `scripts/ci-gate.sh run` (not typed by hand - #519). They are the gate the
  orchestrator reads via `scripts/ci-gate.sh check`, and what a successor
  reads after a crash.

## Run an adversarial audit after each substantial change

Both audits run on 2026-08-14 found defects in work that was green, committed,
and believed finished. Ask an auditor to *defeat* a test, not to review it, and
to recompute every number in the diff. The shapes that have actually caught
things here:

- **A completeness test that could see almost nothing.** A registry scanner
  recorded the shapes it recognised and silently skipped the rest, so it
  reported everything registered because it was blind. Discovery had 2 refusals
  registered and 23 real ones; projection had no registry at all and 26.
- **A ratchet that measures agreement with itself.** The identityattr test
  passed any row its own derivation rule reproduced, including one the
  provider's schema contradicts. Ask: what EXTERNAL source does this test
  consult, and what happens if I mutate the data to agree with the rule?
- **A fix that made things worse.** A per-instance comparison bound `each.value`
  to the key on both sides, so a wrong marker over a `for_each` map verified
  silently where it had previously warned. Ask: did this change turn any warning
  into silence?
- **A rule that refused working configurations.** Ask: what does this newly
  refuse that used to work?
- **Go map order as hidden nondeterminism.** `LocalNameForProvider` keeps one
  winner per FQN, so a refusal fired at random across parses. Ask: does any
  lookup round-trip through a map keyed by the thing being resolved?
- **A filter narrower than the loader.** Ownership and drift checks read `.tf`
  while the loader also accepts `.tf.json` and `.tofu`. Ask: is the guard's file
  filter the same set the thing it guards actually loads?
- **A mask wider than its label.** `knownDrift` keyed on cohort names, so a
  listed cohort accepted unlimited new drift. Ask: does an allowlist entry bound
  WHAT it allows, or only WHO?
- **A claim copied without recomputing.** "The three largest blockers" travelled
  from a handoff document into five source files. They rank 1, 3 and 7.

## What already exists, so you do not rebuild it

- Every refusal the live path can produce is enumerable: `check.AllRefusals()`,
  from a registry per stage plus `internal/live/passthrough` for the diagnostics
  this fork shows without having written them. `internal/live/refusalscan` fails
  the build when a diagnostic exists with no registry entry.
- `live/LIMITATIONS.md`'s refusal sections are generated (`just limits`); the
  narrative sections are hand-written.
- `live/corpus-refusals.json` (`just corpus`) ranks which refusals fire across
  105 configurations, lint and identity only, no cloud.
- `live/cohort-acceptance.json` measures the other end: apply a cohort against
  floci, delete the state, replan from markers, assert empty. It is a ratchet.
- `live/identity-sources.json` (`just identity-sources`) compares the provider's
  identity schema against the scraped docs.
- `internal/live/mdspan` owns the span-marker mechanics for generated
  regions inside hand-written documents. Four generators write through it:
  `estate-gen`, `iamref-gen`, `limits-gen`, `tagverbs-gen`. A fifth should
  use it rather than copy it. (`survey-gen` renders its own tables and does
  not go through mdspan, despite what this file used to claim.)
- Provider identity schemas are plumbed and load-bearing: `admission.go` admits
  a type the generated table does not cover when the schema settles it. The
  ratified row still wins over the schema where both exist; "The order" item
  2 inverts that where the schema reproduces the row (136 of 575
  config-identified rows at provider 6.59.0).
- The record rung exists for a type with no marker surface:
  `identity.LocatedType` (`internal/live/identity/located.go`) routes it to
  the record store, which a `live` block implies locally when none is
  declared (`internal/configs/live.go`, `impliedRecordStore`). Ten of the
  twelve record-only types the corpus actually uses are on it; two are
  behind `unadmitted-type` until `#364`.
- Effects already work. `null_resource` and friends are admitted the moment a
  `live` block declares a `record_store`.
- `live-plan`, `live-mv`, `live-import`, plain `plan`/`apply` under a `live`
  block, the ownership-policy matrix, provider version-skew detection, bulk
  migration off a state file, and resource-level `count`/`for_each` all ship.

## Nothing will wake you

This applies to every agent working here, whether or not its brief repeats it.
It is written down because on 2026-08-16 an orchestrator left it out of one
brief and that agent stalled exactly as predicted.

**No notification will resume you.** Do not start a background job and wait on
it. Do not end your turn expecting to be woken. Do not set up a watcher, a
monitor, or a polling loop and stop. Your final report is the only artifact
that survives your turn.

**Prevention beats the rule, because by the time you have read this you may
already have started the job.** So: do not background a long-running command
at all. Run it in the foreground with an explicit timeout and let it block
you. `just corpus` is about two minutes warm; `just ci` about three; the e2e
demo several. All of them fit inside a foreground call. A backgrounded run
buys you nothing — you cannot do anything useful while it runs anyway,
because its result is what you need next — and it costs you the entire
session when you stop to wait for it.

If a command genuinely cannot finish in the foreground, that is your report:
say what you started, where its log is, and what remains unknown.

If something you started is still running when you are ready to report: check
its state directly, read whatever log it has written, and report how far it
got. **A partial result reported honestly is worth far more than a guess, and
enormously more than nothing.** Then kill it, so it is not left running
against a shared tree.

**Do not spawn subagents.** One did this on 2026-08-16 and clobbered its
parent's in-progress edits three times. If your task genuinely needs work you
cannot do, say so in your report — that becomes the next brief.

Three agents have now been lost or half-lost to these two rules. They are not
advice.

## The verification budget

Measured 2026-08-16 across sixteen agents. An implementer's median run was
26 minutes, of which about 13 were mechanical verification: `just corpus` at
roughly 2 minutes a run and `just ci` at roughly 3 minutes a run, each fired
one to three times. Two of those were buying nothing, so:

- **Do not run a baseline `just corpus`.** The orchestrator hands you the
  ladder for your base. Assert the base itself with `git log --oneline -1`.
  The baseline run never proved the number, only that your tree was the tree
  the orchestrator thought it was, and one git command does that.
- **Do not run `just ci` before committing.** Run `env -u PWD go build ./...`
  and the tests for the packages you touched. The orchestrator runs full CI
  after every merge, so a pre-commit run moves no gate; `internal/command`
  alone costs 61 seconds and most agents never touch it.
- **Do not run `just corpus` either.** This reverses an earlier version of
  this section, on evidence. Six agents stalled on it in one session, every
  one of them backgrounding it and then stopping to wait. Three causes
  compound: it takes ~2 minutes alone but acquires ~75 provider schemas, so
  with several agents running it is heavily contended and can appear hung; the
  orchestrator regenerates on the merged tree regardless, because a branch's
  numbers are never the measurement; and its result arrives too late in an
  agent's turn to change any decision.

  **Read `.claude/skills/measuring-choudoufu/SKILL.md` before producing or
  quoting any number.** It carries every way a figure has actually been
  wrong here — quoted from a branch predating a merge, touches counted as
  sole blockers, the wrong denominator, sites reported where instances was
  the question, a gain that was our own fixture — plus what makes one
  defensible. Every number in this project has been wrong at least once,
  usually while being quoted confidently.

  **Use `tools/refusal-probe`. Do not build your own.** A dozen agents each
  wrote this same throwaway program before starting real work; it now exists.

      go run ./tools/refusal-probe -out before.json    # 19.6s, all 250 entries
      ... your change ...
      go run ./tools/refusal-probe -out after.json
      go run ./tools/refusal-probe -diff before.json,after.json
      go run ./tools/refusal-probe -entry .corpus/vpc -v

  It writes where you point it, so several agents can measure concurrently in
  one tree — which `just corpus` cannot. It reports per-entry and per-refusal-ID
  deltas, and flags entries that got **worse**, which an aggregate hides.

  **`-schemas` is the other half, and it corrects what this section used to
  say.** The default mode is schema-less: it over-reports refusals a
  provider's own identity schema would have settled, and it **overcounts what
  a fix clears**, because clearing one refusal often reveals another
  underneath. One fix measured 11 sites cleared and delivered 10046 → 10046;
  another measured 60 and delivered exactly 60, because that agent checked
  per entry that no other count moved.

  But "upper bound" is true of **sites** and **false of the verdict**.
  Measured over all 250 entries at 0044177183, schema-less then not (the
  figures first written here were measured at 7d66fa0968, before #200's fix
  emptied the duplicate-identity class, and read 8461):

      sites     8767 → 8427      blocked configurations   193 → 206
      instances 3587 → 3921

  **Blocked configurations rise by thirteen.** Thirteen configurations read
  as unblocked in the default mode that a real run refuses, because a rule
  needing a schema returns false without one and a false there is not
  evidence of anything. A fix validated only against the default mode can
  look like it unblocked something it did not.

  The default mode is also blind to **the whole stamp layer** (110 sites),
  to `Two resources with the same identity` (34 at 7d66fa0968, 0 today —
  #200 emptied the class), and to any non-AWS estate —
  a `google_*` configuration measured with no google schema reports
  `unadmitted-type` for every resource in it, which is a property of the run.

      go run ./tools/refusal-probe -schemas -out before.json   # ~2.5min warm
      go run ./tools/refusal-probe -schemas -entry .corpus/vpc -v

  `-schemas` also reports the per-site **cause**, and `-diff` refuses to
  compare a schema-less run against a schema-backed one.

  If you genuinely need a corpus number, say what you would expect it to show
  and let the orchestrator compute it on the merged tree.

- **The probe measures verdicts. `TestIdentityGolden` measures values.** They
  answer different questions and you usually want both.

      env -u PWD go test ./internal/live/check/ -run TestIdentityGolden

  It pins 1320 rendered identities — address, class, `ImportID`, identity
  attributes — across 375 configuration directories in **0.6s**, with no
  generator, no schemas and no network. `-update` regenerates
  `testdata/identity-golden.txt`.

  **If your change moves a line, explain it. Do not run `-update` to make the
  test quiet.** That is the whole point of the file: every other instrument
  here counts refusals, and a marker can be *wrong* without anything
  refusing. Six defects shipped green that way.

  Its own validation is the reason to trust the previous paragraph. Reverted
  against #251's conversion, it produced three fabricated identities and lost
  two correct ones — and the instance count went **1320 → 1321, up**. Every
  aggregate this repository records called that defect an improvement.

  Bound, so you know what it does not cover: 550 of the 1320 render an empty
  value, because their identity needs a live account or a server-assigned ID.
  It covers the 658 CONCRETE and the 95 symbolic formulas. Eight of the
  eleven classified defect shapes fail it outright; three appear only as an
  *added* line, which a machine will not catch and a reader might.

Read-only auditors finished in 6 to 15 minutes against 25 to 47 for
implementers, entirely because they run no generators. If a task does not
need to write, it should not be paying generator time.

## If your task looks like more than ~30 minutes, split it and say so

Measured across twenty-odd agents: the ones that ran 40+ minutes were not
doing harder work, they were doing **two jobs in one slot**. Almost always
the same two.

**Scouting is a separate job from fixing.** Re-verifying a closure, bucketing
sites by cause, and reading the actual HCL is 5 to 15 minutes, and it very
often changes what the fix should be — five briefs this session were
materially wrong and the agent found out only after committing to an
approach. Do the scouting, report it, and let the next slot start from a
correct brief instead of a stale one. A report with no commit is a good
outcome, not a wasted slot.

**Building the measurement is a separate job from making the change.** That
one is now solved: use `tools/refusal-probe`.

So when a brief hands you "verify the closure, bucket the sites, then fix the
largest bucket" — that is two slots. Do the first half well, hand back the
bucket distribution, and say plainly that the fix is the next slot's work.
Do not silently narrow the scope instead; say which half you did.

The same applies mid-task. If you discover the real fix lives in a file
another agent holds, or needs a design decision, or turns out to be three
buckets rather than one: **stop and report**, do not push on. The orchestrator
can start a correctly-scoped agent immediately, which is faster than you
finishing a wrong one.

## Tests here are a regression gate, not a defect-finder

Every defect that mattered on 2026-08-16 was green, committed and believed
finished when it was found, and CI caught none of them:

- `count.index % 3` in an identity-bearing argument, where indices 0 and 3
  both render the same value and two instances resolve to one live identity.
  The walker enumerated unsafe shapes and defaulted to safe, so any
  `BinaryOpExpr` fell through.
- A provisioner admitted on a record-backed type on the claim that the record
  store carried the tainted bit. `recordPayload` had no status field at all.
- A data-read cascade reporting "no configuration edit is needed" over a
  dependent whose second identity component was missing entirely, because
  `resolveInstance` returns at the first failing component.
- A 234-site figure that could not exist, because a bare `locals` block
  referencing `each` or `count` is not valid OpenTofu.

All four came from adversarial reading or a corpus scan. Budget the time
accordingly: an extra audit pass buys more than an extra CI run.

## Your brief is a lead, not a fact

Three briefs written with confident scoping on 2026-08-16 were each partly
wrong, and in every case the agent that scanned the corpus instead of
trusting its brief is what caught it. One found `element`/`lookup` doing the
same job as an index expression with no `IndexExpr` in the tree; one proved
its issue's central premise impossible; one found that the "all thirteen are
single-component" claim it was handed held for four.

Verify the claims in your brief against the code and the corpus. Refuting one
is a success and should lead your report, not be buried in it.

## Where the work lives

`HANDOFF.md` carries the order and the reason; the tracker carries the
evidence and the figures; `bash scripts/pickup.sh` prints the state of the
work. `gh issue list -R INTENTIUS/choudoufu` - a bare `gh` in this clone
resolves to `opentofu/opentofu`, silently.

The goal state is the gauntlet's: every estate in `live/gauntlet/estates.json`
clears every active stage (`live/GAUNTLET.md`). Underneath the units,
HANDOFF's "The order" is the foundation sequence, ruled 2026-08-23; do not
reorder it.
