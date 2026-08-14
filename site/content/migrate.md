# Migrate an existing estate

This is the path most people arrive on: an OpenTofu configuration that already
manages live AWS resources, and a state file you would like to stop having.

Deleting the state file is the easy half. Binding the live resources to your
configuration is the half that needs attention, because it does not happen
automatically and the failure mode is quiet.

:::warning
Adding a `live` block to a configuration that already manages live resources
does not bind them. A resource with no ownership marker is not yours yet, so
the first plan reads it as absent and proposes creating a second one beside it.
Applying that plan does not fail. It creates the duplicate.

Run `choudoufu plan` and read the `Adoptable` and `Unowned` sections before you
apply anything.
:::

## What binds on its own, and what does not

Three groups, and which one a resource falls into decides how much work it is.

**Already marked.** A resource carrying this estate's `tofu-estate` and
`tofu-address` tags binds on the first plan with no action from you. If you are
arriving from `choudoufu live-import`, this is everything.

**Offered for adoption.** For a resource whose identity AWS assigned (a VPC, a
subnet, a security group), the configuration holds nothing that identifies the
live object, so the marker is the only way back to it. The plan can still offer
a match when the configuration content is distinctive enough to be compared.
The classifier does that for these types:

| Type | Matched on |
|---|---|
| `aws_security_group` | `name` |
| `aws_vpc` | `cidr_block` |
| `aws_subnet` | `cidr_block` and `availability_zone` |
| `aws_route53_zone` | `name` |
| `aws_lb` | `name` |
| `aws_lb_target_group` | `name` |
| `aws_sns_topic` | `name` |
| `aws_launch_template` | `name` |
| `aws_sfn_state_machine` | `name` |
| `aws_acm_certificate` | `domain_name` |

`internal/live/foreign/classify.go` is what settles this list; check it there
before relying on this table.

**Adopted by hand.** Everything else with an ownership marker to write.
`aws_route_table`, `aws_internet_gateway`, `aws_kms_key` and `aws_lb_listener`
are server-assigned too, but nothing in their configuration tells one from
another, so the classifier can never offer them. A route table is "the one
attached to this VPC"; a listener is a port and a protocol on a load balancer
named only by a live ARN. Write their markers yourself.

`aws_eip` is bound by slot marker, so a pre-existing unmarked EIP is never
offered. The first apply gives it a fresh slot instead of recognising the old
one. The same applies to every `count` or `for_each` instance of any type:
content matching only considers instances still waiting to be found with no
index or key. If a specific instance must survive rather than be duplicated,
hand-write its markers before the first apply.

## The loop

1. **Add the block.** Put `live { estate = "..." }` in `terraform`, remove any
   `backend` or `cloud` block, and delete the state file. The two blocks are
   refused alongside a `live` block, so this is not optional.
2. **Plan.** `choudoufu plan` runs discovery and prints an `Adoptable` section
   above the ordinary plan, one entry per live resource that matches a declared
   block on everything discovery can compare but carries no marker yet.
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
   provider block or the environment supplies them, and the write lands in the
   same region and on the same endpoint the plan just listed instead of
   wherever your AWS CLI profile points. Every value is shell-quoted, so a
   `for_each` key containing a space or a bracket survives the paste.

   The printed one-liner covers the types tagged through `ec2 create-tags`. An
   `aws_route53_zone`, `aws_lb`, `aws_lb_target_group` or `aws_sns_topic`
   candidate is offered with its marker pair and no command, because each of
   those services has its own tagging call: `route53
   change-tags-for-resource`, `elbv2 add-tags`, `sns tag-resource`. Write the
   same two tags with the call for that service.
5. **Plan again.** Every adopted resource reads back its own markers and
   reports no changes.

There is no `choudoufu adopt` command, and there does not need to be. Two tags
is the whole contract (`live/MARKERS.md`), so any tool that can write two tags
can adopt a resource.

## Client-named resources, and the `Unowned` section

A client-named type carries its identity in the configuration already: an S3
bucket's name, an IAM role's name, a log group's name. There is no discovery
step to skip, so it is tempting to assume the resource at your declared name is
yours.

It is not treated that way. A live resource at a declared client name that does
not carry this estate's `tofu-estate` marker is refused from entering prior
state. The plan proposes creating the resource your configuration declares,
which the cloud will reject for name-unique types while the unmarked one holds
the name, and the refusal is printed as an omission tagged `[UNOWNED]`.

Those refusals also gather into a rendered `Unowned` section, one entry per
live resource:

- **`[ADOPTABLE]`** shows the two tag values that claim it for this estate,
  ready to copy.
- **`[IN_THE_WAY]`** belongs to another estate, or cannot be checked. It only
  blocks the create the plan proposes.

Adoption here is the same deliberate tag write as the server-assigned path.
Read the entry, run the tag write it names, plan again.

If you would rather not do this one resource at a time, the ownership policy
matrix can do it for you: `policy { declared_untagged = "adopt" }` in the `live`
block adopts that whole quadrant. Read what the other three quadrants do before
setting it.

## What has no adoption path

`aws_route`, `aws_route_table_association` and `aws_iam_role_policy_attachment`
carry no tags, so there is nowhere to put a marker. Their identity is a
composite of their already-admitted parents, and once the parents are adopted
these bind on the next plan with no separate step.

A resource whose type is outside the admission table has no adoption path at
all. Hand-stamping markers onto it does not help, because nothing sweeps for a
type this configuration cannot declare. [Will my config
work](compatibility.html) covers how to find out which of your types those are.

## Moving a large estate in one go

`choudoufu live-import` reads an existing state file once, verifies each entry
against the live system, and stamps markers on what verifies. It is the bulk
path, and it leaves you in the "already marked" group above.

## If you are used to import, moved and removed

Upstream needs those three constructs because state is authoritative and each
one edits that record surgically. `import` writes an entry, `moved` rewrites an
entry's address, `removed` with `lifecycle { destroy = false }` drops an entry
without touching the object.

With no file of record, there is nothing to edit. Adopting is the marker stamp
above, or `live-import` in bulk. Renaming is `choudoufu live-mv <old> <new>`,
which rewrites the `tofu-address` tag in place and leaves unadopted resources
alone. `moved` blocks are refused by lint.

Forgetting without destroying is the one place the parallel is not exact by
default. Deleting a resource block leaves its marker on the live object, and
the `undeclared_tagged` quadrant defaults to `delete`, so the next plan sweeps
it. That matches what upstream does without a `removed` block.

The equivalent of `removed` with `destroy = false` is to set that quadrant
instead of accepting the default:

```hcl
live {
  estate = "my-estate"

  policy {
    undeclared_tagged = "untag"   # stop managing it, leave it running
  }
}
```

`untag` removes this estate's marker and leaves the resource alone. `keep`
leaves both the marker and the resource untouched. [Day-2
operations](day2.html) covers the rest of the matrix.

## Getting back out

Worth knowing before you start, because it costs nothing to keep the door open.

The markers are plain tags and the resources are ordinary resources. Remove the
`live` block, restore a `backend` if you want one, and import the resources
into a fresh state file with stock tooling. The marker tags can stay, since
stock OpenTofu ignores them, or you can delete them with your cloud CLI.
