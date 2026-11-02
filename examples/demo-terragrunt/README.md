# Terragrunt with choudoufu as its binary

A stock three-unit Terragrunt repo. Switching to choudoufu changed two things:
the binary, and a `live` block in each unit. Nothing else about the layout
moved.

```
root.hcl                 terraform_binary = "choudoufu"; a generated provider; remote_state
network/terragrunt.hcl   include "root", inputs
network/main.tf          the unit's resources
network/live.tf          the live block: estate name and record store
app/                     the same files; a dependency block on network
edge/                    the same files; dependency blocks on app and network
```

## What is choudoufu-specific

- `terraform_binary = "choudoufu"` in `root.hcl` (`TG_TF_PATH` does the same
  from the environment). Terragrunt runs it where it would run tofu.
- A `live` block in each unit (`network/live.tf`, `app/live.tf`,
  `edge/live.tf`): the estate's name and the record store that holds one record
  per resource. It is needed once per unit, so there are three of them. It sits
  in its own file so the unit's other files are the ones a plain repo would have.

Everything else is stock: `include`, `inputs`, `dependency` blocks with
`mock_outputs`, a generated provider, and the S3 backend declared with a
`remote_state` block, as Terragrunt's state backend docs show it. Terragrunt
runs with its defaults, including reading a dependency's outputs straight from
that backend's state (the default since v1.2.0). choudoufu ignores the backend
for state (an estate keeps records, not a state file) and says so with a
warning; after an apply it writes the unit's outputs to the key the backend
names, which is what Terragrunt then reads.

What differs from the docs' example, and why:

- The provider's `access_key`, `secret_key`, `skip_*` and `s3_use_path_style`,
  and the backend's `use_path_style`, are for the floci emulator the demo runs
  against. The endpoints come from the environment.
- `mock_outputs` with `mock_outputs_merge_strategy_with_state = "shallow"` is
  how Terragrunt's docs have a plan read an output its dependency has not
  applied yet, which the create act adds. Stock Terragrunt plans on the mock;
  with choudoufu the mock is never used.
- `mock_outputs_allowed_terraform_commands` lists `show` because the workflow's
  report step runs `terragrunt run -- show -json <plan file>` in each unit.

`live/demo_workaround_lint_test.go` (`TestTerragruntDemoCarriesNoWorkaround`)
fails if the tree, its scenario trees included, gains a workaround this demo
once needed: a `generate "backend"` block in place of `remote_state`,
`--no-dependency-fetch-output-from-state` or its `TG_` variable,
`dependency_fetch_output_from_state = false`, `terraform_estate_outputs`, or a
composed root (a module call to a directory with a `live` block). It also
checks that `root.hcl` sets `terraform_binary = "choudoufu"` and keeps the
`remote_state` block. It catches workarounds seen so far; a new kind is left
to review.

## The workflow

`.forgejo/workflows/terragrunt-choudoufu.yml` runs Terragrunt's own CLI.

- On the pull request: `terragrunt run --all --out-dir <dir> -- plan`, posted
  as a comment. `<dir>` holds one plan file per unit, and the job keeps it in
  the demo's bucket under the pull request's head commit.
- On the merge: the job checks for a person's approving review of the merged
  pull request and stops without one, fetches that `<dir>`, and runs
  `terragrunt run --all --out-dir <dir> -- apply`. Nothing is planned again
  first: the plan applied is the plan reviewed on the pull request.
- Each plan file carries the whole joint plan, so the merge's job needs nothing
  else from the pull request's job. Before it changes anything, choudoufu plans
  the live system again and refuses, with exit status 3, if the result is not
  the reviewed plan (a value that was known after apply is not compared). The
  refusal names the command that plans again.
- The plan files keep an HMAC of each sensitive planned value, not the value.
  Both jobs read the key from the `CHOUDOUFU_PLAN_HMAC_KEY` secret (hex or
  base64 of at least 32 bytes). With the secret unset, a merge job applying
  a plan with a known sensitive value refuses, since the key stayed with the
  pull request's job.
- After each stage, chant's `terraform report` writes what behold reads. That
  step is not part of running the infrastructure. `terragrunt-plan-json`
  (on the demo host) joins the units' `show -json` documents for it, and
  turns each `dependency` block into the read between estates that behold draws.

## How the single plan works

Terragrunt starts choudoufu once per unit, in dependency order. choudoufu finds
that the invocations share one `terragrunt run --all` parent and joins them:
each unit registers with its inputs, and the plan is made over the units
registered so far, so the plan after the last unit covers all three.

Terragrunt hands app network's outputs as it read them from network's state,
which holds what was last applied (8080 in the edit act, the mock for
`subnet_b_id` in the create act). choudoufu does not plan on those values. It
reads the unit's own `terragrunt.hcl` and its include, and an input written as
`dependency.network.outputs.<output>` (or an expression of such reads and
OpenTofu's functions, like edge's `[dependency.network.outputs.subnet_a_cidr]`)
becomes a reference to network's module in the joint plan. So app plans with
network's planned port, 8090, and app's worker shows its subnet as
`(known after apply)` and receives the real id in the same apply.

A read choudoufu cannot trace to the input it feeds (an input that also calls
`get_env`, a `read_terragrunt_config`) keeps Terragrunt's value. That value is
right only while the upstream's outputs do not change, so if the joint plan
changes them, the plan fails and names the read.

Each unit's plan file carries the joint plan, and applying network's applies
the whole joint plan once; the other units' applies report that it is done.

## Against stock Terragrunt

Stock `run --all plan` plans each unit as its own run. In the create act, app
reads `dependency.network.outputs.subnet_b_id` before network has created the
subnet, so the dependency block needs `mock_outputs` (and
`mock_outputs_merge_strategy_with_state = "shallow"`, since network's state
exists but lacks the new output) and the plan reviewed for app holds the mock.
With choudoufu the blocks are the same, the mock is never used, and the plan
shows the real thing: the id is unknown until apply. Stock Terragrunt also
cannot apply a saved `run --all` plan honestly, because the plan file of app
holds the mock; here the saved plan holds `(known after apply)` and the one
apply fills it in.

## Limits

- The one-apply path needs saved plan files. `terragrunt run --all apply`
  without `--out-dir` applies the units one at a time, in order, each reading
  its upstream's recorded outputs.
- A dependency read is taken from the joint plan when choudoufu can trace it
  to an input (above). One it cannot trace stops a plan that changes what it
  reads.
- The joint run is found from the parent process's name and arguments, so a
  wrapper script around `terragrunt` is not recognised.
- A failed joint apply leaves what applied, applied; every unit's apply reports
  the failure, and a re-run plans only what is left.
- `mock_outputs_allowed_terraform_commands` lists `show` because the report
  step runs `terragrunt run -- show -json <plan file>` in each unit.

## The pipeline run: terragucci

terragucci runs this tree, as its harness scenario
[`tg-compose-terragrunt`](https://github.com/INTENTIUS/terragucci/blob/8ad15b839f2c6c60219dc9483eba2cb2c9b63421/stack/harness/scenarios/tg-compose-terragrunt/scenario.json)
(claim `choudoufu/compose-terragrunt`, INTENTIUS/terragucci#1296). The tree
stays here: terragucci's `stack/harness/seed.sh` reads it from choudoufu
the way its `stack/demo.sh` does, and pushes it to the stack's Forgejo with
`DEMO_SCENARIOS=terragrunt-choudoufu-create stack/demo.sh up` running for
the job images and the record store. The create scenario's `pull/` files
are the pull request; `.forgejo/workflows/terragrunt-choudoufu.yml` plans
it with choudoufu, the second person approves, the merge applies the
reviewed plan, and network's records must be in its record store. Its
`BREAK=1` adds a resource live-check refuses to every pull request, so the
plan fails and nothing applies. The row is the product proof for
choudoufu#2213, #2219 and B6.

## The base

Every scenario starts from a running estate that matches
the demo host's compose stack. network holds `aws_vpc.main`,
`aws_subnet.subnet_a` (10.0.1.0/24) and the `http_proxy_port` terraform_data.
app holds `aws_security_group.web`, `aws_network_interface.web` in subnet a
and the `listener_port` terraform_data. edge holds `terraform_data.allowlist`
(`["10.0.1.0/24"]`) and the `upstream` terraform_data. The demo host's loop applies
the base outside the workflow, and each reset applies it again, downstream first.

## Scenarios

- `terragrunt-choudoufu-edit`: network's `http_proxy_port` input goes from 8080
  to 8090 and it gains an `admin_port` of 9000. app adds an
  `admin_listener_port` and edge an `admin_upstream`. 3 to add, 3 to change,
  0 to destroy, every value known at plan.
- `terragrunt-choudoufu-create`: network adds `aws_subnet.subnet_b`
  (10.0.2.0/24) and outputs `subnet_b_id` and `subnet_b_cidr`. app reads
  `dependency.network.outputs.subnet_b_id` for one `aws_network_interface.worker`,
  and edge's allowlist gains the new range. 2 to add, 1 to change, 0 to destroy.
  One value is unknown at plan: subnet b's id, at its source and in the
  worker's `subnet_id`.
