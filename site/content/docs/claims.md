---
title: "The claims"
weight: 1
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
Claim 15 inverts the control rather than dropping it: its risk is a
refusal that fires unconditionally, so its `BREAK=1` run removes the
fault and requires the run to succeed.

| Claim | Scenario | ~time |
|---|---|---|
| Owned resources cannot fall out of plans unnoticed | `just smoke no-silent-orphans` | 2 min |
| Contention settles at the platform API, never in a lock | `just smoke no-self-managed-locks` | 2 min |
| Staleness costs reads, never results | `just smoke staleness-costs-reads` | 3 min |
| Declaring the backend is the whole setup | `just smoke backend-sets-itself-up` | 1 min |
| Recovery is a re-run, never surgery | `just smoke recovery-is-a-rerun` | 2 min |
| The roundtrip: one command in, one file out | `just smoke roundtrip` | 3 min |
| Identity is a tag you can read and move | `just smoke identity-is-a-tag` | 3 min |
| Stock when you need it | `just smoke stock-when-you-need-it` | 3 min |
| Unchanged is free | `just smoke unchanged-is-free` | 3 min |
| The cache serves the whole estate | `just smoke cache-serves-the-whole-estate` | 2 min |
| A count pool is a fungible set | `just smoke count-is-a-fungible-set` | 2 min |
| Carve by retag | `just smoke carve-by-retag` (needs Go) | 6 min |
| The tag is the boundary | `just smoke the-tag-is-the-boundary` | 4 min |
| A plan costs its estate, not its account | `just smoke plan-cost-tracks-the-estate` | 2 min |
| Apply exactly what was approved | `just smoke apply-what-was-approved` | 3 min |

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

## Claim 8: stock when you need it

Stock behavior is not a mode you leave behind - it is the fallback,
whole and exact, one deleted live block away. The scenario measures
that rather than promising it: choudoufu and the pinned stock oracle
plan the same state-backed estate side by side with debug logging on,
and the plan texts match and so do the request counts, exactly. And
with the live backend on, what you pay scales with your estate rather
than the account around it.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke stock-when-you-need-it

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke stock-when-you-need-it and report the
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
3. `the live backend on - and you pay for your estate, not your
   account` - the
   live estate goes up and its plan's request count is measured. Twenty
   foreign resources then appear in the account and the count is
   measured again; it must not move.
4. `teardown` - estate and clutter both removed.

The `BREAK=1` run plans the choudoufu leg with the live block on. The
asked-for machinery must show up in the measurement - a live plan that
measured identical to stock would mean the parity comparison compares
nothing.

## Claim 9: unchanged is free

Re-planning an estate that did not change should not cost a full
re-read of it, and here it does not. On the `-refresh=false` path, an
instance the run can vouch for is served from the state cache and its
wire reads are never made - the bill is measured live, in the run's own
debug stream. The whole pass answers to one estate-level argument:
`reads = "full"` in the live block turns it off (`CHOUDOUFU_READS`
overrides per run), and turning it off may change the price but never
the plan. For record-backed resources the attestation is the record
itself, on every default plan, with nothing opted into.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke unchanged-is-free

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke unchanged-is-free and report the "caught" line: it
overwrites a record with garbage, and the run must refuse by name
rather than plan against made-up values.
```

The steps as they print:

1. `stand the estate up` - an estate and a fresh cache, nothing changed
   since.
2. `the free re-plan, and the argument that refuses it` - the same
   `-refresh=false` plan runs under the default policy and again under
   `CHOUDOUFU_READS=full`. Selective serves the vouched instances and
   the request count drops; full serves nothing and pays every read;
   the two outputs must not differ by a byte. The toggle prices the
   plan, never changes it.
3. `teardown the cloud estate`.
4. `the record-backed half - the record is the attestation` - a
   `terraform_data`'s record is edited behind the tool's back, and the
   next default plan surfaces the named reconvergence (`~ input`). The
   record is not a cache of the values; it is the values.

The `BREAK=1` run overwrites the record with garbage. The run must fail
with the record refusal, naming the exact address - a store that cannot
answer never improvises. Default plans are untouched by all of this:
they read fully under either policy, because the read is drift
detection (claim 3 pins that forever).

## Claim 10: the cache serves the whole estate

`-refresh=false` is the path that serves from the state cache instead of
reading live. Until now it served the schema-admitted and record-backed
slice; a converged estate's server-assigned resources - VPCs, subnets,
security groups, every id the cloud hands out - were read live anyway.
Now they are served too, so one estate of a decomposed terralith plans
at the speed of reading a file rather than re-reading the cloud. A
default plan still refreshes, because the read is drift detection, and
the serving is vouched by the estate sweep, so a deleted resource is
caught rather than served from cache.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke cache-serves-the-whole-estate

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke cache-serves-the-whole-estate and report the "caught"
line: it deletes a resource out of band, and the plan must surface it
rather than serve the gone object from cache.
```

The steps as they print:

1. `stand the estate up` - four server-assigned resources with a warm
   cache.
2. `a default plan reads them all` - zero served; a default plan
   refreshes for drift by ruling.
3. `-refresh=false serves every instance from cache` - all four served,
   the estate planned without re-reading a resource.
4. `serving is existence-vouched` - the estate sweep confirms each is
   still live before serving, so a deletion is caught.

The `BREAK=1` run deletes a resource out of band. The sweep no longer
vouches it, so it is not served from cache and the plan surfaces it -
losing an object costs a read, never a wrong plan.

## Claim 11: a count pool is a fungible set

A `count` block declares a set, and stock tools treat it as a list:
instance 2 is whatever sits at index 2. Shrinking the count renumbers
the tail and rebuilds it. Choudoufu names each member with a `tofu-slot`
marker instead, a stable id minted once and never reused. The lint
boundary forbids any argument from reading `count.index`. The index is where a
member sits today; the slot is what it is. So a pool of three scales to
two by removing exactly one member and rebuilding nothing, and the
survivors keep their live ids. Strip the slot from one member where no
local record names it, and the set has two rules for naming its members,
so the run refuses rather than guess.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke count-is-a-fungible-set

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke count-is-a-fungible-set and report the "caught"
line: it deletes the local record, strips the slot marker from one
member, and the plan must refuse the half-slotted set by name rather
than bind the odd member by a guess.

## Claim 12: carve by retag

In stock tooling every ownership boundary is a state file, so splitting a
monolith into team estates is state surgery. Each resource is moved
between files by hand, and for a moment it sits in two ledgers or in
none. Here the boundary is
a tag. A resource leaves one estate for another by having its
`tofu-estate` tag rewritten. The tool refuses a write that would leave
either side dirty, and afterwards each side plans clean and pays only
for what it holds.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info), the AWS CLI is installed, and Go is installed (this
scenario generates its estate with go run). From the repo root run:

  just smoke carve-by-retag

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke carve-by-retag and report the "caught" line: it moves
the six blocks but skips the retag, and the monolith must propose
destroying the leavers while the new estate proposes building them.
```

The steps as they print:

1. `stand up a pool of three` - three elastic IPs, three distinct slots.
2. `capture the survivor at the middle seat` - the allocation id that
   holds slot 1 is written down.
3. `scale to two - one removed, nothing rebuilt` - count drops to 2; the
   plan shows one destroy and zero creates, then applies.
4. `the middle survivor is the same live object` - the id from step 2 is
   still allocated. Its seat moved and its identity did not.
5. `teardown` - the pool is destroyed.

The `BREAK=1` run deletes the local files, cache and record store both,
so nothing but the tags names a member, then deletes the `tofu-slot` tag
from one of them. Two members now answer by slot and one has no answer,
so the plan refuses the half-slotted set and names the slot disagreement
rather than binding the odd member by position. Beside an intact record
the same strip is a repair, not a guess: the record names the member and
the plan re-stamps its slot.
1. `stock stands the terralith up` - the pinned stock OpenTofu applies
   the generated terralith the ordinary way. It is 79 resources. Most of
   them are IAM. A small ECS layer and a Route 53 fan-out sit beside them.
   The estate carries `count` and `for_each` expansion and one
   module-nested pod. One state file, and not a marker anywhere.
2. `one command adopts it, and the state file is deleted` - `live-import`
   reads the file once and stamps 38 resources; the other 41 are
   untaggable and compose their identity from a stamped parent. The file
   goes, and the plan is clean. The request count of that plan is kept
   for step 6.
3. `carve a team out` - six blocks move to a new root with its own live
   block, the git half any tool needs. Then `live-mv -from-estate` runs
   three times in the destination, once for each resource that carries a
   marker. The inline policy and the two attachments carry none and need
   no write. The plain CLI reads the role's new estate tag and its inline
   policy still attached.
4. `both sides plan clean` - the monolith no longer declares or owns the
   six; the team estate declares all six and owns the three. No state was
   split, nothing was rebuilt.
5. `carve across a reference` - the ECS execution role leaves for an IAM
   estate. The task definition that stays reads the same ARN through a
   data source, the cross-estate pattern the docs give, and all three
   estates plan clean.
6. `a plan costs what its estate holds` - the team estate's plan makes a
   fraction of the requests the monolith's did.
7. `teardown` - the monolith destroys its 71 resources. The IAM estate
   destroys 2 and the team estate 6. Nothing is left.

The `BREAK=1` run does the git half of the carve and never rewrites a
tag. That is the two-ledger window stock lives in, manufactured: the
monolith must name the leavers under owned and undeclared and propose
destroying them, and the new estate must propose creating what it
declares and does not own. Either side planning clean would show the
tag write had changed nothing.

The verb this claim rests on landed with it, and so did the rule it
exposed. A move leaves the source's records for the resource behind, and
the source's next plan found the moved role's untaggable children in
those records and proposed destroying them. The live tag decides. A
parent whose marker names another estate never anchors a child for this
one, whatever a left-behind record says, and that rule is why step 4
plans clean.

## Claim 13: the tag is the boundary

In stock Terraform and OpenTofu, who owns a resource is a line in a state
file. Changing that line is state surgery: no IAM policy can gate it,
because the cloud never sees it, and nothing in the account records it.
Under choudoufu ownership is a tag on the resource, and a tag write is an
API call the cloud's own policy engine evaluates per resource. A role can
be fenced to half an estate by a condition on the ownership tag, with the
grant `live/MARKERS.md` publishes under "Granting an estate". A carve, one
half moving into an estate of its own, is then a governed write the
platform can refuse. The scenario turns the emulator's IAM enforcement on
for its run; the harness's own key stays privileged, and only the two
roles the scenario creates and assumes are governed.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke the-tag-is-the-boundary

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke the-tag-is-the-boundary and report the "caught"
line: it drops the conditions from Bob's grant, and Bob's write on
Alice's half must go through, which proves the condition and not the
credentials was the boundary.
```

The steps as they print:

1. `the platform stands one estate up, two halves in it` - two instances
   in estate app, one under module.net and one under module.data, with
   markers stamped by the account.
2. `two roles, two halves, one grant shape` - Alice may act on
   module.data.* and create into data; Bob may act on module.net.* and
   create into net. The evidence line prints the conditions.
3. `Alice converges her half` - a tag change on the database, applied
   under Alice's session.
4. `Alice is denied on Bob's half - by AWS, not by this tool` - the same
   kind of change on the gateway. The provider's CreateTags comes back
   403 and the gateway is untouched.
5. `Bob converges the same change` - his session, his half.
6. `the carve begins with a git move, and Bob's attempt at the retag is
   denied` - the data module moves to a new root, and Bob's
   `live-mv -from-estate=app` is refused by the platform before anything
   moves.
7. `Alice completes the carve: one governed tag write` - the same
   command under Alice's session, and tofu-estate becomes data.
8. `both estates plan clean, each under its own role` - No changes in
   data under Alice and in app under Bob.
9. `teardown - each estate by its own destroy`.

The `BREAK=1` run replaces Bob's grant with the same reach and no
conditions, then has Bob change a tag on Alice's half. The write must go
through. If the platform still refused, something other than the
condition was the boundary and the claim would prove nothing.

One emulator note. Real EC2 refuses with `UnauthorizedOperation`; the
emulator refuses with a 403 whose body the EC2 SDK cannot parse, so the
provider prints `api error UnknownError`. The scenario matches both, and
the gap is filed as lex00/floci#189.

On the real account, the same carve ran in us-east-2 on 2026-09-03 with
the roles assumed through STS. Every governed write was in the account's
own CloudTrail event history within a minute. The two refusals
carry the code real EC2 uses, and each record names the session that was
refused:

```text
04:39:31Z  alice  OK                            Name=database-v2           i-01e1006285c2b37b3
04:39:47Z  alice  Client.UnauthorizedOperation  Name=gateway-v2            i-0d3d2031d0b946a23
04:40:02Z  bob    OK                            Name=gateway-v2            i-0d3d2031d0b946a23
04:40:32Z  bob    Client.UnauthorizedOperation  tofu-estate=boundary-data  i-01e1006285c2b37b3
04:40:40Z  alice  OK                            tofu-estate=boundary-data  i-01e1006285c2b37b3
```

Each line is one `ec2:CreateTags` event, and
`live/smoke/evidence/the-tag-is-the-boundary.cloudtrail.json` holds the
five with their event IDs and the lookup that returned them. No state
file could have produced that record, because a state edit is not an API
call. The estate was torn down afterwards and the account listed back to
baseline.

## Claim 14: a plan costs its estate, not its account

A bound state file makes a terralith's plan pay for the whole account:
every resource anyone owns sits in the one file every plan reads end to
end. Here ownership is a tag, not a file, so a plan of one estate reads
only that estate's resources - and stays that cheap no matter how large
the rest of the account grows around it.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. From the repo root run:

  just smoke plan-cost-tracks-the-estate

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke plan-cost-tracks-the-estate and report the "caught"
line: it replans the same estate account-wide instead of scoped to its
own tag, and the cost must jump to the account-wide shape.
```

The steps as they print:

1. `stand up one estate, and plan it alone` - a four-resource network
   estate (a VPC, two subnets, a security group) applies, then plans.
   Its request count is recorded.
2. `grow the account with another estate, and replan the first` - an
   eight-resource estate joins the account under a different tag. The
   first estate replans to the same request count as step 1, whether or
   not the second estate exists.
3. `what reading the whole terralith would cost` - an account-wide,
   adoption-only scan of the same account costs measurably more than
   the estate-scoped plan - the shape a bound state file would force on
   every plan, regardless of which estate you actually meant to touch.
4. `teardown` - both estates destroyed.

The `BREAK=1` run makes the same request the account-wide scan in step 3
made, against the same estate step 1 and 2 scoped for free. If the cost
did not climb to that account-wide shape - more than triple what scoping
cost, the threshold the scenario checks - something other than the
estate scoping was keeping the plan cheap, and the claim would prove
nothing.

## Claim 15: apply exactly what was approved

CI runs Terraform as: plan on the pull request, a human approves, apply
exactly what was approved. The artifact that crosses that gate is the
plan file, and here it stays the stock one - `plan -out=FILE`, `apply
FILE`. What changes is what the apply does with it. It never replays the
file. It reads the live system and plans against what is there now, the
way every live-markers run does, and then compares its own fresh plan
with the one the file describes: same resources, same actions, same live
objects. Matching, it applies without asking again, because the file was
the approval. Differing, it refuses by name and exits 3, which is a
pipeline's signal to send the change back to review rather than to page
somebody about a broken run.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. From the repo root run:

  just smoke apply-what-was-approved

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke apply-what-was-approved and report the "caught" line:
it leaves the world unmoved, and the same file must APPLY - a comparison
that refuses every plan file it is handed would prove nothing.
```

The steps as they print:

1. `stand the estate up` - the fixture applies, every resource carrying
   its ownership markers.
2. `the change under review` - a log group's retention goes from one day
   to three, and `plan -out=approved.tfplan` writes the stock-format
   file a pipeline would attach to the pull request.
3. `the world moves while the approval waits` - a subnet appears in the
   account carrying this estate's markers for an address the
   configuration does not declare, so the next plan proposes destroying
   it: a change nobody approved.
4. `apply the approved plan` - the apply re-reads the live system,
   compares, and refuses. The scenario asserts the refusal's own summary
   line, that the row it prints is `aws_subnet.crashed  Delete
   subnet-...`, and that the exit status is 3.
5. `re-plan, re-approve, apply` - the way forward the refusal names. The
   same two commands over the world as it now is, and the approved
   change lands: the log group's retention reads 3 and the unapproved
   subnet is gone.
6. `teardown` - the estate destroyed.

The `BREAK=1` run is the inverse control, and it is the one this claim
needs. A refusal that fires for every plan file handed to it is not a
check, and it would pass step 4 forever. So `BREAK=1` skips the
out-of-band change and the same file must apply cleanly; the scenario
fails if it refuses.

## Reading a run

Every scenario narrates each step the same way: first why the step
exists and the exact command it runs, then real output indented as
evidence under a verdict line starting with `->`. The final paragraph of
each run recaps what you watched. The
[harness page](https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/README.md)
documents every knob; pinning the emulator image and the choudoufu
version are both there.
