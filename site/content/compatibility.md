# Will my config work?

Probably not without changes, and the reason is usually not the one people
expect.

Resource type coverage is rarely what stops a configuration, and `choudoufu
live-check` below answers for your exact types. What stops configurations is
how they are written and how they are run. A `for_each` over a data source, a
`count.index` in a resource name, a `backend "s3"` block, or a CI pipeline
that saves a plan file.

This page lists what actually bounces. Read it before you spend an afternoon on
a migration.

## The one rule underneath most of it

Every `count`, every `for_each`, and every identity-bearing argument must be
computable from `var`, `local`, `path` and `terraform` alone, plus functions
over those.

No data sources. No module outputs. No attributes of other resources.

The reason is that markers have to be written before anything is created, and a
marker has to say which configuration address a live resource belongs to. If
the set of instances is not knowable until after a provider has been called,
there is no marker to write.

Most of the refusals below are a consequence of that one rule.

## Ask it directly

```
choudoufu live-check ./
```

Point it at any OpenTofu configuration. It needs no `live` block, makes no
cloud calls, and does not care whether the directory has ever heard of this
fork. It prints a verdict, then every refusal that fired with its site count,
the types responsible, and what to do about each.

Run `choudoufu init` in the directory first if you can. With provider schemas
available it judges types from the provider's own identity schema as well as
the built-in table, and admits more. Without them it says so and tells you the
answer is pessimistic.

**It checks two of the five stages.** Lint and identity resolution run without a
provider, which is what makes the command fast and credential-free. Marker
stamping, discovery and projection need a cloud and are not checked, and the
command says so every time. A clean result is necessary but not sufficient, so
run a plan against a non-production account before believing a migration will
work.

## Your provider

AWS only. Every `google_*`, `azurerm_*`, `kubernetes_*` and `helm_*` resource
is refused. There is no second cloud on the roadmap
([#5](https://github.com/INTENTIUS/choudoufu/issues/5)).

## Your resource types

A type is admitted when its identity can be recovered from the live system,
either from the admission table, from the provider's own identity schema, or
from the way your configuration names it.

Common types are largely covered. The gap that hurts is connective tissue.
`aws_ecs_service`, `aws_lambda_permission`, `aws_cloudwatch_event_rule` and
`aws_cloudwatch_event_target`, `aws_api_gateway_deployment` and
`aws_api_gateway_resource`. You cannot run ECS, EventBridge or an API Gateway
without some of these, and none of them is reachable yet through any of the
three admission paths above.

`live/LIMITATIONS.md` in the repository carries the current per-type detail.

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

An instance key becomes part of the `tofu-address` marker, so it has to survive
being written to a tag and read back. The permitted set is letters, numbers,
space, and `+ - = _ / @`.

`.` and `:` are excluded even though AWS allows them in a tag value, because
the marker uses `.` to separate address segments and `:` to introduce an
instance key. A key containing either produces a marker that cannot be split
back into the address it came from.

In practice that rules out keying on CIDR blocks, hostnames, ARNs, or anything
dotted, which is a very common idiom.

### Identity arguments

For a type whose name in configuration is its identity, the argument carrying
that name has the same static requirement.

| Written like this | Why it stops |
|---|---|
| `bucket = data.aws_s3_bucket.x.id` | read from a data source |
| `name = module.naming.prefix` | read from a module output |
| `name = lower("${var.env}-app")` | reached through a function or operator |
| `bucket_prefix = "app-"` with no `bucket` | the identity argument is not set |
| `name = var.secret_name` where the variable is `sensitive` | identities appear in logs and plan output |
| `name = "app-${uuid()}"` | a different value on every evaluation |
| reading `.arn` where the table expects `name` | that attribute is not part of the identity |

The registry of these refusals, with a one-line description of each, is
`internal/live/identity/refusals.go`. It is the same list the code enforces.

## Your modules

A marker binds to a configuration address, and it stays correct for exactly as
long as that address stays stable. That one test decides which module forms
work.

**A plain `module "app" {}` call** with neither `count` nor `for_each` is
traversed the same way the root module is. A resource inside it binds by its
module-qualified address, `module.app.aws_x.y` or
`module.a.module.b.aws_x.y` at any depth. Nothing extra is needed.

**`for_each` on a module call** works when every key is evaluable from
configuration alone. A key you chose does not move when a sibling is added or
removed, so `module.app["prod"]` stays `module.app["prod"]` whatever happens to
`module.app["staging"]`. Keys are held to the same marker-safe character and
length rules as a resource's own `for_each` key, because the key becomes part
of every address inside the module.

**`count` on a module call is refused permanently.** Expansion by `count`
renumbers every address inside the module on any insertion or removal above the
changed index. Removing element zero turns `module.app[1]` into
`module.app[0]`, silently pointing every marker beneath it at the wrong live
resource. A marker records an address, not a position, so no future work closes
this. Rewrite it as a keyed `for_each` over your own stable names, move the
resources to the root module, or give the module its own estate.

### Resources inside a keyed module need hand-written markers

Auto-stamping cannot reach a resource declared inside a `for_each`'d module,
because the module's instances share one HCL body for the `tags` argument and
no single literal address is correct for all of them. Rather than guess,
choudoufu leaves such a resource alone when it already declares `tags`, and
raises a must-stamp error when it declares none and its type needs discovery to
be found again.

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
per `live/MARKERS.md`, where `[` becomes `:` and `]` and `"` are dropped. `choudoufu
live-mv` reads and writes those the same way it does a root address, so
flattening a module into the root, moving a resource into a module, or renaming
across two module instances are all ordinary renames. Only a step carrying a
`count` key stays refused.

`choudoufu live-import` is narrower. It ratifies root-module state entries only
and reports the count of non-root module instances it saw and skipped. A
module-tree estate adopts by planning with a `live` block added, the ordinary
path on [Migrate an existing estate](migrate.html).

## How you run it

A configuration can be entirely acceptable and still be refused by how it is
invoked.

- `backend "s3" {}` and `cloud {}` are refused. There is no state to store.
- Any workspace other than `default`, and `workspace new` / `workspace select`.
- Every `tofu state` subcommand, including read-only `state list` and
  `state show`.
- `import`, `refresh`, `taint`, `untaint`.
- `-out` to save a plan, and `apply <planfile>`. **This is how most CI runs
  Terraform**, so check it first. Ordinary `apply`
  re-plans and re-confirms. The design that ties a reviewed plan to the apply
  that follows is settled in
  [#74](https://github.com/INTENTIUS/choudoufu/issues/74)'s RFC
  (`rfc/20260814-plan-approval.md` in the repository), and it is not
  implemented yet.
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
  is secret material that only the state file ever remembered. A micro-state
  record that holds a secret would be a state file with extra steps, so these
  are refused rather than recorded. Permanent.
- `local_file` and other `local_*` resources.
- `module { count = ... }`. Permanent. A `for_each` on a module call works when
  its keys are static.

## Effects do work

An older refusal message said these were unsupported. They are not.

`null_resource`, `terraform_data`, `time_*` and non-secret `random_*` are
admitted as soon as the live configuration declares a `record_store`.

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "ssm" {}
```

The label picks the backend, one of `local`, `ssm` or `s3`. [Where things are
stored](storage.html) has the arguments and how to choose.

Without one they are refused. With one they run through the stock provider
lifecycle exactly as upstream.

## Two hazards that are now refusals

Both of these used to fail silently. Lint refuses them now, with a message
naming the fix.

**`lifecycle { ignore_changes = [tags] }` would defeat ownership markers.** The
stamp pass adds `tofu-estate` and `tofu-address` to the resource's tags, and a
plan that ignores tag changes discards the markers before they are ever
written. The resource would never be marked, and the next plan would propose
creating another one. Lint refuses `ignore_changes = all`,
`ignore_changes = [tags]`, and any entry naming a marker key directly
([#103](https://github.com/INTENTIUS/choudoufu/issues/103)). Ignoring a tag
key of your own, such as `tags["Owner"]`, stays admitted.

**A module call's `providers` mapping to an aliased configuration is refused.**
Live mode plans a module's resources against the root configuration's default
provider, so honouring `providers = { aws = aws.useast1 }` would take a design
change, and until that lands an estate built that way would plan against the
wrong account or region with no diagnostic. Lint refuses the aliased mapping
instead ([#104](https://github.com/INTENTIUS/choudoufu/issues/104) has the
reasoning). `providers = { aws = aws }` is admitted, since it names what live
mode already does, and provider aliases at the root work correctly.

A `provider` block declared *inside* a child module is the adjacent case,
and it is refused too. The module's resources would be served by the root
configuration's provider config instead, silently, so lint refuses the block
by name. [#70](https://github.com/INTENTIUS/choudoufu/issues/70) has the
measurement behind the ruling, that none of the ten most-installed shared AWS
modules declares one, and that upstream itself calls the pattern legacy. Configure
providers at the root and let modules receive them implicitly, which is the
only proven pattern.

## Editors and linters

The `estate.chdf.hcl` sidecar file exists for exactly this concern
([#72](https://github.com/INTENTIUS/choudoufu/issues/72)). It carries the
live configuration in a file whose extension stock tooling never reads, so a
repository adopting live markers through the sidecar keeps every `.tf` file
free of non-standard syntax. Stock `terraform validate`, `tflint` and editors
keep passing untouched.

The in-`terraform` `live` block is the one form that costs you here. Stock
Terraform and stock OpenTofu reject a configuration containing one.

```
Error: Unsupported block type

  on main.tf line 6, in terraform:
   6:   live {

Blocks of type "live" are not expected here.
```

This is expected. `live` is this fork's addition to the `terraform` block's
schema, and nothing signals it to a tool that never heard of it. Any tool that
validates the `terraform` block against upstream's schema behaves the same way,
which includes `tflint`, since it decodes HCL through OpenTofu's own
configuration libraries. Tools that only tokenize HCL, including most syntax
highlighters and formatters, are unaffected.

Some teams prefer the configuration in one place and keep the in-block form
anyway. The options then are running `choudoufu validate` in CI instead of
stock `terraform validate`, or keeping the `live` block in a small root module
that stock tooling has no reason to touch. Or move the block's content into
the sidecar, which is one file and zero edited lines. Declaring both forms at
once is an error.

## Where this page's ordering comes from

[`live/corpus-refusals.json`](https://github.com/INTENTIUS/choudoufu/blob/main/live/corpus-refusals.json)
in the repository measures which refusals fire, and how often, across a corpus
with two populations, the fixtures in this repository and the `examples/`
root modules of `terraform-aws-modules` repositories pinned to exact commits.
The per-refusal and per-population counts live in that artifact. This page
deliberately does not copy them, because a copied count is stale the moment
the corpus is re-run.

That measured ranking is why the static-evaluability rule leads this page.
Several of the refusals that fire most often are that one rule, surfacing
under different diagnostics.

**Do not read the corpus as a compatibility rate.** Module `examples/`
directories exist to demonstrate a module's full surface, so they lean much
harder on variables, conditionals and `dynamic` blocks than a configuration
describing one deployment, and they refuse almost across the board. For that
reason the artifact marks each population as a ranking and carries no
rate-like figure, which
[#118](https://github.com/INTENTIUS/choudoufu/issues/118)
settled. A number that could honestly be read as a rate waits on a
population of whole single-deployment estates, which needs a source before it
can exist.

Run `choudoufu live-check` on your own configuration rather than inferring
anything about it from the corpus.
