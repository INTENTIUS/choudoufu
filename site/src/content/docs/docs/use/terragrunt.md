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
2. One `generate "estate"` block in the same file, so every unit gets a live
   sidecar:

```hcl
# root.hcl
terraform_binary = "choudoufu"

generate "estate" {
  path      = "estate.chdf.hcl"
  if_exists = "overwrite"
  contents  = "estate = \"${replace(path_relative_to_include(), "/", "-")}\"\n"
}
```

`remote_state` stays. Its generated backend block is where the records go:
with no `record_store` in the sidecar, choudoufu writes the unit's records to
the same bucket under `tofu-records/<estate>/` and prints the bucket, prefix
and estate on every `init` and `plan`. Use `generate = { path = "backend.tf",
if_exists = "overwrite" }` in `remote_state`, so the bucket is written into
the backend block; a bucket passed only as `-backend-config` is not read, and
the unit falls back to the local store and says so.

The sidecar is read from the directory the binary runs in, which for a unit
with a `terraform { source = ... }` is its `.terragrunt-cache` copy; Terragrunt
writes generated files there too. Leave out the `estate` line and the name
comes from the backend key, so `key = "${path_relative_to_include()}/terraform.tfstate"`
gives the same name. An explicit `record_store` block in the sidecar always
wins.

`include`, `inputs`, `dependency` blocks with `mock_outputs` and generated
providers stay as they are. For a unit that already owns resources, run
`choudoufu live-import` in it before the first apply: it reads the old state
straight from the backend, so no `state pull` is needed. See
[Migrate](/choudoufu/docs/use/migrate/).

## Terragrunt on the live backend

- `run --all` joins the units in one plan.
- A `dependency` reads the planned value.
- A new ID arrives as known after apply, so `mock_outputs` stays as a fallback.
- One apply runs the reviewed plan, and choudoufu refuses a plan that no longer matches.

## The workflow

The pull request plans into `plans`, and the merge applies those files,
planning nothing first:

```
terragrunt run --all --out-dir plans -- plan
terragrunt run --all --out-dir plans -- apply
```

Every plan file carries the whole joint plan, so the merge's job needs only
that directory. Separate jobs need one CI secret in `CHOUDOUFU_PLAN_HMAC_KEY`
([why](https://github.com/INTENTIUS/choudoufu/blob/main/live/OUTPUTS.md)).

## Limits

- `run --all apply` without `--out-dir` applies one unit at a time, each
  reading its upstream's recorded outputs.
- A `dependency` read is taken from the joint plan when it feeds an input
  directly, or through OpenTofu functions. An input that also calls `get_env`,
  or a value read through `read_terragrunt_config`, keeps Terragrunt's value.

A worked example is
[`examples/demo-terragrunt`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/demo-terragrunt/README.md).
