---
title: "The claims"
weight: 2
---

# Claims you can run

In stock Terraform and OpenTofu, the state file is the record of what you
own. Everything defends that file: backends store it and locks serialize
access to it, and if you lose it the tool no longer knows your
infrastructure exists. Choudoufu moves the record onto the platform
itself - identity as tags on each resource, values in a record store,
effects as receipts - and demotes the state file to a disposable cache.

That design implies testable promises. Each one is a smoke
scenario: Docker plus a local AWS emulator, one to three minutes each;
exit 0 means every assertion held. Each scenario can also run inverted. Under
`BREAK=1` it manufactures the exact corruption the claim guards against
and passes only by catching it. A test that cannot
fail proves nothing, so every claim ships with its failure demonstrated.

| Claim | Scenario | ~time |
|---|---|---|
| Owned resources cannot fall out of plans unnoticed | `just smoke no-silent-orphans` | 2 min |
| Contention settles at the platform API, never in a lock | `just smoke no-self-managed-locks` | 2 min |
| Staleness costs reads, never results | `just smoke staleness-costs-reads` | 3 min |
| Declaring the backend is the whole setup | `just smoke backend-sets-itself-up` | 1 min |
| Recovery is a re-run, never surgery | `just smoke recovery-is-a-rerun` | 2 min |
| The roundtrip: one command in, one file out | `just smoke roundtrip` | 3 min |
| Identity is a tag you can read and move | `just smoke identity-is-a-tag` | 3 min |
| Stock until you say otherwise | `just smoke stock-until-you-say-otherwise` | 3 min |
| The record is the values | `just smoke record-is-the-values` | 1 min |

## Claim 1: owned resources cannot fall out of plans unnoticed

When an apply crashes after the create call but before the write to
state, stock tooling orphans the resource: it exists and it bills, but no
plan will ever mention it again. Here the plan reads identity from the
resource's own tags, so a resource nobody remembers still walks into the
next plan by name.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke no-silent-orphans

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke no-silent-orphans and report the "caught" line: the
scenario creates the one shape the claim excludes and must fail to
claim it.
```

The steps, in the order they print:

1. `stand the estate up` - an apply builds a small VPC estate; every
   create call carries the two identity tags, estate and address.
2. `the crash shape` - a subnet is created the way a crashed apply
   leaves one: real resource, tags written, recorded nowhere. Stock
   tooling can never see this subnet again.
3. `the next plan finds it` - the forgotten subnet appears as a named
   plan line. Nobody re-imported it and no file remembered it; the tags
   did.
4. `a deleted block is the same story` - a resource removed from the
   configuration surfaces as a destroy the same way, through the same
   read.
5. `applying removes them - loudly, exactly` - the plan proposes
   exactly two destroys and the apply performs exactly two.
6. `where the machinery does not reach, it says so out loud` - two of
   the estate's types sit outside the sweep today, and the apply names
   them and the consequence up front. Degrading to a warning is
   allowed; silence is not.
7. `the same claim where values live in the record store` - a
   `terraform_data` resource has no cloud presence to tag, so its
   record lives in the record store; delete its block and it surfaces
   from the store's own list. No state file or cloud is involved.
8. `teardown` - the estate is destroyed to an exact count.

The `BREAK=1` run creates the subnet without identity tags. That is
the one shape the claim excludes, so the scenario must refuse to claim
it.

## Claim 2: contention settles at the platform API, never in a lock

Stock backends take a lock before touching state, because two writers
corrupting one file is fatal when the file is the record. A stuck lock
then needs `force-unlock`. With no authoritative file to defend there is no lock at all; two
racing applies are refereed by the platform's own uniqueness rules.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke no-self-managed-locks

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke no-self-managed-locks and report the "caught" line:
it strips the race winner's identity marker and convergence must fail.
```

Step by step:

1. `there is no lock to force open, and the tool says so` -
   `force-unlock` refuses with the true reason instead of pretending a
   lock exists.
2. `the race` - two applies of the same client-named IAM role start at
   the same moment. The cloud's name-uniqueness constraint referees;
   the phrase "Acquiring state lock" appears in neither output.
3. `the loser converges by reading reality` - the losing apply's next
   plan is `No changes.` Its whole recovery is one ordinary plan.
4. `the one race the API cannot referee is a named collision` -
   server-assigned resources can genuinely duplicate; the duplicate
   surfaces as a named pair rather than hiding.
5. `the human resolves it` - one delete, and the estate is clean again.
6. `teardown`.

## Claim 3: staleness costs reads, never results

A stale state file is the classic failure: the file is the record, so
its lies become your plans. Here the file is a cache, never consulted for
ownership; live reads win every disagreement.
Losing or corrupting it costs a slower run and nothing else.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke staleness-costs-reads

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke staleness-costs-reads and report the "caught" line:
it moves the live world mid-comparison and the equality check must
notice.
```

In print order:

1. `manufacture a genuinely ancient cache` - apply, save the cache
   aside, destroy the whole estate, apply again. The saved cache now
   remembers only dead ids; the run proves the old and new VPC ids
   differ.
2. `three cache states, one answer` - the same plan runs against the
   fresh cache, then the ancient one, then no cache file at all. The
   outputs are byte-identical.
3. `the world moves and the fresh cache does not hide it` - a setting
   is changed behind the tool's back with the AWS CLI; the next plan
   shows the drift straight through a fresh cache, and the apply
   reconverges it.
4. `the one opt-in, and where the cost actually lives` -
   `-refresh=false` is the single path that serves reads from cache,
   and only for instances the sweep has already verified. The run
   measures its cache hits, then reruns with the cache gone to show
   none. The two outputs prove equal and both request counts print side
   by side. The price of staleness is paid in work, never in answers.
5. `the same answer where values live in the record store` - the same
   ancient-cache trick against the record store, plus a phantom: the
   cache remembers a resource that no longer exists anywhere. The plan
   neither destroys the phantom nor misses the survivor.
6. `teardown`.

## Claim 4: declaring the backend is the whole setup

Stock remote state has a day one: create a bucket, enable versioning,
create a lock table, write IAM for both, run `init`, answer migration
prompts, keep it all in step forever. Here the backend's stores
provision themselves at first use, and each proves its own read path
with a sentinel before any plan trusts it.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke backend-sets-itself-up

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke backend-sets-itself-up and report the "caught" line:
it makes the store unreachable and the run must refuse by name rather
than plan an empty-looking estate.
```

As the run prints them:

1. `no store declared - the local one appears unbidden` - a live block
   with nothing about storage gets a `.tofu-records` directory beside
   the module at first use, sentinel already written. Zero setup steps.
2. `it works: the effect survives between runs` - the recorded
   resource survives a replan, so the store is real, not scaffolding.
3. `a cloud store is one declaration, and it provisions itself` -
   `record_store "ssm" {}` is the entire cloud setup; the store writes
   its sentinel into Parameter Store and the AWS CLI reads it back.
4. `teardown` - nothing to deprovision, because nothing was ever
   provisioned by hand.

The `BREAK=1` run makes only the SSM store unreachable while the
provider stays healthy. A store that cannot answer must refuse loudly,
naming itself, because a store that answers with silence would read as
an empty estate and the next plan would propose rebuilding everything.

## Claim 5: recovery is a re-run, never surgery

Two disasters end an estate's day under stock. An apply that crashes
after a create call leaves a resource no state file knows about;
re-applying creates a duplicate and the original leaks. A lost state
file is worse, because the file was the record of everything you own.
Both end the same way here: run it again.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke recovery-is-a-rerun

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke recovery-is-a-rerun and report the "caught" line: it
withholds the crashed resource's markers and the bind check must fail
rather than claim an unmarked resource.
```

The steps as they print:

1. `the crash - an apply dies after its first create` - the VPC is made
   with the AWS CLI and stamped with the estate's markers, exactly what
   the dead apply would have written before crashing. The configuration
   still declares it.
2. `re-run the apply - it binds, completes, duplicates nothing` - the
   whole recovery is the same apply again: it finds `aws_vpc.main`
   already owned and builds the rest around it; the vpc keeps its id
   and the follow-up plan is clean.
3. `now lose every local file` - the cache and the whole `.terraform`
   directory are deleted; after an init, the next plan is `No changes.`
   The narration also says what the deleted cache held, and why that
   disposable file is the one place allowed to hold it.
4. `teardown` - the crashed vpc is destroyed with the rest of the
   estate. It was a full citizen from the moment it was bound.

The `BREAK=1` run withholds the markers. The estate must refuse to bind
an unmarked resource, so the re-run builds a second vpc - stock's crash
behavior, demonstrated as the exact boundary of the claim.

## Claim 6: the roundtrip - one command in, one file out

Migrating to a new state tool is usually a trapdoor: once your estate
is in, the only way back is another migration project. Here the door in
is `live-import`, which reads the state file you already have and
stamps ownership markers on what verifies, and the door out is the
local cache, which is a stock-format state file ready to hand back.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke roundtrip

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke roundtrip and report the "caught" line: it skips
live-import, and the plan must propose a duplicate estate - the
documented quiet failure of every migration.
```

The steps as they print:

1. `stock stands the estate up, tagless` - pinned stock OpenTofu, in
   its own container, applies a seven-resource estate with no tags
   anywhere. Identity exists only in `terraform.tfstate`.
2. `the door in - one command` - `live-import` reads that file once
   and verifies every resource against the live system; what verifies
   gets the two markers. Nothing else is touched.
3. `bound - and the old record is now optional` - the choudoufu plan is
   clean, the state file is deleted, and an ordinary apply keeps the
   estate converged while refreshing the cache.
4. `the door out - one file` - the cache is copied to
   `terraform.tfstate` and the live block removed. Stock's first plan
   back proposes only the removal of the two marker tags; one apply
   later, nothing of the fork remains.
5. `teardown - by stock, from the handed-back file` - stock destroys
   all seven resources using the file choudoufu handed back.

The `BREAK=1` run skips the one command. The plan must then propose
building a duplicate estate beside the real one, because turning on the
live block never binds resources by itself - the markers do.

## Claim 7: identity is a tag you can read and move

Because ownership lives on each resource as two tags, three things
follow that stock cannot offer: estates in one account are isolated by
construction, any AWS tool can answer ownership without this tool
present, and renaming a resource in code is a tag rewrite where stock
demands `state mv` surgery.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke identity-is-a-tag

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke identity-is-a-tag and report the "caught" line: it
renames the resource in code but skips the retag, and the plan must
propose the destroy-and-recreate stock would inflict.
```

The steps as they print:

1. `two estates stand up in one account` - two copies of the estate,
   different estate tags, one account. Nothing else separates them.
2. `any AWS tool answers ownership` - the plain CLI's tagging API lists
   each estate's resources and reads a resource's address tag. No
   choudoufu involved.
3. `neither estate can see the other` - both plans are clean, and
   neither plan output ever names the other estate's resources.
4. `a rename is a retag, not surgery` - `aws_vpc.main` becomes
   `aws_vpc.core` in code, `live-mv` rewrites the address tag on the
   live resource, and the next plan is clean. No state file was edited,
   because there is none to edit.
5. `teardown - both estates`, each by its own destroy.

The `BREAK=1` run skips `live-mv` after the code rename. The live vpc
still wears the old address, so the plan must treat the new name as
missing and the old one as orphaned - stock's destroy-and-recreate,
demonstrated as what the retag saves you from.

## Claim 8: stock until you say otherwise

A configuration with no live block gets stock behavior - measured, not
promised. The scenario plans the same state-backed estate with choudoufu
and with the pinned stock oracle, side by side with debug logging on:
the plan texts match and so do the request counts, exactly. And once you
do turn the live block on, what you pay scales with your estate rather
than the account around it.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke stock-until-you-say-otherwise

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke stock-until-you-say-otherwise and report the
"caught" line: it runs the choudoufu leg with the live block ON, and
the measurement must show the difference.
```

The steps as they print:

1. `a stock estate, stood up by choudoufu with no live block` - the
   fixture's live block is removed and choudoufu applies the ordinary
   way: a real `terraform.tfstate`, no markers, no hooks.
2. `same plan, same requests` - choudoufu and the pinned oracle each
   plan the estate with `TF_LOG=debug`; the scenario asserts the
   filtered plan texts are equal and the request counts identical.
   This is the #588 parity measurement as a two-minute demo.
3. `say otherwise - and pay for your estate, not your account` - the
   live estate goes up and its plan's request count is measured. Twenty
   foreign resources then appear in the account and the count is
   measured again; it must not move.
4. `teardown` - estate and clutter both removed.

The `BREAK=1` run plans the choudoufu leg with the live block on. The
asked-for machinery must show up in the measurement - a live plan that
measured identical to stock would mean the parity comparison compares
nothing.

## Claim 9: the record is the values

A record-backed resource - `terraform_data` and its kin - has no cloud
home; its values live in the record store. That makes the store's
answer the authoritative read, consulted on every default plan with
nothing opted into. This is the only scenario that needs no Docker, no
emulator, and no credentials: the class it covers has no cloud, and
neither does its proof.

```text
Clone https://github.com/INTENTIUS/choudoufu. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke record-is-the-values

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke record-is-the-values and report the "caught" line: it
overwrites the record with garbage, and the run must refuse by name
rather than plan against made-up values.
```

The steps as they print:

1. `stand it up - one resource, one record` - one apply, and the record
   on disk carries the resource's values as plain JSON.
2. `the default plan reads the record - and only the record` - a
   no-flag plan converges from the store's own answer.
3. `mutate the record out of band - the next default plan sees it` -
   the record's value is edited behind the tool's back, and the next
   default plan proposes the named reconvergence (`~ input`). The
   record is not a cache of the values; it is the values.
4. `reconverge and tear down` - one in-place apply restores the
   configuration's value, and the destroy leaves nothing.

The `BREAK=1` run overwrites the record with garbage. The run must fail
with the record refusal, naming the exact address - a store that cannot
answer never improvises. Where values live on cloud resources instead,
the read stays the drift detector on default plans, and vouching serves
only the explicit `-refresh=false` path (#692, measured on real AWS:
13 requests down to 5).

## Reading a run

Every scenario narrates each step the same way: first why the step
exists and the exact command it runs, then real output indented as
evidence under a verdict line starting with `->`. The final paragraph of
each run recaps what you watched. The
[harness page](https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/README.md)
documents every knob; pinning the emulator image and the choudoufu
version are both there.
