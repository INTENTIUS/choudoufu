# Day-2 operations

Running an estate after the first apply. Renaming, removing, recording effects
the cloud cannot report, and working with other people.

## Renaming a resource

Rename the resource block, then rewrite the marker.

```
choudoufu live-mv aws_vpc.old aws_vpc.new
```

That rewrites the `tofu-address` tag on the live resource carrying the old
address. The tag write is the move, so `moved` blocks are refused. Resources
never adopted are left alone.

A destination address absent from your configuration is refused unless you pass
`-allow-missing-config`. `-dry-run` shows what it would write. Full options in
`choudoufu live-mv -help`.

## Removing a resource

Deleting a resource block leaves its marker on the live object, and the sweep
destroys a marked, undeclared, taggable resource on the next plan. That matches
upstream without a `removed` block.

To stop managing something without destroying it, change what happens to a
resource you no longer declare.

```hcl
# estate.chdf.hcl
estate = "my-estate"

policy {
  undeclared_tagged = "untag"
}
```

`untag` removes this estate's marker and leaves the resource running. `keep`
leaves both alone.

### Two cases where removal leaves an orphan

Standing limits rather than races, and the plan names them every time.

**Types carrying no tags.** `aws_route`, `aws_route_table_association`,
`aws_s3_bucket_policy`, `aws_s3_bucket_versioning`, `aws_iam_role_policy`,
`aws_kms_alias`, `aws_route53_record` and others have nowhere to put a marker,
so deleting the block removes the only record of which live resource it was.
Destroy the resource before removing its block, or delete it out of band. The
set is determined at runtime from each type's provider schema rather than
fixed.

**Types outside the admission table.** A live resource carrying this estate's
markers at an unadmitted type is invisible to the removal sweep, which is
defined over the admission table.

Both appear in every plan under "Not swept for removal". Types a provider
cannot list or tag are reported by count, since that holds every run. Pass
`-verbose` to itemise them. A list call that actually failed is itemised every
time.

### The sweep can be a run behind

Finding resources you own but no longer declare may go through AWS's Resource
Groups Tagging API, which is eventually consistent. A resource whose tags have
not propagated is not returned, so an orphan can be reported one run late.

That is the only direction this bites. Binding the resources you *do* declare
reads each type through its own service API rather than the tag index, so a
freshly tagged resource is never mistaken for a missing one and no plan
proposes a duplicate because of it.

## Choosing what happens to each kind of resource

Every resource choudoufu sees falls into one of four situations, decided by
whether your configuration declares it and whether it carries this estate's
marker. The `policy` block sets what happens in each.

With no `policy` block you get the defaults below, which are today's behaviour.

| The situation you are in | Setting | Default | What the default does |
|---|---|---|---|
| You declare it, and it carries your marker. The ordinary case. | `declared_tagged` | `converge` | Plans and applies it against your configuration, like any resource. |
| You declare it, but no live resource carries your marker for it. | `declared_untagged` | `refuse` | Declines to touch it until you adopt it. |
| **You removed it from your configuration, and it still carries your marker.** | `undeclared_tagged` | **`delete`** | **Destroys it on the next plan.** |
| It carries no marker, and you never declared it. Somebody else's. | `undeclared_untagged` | `keep` | Leaves it alone. |

:::warning
The third row is the one to know before deleting a resource block. Removing the
block does not mean "stop managing this", it means "destroy this", which is
also what upstream does without a `removed` block. Set `undeclared_tagged` to
`untag` or `keep` first if the resource should survive.
:::

### What each setting accepts

| Setting | Can be set to |
|---|---|
| `declared_tagged` | `converge`, `untag`, `keep`, `report` |
| `declared_untagged` | `converge`, `adopt`, `refuse`, `keep`, `report` |
| `undeclared_tagged` | `delete`, `untag`, `keep`, `report` |
| `undeclared_untagged` | `keep`, `delete`, `report` |

`converge` manages it normally. `adopt` claims it by writing your marker.
`refuse` declines until it is adopted. `untag` drops your marker and leaves the
resource running. `keep` touches nothing. `report` shows it in plan output and
does nothing else.

Combinations with no coherent meaning are refused at lint. You cannot `adopt`
something carrying neither a declaration nor a marker, and you cannot `delete`
something your configuration still declares.

### Reconciling a whole account

`undeclared_untagged = "delete"` destroys resources your configuration has
never mentioned. It requires a `scope` block, the only setting that does.

Re-read the two orphan cases above before enabling it. The sweep cannot see
every resource in the account, so a clean reconciliation does not mean a clean
account.

## Effects the cloud cannot tell you about

Nothing in the live system records that a database migration, a script, or a
one-shot API call happened, so no marker reads back.

`null_resource`, `terraform_data`, `time_*` and non-secret `random_*` work once
the live configuration declares a `record_store`.

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "ssm" {}
```

The label picks the backend, one of `local`, `ssm` or `s3`. Those resources
then run the stock provider lifecycle exactly as upstream.

[Where things are stored](storage.html) covers the backends, what records hold,
and why a receipt must not go in there.

## Running this with other people

Ownership lives on the resources themselves, and records settle concurrent
writes by conditional write. Two simultaneous applies against one estate
resolve one of four ways.

| Race | Outcome |
|---|---|
| Two creates of the same client-named resource | The cloud's uniqueness constraint rejects the second. The loser re-plans, binds to the winner's resource, and comes back clean. |
| Two creates of the same server-assigned resource | Both are created. The next plan reports a marker collision naming both live IDs and refuses rather than guessing. A human deletes one. |
| Divergent in-place updates | Last writer wins at the API. The next plan reads the live system and converges. |
| An update racing a destroy | The loser gets not-found, re-plans, and converges. |

No race orphans a resource silently. Each case is a clean re-plan or a named
collision.

Compare a backend whose lock fails or was never configured, where the last
state write wins and the loser's resource drops silently out of every future
plan. A crash mid-apply is the same story, lock or no lock, because a resource
created but not yet recorded is orphaned either way. Under markers the tag rode
the create call itself, so the resource is discoverable and there is nothing to
unlock or recover.

None of that argues for applying concurrently. Serialize applies in CI, where
the real mutex has always been.

## Sharing values between estates

There is no remote state to read. `live/OUTPUTS.md` covers the cross-estate
pattern, and `data "terraform_remote_state"` is refused.

## Plan, review, apply

Saved plan files are refused, so planning in a PR and applying that exact
artifact has no equivalent yet. Ordinary `apply` re-plans and re-confirms
against the live system, which is the honest behaviour, but nothing today
detects that the world moved between review and apply.

The design closing that gap is settled.
[#74](https://github.com/INTENTIUS/choudoufu/issues/74) chose a plan
fingerprint, a digest printed at plan time that apply checks against its own
fresh plan, refusing on mismatch. `rfc/20260814-plan-approval.md` holds the
design. Not implemented yet.
