---
title: "Migrate an existing estate"
weight: 3
---

# Migrate an existing estate

Most people arrive here, with an OpenTofu configuration already managing live
AWS resources.

Migrating means binding those resources to your configuration, one marker at a
time, until each carries its own ownership record. It does not happen
automatically and the failure mode is quiet.

{{% hint warning %}}
Turning on live markers does not bind resources you already manage. A resource
with no marker is not yours yet, so the first plan reads it as absent and
proposes a second one beside it. Applying that plan does not fail. It creates
the duplicate.

Run `choudoufu plan` and read the `Adoptable` and `Unowned` sections before
applying anything.
{{% /hint %}}

## What binds on its own, and what does not

Three groups. Which one a resource falls into decides the work.

**Already marked.** A resource carrying this estate's `tofu-estate` and
`tofu-address` tags binds on the first plan with no action from you. Arriving
from `choudoufu live-import`, this is everything.

**Offered for adoption.** Where AWS assigned the identity, a VPC, a subnet, a
security group, the configuration holds nothing naming the live object and the
marker is the only way back. The plan still offers a match when configuration
content is distinctive enough to compare, a VPC by `cidr_block`, a security
group by `name`, a subnet by `cidr_block` and `availability_zone`. `matchTable`
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
same holds for every `count` or `for_each` instance of any type, since content
matching only considers instances still waiting with no index or key.
Hand-write markers before the first apply if a specific instance must survive.

## The loop

1. **Add the sidecar.** Create `estate.chdf.hcl` beside the configuration with
   `estate = "..."` as its body, or put `live { estate = "..." }` in
   `terraform`. Either form, not both. Remove any `backend` or `cloud` block
   and delete the state file. Both blocks are refused alongside a live
   configuration.
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
5. **Plan again.** Every adopted resource reads back its own markers and
   reports no changes.

There is no `choudoufu adopt` command and no need for one. Two tags is the
whole contract (`live/MARKERS.md`), so any tool that writes two tags can adopt
a resource.

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

## Moving a large estate in one go

`choudoufu live-import` reads an existing state file once, verifies each entry
against the live system, and stamps markers on what verifies. The bulk path,
leaving you in the "already marked" group above.

## If you are used to import, moved and removed

Upstream needs those three because state is authoritative and each edits that
record surgically. `import` writes an entry, `moved` rewrites an address,
`removed` with `lifecycle { destroy = false }` drops an entry without touching
the object.

With no file of record there is nothing to edit. Adopting is the marker stamp
above, or `live-import` in bulk. Renaming is `choudoufu live-mv <old> <new>`,
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
