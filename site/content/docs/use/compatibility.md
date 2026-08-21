---
title: "Will my config work?"
weight: 1
---

# Will my config work?

Probably not without changes.

What stops a configuration is how it is written and run, not which resource
types it uses. A `for_each` over a data source, a `count.index` in a resource
name, a `backend "s3"` block, or a CI pipeline that saves a plan file. Run
`choudoufu live-check` for a verdict on your exact code.

## The one rule underneath most of it

Every `count`, every `for_each`, and every identity-bearing argument must be
computable from `var`, `local`, `path` and `terraform` alone, plus functions
over those.

No data sources. No module outputs. No attributes of other resources.

Markers are written before anything is created, and a marker names which
configuration address a live resource belongs to. If the set of instances is
unknowable until a provider has been called, there is no marker to write.

Most refusals below follow from that rule.

## Ask it directly

```
choudoufu live-check ./
```

Point it at any OpenTofu configuration. No `live` block, no cloud calls, no
requirement that the directory has heard of this fork. It prints a verdict,
then every refusal that fired with its site count, the types responsible, and
what to do about each.

Run `choudoufu init` first if you can. With provider schemas available it
judges types from the provider's own identity schema as well as the built-in
table, and admits more. Without them it says the answer is pessimistic.

**It checks two of five stages.** Lint and identity resolution need no
provider, which is what makes the command fast and credential-free. Marker
stamping, discovery and projection need a cloud and go unchecked. A clean
result is necessary, not sufficient. Run a plan against a non-production
account before trusting a migration.

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

## How your configuration is written

This is the group that catches people.

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

**`count` on a module call is refused permanently.** `count` renumbers every
address inside the module on any insertion or removal above the changed index.
Removing element zero turns `module.app[1]` into `module.app[0]`, silently
pointing every marker beneath at the wrong live resource. A marker records an
address, not a position, so no future work closes this. Rewrite as a keyed
`for_each` over stable names, move the resources to the root module, or give
the module its own estate.

### Resources inside a keyed module need hand-written markers

Instances of a `for_each`'d module share one HCL body for `tags`, so no single
literal address is correct for all of them and auto-stamping cannot reach
inside. choudoufu leaves such a resource alone when it already declares `tags`,
and raises a must-stamp error when it declares none and its type needs
discovery.

Thread the module's own `each.key` through and build the address from it.

```hcl
# root module: the call passes its own each.key through
module "wrapped" {
  source   = "./wrapped"
  for_each = toset(["a", "b"])
  key      = each.key
}
```

```hcl
# wrapped module: receives it as a variable
variable "key" {
  type = string
}
```

```hcl
# wrapped module: builds its own address from the variable
resource "aws_eip" "app" {
  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "module.wrapped[\"${var.key}\"].aws_eip.app"
  }
}
```

`live/e2e/estate-module-keyed/` is the two-instance fixture this is drawn from,
proven against a live emulator.

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
admitted once the live configuration declares a `record_store`. An older
refusal message called them unsupported. They are not.

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "ssm" {}
```

The label picks the backend, one of `local`, `ssm` or `s3`. [Where things are
stored]({{< relref "/docs/use/storage" >}}) has the arguments. Without one they are refused. With one
they run the stock provider lifecycle exactly as upstream.

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

## Where this page's ordering comes from

[`live/corpus-refusals.json`](https://github.com/INTENTIUS/choudoufu/blob/main/live/corpus-refusals.json)
measures which refusals fire and how often across the corpus. This page copies
no count from it, because a copied count goes stale the moment the corpus
re-runs.

That measured ranking is why the static-evaluability rule leads this page.
Several of the most frequent refusals are that one rule under different
diagnostics.

**Do not read the fixture or module-example populations as a compatibility
rate.** Module `examples/` directories demonstrate a module's full surface, so
they lean far harder on variables, conditionals and `dynamic` blocks than a
configuration describing one deployment, and refuse almost across the board.
Those populations are marked as a ranking, settled by
[#118](https://github.com/INTENTIUS/choudoufu/issues/118). One population can
honestly carry a rate since
[#147](https://github.com/INTENTIUS/choudoufu/issues/147), whole deployment
root modules published by their operators, pinned by commit, marked
`reads_as: rate`.

Run `choudoufu live-check` on your own configuration rather than inferring
anything from the corpus.
