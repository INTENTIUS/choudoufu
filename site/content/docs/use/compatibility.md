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

Admission is not the whole story. `the tier definitions (#417)` names
four tiers by what recovers a type's identity when the record store, the
state file, or the tool itself is gone: marker-carried, declaration-carried,
record-carried, and excluded by design. `live/readiness.json` assigns every
provider type exactly one tier and one of six statuses; the table below is
generated from it. `live/COVERAGE.md` carries the same table with more
context, and [Resource tier lookup]({{< relref "/docs/use/resource-tiers"
>}}) carries it broken out per type, in customer language, with a reason for
anything short of in-contract.

One reading trap before the table. The record-carried row counts two
populations: the types the record-located mechanism actually covers, and a
larger set of untaggable types with no admission row yet that the classifier
lands there by elimination. They differ threefold, and [Resource tier
lookup]({{< relref "/docs/use/resource-tiers" >}}) separates them.

<!-- readiness-gen:begin readiness-tiers -->
| Tier | in-contract | pending-ratification | needs-separator | needs-evidence | pending-mechanism | excluded | Total |
|---|---|---|---|---|---|---|---|
| marker-carried | 682 | 161 | 1 | 2 | 0 | 0 | 846 |
| declaration-carried | 341 | 37 | 0 | 1 | 0 | 0 | 379 |
| record-carried | 96 | 294 | 3 | 16 | 62 | 0 | 471 |
| excluded by design | 0 | 0 | 0 | 0 | 0 | 3 | 3 |
| **Total** | 1119 | 492 | 4 | 19 | 62 | 3 | 1699 |
<!-- readiness-gen:end readiness-tiers -->

## How your configuration is written

This is the group that catches people. Every row below is a different way of
asking an address or an identity to resolve before a provider can answer it;
[Identity]({{< relref "/docs/model/identity" >}}) states the rule in full.

### Expansion

`count` and `for_each` are expanded before anything is read, so that a marker
exists for every instance. That does not mean the values have to be written
in the configuration text. Two phases run in front of resolution and both
feed it:

- **Data sources are read first** ([#179](https://github.com/INTENTIUS/choudoufu/issues/179)),
  so `count = length(data.aws_availability_zones.all.names)` and
  `for_each = toset(data.aws_subnets.x.ids)` both expand normally. The phase
  calls a provider on purpose, ahead of resolution, precisely so that they
  can. What is refused is a data source that *cannot* be read that early.
- **A sibling's own keys carry across.** `for_each = aws_subnet.this` borrows
  that resource's expansion and `count = length(aws_eip.pool)` borrows its
  cardinality, neither needing a single live ID. Where a run has already
  resolved and discovered once, a second pass can also answer a `count` or
  `for_each` that reads a genuinely computed attribute of a sibling
  ([#187](https://github.com/INTENTIUS/choudoufu/issues/187)).

| Written like this | Why it stops |
|---|---|
| `for_each = module.net.subnet_ids`, and `count = length(module.net.subnet_ids)` | a module output is evaluable in an identity argument but not in an expansion. The expansion pass refuses it as "Module output not supported in static context" even when the output is a literal |
| `for_each = { for s in aws_subnet.app : s.id => s }` | the key clause reads the iteration variable, so the key is a live ID rather than one of the parent's own keys. A comprehension whose key clause does not read the value variable expands from the parent's keys |
| `for_each` over a `count`-expanded resource | `count` produces a tuple, and stock OpenTofu rejects a tuple as a `for_each` argument too |
| `count.index` in an identity-bearing argument, where two indices render the same value | both instances resolve to one live identity, so one marker is written over the other |
| a data source that cannot be read before the plan | it depends on a managed resource, names one in `depends_on`, has a non-static argument, or its provider cannot be configured pre-plan |

That last-but-one row is much narrower than it once was. `count.index` in an
ordinary tag, description or other non-identity argument is not refused at
all, and in an identity-bearing argument the test is collision rather than
indexing: `"name-${count.index}"`, `100 + count.index`,
`format("web-%d", count.index)` and `var.zones[count.index]` over distinct
zones are all admitted, and `count.index % 3` is admitted at `count = 3` and
refused at `count = 5`. `live/LIMITATIONS.md`'s `count-index-in-tag` entry
carries the full rule.

### `for_each` keys

An instance key becomes part of the `tofu-address` marker, so it must survive
being written to a tag and read back. Since
[#210](https://github.com/INTENTIUS/choudoufu/issues/210) that boundary is
wide: **every printable rune is permitted except six**. A key outside the raw
AWS tag-value character set is escaped into the marker rather than refused,
so dotted keys and CIDR blocks work. `alice.smith`, `2001:db8::/64`,
`eu-west-1a`, `eu/west` and `at@sign` are all ordinary keys.

Letters, digits, space and <!-- markerkey:extras -->`+` `-` `=` `.` `_` `:` `/` `@`<!-- /markerkey:extras -->
need no escaping, with one wrinkle: `+` is the escape introducer, so a
literal `+` is doubled inside the marker. It round-trips, and no key is
refused for containing one.

The six exclusions are <!-- markerkey:excluded -->`"` `\` `$` `%` `[` `]`<!-- /markerkey:excluded -->,
each colliding with an escaping rule this fork does not own:

| Rune | Why |
|---|---|
| `"` and `\` | `addrs`' `toHCLQuotedString` backslash-escapes them when OpenTofu renders a key into the declared side of an address comparison, so the key would decode differently on each side |
| `$` and `%` | the same function doubles either one when it immediately precedes `{`, a transformation with no per-rune inverse |
| `[` and `]` | `markers`' `EscapeAddress` scans for them to find an instance key's boundaries, before any key-level escaping runs, so a raw bracket corrupts the scan itself |

An empty key is refused too. An escaped address ending in a bare `:` does not
parse back as a marker.

One case is narrower. When the `for_each` expression itself cannot be read
statically, because it is rooted at a data source, another resource, or
anything else known only once the cloud has been read, the stamping pass
cannot build the per-key escape table, so the key set narrows back to the
unescaped one: letters, digits, space and
<!-- markerkey:extras -->`+` `-` `=` `.` `_` `:` `/` `@`<!-- /markerkey:extras -->
([#227](https://github.com/INTENTIUS/choudoufu/issues/227)).

The rule lives in `internal/live/markerkey`, and both enforcement points,
lint and the resolver, read it from there, so the two cannot drift.

### Identity arguments

Where the name in configuration is the identity, that argument must resolve
before a provider is called. An expression containing **no managed-resource
reference** is statically evaluated, and that covers a lot: literals, string
templates, input variables, locals, functions, `path.*`,
`terraform.workspace`, module outputs, data-source results, and arbitrary
composition of those. All three of these resolve:

```hcl
name   = lower("${var.env}-app")   # pure functions are fine
name   = module.naming.prefix      # a module output is an expression written
                                   # in the child scope, and this resolver
                                   # can enter that scope and evaluate it
bucket = data.aws_s3_bucket.x.id   # read by the data-read phase before
                                   # resolution begins
```

An expression that **does** reference a managed resource takes one of two
routes instead. By default it is matched structurally: a bare traversal
becomes a reference to that parent's identity attribute, and a string
template becomes a sequence of literal and parent parts, which is what makes
`"${aws_route_table.main.id}_0.0.0.0/0"` expressible. Where a run has already
resolved and discovered once, a second pass can answer a reference to a
sibling's genuinely computed attribute from what the cloud holds, which is
what admits `aws_acm_certificate.cert.domain_validation_options`.

What still stops:

| Written like this | Why it stops |
|---|---|
| a managed-resource reference inside a function call or arithmetic, with no live value for it | structural matching handles traversals and templates, not computation over a value that does not exist yet |
| `bucket_prefix = "app-"` with no `bucket` | the identity argument is not set |
| `name = var.secret_name` where the variable is `sensitive` | identities appear in logs and plan output. Wrap the specific value in `nonsensitive(...)` where it is not genuinely secret |
| `name = "app-${uuid()}"` | `uuid()`, `timestamp()` and `bcrypt()` return a different value on every evaluation |
| reading `.arn` where the table expects `name` | that attribute is not part of the identity |

`internal/live/identity/refusals.go` registers every refusal this pass can
produce, each with a one-line description, and `TestRefusalsRegistered` fails
if a new one is added without describing it there. It is the list the code
enforces.

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

**`count` on a module call** is admitted when the count is statically
evaluable and none of the call's own arguments read `count.index`, directly
or by indexing a sibling's count-expanded collection
([#195](https://github.com/INTENTIUS/choudoufu/issues/195)). Resolution
traverses each instance, and `module.app[0].aws_x.y` binds exactly as soundly
as `module.app.aws_x.y` does. Shrinking a `count` retires the highest index
and never renumbers a survivor, which is what makes the address stable.

Two shapes are still refused: a `count` this pass cannot evaluate at all, as
non-static, and a statically-evaluable `count` whose own arguments read
`count.index`, for the leak. That second one is the same rule as `count.index`
reaching a resource's own identity.

Stamping keeps up. A call with exactly one instance is stamped with that
instance's key, and since
[#378](https://github.com/INTENTIUS/choudoufu/issues/378) a call with more
than one is stamped through `tofu.marker_module_prefix`, the same mechanism a
`for_each`'d call already used, so `module.sites[0]` and `module.sites[1]`
render their own addresses out of the one shared body.

A resource inside a `for_each`'d module needs its own marker built by hand
from the module's own key; see [How to write markers inside a for_each'd
module]({{< relref "/docs/use/keyed-modules" >}}).

### Crossing a module boundary

A marker carries the full module-qualified address, escaped into a tag value
per `live/MARKERS.md`, where `[` becomes `:` and `]` and `"` are dropped.
`choudoufu live-mv` handles those like any root address, so flattening a module
into the root, moving a resource into a module, and renaming across two module
instances are ordinary renames. A step through a `count`-keyed module instance
is one too, since
[#317](https://github.com/INTENTIUS/choudoufu/issues/317) retired the premise
that a module `count` renumbers its survivors. What `live-mv` refuses is the
pair of addresses that describes no move: the same address twice, two
different resource types, and anything that is not a managed resource.

`choudoufu live-import` traverses the whole state, every managed resource
instance, root module and child module alike.
[#59](https://github.com/INTENTIUS/choudoufu/issues/59)'s module epic gave the
other walkers (identity, discovery, stamp, projection, mv) real traversal and
this one matches them, so a resource's `tofu-address` marker carries its full
module path exactly as an ordinary plan and apply would write it.

The one piece #59 left for later is provider aliasing that crosses a module
boundary. A module inheriting its caller's provider, the overwhelmingly common
case, is unaffected.

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

A module call's `providers` mapping is honoured, and used to be refused. Since
[#188](https://github.com/INTENTIUS/choudoufu/issues/188)
`internal/live/providerscope` walks every ancestor call's mapping, the same
resolution stock OpenTofu performs, and planning, discovery and the projection
all read a resource's provider configuration through it. So
`providers = { aws = aws.useast1 }` plans and applies against the account or
region it names. The one mapping still refused is the child-side
`configuration_aliases` shape, `providers = { aws.primary = aws }`, and only
when the root declares no configuration under that same alias name.

One consequence of spanning configurations remains. An adoption hint's
`--region` and
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
  [#74](https://github.com/INTENTIUS/choudoufu/issues/74)'s ruling settles the
  design for tying a reviewed plan to its apply. Not implemented yet.
- `-json` and `-json-into`.
- `-refresh-only`. Both sides of that comparison are the live system here, so
  there is nothing for it to do.
- `-state`, `-state-out`, `-backup`, `-generate-config-out`.

`apply -destroy` (and `choudoufu destroy`) work: they remove every object
this estate owns, in one apply, the same way `apply` after deleting every
resource block already did - see
[#320](https://github.com/INTENTIUS/choudoufu/issues/320).

## Constructs this page used to refuse, and no longer does

This section was a list of six constructs refused outright. All six have
since been admitted, five of them conditionally, and the conditions are worth
reading because each one is met by an ordinary estate.

- **`provisioner "local-exec"`, `"remote-exec"` and `"file"`, and
  resource-level `connection` blocks.** Admitted since
  [#353](https://github.com/INTENTIUS/choudoufu/issues/353). Stock keeps one
  piece of memory about a provisioner, the tainted bit set when a create-time
  provisioner fails, and the estate's record store is where that bit lives
  here. A `live` block implies a record store, so this needs no extra
  declaration. Nothing about the provisioner's *content* is stored, exactly as
  stock stores nothing: changing a `local-exec`'s `command` between runs
  produces an empty plan either way.
- **`data "terraform_remote_state"`.** Read from its own backend before
  resolution needs it. Its own arguments (`backend`, `config`, `workspace`)
  must be statically evaluable, the same rule any data source's arguments
  draw, and the backend's reachability and credentials are treated as a fact
  about the run rather than about the configuration. The real limitation is
  staleness, not refusal: once a producer estate adopts markers it stops
  writing that state file, and a reader pointed at it keeps returning a
  snapshot frozen at migration time, with nothing on either side able to
  detect it. `live/OUTPUTS.md`'s cross-estate pattern is the answer to *that*.
- **`moved` blocks.** Admitted. Two shapes are refused, and both are ways the
  alias would be built wrong rather than untidiness: a source address the
  configuration still declares, which stock refuses too as "Moved object still
  exists", and a pair whose two ends name different resource types. An
  endpoint passing through a `count`-expanded module instance is admitted
  since [#330](https://github.com/INTENTIUS/choudoufu/issues/330), and so is
  one through a `count`-expanded resource.
- **`random_password`, `random_bytes` and the `tls_*` family.** Admitted under
  the default. This is a setting, not a ban: what remembers a generated secret
  on stock OpenTofu is the state file, in clear, and what remembers it here is
  the estate's record store. Under `strict { secrets = "store" }`, the
  default, they run the stock lifecycle. Under
  `strict { secrets = "refuse" }` they are refused, which is what that setting
  is for. The record store is not a secret manager, and an estate that must
  hold no secret material at all is the case the toggle exists to serve.
- **`local_file` and `local_sensitive_file`.** Both admitted, `local_file`
  outright and `local_sensitive_file` under the same `secrets` default as
  above. `local_file` keeps one rule the record-backed types do not: its
  `filename` names a real file on the machine that ran the apply, so two
  instances at distinct addresses can still collide on one path, and the
  `count.index` check keeps running over its arguments.
- **`module { count = ... }`.** Admitted when the count is statically
  evaluable and the call's own arguments do not read `count.index`. See [Your
  modules](#your-modules) above.

One construct in the same family genuinely is refused, and it has its own
section below: a module call's child-side `providers` mapping,
`providers = { aws.primary = aws }`, where the root declares no configuration
under that alias name.

## Effects do work

`null_resource`, `terraform_data`, `time_*` and the `random_*` family are
admitted. They run the stock provider lifecycle exactly as upstream, against a
record in the estate's record store. An older refusal message called them
unsupported. They are not.

Nothing has to be turned on for that: a `live` block with no `record_store`
block of its own gets an implied local one, a `.tofu-records` directory beside
the module, the way stock implies a local state file. Declaring an estate is
the whole setup step. Name a `record_store` to put the records somewhere a
team shares instead.

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

**A module call's child-side `providers` mapping can name an alias nothing
resolves.** [#104](https://github.com/INTENTIUS/choudoufu/issues/104) opened
this as a refusal of both shapes a mapping's alias can take, because nothing
in the live path read the mapping at all.
[#188](https://github.com/INTENTIUS/choudoufu/issues/188) closed that for the
parent-side shape: `providers = { aws = aws.useast1 }` is now resolved by
`internal/live/providerscope` and honoured, which is every one of the 110
sites the corpus had ever produced for this rule. What stays refused is
`providers = { aws.primary = aws }`, the `configuration_aliases` shape, where
the alias is on the child side and the root declares no configuration under
that name for the module's resources to resolve against; the provider would
be configured from the environment with nothing from the configuration
reaching it. `providers = { aws = aws }` is admitted, naming what live mode
already does, and so is `{ myaws = aws }`, where only the child's local name
differs. Root-level provider aliases work correctly, and a resource's own
`provider =` argument is honoured.

A `provider` block inside a child module is a different question, and it is
admitted. [#70](https://github.com/INTENTIUS/choudoufu/issues/70) originally
refused every one of them, on the measurement that none of the ten
most-installed shared AWS modules declares one and that upstream calls the
pattern legacy. The corpus then found a real site using exactly that shape,
and since [#201](https://github.com/INTENTIUS/choudoufu/issues/201) live mode
walks to a module's own provider block and honours it rather than falling
back to the root. The one shape the rule still names, a module-local provider
block reached through a call using `count`, `for_each`, `enabled` or
`depends_on`, is rejected by OpenTofu's own configuration validation before
lint ever runs, in this fork and in stock alike.

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
