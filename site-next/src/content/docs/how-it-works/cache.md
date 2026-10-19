---
title: The cache
description: What the state file is once ownership lives on the resources, and what losing it costs.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/cache/ and check whether this
  repository's CI uploads `.terraform/`. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Every run keeps an ordinary state file as a cache,
`.terraform/choudoufu-cache.tfstate`, on the machine that ran. Three rules
govern it. It is never consulted for ownership. When it and the live system
disagree, live wins. Losing it costs a slower run and nothing else.

It is not part of the backend and no two machines share it, so a fresh
checkout or a new CI runner plans correctly with no cache at all.

## What losing it costs

A read. Delete the file, corrupt it, or let it go stale for a month, and the
next plan answers the same as a fresh one. A default plan reads the live system
every time, so the cache saves nothing there; it pays out only on
`-refresh=false`.

## The cache is the exit

The file is a stock-format state file on purpose. Copy it to
`terraform.tfstate`, remove the `live` block, and stock OpenTofu plans,
converges and destroys with it.

The file is unencrypted and holds sensitive values in clear, like any state
file. [`live/STORAGE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/STORAGE.md)
has the knobs and what the cache holds.
