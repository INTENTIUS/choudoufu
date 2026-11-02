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
`planfiles.required: true`. Atmos's planfile check also compares a value that
was known after apply and is known now. With `warn`, choudoufu runs the
matching check and refuses a plan that changed.

## Atmos on the live backend

- The components you plan with `--affected` join one plan.
- A changed value reaches the components that read it.
- A new ID arrives as known after apply.
- The deploy applies the reviewed plan, one apply per component.

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

A worked example is
[`examples/demo-atmos`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/demo-atmos/README.md).
