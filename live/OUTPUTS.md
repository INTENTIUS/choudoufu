# Cross-estate values

This file answers issue #62. "Split into independent estates" is the
forwarding advice both for an estate that has grown too large (#52) and for
a module that has to leave. As of issue #59's phases 1-2 that is no longer
most modules. A static module tree or a statically-keyed `for_each` module
binds in place, same as the root, while a `count`-expanded module block is
refused permanently and still has to leave. A genuinely independent module
may also be split out by choice rather than necessity (#59 phase 3). Once
that split happens, the two estates need a way to share values: a network
estate's VPC ID, an IAM estate's role ARN.

`terraform_remote_state` is not refused. Since #179 stage 3 it is read
from its own backend through `internal/live/dataread`, under the same
eligibility rule as any other data source (`live/COMPATIBILITY.md`, "Constructs
this page used to refuse"). What it cannot do is stay current: once a
producer estate adopts live markers it stops writing the state file, and a
reader pointed there keeps getting a snapshot frozen at migration time, with
no "abandoned as of" marker on either side to say so (`live/LIMITATIONS.md`,
"A foreign stack's remote state can go stale with no way to detect it"). This
file is the answer to that staleness: what an estate reads instead once its
producer no longer keeps a state file of record.

## The decision

Read the producer's live resource with a data source of its own type,
with no new construct, namespace, or lint rule. This was already the lint
refusal's forwarding advice in prose. This file makes it the normative
spec, the way `live/RECEIPTS.md` did for receipts.

A dedicated "estate output" surface was considered and declined: outputs
written to `aws_ssm_parameter`s under an estate namespace, the receipts
pattern's machinery pointed at outputs instead of effects, read back by an
ordinary `aws_ssm_parameter` data source. See "Why not outputs-as-receipts"
below for why. Reading the output values an estate already records is a
narrower question, answered in "What an output read is for".

## The pattern

A consumer estate declares a data source of the producer's resource type
and filters it down to exactly one live resource. The recommended filter is
the producer's marker tags (`live/MARKERS.md`), `tofu-estate` and
`tofu-address`. Both tags are already written on every managed resource
for free, and the pair is already unique within an account: an address is
unique within its estate, and an estate name is unique across the
account, so a consumer needs no new naming convention.

```hcl
# In the consumer estate, reading a VPC a separate "network" estate owns:
data "aws_vpc" "network" {
  filter {
    name   = "tag:tofu-estate"
    values = ["network"]
  }
  filter {
    name   = "tag:tofu-address"
    values = ["aws_vpc.main"]
  }
}

resource "aws_subnet" "app" {
  vpc_id     = data.aws_vpc.network.id
  cidr_block = "10.0.1.0/24"
}
```

Where a type's data source offers no tag filter, an ARN-identity type, or
one whose list schema has no filter argument (`live/survey-full.json`'s
signals and `live/LIMITATIONS.md`'s "Emulator-blocked"/registry sections
name several),
fall back to whatever client-assigned identity that type's data source does
expose: a name, a bucket, an ARN built from a name the consumer already
knows. Either way the consumer reads the producer's live resource
through the provider's read contract for that type, not through a side
channel this mode maintains on the producer's behalf.

This is the "read the producer's own live resource with a data source of
its own type" forwarding address that `live/LIMITATIONS.md`'s remote-state
staleness entry gives. This file is the spec that address points to by
name.

## Why not outputs-as-receipts

The receipts pattern (`live/RECEIPTS.md`) exists for one specific case: "an
effect that has no queryable live state of its own." A migration changes
rows, not a resource an API can list. A cache purge changes what a CDN
serves, not a record OpenTofu can read back. A receipt is memory
manufactured for a fact the live system cannot answer.

Most of an estate's outputs are the opposite case. A VPC ID, a role ARN, a
bucket name: every one of these already lives on a real, queryable resource
that a data source of its own type reads correctly and current, on every
plan, with no memory at all. Pointing the receipts machinery at outputs would
build memory for a fact the live system already answers, which is
what `live/LIMITATIONS.md`'s recurring test names: "every banned
feature exists to maintain or repair the store. That is the test for edge
cases." An SSM-parameter mirror of a live attribute is a store by that
test, even though it looks like a receipt.

Three concrete problems follow from treating it as one anyway.

1. **It cannot satisfy the leaf rule.** `RuleReceiptLeaf` (`live/
   RECEIPTS.md`'s Guard 4) keeps a receipt something nothing depends on, so
   that losing it costs one idempotent re-run and never a wrong plan
   elsewhere. An "output" is only useful if other estates *do* depend on
   it, which is the entire ask in issue #62. Pointing the receipts machinery
   at outputs means either breaking the leaf rule for this one flavor of
   parameter, which unravels the property that makes every other receipt
   safe to lose, or building a second, parallel set of rules that looks
   like a receipt but obeys the opposite constraint. The second option is
   new machinery that only resembles a receipt, and it gives up the "no
   new resource kinds" simplicity this option was supposed to buy.
2. **It is a derivative copy, which Guard 2 warns against.**
   RECEIPTS.md's Guard 2 rejects hashing raw inputs partly because a
   receipt "keeps [itself] from becoming a second copy of configuration
   data that now has to be kept in sync with the first." An SSM parameter
   mirroring a VPC ID is that second copy. Every producer apply
   has to remember to keep the mirror current, and every consumer plan now
   trusts a value that can go stale relative to the resource it mirrors.
   That is the risk a projection rebuilt every run avoids, per the docs
   site's "Where does the state live?", where a stale or missing projection
   "costs a re-read, never a wrong plan". A data source reading the
   producer's resource directly cannot go stale this way, because there is
   nothing between the read and the value.
3. **It does not buy the stability it is sold on.** The case for a
   first-class output surface is that it "makes the producer's contract
   explicit and stable across producer refactors." But a producer refactor
   that changes a resource's live identity (replacing a VPC, splitting one
   bucket into two) changes what a consumer sees whether the consumer reads
   the resource directly or through a mirrored SSM parameter. The mirror
   does not insulate the consumer from the refactor. It only adds a second
   place the refactor has to remember to update, and a forgotten update
   there is a silent, consumer-visible staleness bug that reading the
   resource directly cannot produce.

A dedicated output surface costs a namespace convention, a naming spec, and
(per the issue) lint support to keep it statically recognizable. Plain data
sources cost nothing to build, cannot drift from what the producer holds,
and are already how Terraform practitioners read another workspace's
resources without a backend. The cheaper option also delivers the stability
the expensive one promises.

## What an output read is for

Issue #1371 asks what reading another estate's recorded outputs gives that
a data source does not. The rule above stays. If a live resource holds the
value, read the live resource. A corpus survey on 2026-09-19 says that
covers most of what estates pass to each other. It matched 81 cross-estate
reads to a producer output, loosely, by name. Of those, 39 were one
attribute a data source returns. Another 32 were lists or built strings a
consumer can rebuild from tag-filtered data sources.

The other 10 had no live resource behind them, and these are what an output
read is for. About 25 of the 311 root outputs of deployments in the corpus
are like this. There are three kinds:

- A value the producer chose. The `cluster-infrastructure` root in
  govuk-infrastructure outputs the literal `"cluster-services"` as
  `cluster_services_namespace`. Its `cluster-services` root reads that
  through `tfe_outputs`.
- The result of running something. In govuk-aws, `app-publishing-amazonmq`
  outputs the decoded result of a Lambda invocation.
- A value held by a system the consumer has no provider for. simpleinfra's
  `fastly-tls-subscription` outputs a Fastly configuration ID.

Without an output read the consumer copies the value into its own
configuration, and nothing keeps that copy true.

A second, smaller gain is the grant. A data-source read needs describe
permission on the producer's service, which often covers every resource of
that type in the account. The output read needs `s3:GetObject` on one
producer's `tofu-outputs/` prefix and shows only what that producer
published. This matters only when the consumer does not already use that
service.

None of this reopens the objection above, which declined building a mirror
of live attributes. Every estate already records its root output values
under `tofu-outputs/<estate>/` for its own plan to read. The only question
is whether another estate may read them.

Staleness still rules out a value that mirrors a live attribute. The record
is as of the producer's last apply and does not move when someone changes
the resource out of band, so those values stay on data sources. A value that
comes from the producer's configuration can change only when the producer
applies, so its record is as current as the value can be.

The read is built (#1371, ruled 2026-09-26) as the builtin terraform
provider's `data "terraform_estate_outputs"`, with `estate` and `names`
arguments and a `values` object. The block is the declaration: it names the
producer, as `render-policy.sh --reads-outputs-of` does on the IAM side. Its
reader is `internal/live/projection`'s `ReadEstateOutputs`, not
`ReadRootOutputValues`, which logs and skips every read error: a missing
grant refuses and names the other estate, and a name the producer never
recorded refuses and names both. What crosses is what is written: root
outputs that are non-sensitive and wholly known. The consumer's plan warns
that the value is as of the producer's last apply, with the time the record
was written. [Day-2 operations](https://intentius.io/choudoufu/docs/use/day2/) is the operator's page.

On `record_store "kubernetes"` the declaration moves into the record_store
block, because there the store is where the boundary is. Each estate keeps
its records in a namespace of its own by default (`tofu-records-<estate>`),
and that namespace is the read isolation, so the producer's outputs are not
in the consumer's store at all; in a namespace shared by configuration the
consumer's own store refuses a record labelled as another estate's (#1355).
The consumer declares the read with `reads_outputs_of "<estate>" {}`, adding
`namespace = "..."` when the producer's records are not in its default
namespace. Only a declared estate's namespace is opened, through a store
opened as that estate and able to `get` and nothing else; a read of an
undeclared estate is refused before anything is sent ("This estate does not
declare that it reads another estate's outputs"). The grant is a Role in the
producer's namespace with `get` on the named output Secrets and no other
verb, and a refusal by RBAC prints the `kubectl create role` and
`rolebinding` lines that grant exactly that.
`live/smoke/drafts/k8s-an-estate-reads-another-by-declaring-it.sh` is the
draft proof (not yet run).

On `record_store "azurerm"` the read works as on `s3`: every estate sharing
the container reads through the same store, and a `reads_outputs_of`
block is refused. The grant is the difference. The ABAC read fence
([`live/azure/record-store-abac.condition`](azure/record-store-abac.condition))
lets a principal read only blobs tagged with its own estate and list only
its own three prefixes, and there is no `render-policy.sh` for it yet, so a
consumer under that condition needs a second role assignment, written by
hand, whose condition names the producer's estate and its `tofu-outputs/`
prefix. Built from the condition's documentation and not measured.

A record goes when nothing stands behind it. A destroy of the whole estate
(no `-target`, no `-exclude`) deletes every record under
`tofu-outputs/<estate>/`, and an apply after an `output` block is removed
deletes that output's record, so a consumer naming either is refused with
"Another estate has not recorded this output" rather than served the last
value. A scoped apply deletes nothing it did not evaluate. This is
`PruneRootOutputValues`, called from the apply's write-back.

## `choudoufu output` reads the record store

In a directory with a live block, `choudoufu output` and `output -json` answer
from `tofu-outputs/<estate>/`, not from a state file, so a fresh checkout with
no local cache works (choudoufu#2213). This is what a Terragrunt `dependency`
block (it runs `<binary> output -json`) and Atmos `!terraform.output` call.
A composed root, whose outputs belong to several estates, still takes the stock
path.

The output names come from the directory's configuration, because the
namespace has no listing. The JSON is the stock document, `{name: {sensitive,
type, value}}`, with `sensitive` always `false`, since a sensitive output is
never recorded. What is not recorded is left out, never returned as null:

- a sensitive output and an output whose value was not wholly known at the end
  of the apply, and an output no apply has recorded yet;
- `output -json` (everything) omits them and warns on stderr, naming each, so
  stdout stays a clean JSON document;
- `output NAME` for one of them exits 1 and says why (marked sensitive, or no
  recorded value). `-show-sensitive` changes nothing, as there is no value.

A record store that cannot be opened, or a record that cannot be decoded, is an
error. Warnings and errors go to stderr.

`terraform_remote_state` and Atmos `!terraform.state` read a state file from a
backend, not through `output`, and Atmos reads it itself, so no binary can
answer it. They are served by the apply (next section).

### Outputs published to the backend

After an apply in a directory with a live block and a backend block (a
hand-written one, a Terragrunt `generate` or `remote_state` `backend.tf`, an
Atmos `backend.tf.json`; the live block otherwise ignores it), choudoufu
writes a stock state document to the location that block names: version 4,
`outputs` filled, `resources` empty. A stock root's `terraform_remote_state`
(plan-time, no binary of ours involved) and Atmos's `!terraform.state` read
the outputs as they always did (choudoufu#2213).

- It is written last, after the apply's records. If the records fail, nothing is
  published.
- The values are the ones `output` returns: read from the record store, so a
  sensitive output and one not wholly known are never published, and a scoped
  apply does not blank outputs it did not evaluate. A whole destroy leaves a
  document with no outputs.
- No backend block, a `local` one, or no record store: nothing happens.
- The write goes through the backend's own state manager, so its locking is
  used (the S3 lockfile or DynamoDB table; gcs and azurerm locks), and the lock
  is released before the apply returns. A lock held by someone else means no
  write and a warning, never a wait.
- Lineage and serial are the backend's: an existing document keeps its lineage
  and its serial goes up by one, an unchanged document is not rewritten, and a
  first write gets a new lineage. A document that holds resources is a stock
  state somebody still owns and is never overwritten.
- A backend that cannot be written (unreachable, no permission, a block
  without its bucket) is a warning. The apply never fails because of it.
- The workspace is the one in `TF_WORKSPACE` or `.terraform/environment`,
  else `default`.

What is not covered: a backend configured only through `init -backend-config`
flags (the apply sees `backend "s3" {}` with no bucket and warns); state
encryption (the document is written unencrypted); a composed root; and
sensitive outputs, by rule. Verified against floci's S3 for the s3 backend,
with a stock `tofu` root reading the key. The gcs and azurerm backends go
through the same code and are not exercised against an emulator here.

A pull request that changes a producer's output and a consumer that reads
it would otherwise plan the consumer on the old recorded value. #2051 adds
`plan -estate-outputs-from <estate>=<plan.json>`, repeatable, which answers
that estate's reads from its `show -json` document's `planned_values`
instead of the record store. An output only known after apply, or
sensitive, is refused naming the output and the estate; the plan never
proceeds on an unknown or a placeholder. The saved plan records the values
it read, and `apply <planfile>` reads the record store again and refuses
with exit 3, naming estate, output, planned and applied value, until the
producer has applied and recorded those same values. `live-plan-set` does
this for a set: a root reading another root of the set is planned after it
and marked `"reads": "planned"` with `planned_from` naming the upstream.

An output read is never for a value a data source can read.

## Composed runs: one plan and one apply across estates

A root that has no `live` block of its own and calls estate directories as
modules runs them in one plan and one apply:

```hcl
# root/main.tf
module "net" { source = "../net" }   # ../net has its own live block, estate "net"
module "app" { source = "../app" }   # ../app has its own live block, estate "app"
```

```hcl
# app/main.tf, the same file whether app runs alone or composed
data "terraform_estate_outputs" "net" {
  estate = "net"
  names  = ["vpc_id"]
}
resource "aws_s3_bucket" "logs" {
  bucket = "app-logs"
  tags   = { vpc = data.terraform_estate_outputs.net.values.vpc_id }
}
```

Run it from the root directory with the usual `choudoufu plan` and
`choudoufu apply`. No flag turns it on; a root with no live block that calls
local directories carrying one is composed.

An estate's read of an estate that is in the same run is not sent to the
record store. The block waits for the module, and while that module has
changes pending the read is deferred to the apply, like any data source whose
dependency has changes. At the apply the values come from the module's
outputs, so an id that does not exist until the apply is available to the
other estate in the same apply. Only the outputs that are unknown at plan are unknown to the
reading estate: an output whose resource already exists keeps its planned
value, so a resource of the other estate that uses it is not replaced because
the module has something else pending. The read is still redone at the apply. If the module has nothing pending, the read
happens in the plan, from outputs the walk already holds. The `values` object
has one attribute per name, as before. Outputs that are sensitive are refused
with the same summary as a store read. The module's outputs are also recorded
for the estate after the apply, so a later run of another estate on its own
reads them from the store as usual.

Records and markers follow the estate, not the composition. Each module is
treated as the root of its own directory for ownership: its estate name, its
record store (a local one is the `.tofu-records` beside the estate's own
files), its markers, and the address in `tofu-address` and in the record key
(`aws_vpc.main`, not `module.net.aws_vpc.main`). See "A composed run" in
[LIMITATIONS.md](LIMITATIONS.md) for what the root may hold, how roles work,
and what a failed apply leaves.

## Joint runs: Terragrunt and Atmos keep their layout

A Terragrunt repo (one directory per unit, each with `terragrunt.hcl`,
`inputs` and `dependency` blocks) or an Atmos stack (one component per
directory, `vars`, `!terraform.output`) is used as it is. The only change is
the binary: `terraform_binary = "choudoufu"` in Terragrunt,
`components.terraform.command: choudoufu` in Atmos. Each unit keeps its own
directory, inputs, `live` block and records.

Both tools run the binary once per unit, in dependency order: `init`, then
`plan` or `apply` with the unit's inputs in `TF_VAR_` variables (Terragrunt)
or a generated `-var-file` (Atmos), and `output -json` in a dependency's
directory to read what it publishes. When the invocations come from one
`terragrunt run --all` or one `atmos terraform ... --all` / `--affected`
(found from the parent process: its name and its arguments), choudoufu joins
them into one run:

- Each unit that has a live block registers itself in a session shared by
  the invocations (a directory under the temporary directory named for the
  tool process): its directory, the values of its variables from every
  source the unit itself would use (`TF_VAR_`, `terraform.tfvars`,
  `*.auto.tfvars`, `-var`, `-var-file`), and the outputs it declares.
- The composed root of "Composed runs" above is written over the units in the
  session, one module call per unit carrying that unit's inputs, and planned.
  It is planned again as each unit registers, so the plan after the last unit
  is one plan across all of them. Each unit is still its own estate, with its
  own record store and markers.
- `output -json` in a unit's directory during the plan answers from that joint
  plan. An output that is known is returned as it is. An output that is not
  known until apply is returned as the string `(known after apply from <unit>.<output>)`.
  The tool puts that string into the dependent unit's input, and the joint
  root turns it into a reference to the upstream module's output, so the
  dependent's resource shows `(known after apply)` in the same plan and is
  resolved by the same apply. An output that is only partly unknown is returned
  as one such string.
- `plan -out=<file>` writes a file that names the session and the unit and
  carries the joint plan itself: the plan file of the composed root (with its
  configuration snapshot), its `show -json`, the units and their inputs, and
  the root's lock file. Each later unit's plan rewrites every unit's file, so
  all of them carry the last joint plan. `show -json <file>` is the unit's
  slice of the plan the file carries: the module's step is taken off every
  address, and the outputs are the unit's own. `apply <file>` applies the
  whole joint plan, in dependency order, once; the other units' `apply <file>`
  find it done and report that. A failed joint apply is reported by every
  unit's apply.
- The file is applied from what it carries, so a plan made on a pull request
  is applied at merge in another job or on another machine, where the session
  is gone: `terragrunt run --all --out-dir <dir> -- plan`, keep `<dir>`, then
  `terragrunt run --all --out-dir <dir> -- apply`. The apply lays the
  configuration out from the plan's snapshot, plans the live system again and
  refuses, exit status 3, if the result is not the reviewed plan (#878's
  check; a value that was known after apply in the reviewed plan is not
  compared). The refusal names the tool's command that plans again. An input
  the tool gives a unit at apply with another value than at plan is refused
  the same way (Terragrunt gives none to an apply of a plan file).
- Atmos's `deploy` with a planfile store shows the stored plan, makes a fresh
  plan and applies the fresh one. When a stored file from another run is shown
  inside a joint run, choudoufu keeps that unit's slice, and the apply of the
  unit's fresh plan refuses, exit status 3, when its slice differs from the
  reviewed one in anything but values that were known after apply. Atmos
  verifies each component just before it applies it, after its upstream has
  applied, and counts "known after apply" becoming a value as drift, so a
  reviewed plan that carried an unknown cannot verify equal: use
  `planfiles.verify: warn` with `planfiles.required: true`, and choudoufu's
  check is the one that refuses.
- `plan` prints the unit's own resources and the path of the whole plan.

A unit run alone, a run of the tools without `--all` / `--affected`, a unit
without a live block, and any plan that uses `-target`, `-destroy`,
`-refresh-only`, `-replace`, `-json` or `-generate-config-out` take the
ordinary path. `CHOUDOUFU_JOINT_RUN=off` turns joining off. For a driver that
is neither tool, any other value of `CHOUDOUFU_JOINT_RUN` is the run id the
invocations share. `CHOUDOUFU_JOINT_TIMEOUT` (default 30m) is how long an
invocation waits for another that is planning or applying.

What the tools' own models decide:

- Terragrunt `run --all apply` and Atmos `apply --all` / `--affected` without
  a plan file apply one unit at a time: the tool starts a unit only when the
  units it depends on have exited, and Atmos does not accept a plan file in a
  bulk apply (`--from-plan` is refused with `--all` and `--affected`). So the
  joint plan is the one plan, and a joint apply needs the files from
  `plan -out` (`terragrunt run --all --out-dir <dir> -- apply`). Without them each unit
  applies on its own, in order, and reads its upstream unit's recorded
  output from the record store (`choudoufu output`), which is by then known.
- Atmos calls `output -json` for a component with no inputs of its own, so a
  component joins the plan when Atmos plans it, not before. A dependency read
  of a component Atmos has not planned yet is answered from the record store.
- `--affected` plans the units the tool names. A dependent of a changed unit is
  in the joint plan only if the tool includes it (`--include-dependents`).
- The joint plan is all or nothing: a unit whose configuration fails in the
  joint plan fails the plan of every unit that waits for it, and each
  unit's invocation prints the error. A unit that does not load at all (a syntax
  error) plans alone and reports its own error.
- An input that is a number, a bool or an object whose attribute is unknown
  cannot be carried: the placeholder is a string. Strings, and strings inside
  lists and objects, are carried.

(`internal/command/joint_*.go`, asserted by `internal/command/joint_test.go`.
`CHOUDOUFU_JOINT_DEBUG=1` says why an invocation was not joined.)

## Demonstrated

`internal/live/lifecycle/cross_estate_live_test.go`'s
`TestCrossEstateDataSourceAgainstFloci` is the live proof: two independent
estates, two independent `choudoufu apply` runs, no state file at any
point. A producer estate creates a VPC. A consumer estate's `aws_vpc` data
source, filtered on the producer's marker tags rather than
`terraform_remote_state`, resolves to that VPC's real ID, and a subnet
created from it ends up inside the producer's real VPC, confirmed by
reading it back independently with the AWS CLI. The readback, rather than
the plan alone, is what verifies the value flow.

```
TF_FLOCI_TEST=1 go test ./internal/live/lifecycle/ -run TestCrossEstateDataSourceAgainstFloci -v
```
