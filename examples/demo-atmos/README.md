# The Atmos and choudoufu demo's tree

A stock Atmos repo: three components (`network`, `app`, `edge`) under
`components/terraform/`, one stack (`stacks/deploy/dev.yaml`) with `vars` per
component, and `!terraform.output` between them. app reads network's outputs
(`subnet_a_id`, `vpc_id`, `http_proxy_port`, and `subnet_b_id` in the create
act); edge reads app's `listener_port` and network's subnet ranges.
`dependencies.components` orders them. The demo's Forgejo
workflow (`.forgejo/workflows/atmos-choudoufu.yml`) runs the Atmos CLI.

## What differs from a plain Atmos repo

Two things, and no others in the Atmos configuration or the stack (the
planfile store and `ci` settings in `atmos.yaml` are Atmos's own):

1. `components.terraform.command: choudoufu` in `atmos.yaml`.
2. A `live` block in each component's `terraform {}` block, naming its estate
   and the record store that holds its resources' records, in place of a state
   file.

Workspaces are on (Atmos selects `dev` for the stack, and choudoufu accepts it
and warns that the estate is the identity), `init -reconfigure` is Atmos's
default, and Atmos writes `backend.tf.json` from the stack's S3 backend section
(`auto_generate_backend_file: true`, which is a normal Atmos setting and not a
choudoufu one). choudoufu ignores that backend, and after each apply publishes
the component's outputs to it, so `!terraform.state` and `terraform_remote_state`
readers keep working.

Also not about choudoufu: the stack's S3 backend sets `use_path_style` and the
`skip_*` flags, and each component's provider names the emulator's keys,
because the demo runs against the floci emulator.

Not part of the Atmos repo's own configuration: the workflow's report step
(`.forgejo/composed-plan.mjs`) joins the components' slices of the joint plan
into the one document chant's `terraform report` reads for behold.

## The workflow

- Pull request: `atmos terraform plan --affected --include-dependents --ci`.
  That is one plan across the components the change reaches. choudoufu joins
  the per-component invocations (it finds them from the parent `atmos`
  process), so a value that exists only after apply, such as a new subnet's id,
  is carried into the component that reads it and shows as "known after
  apply". Atmos keeps each component's plan in its planfile store
  (`components.terraform.planfiles`, an S3 bucket on the demo's emulator),
  keyed by the pull request's head commit.
- Merge: the repo merges fast-forward only, so the merge pushes the pull
  request's head commit, which is the key of the stored plans. The workflow
  checks for an approving review from someone other than the author and runs
  `atmos terraform deploy --affected --include-dependents --ci`. The deploy
  applies network, then app, then edge, each in its own apply. For each one it
  takes the stored plan, plans again and applies the fresh plan once it
  matches.
- Each component applies exactly as reviewed. Atmos counts a value that was known
  after apply in the stored plan and is known now (app's worker's subnet, once
  network has applied) as drift, and no honest plan can make the two compare
  equal, so `planfiles.verify` is `warn` and `planfiles.required` is `true`
  (Atmos docs: components/terraform/planfiles, "Drift Verification").
  choudoufu does the refusing. When Atmos shows it a component's stored plan,
  it keeps that component's slice, and the apply of the fresh plan refuses
  with exit status 3 if it differs from the slice in anything but those
  values. The refusal names the command that plans again.

## The contrast

In a stock Atmos repo, `!terraform.output network subnet_b_id` reads network's
applied state. Before network has applied, app gets a default or a mock, or the
plan fails. Here app's plan carries the id as unknown.

## `!terraform.state`

The demo reads outputs with `!terraform.output`. `!terraform.state` works the
same way under choudoufu, with no other change to the repo:

- Atmos reads `!terraform.state` straight from the component's S3 backend,
  without running choudoufu. What is there is the outputs document choudoufu
  publishes after each apply, so Atmos hands app network's last-applied
  outputs: 8080 in the edit act, and nothing (`null`) for `subnet_b_id` in the
  create act, since network has not applied it.
- choudoufu does not plan on those values. It reads the stack the way Atmos
  resolves it (`atmos describe stacks` with functions and templates left
  unprocessed), and a var that is `!terraform.state <component> <output>` or
  `!terraform.output <component> <output>`, with a plain output name or a
  path such as `.<output>.key` or `.<output>[0]`, becomes a reference to that
  component's module in the joint plan. Both acts plan as they do with
  `!terraform.output`: app with network's planned port, and the worker's
  subnet as known after apply.
- After the deploy, each component's outputs are published to its own key
  under the stack's workspace (`network/dev/terraform.tfstate`), so the next
  `!terraform.state` read sees them.
- A read choudoufu cannot trace to one var (a yq expression with `//` or
  quotes, `!store`, a template calling `atmos.Component`, a function nested
  inside a list or map) keeps Atmos's value. That value is right only while the
  upstream's outputs do not change, so if the joint plan changes them, the
  plan fails and names the read.

## Limits

- An output read is taken from the joint plan when choudoufu can trace it to a
  var (above). One it cannot trace stops a plan that changes what it reads.
- `--affected` plans what Atmos names, so `--include-dependents` is needed for
  components that read a changed one.
- The apply is not all or nothing. If edge fails, network and app stay applied.
- Atmos's stored plans are keyed by commit, which is why the repo merges
  fast-forward only. A pull request that is behind `main` is rebased and
  planned again before it merges.
- Atmos's deploy applies the components in turn, each in its own apply, so app
  reads network's real outputs when its turn comes.

## The scenarios

The base is a running estate that matches the demo host's compose stack.
network holds `aws_vpc.main`, `aws_subnet.subnet_a` (10.0.1.0/24) and the
`http_proxy_port` terraform_data. app holds `aws_security_group.web`,
`aws_network_interface.web` in subnet a and the `listener_port` terraform_data.
edge holds `terraform_data.allowlist` (`["10.0.1.0/24"]`) and the `upstream`
terraform_data.

Each scenario has `pull/` (the pull request's files, components and stack),
`ask.json` (the prompts and replies behold's pane plays) and `about.json`.
No scenario replaces or destroys anything.

- `atmos-choudoufu-edit`: network's `http_proxy_port` goes from 8080 to 8090
  and it gains `admin_port` 9000. app adds `admin_listener_port` and edge
  `admin_upstream`. 3 to add, 3 to change, 0 to destroy, every value known at
  plan.
- `atmos-choudoufu-create`: network adds `aws_subnet.subnet_b` (10.0.2.0/24)
  with outputs `subnet_b_id` and `subnet_b_cidr`. app adds
  `aws_network_interface.worker` in that subnet, reading `subnet_b_id`; edge's
  allowlist gains the new range. 2 to add, 1 to change, 0 to destroy. One value
  is unknown at plan: subnet b's id.

The demo host's loop applies the base with `atmos terraform apply` in the demo image,
outside the workflow, and each reset applies it again.
