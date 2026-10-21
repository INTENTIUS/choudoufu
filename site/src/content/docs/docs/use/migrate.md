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

Migrating puts a marker on each live resource your configuration manages. Do it
before the first apply.

## Check the configuration

```
choudoufu init
choudoufu live-check ./
```

`live-check` runs offline and names each fix.

## Keep the state file

Keep `terraform.tfstate` as the input to `live-import` and your way back. For
remote state, run `tofu state pull > terraform.tfstate`.

## With a state file: two commands

```
choudoufu live-import -state=terraform.tfstate -estate=my-estate
choudoufu live-import -state=terraform.tfstate -estate=my-estate -approve
```

The first prints a dry report. The second marks every `VERIFIED` or `DRIFTED`
entry.

## Without a state file

1. Create `estate.chdf.hcl` with `estate = "my-estate"`.
2. Run `choudoufu plan` and read the `Adoptable` section.
3. Execute the adopt commands it prints.
4. Plan again to see no changes.

On Kubernetes, set `policy { declared_untagged = "adopt" }` to adopt
unlabelled objects that hold a declared name.

## Getting back out

Copy `.terraform/choudoufu-cache.tfstate` to `terraform.tfstate`, remove the
`live` block, and run `CHOUDOUFU_UNMIGRATE=my-estate choudoufu apply`
([`live/MIGRATE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MIGRATE.md)).
