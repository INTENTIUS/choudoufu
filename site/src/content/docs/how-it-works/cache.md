---
title: The cache
description: The state file as a local cache.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/cache/ and check whether
  this repository's CI uploads `.terraform/`. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Every run writes an ordinary state file, `.terraform/choudoufu-cache.tfstate`.
A default plan reads the live system, so a fresh checkout or CI runner plans
correctly from an empty cache.

## The exit

Copy the file to `terraform.tfstate` and remove the `live` block. Stock
OpenTofu runs with it. It holds sensitive values in clear
([`live/STORAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STORAGE.md)).
