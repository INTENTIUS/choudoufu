---
title: Under Terragrunt
description: Swap the binary and add a live block to each unit. run --all plans the units once and applies the reviewed plan once.
prompt: >-
  Read https://intentius.io/choudoufu/docs/use/terragrunt/ and list what this
  repository's Terragrunt units would change to run under choudoufu, unit by
  unit. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

## What you change

1. `terraform_binary = "choudoufu"` in the file every unit includes, or
   `TG_TF_PATH=choudoufu` in the environment.
2. A `live` block in each unit, naming its estate and the record store:

```hcl
# network/live.tf
terraform {
  live {
    estate = "network"

    record_store "s3" {
      bucket = "my-records-bucket"
    }
  }
}
```

`include`, `inputs`, `dependency` blocks with `mock_outputs`, generated
providers and `remote_state` stay as they are. For a unit that already owns
resources, pull its state with the stock binary and follow
[Migrate](/choudoufu/docs/use/migrate/) before the first apply.

## What you get

| | Stock Terragrunt | On the live backend |
|---|---|---|
| One plan covers every unit | No, one per unit | Yes |
| A changed value reaches the units that read it | No, a `dependency` reads the last applied outputs | Yes |
| A new ID reaches the units that read it | Only as `mock_outputs` | Yes, as known after apply, and the mock is not planned |
| The approved plan is what applies | `--out-dir` saves each unit's plan, mocks included | Yes, and choudoufu refuses a plan that no longer matches |
| Applies | One per unit | One |

## The workflow

The pull request plans and keeps the `plans` directory. The merge applies
those files, with nothing planned first:

```
terragrunt run --all --out-dir plans -- plan
terragrunt run --all --out-dir plans -- apply
```

Every plan file carries the whole joint plan, so the merge's job needs only
that directory. The first unit's apply applies it once.

## Limits

- One apply needs the saved plan files. `run --all apply` without `--out-dir`
  applies the units one at a time, each reading its upstream's recorded
  outputs.
- A `dependency` read is taken from the joint plan when it feeds an input
  directly, or through OpenTofu functions. An input that also calls `get_env`,
  or a value read through `read_terragrunt_config`, keeps Terragrunt's value.
- If your pipeline runs `show -json` on the plan files, add `show` to
  `mock_outputs_allowed_terraform_commands`.

Shared limits are on [One plan, one apply, across roots](/choudoufu/how-it-works/across-estates/),
and a worked example is
[`examples/demo-terragrunt`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/demo-terragrunt/README.md).
