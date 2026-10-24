# Terragrunt with choudoufu as its binary

A stock three-unit Terragrunt repo. Switching to choudoufu changed two things:
the binary, and a `live` block in each unit. Nothing else about the layout
moved.

```
root.hcl                 terraform_binary = "choudoufu"; a generated provider and backend
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
`mock_outputs`, a generated provider and a generated `backend.tf`.
choudoufu ignores that backend for state (an estate keeps records, not a state
file) and says so with a warning; after an apply it writes the unit's outputs
to the key the backend names.

One choice follows from how Terragrunt reads dependencies. The backend is a
`generate` block, not a `remote_state` block. With `remote_state`, Terragrunt
reads a dependency's outputs straight from that state and never runs
`output -json`, and a value that exists only after apply is not in the state
yet. With `generate`, Terragrunt asks the binary, and choudoufu answers from
the joint plan.

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
- After each stage, chant's `terraform report` writes what behold reads. That
  step is not part of running the infrastructure. `terragrunt-plan-json`
  (on the demo host) joins the units' `show -json` documents for it, and
  turns each `dependency` block into the read between estates that behold draws.

## How the single plan works

Terragrunt starts choudoufu once per unit, in dependency order. choudoufu finds
that the invocations share one `terragrunt run --all` parent and joins them:
each unit registers with its inputs, and the plan is made over the units
registered so far, so the plan after the last unit covers all three. When
Terragrunt asks network for its outputs, choudoufu answers from that plan. An
output that exists only after apply is handed over as a placeholder string,
which becomes a reference in the joint plan, so app's worker shows its subnet
as `(known after apply)` and receives the real id in the same apply.
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
- Only a value that is a string can be carried as unknown through the
  dependency. A number, bool or object that depends on another unit's
  after-apply value cannot.
- The joint run is found from the parent process's name and arguments, so a
  wrapper script around `terragrunt` is not recognised.
- A failed joint apply leaves what applied, applied; every unit's apply reports
  the failure, and a re-run plans only what is left.
- `mock_outputs_allowed_terraform_commands` lists `show` because the report
  step runs `terragrunt run -- show -json <plan file>` in each unit.

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
