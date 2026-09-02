---
title: "Migrate an existing estate"
weight: 3
---

# Migrate an existing estate

Most people arrive here, with an OpenTofu configuration already managing live
AWS resources.

Migrating means binding those resources to your configuration, one marker at a
time, until each carries its own ownership record. Nothing does this
automatically, and the failure mode is quiet.

{{% hint warning %}}
Turning on live markers does not bind resources you already manage. A resource
with no marker is not yours yet, so the first plan reads it as absent and
proposes a second one beside it. Applying that plan succeeds, and creates the
duplicate.

Run `choudoufu plan` and read the `Adoptable` and `Unowned` sections before
applying anything.
{{% /hint %}}

## Keep the state file until the migration is done

If this estate still has a `terraform.tfstate`, do not delete it yet. It is the
only input `choudoufu live-import` has, and there is no flag that supplies the
addresses another way. Run it against a file that is gone and the command stops
before it reaches the live system at all:

```
Error: Cannot read the state file

Error loading statefile: open terraform.tfstate: no such file or directory
```

A parsed state file is a precondition of ratification itself, not just of
opening the file: `liveimport.Ratify` refuses a nil state with `No state to
ratify`
([`internal/live/liveimport/ratify.go`](https://github.com/INTENTIUS/choudoufu/blob/main/internal/live/liveimport/ratify.go)).
So deleting the file takes [the bulk path](#moving-a-large-estate-in-one-go)
with it, leaving the plan-based loop and its `count`/`for_each` blind spot as
the only way through.

Keeping it costs nothing while you decide. Marker mode does not read a state
file, refuse one, or mention one, so a `terraform.tfstate` sitting beside a
live configuration changes no behaviour at all:
[What you set up by hand]({{< relref "/docs/use/setup#deleting-the-state-file-is-not-enforced" >}})
covers why its presence is harmless and believing it still counts for something
is not.

## What binds on its own, and what does not

Three groups. Which one a resource falls into decides the work.

**Already marked.** A resource carrying this estate's `tofu-estate` and
`tofu-address` tags binds on the first plan with no action from you. Arriving
from `choudoufu live-import`, this is everything.

**Offered for adoption.** Where AWS assigned the identity, the configuration
holds nothing naming the live object and the marker is the only way back. A
VPC, a subnet and a security group are the common cases. The plan still
offers a match when configuration content is distinctive enough to compare: a
VPC by `cidr_block`, a security group by `name`, a subnet by `cidr_block` and
`availability_zone`. `matchTable`
in
[`internal/live/foreign/classify.go`](https://github.com/INTENTIUS/choudoufu/blob/main/internal/live/foreign/classify.go)
holds the full list, though you do not need it in advance. The plan's
`Adoptable` section names each match and what it matched on.

**Adopted by hand.** Everything else with a marker to write. `aws_route_table`,
`aws_internet_gateway`, `aws_kms_key` and `aws_lb_listener` are server-assigned
too, but nothing in their configuration tells one from another, so the
classifier can never offer them. A route table is "the one attached to this
VPC". A listener is a port and protocol on a load balancer named only by a live
ARN. Write their markers yourself.

`aws_eip` binds by slot marker, so a pre-existing unmarked EIP is never offered.
The first apply gives it a fresh slot instead of recognising the old one. The
same holds for every `count` or `for_each` instance of any type: `buildSlots`,
in the same `classify.go` as `matchTable` above, skips any address carrying an
index or a key, so content matching only ever considers instances with
neither.

The blind spot is quiet, which is why it is worth seeing once. Stood up as a
stock estate and then migrated by the plan loop, a five-resource
configuration - `aws_vpc.pool` with `count = 2`, `aws_security_group.svc`
with a two-key `for_each`, and one plain `aws_vpc.solo` - produced exactly one
adoption offer:

```
Adoptable: 1 live resource matches a declared resource
Plan: 5 to add, 0 to change, 0 to destroy.
```

All five already existed. Four were invisible to the offer, and the plan
proposed creating all five. The four do appear, in the `Not read from the live
system` section, each tagged `[NEEDS_DISCOVERY]` with the explanation "Marker
discovery will find it; until then the plan will propose creating it." For an
expanded instance carrying no marker that sentence does not hold: discovery
has nothing to find, because the only thing that would bind the instance is
the marker that is not there yet. Read `[NEEDS_DISCOVERY]` on an indexed or
keyed address as "you have work to do here", not as reassurance.

**That limit belongs to the loop below, not to expanded resources.** It is a
property of matching configuration content against listed objects, which is
what `choudoufu plan` does when it has nothing else to go on. `choudoufu
live-import` has something else to go on: an existing state file already names
every instance, index and key included, so it stamps the address it reads
rather than trying to recognise the object. If your estate is expanded and you
still have its state file, take [the bulk path](#moving-a-large-estate-in-one-go)
and hand-write nothing.

Hand-write markers before the first apply only if you are working the loop
below and a specific instance must survive.

## Moving a large estate in one go

Try this before the loop. `choudoufu live-import` reads an existing state file
once, verifies each entry against the live system, and stamps markers on what
verifies, leaving you in the "already marked" group above with nothing to
hand-write.

{{% hint info %}}
`choudoufu live-import -help` opens with the word `EXPERIMENTAL.`, and the
command list carries `(experimental)` beside the one-line synopsis. Read that
as a statement about the command's surface. It says nothing about what gets
written: the markers it stamps are the same `tofu-estate` and `tofu-address`
pair every other adoption path writes, and the state file is opened once,
read-only, and never modified.
{{% /hint %}}

Two runs, the same two flags:

```
$ choudoufu live-import -state=terraform.tfstate -estate=my-estate
$ choudoufu live-import -state=terraform.tfstate -estate=my-estate -approve
```

The first writes nothing and prints a ratification report. The second stamps
every entry the report showed as `VERIFIED` or `DRIFTED`. `MISSING`,
`UNTAGGABLE` and `UNADMITTED_TYPE` are never stamped, and the report says why
for each one. `-estate` is required here, unlike `live-plan` and `live-mv`,
because there is no configuration to derive the name from.

Run it in a directory `choudoufu init` has already prepared, beside the same
provider configuration the state was last applied with. Identity comes out of
the state file rather than out of a resource block, but reaching the live
system still needs a configured provider.

Because the file is only ever read, there is no rollback step to plan for. A
marker write is additive, and the state file is exactly as usable by stock
OpenTofu after a stamp as it was before.

It is also the path that answers the `count`/`for_each` limit above, and the
one to reach for on an estate that has grown expanded. A generated 79-resource
estate with `count`, `for_each` and module-nested expansion present, applied by
stock `terraform` against a local emulator and then migrated (measured in
[#575](https://github.com/INTENTIUS/choudoufu/pull/575), answering
[#574](https://github.com/INTENTIUS/choudoufu/issues/574)):

| | Count |
|---|---|
| Verified or drifted, so stamped from state | 38 of 79 |
| Untaggable, so no marker to write; identity composes from a stamped parent | 41 of 79 |
| **Needed a hand-typed marker** | **0** |

Every `count` instance, every `for_each`'d record, and every module-nested
`count` instance took its own correctly interpolated marker, down to
`module.team_pod["pod-a"].aws_iam_role.pod_role[0]`. The plan-based loop's
blind spot never fires, because nothing on this path matches content.

Two bounds on that measurement, both worth knowing before you rely on it. The
ratio was taken at one scale, against a generated estate rather than somebody's
real one. And stamping is one tag-write round trip per resource, so it is
linear at roughly 1.3 to 1.4 seconds per stamped resource against a local
emulator (issue #566). Reading the state file and reporting what would be
stamped is separate, read-only, and near flat at about 1.5 seconds either way.

## The loop

For an estate with no state file left, or one small enough that a few tag
writes are less trouble than a bulk run. Its limitation is the one above: an
address carrying a `count` index or a `for_each` key is never offered, so if
your estate is expanded and its state file still exists, use `live-import`
instead.

1. **Add the sidecar.** Create `estate.chdf.hcl` beside the configuration with
   `estate = "..."` as its body, or put `live { estate = "..." }` in
   `terraform`. Either form, not both. Remove any `backend` or `cloud` block:
   both are refused alongside a live configuration, and `init` says so at the
   offending block's own line.

   Leave `terraform.tfstate` alone. It is inert here and it is `live-import`'s
   only input, so deleting it now forecloses the bulk path and buys nothing.
2. **Plan.** `choudoufu plan` runs discovery and prints an `Adoptable` section
   above the ordinary plan, one entry per live resource matching a declared
   block on everything discovery can compare but carrying no marker yet.
3. **Read it.** Each entry names the live resource, what it matched on, and a
   ready-to-run adoption command. This is the step the warning exists for.
4. **Run the adoption commands.** For a type the classifier can offer, the hint
   is the tag write itself.

   ```
   aws ec2 create-tags --resources 'vpc-0123456789abcdef0' \
     --tags 'Key=tofu-estate,Value=my-estate' 'Key=tofu-address,Value=aws_vpc.main' \
     --region 'us-east-1'
   ```

   Paste it as printed. It is built from choudoufu's own provider
   configuration, so it carries `--region` and `--endpoint-url` whenever the
   provider block or environment supplies them. The write lands on the same
   region and endpoint the plan just listed rather than wherever your AWS CLI
   profile points. Every value is shell-quoted, so a `for_each` key containing
   a space or bracket survives the paste.

   The printed one-liner covers types tagged through `ec2 create-tags`. An
   `aws_route53_zone`, `aws_lb`, `aws_lb_target_group` or `aws_sns_topic`
   candidate comes with its marker pair and no command, because each service
   has its own tagging call. Write the same two tags with that call.

   Writing one by hand for an expanded instance, which the loop never offers,
   needs the escaping rule from `live/MARKERS.md`: `[` becomes `:`, and `]`
   and `"` are deleted. So `aws_vpc.pool[0]` is written `aws_vpc.pool:0`, and
   `aws_security_group.svc["alpha"]` is written
   `aws_security_group.svc:alpha`.

   ```
   aws ec2 create-tags --resources 'vpc-9a5e998c' \
     --tags 'Key=tofu-estate,Value=my-estate' 'Key=tofu-address,Value=aws_vpc.pool:0'
   ```

   Two tags are enough even for a `count` instance. `tofu-slot` binds a
   `count` instance where it is present, but a hand-written pair without it
   still binds on `tofu-address`, and the next plan proposes adding the slot
   as an ordinary in-place tags update. Writing the pair and letting the plan
   fill in the slot is correct.
5. **Plan again.** Every adopted resource reads back its own markers and
   reports no changes.
6. **Turn the live block on.** This is the migration's end state: with
   the block in the configuration, the ordinary `choudoufu plan` and
   `apply` run the live backend, and `live-plan` retires. Do it before
   any plain plan or apply - without the block those are stock mode
   (the fallback), and stock mode with no state file proposes
   rebuilding the whole estate. A stock-mode plan that would create
   marker-stamped resources from an empty state now warns and names
   this exact situation.
7. **Delete the state file, if you want it gone.** Not before here, and not
   required at all. Nothing reads or refuses the file itself, and nothing
   checks that you removed it, so this is housekeeping rather than a
   migration step. What IS refused is different and comes later: a run
   without the live block whose plan would strip this estate's markers -
   see [Leaving, and the guard](#leaving-and-the-guard-that-makes-it-deliberate).

There is no `choudoufu adopt` command and no need for one. Two tags is the
whole contract (`live/MARKERS.md`), so any tool that writes two tags can adopt
a resource.

## Leaving, and the guard that makes it deliberate

Leaving is supported and cheap, and the smoke proves it: the
[roundtrip claim]({{< relref "/docs/claims#claim-6-the-roundtrip---one-command-in-one-file-out" >}})
adopts a stock estate, operates it, and hands it back. The exit is one
file and one edit: the cache copied to `terraform.tfstate`, the live
block removed. Stock's first plan back proposes exactly one kind of
change, removing the two marker tags. Run that leg with stock OpenTofu
and you are done.

Run it with choudoufu instead and one guard stands in the way, on
purpose. A choudoufu run WITHOUT a live block behaves as stock does,
with a single measured exception: a plan that would strip a migrated
estate's ownership markers is computed, rendered in full, and then
refused with `Plan would remove this estate's ownership markers`. The
refusal exists for the accidental case - a live block lost to a bad
merge or a wrong directory reads on screen as routine tag drift, and
applying it un-migrates the estate silently. A deliberate exit says
which estate it means:

```
CHOUDOUFU_UNMIGRATE=my-estate choudoufu apply
```

The variable takes the estate's name (or several, comma-separated)
rather than an on/off value, so a setting exported once in CI approves
the estate the operator was looking at and nothing else. With it set,
the same plan carries a warning headline instead of the refusal.

That is the entire boundary. An unmigrated estate never meets the
guard, which is what keeps the
[stock-when-you-need-it claim]({{< relref "/docs/claims#claim-8-stock-when-you-need-it" >}})'s
measured parity intact: no live block means stock behavior, and the one
divergence is this refusal, on a migrated estate, guarding the
migration you already performed.

## Client-named resources, and the `Unowned` section

A client-named type already carries its identity in the configuration, an S3
bucket name, an IAM role name, a log group name. With no discovery step to
skip, it is tempting to assume the resource at your declared name is yours.

It is not treated that way. A live resource at a declared client name without
this estate's `tofu-estate` marker is refused from prior state. The plan
proposes creating what your configuration declares, which the cloud rejects for
name-unique types while the unmarked one holds the name, and the refusal prints
as an omission tagged `[UNOWNED]`.

Those refusals gather into an `Unowned` section, one entry per live resource.

- **`[ADOPTABLE]`** shows the two tag values that claim it for this estate,
  ready to copy.
- **`[IN_THE_WAY]`** belongs to another estate, or cannot be checked. It only
  blocks the create the plan proposes.

Adoption here is the same deliberate tag write as the server-assigned path.
Read the entry, run the tag write it names, plan again.

To avoid doing this one resource at a time, set
`policy { declared_untagged = "adopt" }` in the live configuration. It adopts
every resource in that situation at once. Read what the other three settings do
first.

## What has no adoption path

`aws_route`, `aws_route_table_association` and `aws_iam_role_policy_attachment`
carry no tags, so a marker has nowhere to go. Their identity composes from
already-admitted parents, and they bind on the next plan once the parents are
adopted.

A type outside the admission table has no adoption path at all. Hand-stamping
markers does not help, because nothing sweeps for a type this configuration
cannot declare. [Compatibility reference]({{< relref "/docs/use/compatibility" >}}) covers finding yours.

## If you are used to import, moved and removed

Stock needs those three because state is authoritative and each edits that
record surgically. `import` writes an entry, `moved` rewrites an address,
`removed` with `lifecycle { destroy = false }` drops an entry without touching
the object.

Here the record that decides ownership is the marker on the resource, so those
three have nothing to edit: any tool that can write two tags does the work
they existed for. Adopting is the marker stamp above, or `live-import` in
bulk. Renaming is `choudoufu live-mv <old> <new>`,
rewriting the `tofu-address` tag in place and leaving unadopted resources
alone. `moved` blocks are refused by lint.

Forgetting without destroying is the one inexact parallel. Deleting a resource
block leaves its marker on the live object, and `undeclared_tagged` defaults to
`delete`, so the next plan destroys it. That matches upstream without a
`removed` block.

For the equivalent of `removed` with `destroy = false`, set the policy.

```hcl
# estate.chdf.hcl
estate = "my-estate"

policy {
  undeclared_tagged = "untag"   # stop managing it, leave it running
}
```

`untag` removes this estate's marker and leaves the resource alone. `keep`
leaves both untouched. [The ownership policy matrix]({{< relref "/docs/use/ownership-policy" >}}) covers the rest of the
matrix.

## Getting back out

Know this before you start. Keeping the door open costs nothing.

Markers are plain tags and the resources are ordinary resources. Remove the
live configuration, restore a `backend` if you want one, and import the
resources into a fresh state file with stock tooling. Marker tags can stay,
since stock OpenTofu ignores them, or delete them with your cloud CLI.
