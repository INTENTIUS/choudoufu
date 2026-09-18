# Two estates, one depending on the other

A network estate owns a VPC. A service estate owns a subnet inside that VPC.
The service estate needs the VPC's id, and it gets it without the network
estate publishing anything and without either side storing a copy.

```
terraform/network/     estate "cross-estate-network"   aws_vpc.main       (no output block)
terraform/service/     estate "cross-estate-service"   data.aws_vpc.network + aws_subnet.app
src/estates-apply.op.ts   one Op, two phases: Network, then Service
```

The consumer's whole read is four lines:

```hcl
data "aws_vpc" "network" {
  filter {
    name   = "tag:tofu-estate"
    values = [var.network_estate]      # "cross-estate-network"
  }
  filter {
    name   = "tag:tofu-address"
    values = [var.network_vpc_address] # "aws_vpc.main"
  }
}
```

Both tags are already on the VPC. choudoufu writes `tofu-estate` and
`tofu-address` onto every taggable managed resource in a live root, and the
pair is unique within the account by construction: an address is unique within
an estate, and an estate is one management domain. So the consumer needs no
naming convention invented for its benefit, and the producer needs no
arrangement at all — it does not know it is being read.

`live/OUTPUTS.md` is the decision this demonstrates: read the producer's live
resource with a data source of its own type, with no new construct, no
namespace and no lint rule.

## What there is no copy of

The producer declares no `output` block. That is not a stylistic choice; it is
the point.

A stock split-root layout publishes `output "vpc_id"` in the producer and
reads it back in the consumer through `terraform_remote_state`. That data
source reads the producer's **state file**. A live root writes no state file of
record: ownership is the marker tags on the resources themselves. choudoufu
keeps a state *cache* — `.terraform/choudoufu-cache.tfstate`, and its contents
are candidates verified against the tag index on every run, never facts
trusted — but nothing consults it for ownership, and deleting it changes no
plan. There is nothing for a remote-state read to point at.

Which is worse than it sounds, because `terraform_remote_state` is no longer
refused. The lint rule that refused it was removed under #179 stage 3, and
`site/content/docs/use/compatibility.md` now lists it under constructs
choudoufu used to refuse and does not. So a producer that migrates to live
markers while a consumer still reads its old state file gets no error at all:
the file is still there, the read still resolves, and what comes back is a
snapshot frozen at migration time — real-looking values, silently stale, with
no "abandoned as of" marker on the file to give it away.

The data source has no such failure mode. It resolves against the live system
on every plan, through the provider's own read contract. There is no snapshot
to go stale, and no moment where the two estates disagree about what the id is.
`scripts/prove.sh` check 3 deletes the saved plan and the state cache and then
greps the service root for the VPC id — it is in no file — and check 4 replans
from that empty starting point and still resolves it.

## Why two estates

Because an estate is the unit of ownership. `live/MARKERS.md`: everything
carrying the same `tofu-estate` value is one management domain, matched by one
`live` config block.

Suppose you gave both roots the same estate name, on the reasoning that they
describe one system. Then:

- the network root plans. Its sweep is **estate-scoped**, not root-scoped: it
  asks the account for everything tagged with that estate. It finds the
  subnet, which its own configuration does not declare, and classifies it
  `undeclared_tagged` — a resource this estate owns and no longer declares.
  The default verb for that quadrant is delete. It proposes destroying the
  subnet.
- the service root plans. It finds the VPC the same way, reaches the same
  conclusion, and proposes destroying the VPC.

Two roots, each proposing to destroy the other's resources, on every run. Not
a race or an edge case — the steady state. `live/LIMITATIONS.md` states the
split positively: give the module an estate of its own, with its own
directory, its own live block, and its own estate name. Two estates are two
independent runs.

Splitting them is what creates the question this example answers, and the
answer is the data source above rather than a shared state file.

## Why the ordering is an Op and not a config field

The producer must apply before the consumer. That is the entire operational
content of a dependency between two estates, and there is nowhere in the
configuration to say it.

chant's `terraformRootSchema` is `{dir, workspace, varFiles, backendConfig,
delete}`. There is no `dependsOn`, here or anywhere in the terraform lexicon,
and `TerraformApplyOp` takes a single root positionally — two of those would be
two Ops with nothing between them but a habit.

Ordering in chant is phase order inside one Op. Phases run in sequence and a
failing phase ends the run, so `src/estates-apply.op.ts` takes both roots:

```
phase "Network"   init, plan, apply   root=network
phase "Service"   init, plan, apply   root=service
```

`chant.config.ts` names the two roots. That Op file is the only place that
says which comes first, and `tests/pipelines.test.ts` reads the built
`op.json` to hold it there — the assertion has to read the Op, because no byte
of generated YAML expresses the order. A forge generator emits one job per Op,
so the CI job just runs `chant run estates-apply`.

### What happens if the first one fails

The `Service` phase never starts. That is the right answer and not merely the
default one: the consumer resolves the producer's VPC by its marker tags at
plan time, so a service plan against a producer that did not apply fails on
the read — `no matching EC2 VPC found` — rather than building something
against a stale id. There is no cached value to fall back on. Run
`BREAK=1 scripts/prove.sh` to watch exactly that.

What is left behind is the other half of the question, and the Op's
`onFailure` phase answers it in words rather than pretending to undo it.
Terraform has no rollback: a partial apply is undone by planning and applying
the inverse, which is a decision about the estate and not something a
generated phase can make. What is certain is that both roots are idempotent
under re-run — a live root rebuilds prior state from the live system every
time — so the remedy is to fix the cause and run the Op again.

## The CI

`generate.ts` emits one tree per forge from the same two Ops, checked in beside
them:

```
github/.github/workflows/    estates-plan.yml, estates-apply.yml
forgejo/.forgejo/workflows/  estates-plan.yml, estates-apply.yml
gitlab/ops.gitlab-ci.yml     both jobs in one file
```

`estates-plan` runs on a pull request to `main` and writes nothing.
`estates-apply` runs on a push to `main`. The generated job names are the
names a branch-protection rule can require;
`examples/pipeline-governance` is what a policy naming them looks like.

Credentials are per Op, not per repository: the pull-request job assumes a role
that can only read, and only the push job holds one that can write to both
estates. GitHub and GitLab mint theirs per job over OIDC; Forgejo Actions mints
no OIDC token, so its jobs carry a static key pair each, declared on the Op's
own spec rather than workflow-wide.
`examples/ci-pipelines/generate.ts` carries the long version of all of that —
this project is the same wiring with three roles collapsed to two.

Regenerate with `npm run generate`. The checked-in trees must be what the
generator emits, byte for byte: `tests/pipelines.test.ts` regenerates into a
scratch directory and diffs, and `live/cross_estate_dependency_test.go` is the
backstop for choudoufu's own CI, which has no node — it re-hashes the
generator inputs against the stamp in `generated-from.json`.

Two things about the generated YAML worth knowing before you copy it:

- Neither Op declares a gate, so neither run ever stops for an approval. The
  forge generators still emit a `*-gate-notice` job that fires only when a run
  comes back `gated`, which for these two Ops is never. If you add a gate,
  that job becomes live and needs `gh` on the image —
  `examples/ci-pipelines`' `live-apply` is the shape to copy.
- Neither root sets a `workspace` and neither declares a `backend` or `cloud`
  block. TF025 refuses a non-default workspace on a live root and TF024
  refuses a backend, both for the same underlying reason: those are state-file
  machinery, and there is no state file. Two estates are the separation a
  workspace would have been reached for.

Var files are the third thing to leave alone. `choudoufuLivePlanCommand` never
passes `-var-file`, so a live root planned through `choudoufuLivePlan` ignores
declared var files silently and no lint rule catches it. Both roots here take
their variables' defaults, and the pipelines pass `TF_VAR_aws_region`.

## Running it

Tests, which need no cloud and no docker:

```
npm install
npm test
```

The live proof, against the pinned floci emulator:

```
scripts/prove.sh
```

It builds `./cmd/choudoufu` from this worktree, starts floci on a port the
kernel picks, copies the project to a scratch repository, and prints one
greppable verdict line per check:

```
PROVE check=op-applies-both-estates verdict=pass rc=0
PROVE check=value-crossed-estates   verdict=pass subnet=subnet-088dd5e7 vpc=vpc-59604772
PROVE check=two-estates-two-owners  verdict=pass cross-estate-network / cross-estate-service
PROVE check=no-state-file           verdict=pass no terraform.tfstate under either root
PROVE check=no-stored-copy          verdict=pass vpc-59604772 appears in no file under terraform/service
PROVE check=reresolves-clean        verdict=pass second plan: No changes
```

The value check reads the subnet's real `VpcId` back with the AWS CLI and
compares it to the producer's VPC id. A plan that merely succeeds would prove
nothing; this is the value actually having crossed. `BREAK=1` points the
consumer at an estate nothing ever applied, which turns four of the six checks
red — including that one — so the checks are known to be able to fail.

Against a real account instead, with credentials already in the environment:

```
cd terraform/network && choudoufu init && choudoufu apply
cd ../service       && choudoufu init && choudoufu apply
```

in that order, which is what the Op does.

## Related

- `live/OUTPUTS.md` — the decision, and why it is a data source rather than a
  new output surface built on receipts.
- `live/MARKERS.md` — the marker pair, and the estate as the unit of ownership.
- `internal/live/lifecycle/cross_estate_live_test.go` — the same estate pair
  proved against floci in Go, which is the shape these two roots are copied
  from.
- `examples/ci-pipelines` — one estate, five Ops, gated applies, and the
  per-forge posting table this project deliberately does not repeat.
