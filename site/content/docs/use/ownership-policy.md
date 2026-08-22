---
title: "The ownership policy matrix"
weight: 9
---

# The ownership policy matrix

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

{{% hint warning %}}
The third row is the one to know before deleting a resource block. Removing the
block does not mean "stop managing this", it means "destroy this", which is
also what upstream does without a `removed` block. Set `undeclared_tagged` to
`untag` or `keep` first if the resource should survive. [How to stop managing
or destroy a resource]({{< relref "/docs/use/remove-a-resource" >}}) walks
through it.
{{% /hint %}}

## What each setting accepts

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

## Reconciling a whole account

`undeclared_untagged = "delete"` destroys resources your configuration has
never mentioned. It requires a `scope` block, the only setting that does.

Re-read the two orphan cases in [How to stop managing or destroy a
resource]({{< relref "/docs/use/remove-a-resource" >}}) before enabling it.
The sweep cannot see every resource in the account, so a clean reconciliation
does not mean a clean account.
