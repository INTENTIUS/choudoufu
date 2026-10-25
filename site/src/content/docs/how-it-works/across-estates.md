---
title: One plan, one apply, across roots
description: Roots that read each other plan together and apply the reviewed plan, with Terragrunt, Atmos or a composed root.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/across-estates/ and say
  which way in fits this repository: Terragrunt, Atmos or a composed root.
  Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Teams split an estate into roots so that one state lock covers less. On stock
backends each root then plans alone, and a root that reads another plans on
that root's last applied outputs, or on a mock when the value does not exist
before the apply. The choudoufu live backend joins the roots in one plan.

## Five questions

| | Stock backends | choudoufu live backend |
|---|---|---|
| One plan covers every root | No, one per root, unit or component | Yes |
| A changed value reaches the roots that read it | No, they read the last applied outputs | Yes, from the joint plan |
| A new ID reaches the roots that read it | Only as a mock or a default | Yes, as known after apply, filled in by the apply |
| The approved plan is what applies | Each root's saved plan, mocks included | Yes, and choudoufu refuses a plan that no longer matches |
| Applies | One per root | One, or one per component under Atmos |

Under [Terragrunt](/choudoufu/docs/use/terragrunt/) or
[Atmos](/choudoufu/docs/use/atmos/), swap the binary and add a `live` block to
each unit or component. A [composed root](#a-composed-root) needs neither.

## How the roots join

Terragrunt and Atmos still start choudoufu once per unit. choudoufu sees that
the invocations belong to one `--all` or `--affected` run and plans them
together. An input that reads another unit's output is taken from that plan,
in place of the tool's mock or last applied value. A changed value arrives as
planned, and a value that exists only after apply arrives as known after apply. The plan made
after the last unit covers them all.

Each saved plan file carries the joint plan. Before an apply changes anything,
choudoufu plans the live system again and refuses, with exit status 3, if the
result is not the reviewed plan. A value that was known after apply is not
compared.

A `backend` block beside a `live` block is ignored with a warning, whether you
wrote it or the tool generated it. After an apply, choudoufu writes the root's
non-sensitive outputs where that backend points. Readers that use
`terraform_remote_state` or Atmos's `!terraform.state` keep working.

## Limits shared by every way in

- A read choudoufu cannot trace to the input it feeds keeps the tool's value.
  If the joint plan changes what it reads, the plan fails and names the read.
- A run of one unit, or a flag the joint plan cannot honour, such as `-target`
  or `-destroy`, takes the ordinary path. `CHOUDOUFU_JOINT_RUN=off` turns
  joining off.
- A failed apply leaves every applied resource applied and recorded under its
  own estate. Nothing is rolled back, and a re-run plans only what is left.

## A composed root

A root with no `live` block of its own that calls each estate's directory as a
module plans all of them in one plan and applies them in one apply. No flag
turns it on. An estate reads another through `terraform_estate_outputs`, in a
file that is the same whether it runs alone or composed.

```hcl
# root/main.tf
module "net" { source = "../net" }
module "app" { source = "../app" }
```

Each module keeps its estate name, record store and markers, and a marker
carries the address the resource has in its own directory (`aws_vpc.main`, not
`module.net.aws_vpc.main`). The root holds only module calls with no arguments,
outputs and variables, and choudoufu refuses `-target`. Roles stay per estate when
each module's provider block assumes its own. The rest is under "A composed
run" in
[`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md).

Three cases have not been run against a record store yet
([#2208](https://github.com/INTENTIUS/choudoufu/issues/2208)): an estate run
alone after a composed run, a re-run after a composed apply that failed part
way, and a destroy of a composed root.
