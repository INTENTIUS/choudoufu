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
  stays estate-wide.
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
is not compared. The saving is unmeasured until
[claim 47](smoke/claims/plan-cost-tracks-the-change.md) runs.
