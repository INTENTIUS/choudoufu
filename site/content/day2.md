# Day-2 operations

Running an estate after the first apply: renaming things, removing things,
recording effects the cloud cannot tell you about, and working with other
people.

## Renaming a resource

Rename the resource block in your configuration, then rewrite the marker:

```
choudoufu live-mv aws_vpc.old aws_vpc.new
```

That rewrites the `tofu-address` tag on the live resource carrying the old
address. It replaces `moved` blocks and state surgery outright,
because there is no state to edit. It leaves resources that were never adopted
alone.

A destination address absent from your configuration is refused unless you pass
`-allow-missing-config`. `-dry-run` shows what it would write. Full options are
in `choudoufu live-mv -help`.

## Removing a resource

Deleting a resource block leaves its marker on the live object, and the sweep
destroys a marked, undeclared, taggable resource on the next plan. That matches
what upstream does without a `removed` block.

To stop managing something without destroying it, change what happens to a
resource you have stopped declaring:

```hcl
live {
  estate = "my-estate"

  policy {
    undeclared_tagged = "untag"
  }
}
```

`untag` removes this estate's marker and leaves the resource running. `keep`
leaves both the marker and the resource alone.

### Two cases where removal leaves an orphan

These are standing limits, not races, and the plan names them every time.

**Types that carry no tags at all.** `aws_route`,
`aws_route_table_association`, `aws_s3_bucket_policy`,
`aws_s3_bucket_versioning`, `aws_iam_role_policy`, `aws_kms_alias`,
`aws_route53_record` and others like them have nowhere to put a marker, so
deleting the block removes the only record of which live resource it was.
Destroy the resource before removing its block, or delete it out of band. The
set is determined at runtime from each type's provider schema rather than being
a fixed list.

**Types outside the admission table.** A live resource carrying this estate's
markers at an unadmitted type is invisible to the removal sweep, because the
sweep is defined over the admission table.

Both appear in every plan under "Not swept for removal". Types a provider
simply cannot list or tag are reported by count, since that is true of every
run; pass `-verbose` to itemise them. A list call that actually failed during
this run is itemised every time.

## Choosing what happens to each kind of resource

Every resource choudoufu sees falls into one of four situations, decided by two
questions: does your configuration declare it, and does it carry this estate's
marker? The `policy` block sets what happens in each.

With no `policy` block you get the defaults below, which are exactly today's
behaviour.

| The situation you are in | Setting | Default | What the default does |
|---|---|---|---|
| You declare it, and it carries your marker. The ordinary case. | `declared_tagged` | `converge` | Plans and applies it against your configuration, like any resource. |
| You declare it, but no live resource carries your marker for it. | `declared_untagged` | `refuse` | Declines to touch it until you adopt it. |
| **You removed it from your configuration, and it still carries your marker.** | `undeclared_tagged` | **`delete`** | **Destroys it on the next plan.** |
| It carries no marker, and you never declared it. Somebody else's. | `undeclared_untagged` | `keep` | Leaves it alone. |

:::warning
The third row is the one to know before you delete a resource block. Removing
the block does not mean "stop managing this", it means "destroy this" — which
is also what upstream does without a `removed` block. Set `undeclared_tagged`
to `untag` or `keep` first if the resource should survive.
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

Combinations with no coherent meaning are refused at lint rather than left to
surprise you. You cannot `adopt` something carrying neither a declaration nor a
marker, and you cannot `delete` something your configuration still declares.

### Reconciling a whole account

`undeclared_untagged = "delete"` is the setting that destroys resources your
configuration has never mentioned. It requires a `scope` block, and it is the
only setting that does.

Re-read the two orphan cases above before enabling it. The sweep cannot see
every resource in the account, so a clean reconciliation does not mean the
account is clean.

## Effects the cloud cannot tell you about

A database migration, a script, a one-shot API call: nothing in the live system
records that it happened, so there is no marker to read back.

`null_resource`, `terraform_data`, `time_*` and non-secret `random_*` work as
soon as the `live` block declares a `record_store`:

```hcl
live {
  estate = "my-estate"

  record_store "ssm" {}
}
```

The label picks the backend: `local`, `ssm` or `s3`. Those resources then run
through the stock provider lifecycle exactly as upstream.

[Where things are stored](storage.html) covers the backends, what the records
hold, and why a receipt is a different thing that must not go in there.

## Running this with other people

There is no lock. Ownership lives on the resources, so there is no shared
file for a lock to protect, and the micro-state records use conditional writes
instead (see below). Two simultaneous applies against one estate resolve one of
four ways:

| Race | Outcome |
|---|---|
| Two creates of the same client-named resource | The cloud's uniqueness constraint rejects the second. The loser re-plans, binds to the winner's resource, and comes back clean. |
| Two creates of the same server-assigned resource | Both are created. The next plan reports a marker collision naming both live IDs and refuses rather than guessing. A human deletes one. |
| Divergent in-place updates | Last writer wins at the API. The next plan reads the live system and converges. |
| An update racing a destroy | The loser gets not-found, re-plans, and converges. |

No race orphans a resource silently. Each case is either a clean re-plan or a
named collision.

Compare that with a backend whose lock fails or was never configured: the last state write wins and the loser's resource is
silently dropped from every future plan. A crash mid-apply is the same story,
lock or no lock, because a resource created but not yet recorded is orphaned
either way. Under markers the tag rode the create call itself, so the resource
is discoverable and there is nothing to unlock or recover.

None of that is an argument for applying concurrently. Serialize applies in CI,
which is where the real mutex has always been.

## Sharing values between estates

There is no remote state to read. `live/OUTPUTS.md` covers the cross-estate
pattern, and `data "terraform_remote_state"` is refused.

## Plan, review, apply

Saved plan files are refused, so the CI pattern of planning in a PR and
applying exactly that artifact has no direct equivalent yet. Ordinary `apply`
re-plans and re-confirms against the live system, which is the honest behaviour,
but nothing today detects that the world moved between review and apply.

Tying a reviewed plan to the apply that follows is
[#74](https://github.com/INTENTIUS/choudoufu/issues/74).
