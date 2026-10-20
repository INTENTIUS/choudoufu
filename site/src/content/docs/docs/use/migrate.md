---
title: Migrate an existing estate
description: Put a marker on each resource your configuration already manages, and keep the state file as your way back.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/migrate/ for this repository.
  Run `choudoufu live-check ./`, then `choudoufu init` and the dry
  `choudoufu live-import -state=terraform.tfstate -estate=<estate>` without
  `-approve`, and show me both reports. Do not pass `-approve`.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Migrating puts a marker on each live resource your configuration manages.

:::caution
Mark resources first. The first plan reads an unmarked resource as absent and
proposes a second one beside it.
:::

## Check the configuration

```
choudoufu live-check ./
```

This runs offline and prints a verdict with each refusal and its fix. Run
`choudoufu init` first so provider schemas let it admit more. `-json` prints
the same verdict as one document.

Then plan against a non-production account.
[`live/COMPATIBILITY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/COMPATIBILITY.md)
explains each refusal.

## Keep the state file

Keep `terraform.tfstate`. It is the input to `live-import` and your way back to
stock. For remote state, run `tofu state pull > terraform.tfstate`.

## With a state file: two commands

Run these after `choudoufu init`, with the state's last provider configuration.

```
choudoufu live-import -state=terraform.tfstate -estate=my-estate
choudoufu live-import -state=terraform.tfstate -estate=my-estate -approve
```

The first prints a dry report. The second tags every `VERIFIED` or `DRIFTED`
entry and leaves the state file as it was. The report explains each entry it
skips. This works on AWS and Kubernetes, and is built for Azure.

## Without a state file

This path skips `count` and `for_each` instances.

1. Create `estate.chdf.hcl` with `estate = "my-estate"`, and remove any
   `backend` or `cloud` block.
2. Run `choudoufu plan` and read the `Adoptable` section. Each entry is an
   unmarked live resource that matches a block, with its adopt command.
3. Execute those commands as printed.
4. Run `choudoufu plan` again. Adopted resources report no changes.

Keep the sidecar from then on. A stock plan with no state file proposes
rebuilding the estate.

## Getting back out

Copy `.terraform/choudoufu-cache.tfstate` to `terraform.tfstate` and remove the
`live` block. Stock OpenTofu's first plan proposes removing only the marker
tags. choudoufu refuses that plan. To leave on purpose, run
`CHOUDOUFU_UNMIGRATE=my-estate choudoufu apply`.
[`live/MIGRATE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MIGRATE.md)
has the rest.
