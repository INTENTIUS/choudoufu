---
title: Migrate an existing estate
description: Put a marker on each resource your OpenTofu or Terraform configuration already manages, then keep the state file as your way back.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/migrate/ for this repository.
  Run `choudoufu live-check ./`, then `choudoufu init` and the dry
  `choudoufu live-import -state=terraform.tfstate -estate=<estate>` without
  `-approve`, and show me both reports. Do not pass `-approve`.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

You have a configuration that already manages live resources. Migrating means
putting a marker on each of them.

:::caution
Turning on the live backend does not bind resources you already manage. A
resource with no marker is not yours yet, so the first plan reads it as absent
and proposes a second one beside it. Put the markers on first.
:::

## Check the configuration

```
choudoufu live-check ./
```

It needs no `live` block, no credentials and no cloud calls. It prints a
verdict, then every refusal that fired, with the types responsible and what to
do. Run `choudoufu init` first if you can: with provider schemas it admits more,
and without them it says its answer is pessimistic. `-json` prints the same
verdict as one document whose `schemas` field says which answer you got.

It checks lint and identity resolution only, so a clean result is necessary,
not sufficient. Plan against a non-production account before trusting a
migration.
[`live/COMPATIBILITY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/COMPATIBILITY.md)
explains each refusal.

## Keep the state file

Do not delete `terraform.tfstate`. It is the only input `live-import` has, and
your way back to stock. If your state is remote, pull a copy with
`tofu state pull > terraform.tfstate`.

## With a state file: two commands

Run these in a directory `choudoufu init` has prepared, with the provider
configuration the state was last applied with.

```
choudoufu live-import -state=terraform.tfstate -estate=my-estate
choudoufu live-import -state=terraform.tfstate -estate=my-estate -approve
```

The first writes nothing and prints a report. The second writes a marker on
every entry shown as `VERIFIED` or `DRIFTED`. Entries shown as `MISSING`,
`UNTAGGABLE` or `UNADMITTED_TYPE` are never written, and the report says why.
The state file is never changed. It works on AWS and on Kubernetes.

## Without a state file

This path never offers a `count` or `for_each` instance.

1. Create `estate.chdf.hcl` with `estate = "my-estate"`, and remove any
   `backend` or `cloud` block.
2. Run `choudoufu plan` and read the `Adoptable` section. Each entry is a live
   resource that matches a block and has no marker, with the command that
   adopts it.
3. Run those commands as printed.
4. Plan again. Every adopted resource reports no changes.

## Then

Keep the sidecar or `live` block from here on. Without it a plan is stock mode,
and stock mode with no state file proposes rebuilding the estate.

## Getting back out

Copy `.terraform/choudoufu-cache.tfstate` to `terraform.tfstate` and remove the
`live` block. Stock OpenTofu's first plan proposes removing the marker tags and
nothing else. choudoufu refuses that same plan, so a `live` block lost in a bad
merge cannot un-migrate an estate; to leave on purpose, run
`CHOUDOUFU_UNMIGRATE=my-estate choudoufu apply`.
[`live/MIGRATE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MIGRATE.md)
has the rest.
