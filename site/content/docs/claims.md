---
title: "The claims"
weight: 2
---

# Four claims you can run

In stock Terraform and OpenTofu, the state file is the record of what you
own. Everything defends that file: backends store it and locks serialize
access to it, and if you lose it the tool no longer knows your
infrastructure exists. Choudoufu moves the record onto the platform
itself - identity as tags on each resource, values in a record store,
effects as receipts - and demotes the state file to a disposable cache.

That design implies four testable promises. Each one is a smoke
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

## Reading a run

Every scenario narrates each step the same way: first why the step
exists and the exact command it runs, then real output indented as
evidence under a verdict line starting with `->`. The final paragraph of
each run recaps what you watched. The
[harness page](https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/README.md)
documents every knob; pinning the emulator image and the choudoufu
version are both there.
