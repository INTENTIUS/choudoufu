# Recover an estate

You have lost the cache, or the record store, or both, and there are live
resources on the other side of it. This page is the order to do things in,
and the inventory of what does not come back.

Two claims already demonstrate the recovery, executable, against a real
emulator, each with a `BREAK=1` arm that proves the assertion is
load-bearing. Run them rather than reading a description of them:

| Claim | What it shows | Command |
|---|---|---|
| [Claim 5: Recovery is a re-run, never surgery](smoke/claims/recovery-is-a-rerun.md) | An apply that crashed after a create, and a working copy with every local file deleted. Both end in a clean re-run. | `just smoke recovery-is-a-rerun` |
| [Claim 17: A record-only composite identity survives cache loss](smoke/claims/record-only-survives-cache-loss.md) | The cache gone and the record intact recovers; the record gone as well produces one named duplicate create, never a silent bind. | `just smoke record-only-survives-cache-loss` |

Everything below is the part neither claim supplies: what to do, in order,
and what the damage is when it is not recoverable.

## First, work out what you actually lost

The three artifacts fail differently and only one of them is serious.

**The cache** (`choudoufu-cache.tfstate` under the data directory) is meant
to be disposable. Lose it and a plan pays for reads it would otherwise have
skipped. Nothing about ownership is decided there, so nothing about ownership
changes. Claim 5's step 3 deletes it along with the whole `.terraform`
directory and the next plan is `No changes.`

**The markers** are the ownership record, and they are on the live objects
themselves. Losing your local machine does not touch them. Losing them means
somebody untagged your resources, which is a different problem than this page
and is covered by
[the ownership policy matrix](https://intentius.io/choudoufu/docs/use/ownership-policy/).

**The record store** is the one with no recovery path. It holds the values of
resources that have no cloud object at all, and for a narrow slice of AWS
types it holds the only copy of an identity. That is the rest of this page.
Where those records are kept changes how likely you are to get them back and
changes nothing about the inventory below. [Where things are
stored](https://intentius.io/choudoufu/docs/use/storage/) has
the backends. A bucket is the one store where a deleted record has an undo:
the store refuses a bucket without versioning, so every deleted record is
still there as a noncurrent version until the lifecycle rule expires it.

## "Cannot carry a marker" is not the same as "needs a record"

Worth separating before the numbers, because conflating the two badly
overstates the damage. A type's tag surface decides where a marker could be
written. It does not decide how the identity is recovered, and those are
different questions with different answers.

Of the 1,699 provider types, 852 are untaggable. The survey path - the answer
to "how would a run find this object again" - is one of six, and not one of
them is "read it out of a record". At commit `0182aea761`:

| Survey path | Types | What it means |
|---|---|---|
| moves to Ops | 561 | Nothing binds a listed object to an address; a human decides |
| enumerable, unbindable | 135 | The cloud can list them; nothing pairs a listing with a declaration |
| client-named | 73 | The configuration names it, so the name re-derives every run |
| parent-derived | 48 | Composed from parents that are themselves identified |
| account-derived | 31 | One per account or region; the account is the identity |
| unique-name | 4 | The name is unique by construction |

The bottom four are 156 types that need no carrier at all: their identity
re-derives from the declaration on every plan, with nothing stored anywhere.
`aws_iam_group_policy_attachment` is the shape - untaggable, with nowhere to
hang a marker, and fully identified as `{group}` `/` `{policy_arn}`, the two
parents it attaches. Untaggable cloud resources are overwhelmingly derivable.

The record store's own population is the other thing entirely: resources with
no cloud object at all. The overlap - a live object exists, and a record is the
only copy of which one it is - is the record-carried tier, 96 of whose members
are usable today. That overlap is what the rest of this page is about, and it
is the small part of untaggability, not the shape of it.

## What comes back from the live cloud alone

Most of an estate does. Derived from `live/readiness.json`, last committed at
commit `0182aea761` against provider `hashicorp/aws` `6.59.0`: of the 1,699
provider resource types this fork classifies, 1,225 recover their identity
with no record, no cache and no memory of a prior run.

| Tier | Types | In-contract | How it recovers |
|---|---|---|---|
| marker-carried | 846 | 682 | The `tofu-address` tag on the object is the identity. The sweep reads it back. |
| declaration-carried | 379 | 341 | Recomputed from the configuration plus the parent's live identity, every run. |

`in-contract` is the column that means usable today; the rest is ordinary
admission debt. [The resource tier
lookup](https://intentius.io/choudoufu/docs/use/resource-tiers/) has the per-type table and
the definitions, including the caveat that matters most here: a
declaration-carried instance derived from a parent is only as recoverable as
that parent.

## What does not come back

Two populations, and they are not the same kind of loss.

### Resources with no cloud object at all

`random_pet`, `random_password`, `null_resource`, `terraform_data`,
`time_sleep`, the `tls_*` family. There is nothing live to find, because
nothing was ever created. The record was the object. Lose it and the value
regenerates on the next apply, the same way it would if you deleted these
from a stock state file.

The tombstone and deposed seeds live here too, and they are what make a crash
between a destroy and a create recoverable. Losing them costs that
recoverability, silently, until the next crash.

### The 96 in-contract record-carried AWS types

These do have a live object. It carries no tag, and its identity is minted by
the server rather than written by your configuration, so the record was the
only place the pairing was ever held. Grouped by what breaks, at commit
`0182aea761`:

| Shape | n | Example |
|---|---|---|
| attachment / association | 30 | `aws_efs_mount_target` |
| account or region singleton | 15 | `aws_securityhub_account` |
| WAF Classic | 12 | `aws_waf_ipset` |
| immutable versioned artifact | 9 | `aws_lambda_layer_version` |
| CloudFront sub-object | 7 | `aws_cloudfront_origin_access_control` |
| **server-minted credential** | **5** | **`aws_iam_access_key`** |
| other | 18 | `aws_kms_custom_key_store` |

For the 45 attachments and singletons, AWS enforces uniqueness, so the
duplicate create Claim 17 promises will mostly fail loudly at apply rather than
silently double something. That is the benign half. It is inference from the
AWS APIs rather than something measured here, and "fails loudly" is not the
same as "recovers" - see the honest edge at the end of this page for what you
can actually do about it.

The dangerous subset is where the duplicate succeeds.

### `aws_iam_access_key`, worked

Lose the record store on an estate that declares one, and three things happen
together:

1. **The secret is gone for good.** IAM returns a secret access key exactly
   once, at create. Nothing can reproduce it. Consumers holding it keep
   working, which is why you may not notice.
2. **The plan proposes a create, and it succeeds.** No uniqueness constraint
   refuses a second key for a user.
3. **The original key stays live and valid.** A working AWS credential that
   nothing manages, nothing rotates, and no plan will ever propose destroying,
   because nothing knows it exists.

There is a hard edge that stops this being merely untidy. **IAM allows two
access keys per user**, and the common rotation setup already uses both: one
active, one mid-rotation. In that case the create fails *and* you are left
holding an unmanaged credential, with no way to tell which of the two live
keys was the managed one except by hand.

An orphaned credential is a security finding. Treat a record-store loss on an
estate declaring `aws_iam_access_key` as an incident, audit the user's keys
against what the configuration expects, and deactivate what you cannot
account for.

## What the damage cannot be, and what it can

The reassuring half first, because it is real and it bounds everything above.

**A lost record can never produce a wrong marker.** An identity-bearing
argument is evaluated through `internal/live/staticeval`, whose `Allowed`
predicate admits exactly five traversal roots: `var`, `local`, `path`,
`terraform` and `tofu`. A value that came out of the record store is not one
of them, so it is never folded into a marker. Where the identity cannot be
rendered, the instance is omitted with a named reason and the plan proposes a
create. A duplicate is the failure mode; a marker pointing at somebody else's
object is not.

**A record-backed value can still be a component of another resource's
identity**, and this page says so because two other pages on this site used to
say the opposite. `name = "svc-${random_pet.suffix.id}"` is the ordinary shape.
The corpus estates have it: `corpus-eks-basic`'s cluster takes its name from
`random_string.suffix`, and `terraform-aws-dynamodb-table`'s table is
`"my-table-${random_pet.this.id}"`. Identity resolution does not refuse it. It
handles the reference structurally, and the child resolves as *parent-derived*
with the pet's attribute as a part of its formula.

The consequence is the one to plan around. Lose that record, and the pet is
proposed for create with a fresh value, so every resource named after it is
proposed for create too, under a name no live object has. `renderFormula`
checks that every formula parent is present before rendering anything and
omits the child as `PARENT_UNAVAILABLE` rather than guessing, so nothing is
mis-bound - but nothing rescues it either. Per-instance marker binding runs
only for instances whose identity is server-assigned; a parent-derived
instance is bound by its formula, so its own `tofu-address` tag does not bring
it back.

The live objects are not endangered by this. The sweep still records those
addresses as declared, so their tagged objects are never read as orphans and
never proposed for destruction. They are simply left behind, alongside a
second set the apply would create.

The adoption ledger is where this becomes visible: `choudoufu plan
-adoption-only` prints these instances as **waits on parent**. Read it before
you apply anything.

## The procedure

**1. Change nothing by hand.** No state surgery, no editing records, no
deleting the tags on live objects to "start clean". The markers are the
ownership record and they are the thing that still works.

**2. Restore the record store if you can.** choudoufu reads and writes keys and
keeps no second copy, so whether a restore exists is a property of the backend
and of the durability you put under it. On `s3` the deleted objects have noncurrent
versions, because the store refuses a bucket without versioning, and this is
the whole recovery. It takes `s3:ListBucketVersions` and
`s3:DeleteObjectVersion`, which the estate's own role does not have
([IAM](https://intentius.io/choudoufu/docs/use/iam/#what-a-recovery-needs)). Check before
doing anything else: restoring is strictly better than every option below it,
and it makes the rest of this page moot.

**3. `init`, then plan for the report rather than for the diff.**

```
choudoufu init
choudoufu plan -adoption-only
```

The adoption ledger answers only "which live resource does each declared
instance bind to", and it fits on a screen. The full plan's own resource diff
is the wrong instrument here: it tells you what would change, not why an
instance failed to bind.

**4. Read the classes, not the count.** Each declared instance lands in one:

| Class | What it means | What to do |
|---|---|---|
| `already marked` | The estate's markers are on the live resource. | Nothing. This is the recovered majority. |
| `adoptable now` | A live resource sits at the declared identity, carrying no marker. | Run the tag write the ledger prints. |
| `waits on parent` | A parent it derives from did not resolve. | Fix the parent first; see the section above. |
| `no path` | Needs a marker and no live resource was offered. | The record-carried case. Steps 5 and 6. |
| `in the way` | A live resource holds the identity and is not this run's to claim. | Stop. Another estate owns it. |
| `nothing live` | Nothing was found; an apply creates it. | Correct if the resource genuinely does not exist. Check that it does not. |

**5. Seed what you can from a stock state file.** If a `terraform.tfstate`
still exists that holds these instances - an old backup, a copy from before
the migration, a colleague's working copy - then
[`choudoufu live-import`](https://intentius.io/choudoufu/docs/use/migrate/) reads it, and it
is the only command in the fork that writes an identity record outside an
apply. Do this before anything else in this step.

**6. Plan again, and read the reason on anything still unbound.** Re-run step
3. What remains is the honest edge.

## The honest edge

With no record and no stock state file to seed from, a record-carried instance
has no way back today. It is worth being exact about why, because the obvious
remedy looks available and is not.

**`choudoufu import` is refused under a live block**, with its own message
("Import is not available under live resource markers"). It writes into a state
file that would carry authority, and here the state file is a cache. The
message directs you to adopt the resource by stamping its markers instead -
which is the right answer for most types and no answer at all for this one,
because a record-carried type has no `tags` argument to stamp.

**And nothing else writes a record outside an apply.**
`projection.SeedLocatedForInstance` has exactly two callers, both in
`internal/live/liveimport/stamp.go`, which is the state-file migration path.
That is why step 5 is worth trying and why it stops where it does.

So the record stays absent, and the next plan takes the branch in
`internal/live/projection/build.go` that reads:

```
No persisted record exists yet for <address>, so this resource has not been
created yet. The plan will propose creating it.
```

The mechanism underneath is worth knowing, because it means the answer is
stable rather than intermittent: the run cache loads the estate's whole record
namespace in bulk, so a key inside that namespace that is not in the snapshot
is answered "does not exist" directly, with no backend round trip. There is no
retry that produces a different result.

That is where recovery stops. Issue
[#1323](https://github.com/INTENTIUS/choudoufu/issues/1323) is the open design
for a re-seed, and it notes the thing that makes the gap frustrating rather
than fundamental: every one of the 96 types is importable as far as the
provider is concerned (`facts.not_importable` is false for all of them at
commit `0182aea761`). The identity is derivable. Nothing writes it down.

Until that lands, the options are: restore the store, accept the duplicate
create and clean up the original by hand, or remove the block from the
configuration and manage the object outside the estate. For
`aws_iam_access_key` specifically, re-read the worked example above before
choosing the second one.

One protection that does *not* extend here, so you do not count on it:
`strict { no_source_create = "refuse" }` is the default, and it refuses an
instance with no record, no live marker and no derivable identity - but its
own condition excludes record-backed rows. It protects a config-identified
type whose derivation failed. It does not protect this slice.

## What a machine checks, and what a human last checked

This page tells you to trust these behaviours in an emergency, so it should
say who verified them.

Of the 30 scenarios under `live/smoke/scenarios/`, seven run in CI - the
Kubernetes matrix in `.github/workflows/k8s-smoke.yml`, on every change that
touches them. The other 23, **including both recovery claims on this page**,
are hand-run. Nothing schedules them. They passed when somebody last typed
`just smoke <name>`, and the claim pages record the result rather than a date.

So: run them yourself. Both take about two minutes against the emulator, need
no AWS account, and the `BREAK=1` arm is what tells you the assertion is still
load-bearing rather than passing vacuously.

The two findings in "What the damage cannot be" were read out of the code
rather than measured on a running estate. No scenario covers a record-store
loss underneath a parent-derived child today; that gap is worth a claim of its
own and does not have one.

## Related

- [Where things are stored](https://intentius.io/choudoufu/docs/use/storage/) - what the
  record store holds, per-backend, and who can read it
- [What you set up by hand](https://intentius.io/choudoufu/docs/use/setup/) - what has to
  exist before the first plan
- [Resource tier lookup](https://intentius.io/choudoufu/docs/use/resource-tiers/) - your own
  types, one by one
- [Migrate an existing estate](https://intentius.io/choudoufu/docs/use/migrate/) - the path
  in from a stock state file, which is also the path back if you still have one
