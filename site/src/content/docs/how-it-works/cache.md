---
title: The cache
description: The state file as a local cache.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/cache/ and check whether
  this repository's CI uploads `.terraform/`. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Every run writes an ordinary state file, `.terraform/choudoufu-cache.tfstate`,
on the machine that ran. When it and the live system disagree, live wins. A
fresh checkout or CI runner plans correctly from empty.

## Losing it

A default plan reads the live system every time, so deleting the cache changes
no plan. It speeds up `-refresh=false` only.

## The exit

The file is a stock-format state file. Copy it to `terraform.tfstate` and
remove the `live` block. Stock OpenTofu then runs with it.

It holds sensitive values in clear.
[`live/STORAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STORAGE.md)
has its settings and contents.
