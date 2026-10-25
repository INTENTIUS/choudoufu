---
title: Under Atmos
description: Swap the command and add a live block to each component. --affected plans the components once and applies each one as reviewed.
prompt: >-
  Read https://intentius.io/choudoufu/docs/use/atmos/ and list what this
  repository's atmos.yaml and components would change to run under choudoufu.
  Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

## What you change

1. `components.terraform.command: choudoufu` in `atmos.yaml`.
2. A `live` block in each component's `terraform` block, naming its estate and
   the record store:

```hcl
terraform {
  live {
    estate = "app"

    record_store "s3" {
      bucket = "my-records-bucket"
    }
  }
}
```

The stack and its `vars` stay as they are, with `!terraform.output` and
`!terraform.state` between components. Workspaces and the generated
`backend.tf.json` stay too. choudoufu accepts the workspace and
`init -reconfigure` with a warning each. For a component that already owns
resources, follow [Migrate](/choudoufu/docs/use/migrate/) before the first
apply.

If you keep plans in Atmos's planfile store, set `planfiles.verify: warn` and
`planfiles.required: true`. Atmos counts a value that was known after apply in
the stored plan, and is known now, as drift, so a stored plan that reads a new
ID cannot pass its check. choudoufu does the refusing instead.

## What you get

| | Stock Atmos | On the live backend |
|---|---|---|
| One plan covers every component | No, one per component | Yes |
| A changed value reaches the components that read it | No, `!terraform.output` reads the last applied outputs | Yes |
| A new ID reaches the components that read it | Only as a default or a mock | Yes, as known after apply |
| The approved plan is what applies | The planfile store, which counts a newly known value as drift | Yes, and choudoufu refuses any other change |
| Applies | One per component | One per component |

## The workflow

The pull request plans the components the change reaches, and the merge
deploys them from the stored plans:

```
atmos terraform plan --affected --include-dependents --ci
atmos terraform deploy --affected --include-dependents --ci
```

The deploy applies each component in turn from a fresh plan. choudoufu refuses,
with exit status 3, a fresh plan that differs from the stored one in anything
but values that were known after apply.

## Limits

- `--affected` plans what Atmos names, so `--include-dependents` is needed for
  the components that read a changed one.
- Stored plans are keyed by commit. Merge fast-forward only, or rebase and plan
  again before the merge.
- A var that is `!terraform.output` or `!terraform.state` with an output name or
  a simple path is taken from the joint plan. A yq expression with `//`,
  `!store`, a template calling `atmos.Component`, or a function nested in a list
  or map keeps Atmos's value.

Shared limits are on [One plan, one apply, across roots](/choudoufu/how-it-works/across-estates/),
and a worked example is
[`examples/demo-atmos`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/demo-atmos/README.md).
