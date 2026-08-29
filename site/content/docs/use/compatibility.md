---
title: "Compatibility reference"
weight: 1
---

# Compatibility reference

What choudoufu admits and refuses: the provider and resource types, how a
configuration must be written, and how it may be run.

This is the enumerated list. For why static evaluability is the rule behind
most of it, see [Identity]({{< relref "/docs/model/identity" >}}). To check
your own configuration against this list, see [How to check a configuration
before migrating]({{< relref "/docs/use/check-a-config" >}}).

## Your provider

AWS only. Every `google_*`, `azurerm_*`, `kubernetes_*` and `helm_*` resource
is refused. There is no second cloud on the roadmap
([#5](https://github.com/INTENTIUS/choudoufu/issues/5)).

## Your resource types

A type is admitted when its identity recovers from the live system, through the
admission table, the provider's own identity schema, or the way your
configuration names it.

Common types are largely covered. The connective tissue that long was not,
`aws_ecs_service`, `aws_lambda_permission`, `aws_cloudwatch_event_rule` and
`aws_cloudwatch_event_target`, carries full table rows since the 2026-08-15
ratification batch. API Gateway assembly is the named gap.
`aws_api_gateway_deployment` and `aws_api_gateway_resource` reach none of the
three admission paths.

`live/LIMITATIONS.md` carries the per-type detail.

### Readiness tiers

Admission is not the whole story. `rfc/20260828-readiness-tiers.md` names
four tiers by what recovers a type's identity when the record store, the
state file, or the tool itself is gone: marker-carried, declaration-carried,
record-carried, and excluded by design. `live/readiness.json` assigns every
provider type exactly one tier and one of six statuses; the table below is
generated from it. `live/COVERAGE.md` carries the same table with more
context.

<!-- readiness-gen:begin readiness-tiers -->
| Tier | in-contract | pending-ratification | needs-separator | needs-evidence | pending-mechanism | excluded | Total |
|---|---|---|---|---|---|---|---|
| marker-carried | 682 | 161 | 1 | 2 | 0 | 0 | 846 |
| declaration-carried | 341 | 37 | 0 | 1 | 0 | 0 | 379 |
| record-carried | 99 | 294 | 3 | 16 | 60 | 0 | 472 |
| excluded by design | 0 | 0 | 0 | 0 | 0 | 2 | 2 |
| **Total** | 1122 | 492 | 4 | 19 | 60 | 2 | 1699 |
<!-- readiness-gen:end readiness-tiers -->

## How your configuration is written

This is the group that catches people. Every row below is a different way of
asking an address or an identity to resolve before a provider can answer it;
[Identity]({{< relref "/docs/model/identity" >}}) states the rule in full.

### Expansion

| Written like this | Why it stops |
|---|---|
| `count = length(data.aws_availability_zones.all.names)` | count must be known without calling a provider |
| `for_each = toset(data.aws_subnets.x.ids)` | same, for for_each |
| `for_each = { for s in aws_subnet.app : s.id => s }` | reads another resource's attributes |
| `for_each` over a `count`-expanded resource | count produces indices, not keys |
| `for_each` over a resource in another module | resolution does not cross that boundary |
| `count.index` anywhere in a resource body | the marker would be a constant on every instance |

### `for_each` keys

An instance key becomes part of the `tofu-address` marker, so it must survive
being written to a tag and read back. Permitted are letters, numbers, space,
and `+ - = _ / @`.

The marker uses `.` to separate address segments and `:` to introduce an
instance key, so both are excluded even though AWS allows them in a tag value.
A key containing either produces a marker that cannot be split back into its
address.

That rules out keying on CIDR blocks, hostnames, ARNs, or anything dotted, a
very common idiom.

### Identity arguments

Where the name in configuration is the identity, that argument has the same
static requirement.

| Written like this | Why it stops |
|---|---|
| `bucket = data.aws_s3_bucket.x.id` | read from a data source |
| `name = module.naming.prefix` | read from a module output |
| `name = lower("${var.env}-app")` | reached through a function or operator |
| `bucket_prefix = "app-"` with no `bucket` | the identity argument is not set |
| `name = var.secret_name` where the variable is `sensitive` | identities appear in logs and plan output |
| `name = "app-${uuid()}"` | a different value on every evaluation |
| reading `.arn` where the table expects `name` | that attribute is not part of the identity |

`internal/live/identity/refusals.go` registers each refusal with a one-line
description. It is the list the code enforces.

## Your modules

A marker binds to a configuration address and stays correct as long as that
address stays stable. That one test decides which module forms work.

**A plain `module "app" {}` call** with neither `count` nor `for_each` is
traversed like the root module. A resource inside binds by its module-qualified
address, `module.app.aws_x.y` or `module.a.module.b.aws_x.y` at any depth.

**`for_each` on a module call** works when every key is evaluable from
configuration alone. A key you chose does not move when a sibling appears or
goes, so `module.app["prod"]` survives whatever happens to
`module.app["staging"]`. Keys follow the same marker-safe character and length
rules as a resource's own `for_each` key, because the key becomes part of every
address inside the module.

**`count` on a module call is refused permanently.** Rewrite as a keyed
`for_each` over stable names, move the resources to the root module, or give
the module its own estate. [Identity]({{< relref "/docs/model/identity" >}})
explains why no future work closes this.

A resource inside a `for_each`'d module needs its own marker built by hand
from the module's own key; see [How to write markers inside a for_each'd
module]({{< relref "/docs/use/keyed-modules" >}}).

### Crossing a module boundary

A marker carries the full module-qualified address, escaped into a tag value
per `live/MARKERS.md`, where `[` becomes `:` and `]` and `"` are dropped.
`choudoufu live-mv` handles those like any root address, so flattening a module
into the root, moving a resource into a module, and renaming across two module
instances are ordinary renames. Only a step carrying a `count` key stays
refused.

`choudoufu live-import` is narrower. It ratifies root-module state entries only
and reports how many non-root module instances it skipped. A module-tree estate
adopts by planning with a `live` block added, the ordinary path on
[Migrate an existing estate]({{< relref "/docs/use/migrate" >}}).

## Your accounts and regions

An estate can span provider configurations. One `provider "aws"` block per
account or region, each with its own `assume_role`, resources pinned with the
`provider` meta-argument. Admitted, and proven end to end against the emulator.

One bound. Resources needing marker discovery must share a single provider
configuration. The line runs through how identity is recovered, not through
which account a resource sits in.

**Client-named types span freely.** An S3 bucket, an IAM role, a log group.
Their identity is already in your code, so nothing goes looking for them and
any provider configuration can manage them.

**Server-assigned types share one.** A VPC, a subnet, a security group, a KMS
key. AWS assigns their identity and choudoufu recovers it by reading markers
back, so a list issued against the wrong account or region reports the estate
as missing rather than unreachable. Spanning configurations with these is
refused, naming the configurations involved.

Split the configuration so discovery-needing resources share one provider
configuration, and run them separately. `-target` does not help, because the
check runs over the whole configuration during discovery, before any target
filter applies.

This is where the mode stands today, not a permanent boundary. The multi-pass
machinery already exists.

Two consequences follow.

A `providers = { aws = aws.other }` mapping on a module call is refused
permanently. Live mode does not read that mapping, so resources would be read,
written and swept somewhere other than where you asked, with nothing in the
plan to show it.

Across provider configurations, an adoption hint's `--region` and
`--endpoint-url` can name the wrong region for a resource found under a
different configuration. The printed command is wrong, not the plan.
Check the region before pasting it.

## How you run it

An acceptable configuration can still be refused by how it is invoked.

- `backend "s3" {}` and `cloud {}` are refused. There is no state to store.
- Any workspace other than `default`, and `workspace new` / `workspace select`.
- Every `tofu state` subcommand, including read-only `state list` and
  `state show`.
- `import`, `refresh`, `taint`, `untaint`.
- `-out` to save a plan, and `apply <planfile>`. **This is how most CI runs
  Terraform**, so check it first. Ordinary `apply` re-plans and re-confirms.
  [#74](https://github.com/INTENTIUS/choudoufu/issues/74)'s RFC settles the
  design for tying a reviewed plan to its apply. Not implemented yet.
- `-json` and `-json-into`.
- `-destroy` and `-refresh-only`.
- `-state`, `-state-out`, `-backup`, `-generate-config-out`.

## Constructs refused outright

- `provisioner "local-exec"` and `"remote-exec"`, and resource-level
  `connection` blocks.
- `data "terraform_remote_state"`. Use `live/OUTPUTS.md`'s cross-estate
  pattern instead.
- `moved` blocks. Renaming is `choudoufu live-mv`.
- `random_password`, `random_bytes` and every `tls_*` resource. Their output
  is secret material only the state file ever remembered. A record holding a
  secret would be a state file with extra steps. Permanent.
- `local_file` and other `local_*` resources.
- `module { count = ... }`. Permanent. A `for_each` on a module call works when
  its keys are static.

## Effects do work

`null_resource`, `terraform_data`, `time_*` and non-secret `random_*` are
admitted. They run the stock provider lifecycle exactly as upstream, against a
record in the estate's record store. An older refusal message called them
unsupported. They are not.

Nothing has to be turned on for that: an estate with no `record_store` block
gets an implied local one, a `.tofu-records` directory beside the module.
Declare a `record_store` to put the records somewhere a team shares instead.

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "ssm" {}
```

The label picks the backend, one of `local`, `ssm` or `s3`. [Where things are
stored]({{< relref "/docs/use/storage" >}}) has the arguments.

## Two hazards that are now refusals

Both used to fail silently. Lint now refuses them with a message naming the
fix.

**`lifecycle { ignore_changes = [tags] }` would defeat ownership markers.** The
stamp pass adds `tofu-estate` and `tofu-address` to the resource's tags, and a
plan ignoring tag changes discards the markers before they are written. The
resource would never be marked, and the next plan would propose another one.
Lint refuses `ignore_changes = all`, `ignore_changes = [tags]`, and any entry
naming a marker key ([#103](https://github.com/INTENTIUS/choudoufu/issues/103)).
Ignoring a tag key of your own, such as `tags["Owner"]`, stays admitted.

**A module call's `providers` mapping to an aliased configuration is refused.**
Live mode plans a module's resources against the root default provider, so
honouring `providers = { aws = aws.useast1 }` needs a design change. Until it
lands, an estate built that way would plan against the wrong account with no
diagnostic ([#104](https://github.com/INTENTIUS/choudoufu/issues/104)).
`providers = { aws = aws }` is admitted, naming what live mode already does,
and root-level provider aliases work correctly.

A `provider` block inside a child module is refused too. Its resources would
silently be served by the root provider config instead.
[#70](https://github.com/INTENTIUS/choudoufu/issues/70) carries the
measurement, that none of the ten most-installed shared AWS modules declares
one and that upstream calls the pattern legacy. Configure providers at the root
and let modules receive them implicitly.

## Editors and linters

The `estate.chdf.hcl` sidecar exists for this concern
([#72](https://github.com/INTENTIUS/choudoufu/issues/72)). It holds the live
configuration in a file whose extension stock tooling never reads, so every
`.tf` file stays free of non-standard syntax and stock `terraform validate`,
`tflint` and editors keep passing.

The in-`terraform` `live` block is the form that costs you. Stock Terraform and
stock OpenTofu reject a configuration containing one.

```
Error: Unsupported block type

  on main.tf line 6, in terraform:
   6:   live {

Blocks of type "live" are not expected here.
```

Expected. `live` is this fork's addition to the `terraform` block schema and
nothing signals it to a tool that never heard of it. Any tool validating
against upstream's schema behaves the same way, `tflint` included, since it
decodes HCL through OpenTofu's own libraries. Tools that only tokenize HCL,
including most highlighters and formatters, are unaffected.

Teams keeping the in-block form have three options. Run `choudoufu validate`
in CI instead of stock `terraform validate`, keep the `live` block in a small
root module stock tooling never touches, or move its content into the sidecar,
which is one file and zero edited lines. Declaring both forms at once is an
error.

For how the type and refusal counts on this page are measured, and what not
to read into them, see [How the compatibility numbers are
measured]({{< relref "/docs/use/measurement" >}}).
