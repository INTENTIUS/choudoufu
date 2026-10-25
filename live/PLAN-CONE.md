# Planning a change's cone

GitHub issue #1956. A pull request's plan can read less than the whole
estate. `choudoufu live-cone BASE` prints the cone of the change between
`BASE` and the working tree, one target per line, or as JSON with `-json`.
`choudoufu plan -cone=BASE` plans that cone. Neither is ever the default.

## What is in the cone

The root module's blocks are compared between `BASE` and the working tree,
whitespace aside. The base side is the same directory at `BASE`: the
root's path from the repository top, as git resolves it from the working
directory's real path, so a root at the top, one or more levels down, or
reached through a symlink reads its own files at `BASE` (#2062). Several
estates in one repository, each a root in its own subdirectory, each get
their own cone. The cone is:

- every resource, data, module, variable, local and output block whose
  source changed, and every variable `terraform.tfvars` or `*.auto.tfvars`
  sets differently (a `-var-file` is not read);
- every local module call with a changed file under its source directory,
  a `./` or `../` source resolved against the root's subdirectory (a
  source that leaves the repository is outside the diff and never marks
  the call changed);
- every resource, data and module block deleted since `BASE`;
- the addresses added or changed `moved`, `removed` and `import` blocks name;
- every block that reads any of these, transitively;
- every block of a co-managed type paired with a type in the cone (a
  security group and its rules, a route table and its routes, a role and
  its policies, a manifest and its labels).

The plan graph adds what the cone reads, as it does for any `-target`, and
writes an output only when everything it reads is planned.

## What the cone plan does differently

- The scope holds the deleted and refactored addresses, so their removals
  are proposed rather than withheld.
- The cache-vouch listing narrows to the cone's types. The marker sweep
  stays estate-wide. To read one resource without the sweep or a listing of
  the record store, use `live-plan -resource=ADDR`
  ([PLAN-RESOURCE.md](PLAN-RESOURCE.md)).
- `-out FILE` writes `FILE.cone.json`, the cone's address set. `apply FILE`
  replans that cone and refuses with exit 3 when its plan covers a
  different set. A plan file without one applies as before.

## The guard

The plan covers the whole estate instead, with a warning naming why, when:

- `BASE` is not a commit, or either side does not parse;
- the root's directory did not exist at `BASE`, or held no configuration
  there (`root-new-at-base`): there is nothing to diff against;
- a provider block, the `terraform` block, the `live` block or the lock
  file changed;
- the root module has JSON configuration;
- a `moved` or `import` block names an address nothing declares;
- the cone reaches nothing, or holds over 30% of the estate's blocks;
- the sweep finds a marked object outside the cone that nothing declares;
- `-estate-outputs-from` is given, since another estate's planned outputs
  are a change the diff cannot show.

`-cone` is refused with `-target`, `-exclude`, `-destroy`,
`-refresh-only`, `-json` and on a configuration without a live block.

## What it does not do

Drift outside the cone is not reported: merge and scheduled drift plans
should stay full. Data sources are not compared with the cache, so a data
source whose value moved (a latest AMI) widens nothing. The binary version
is not compared.

## What a plan costs

A plan reads the live system and not a file, so it makes more API calls than
a plan that trusts a state file. How many more depends on which of two things
it has to do. The cone above narrows the first.

### The two terms

The read pass reads each resource you own. It costs what stock's refresh
costs for the same resources, call for call.

The sweep looks for resources carrying your estate's marker that no block
declares. Its width comes from how many resource types are admitted, and not
from how big your estate is. It is the expensive one.

An established estate does not pay the full sweep. Its own records say which
types it holds, so the sweep narrows to those. The first plan of a new estate,
an adoption, and an audit have nothing to narrow by and sweep in full.

### The numbers

Measured on a 79-instance estate against the pinned emulator, at commit
`fce6b53e20` on 2026-09-12T13:33:13Z (the newest `terralith-scale` scale-1
record in `live/gauntlet-scale.json`):

| | choudoufu | stock |
|---|---|---|
| An ordinary plan | 186 calls | 150 |
| The same plan again | 186 | |
| A full-sweep audit | 776 | 150 |

These are API calls against an emulator. They have not been measured in money,
and wall-clock time on an emulator says little about AWS.

### Turning a phase down

Each phase has its own bound on how many calls it has in flight. Neither is
stock's `-parallelism`, which bounds the graph walk.

| Variable | Bounds | Default |
|---|---|---|
| `TOFU_LIVE_SWEEP_PARALLELISM` | the sweep's per-type list calls | 10 |
| `TOFU_LIVE_READ_PARALLELISM` | the read pass's per-instance import and read | 10 |

Lower them if an account's APIs push back, or plan a cone (above).
`-refresh=false` serves unchanged resources from
[the cache](https://intentius.io/choudoufu/how-it-works/cache/) and skips
their reads.

### The full measurements

Every figure, fixture and commit behind this section is in the repository:
[`live/costs/plan-cost.md`](costs/plan-cost.md) for the sweep and read pass
at each scale, and [`live/costs/what-you-pay.md`](costs/what-you-pay.md)
for adoption, migration, day two at scale and the real-AWS runs. The machine
readable records are `live/gauntlet-scale.json`.
