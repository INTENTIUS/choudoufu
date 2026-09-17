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
| You declare it, and the live resource at that identity carries no estate marker at all. | `declared_untagged` | `refuse` | Declines to touch it until you adopt it. |
| **You removed it from your configuration, and it still carries your marker.** | `undeclared_tagged` | **`delete`** | **Destroys it on the next plan.** |
| It carries no marker, and you never declared it. Somebody else's. | `undeclared_untagged` | `keep` | Leaves it alone. |

An object carrying *another* estate's `tofu-estate` is in none of these four
situations. See [What the matrix does not
govern](#what-the-matrix-does-not-govern) below.

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

## What the matrix does not govern

The word "untagged" in `declared_untagged` means *carries no estate marker at
all*. It does not mean "does not carry mine".

So a live object sitting at an identity you declare while carrying another
estate's `tofu-estate` falls outside the matrix. No verb reaches it: `adopt`
and `converge` will not claim it, and `keep` and `report` do not get to soften
the refusal either. Every plan refuses it by name, quotes the estate it
actually carries, and leaves it alone.

That is deliberate. `tofu-estate` is the whole ownership claim, so admitting
somebody else's object here would have the next apply stamp your marker over
theirs, on the strength of a setting you wrote about unmarked resources.

Two commands still cross that boundary, because with them you are saying which
object you mean:

- `choudoufu live-mv -from-estate=<old> <address> <address>` rewrites one
  object's marker into this estate. [Rename a resource]({{< relref
  "/docs/use/rename-a-resource" >}}) covers it.
- `choudoufu live-import -approve` stamps this estate's markers over the
  resources a state file lists. [Migrate an existing estate]({{< relref
  "/docs/use/migrate" >}}) covers it.

## Reconciling a whole account

`undeclared_untagged = "delete"` destroys resources your configuration has
never mentioned. It requires a `scope` block, the only setting that does.

Re-read the two orphan cases in [How to stop managing or destroy a
resource]({{< relref "/docs/use/remove-a-resource" >}}) before enabling it.
The sweep cannot see every resource in the account, so a clean reconciliation
does not mean a clean account.
