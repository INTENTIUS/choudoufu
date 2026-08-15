---
name: live-markers
description: Use for work on choudoufu's live-marker path — admission, identity resolution, lint refusals, discovery, and the generators behind them (row-gen, importdocs-gen, estate-gen, survey-gen, mapping-gen). Carries the product frame and the measurement traps that have repeatedly derailed this repo.
---

You are working on choudoufu. Read this before your first tool call.

## What the product is

choudoufu is an OpenTofu fork that runs a user's **existing** OpenTofu
configuration against live cloud resources, using cloud tags as ownership
markers instead of a state file.

**The goal: people's existing OpenTofu should work under live markers, with
extremely narrow exceptions.** Onboarding them from regular OpenTofu is the
product. Judge your work against that, not against any internal tidiness.

**Type parity is the bar (maintainer ruling, 2026-08-15): choudoufu must
support every resource type stock OpenTofu supports.** A type in
`tools/row-gen/rejected.json`, a needs-hand-separator or evidence-only
bucket, an unadmitted cfn-unmodeled type, or plan-and-create-only
schema-fallback support is DEBT carrying an obligation to build the missing
vocabulary or extraction - never an acceptable residue. When a ledger entry
records why a type could not be admitted, that is the work's address, not
its conclusion. The one sanctioned exclusion is credential material
(aws_iam_access_key, aws_iot_certificate, aws_ivs_playback_key_pair,
aws_appstream_directory_config - client-supplied or minted secret material
that would persist in config or state). Do not offer "leave it rejected" as
an option.

The invariant is "no state ops" (issue #73). Identity moves to cloud tags,
receipts move to per-estate cloud records (`record_store`), and what remains in
the state file is effects only — `null_resource`, `terraform_data`, `time_*`,
non-secret `random_*` — through the stock provider lifecycle, as upstream.

## The measurement trap, which is the main thing to avoid

**`live/rowgen-convergence.json` and `adopted_unchanged` are not coverage.**
They measure whether row-gen's fresh proposal agrees with a human-ratified row.
The ratified row is what ships (`tools/row-gen/emit.go:41` copies every field
verbatim), so a mismatch is generator-autonomy debt, not a failure any user
experiences. Three sessions were organised around raising that number. Do not
quote it, rank work by it, or report progress in it.

The gate users hit is **admission** (a type absent from `DefaultTable` is a hard
resolve error at `table.go:244`), and above that the **config-language
subset**: every `count`, `for_each`, and identity-bearing argument must be
statically evaluable from `var`/`local`/`path`/`terraform` alone. Type coverage
is not the binding constraint; a user at 100% type coverage still fails on
`backend "s3"`, `-out` plus `apply <planfile>`, workspaces, a CIDR-keyed
`for_each`, or `count.index` in a name.

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
  a type the generated table does not cover when the schema settles it.
- Effects already work. `null_resource` and friends are admitted the moment a
  `live` block declares a `record_store`.
- `live-plan`, `live-mv`, `live-import`, plain `plan`/`apply` under a `live`
  block, the ownership-policy matrix, provider version-skew detection, bulk
  migration off a state file, and resource-level `count`/`for_each` all ship.

## Where the work lives

The issue tracker, and nowhere else. `gh issue list -R INTENTIUS/choudoufu`.
There is no handoff document; one existed, accumulated four false load-bearing
claims across three sessions, and was retired into the tracker.
