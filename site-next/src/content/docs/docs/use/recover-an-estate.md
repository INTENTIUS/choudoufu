---
title: Recover an estate
description: What to do when the cache, the record store, or both are lost and the live resources are still there.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/recover-an-estate/: run
  `choudoufu plan -adoption-only` and give me each instance's class.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Work out what you lost, then follow the steps in order.

| Lost | What it costs | What to do |
|---|---|---|
| The cache | One slower plan | Run `choudoufu plan` |
| Your working copy | The same | Check it out again and plan |
| Markers on live resources | Those resources look unowned | Find who untagged them |
| The record store | Record-backed resources, and the identity of a few untagged AWS types | The procedure below |

Most resources come back from their markers alone.

## The procedure

1. Change nothing by hand. Do not edit records or strip tags.
2. Restore the record store if you can. In a bucket, a deleted record stays as
   a noncurrent version until the lifecycle rule expires it. Removing the
   delete marker brings it back; that needs the
   [recovery permissions](../bucket/#what-a-recovery-needs). If it works, stop.
3. Run `choudoufu init`, then `choudoufu plan -adoption-only`.
4. Read each instance's class.

| Class | What to do |
|---|---|
| `already marked` | Nothing |
| `adoptable now` | Run the tag write it prints |
| `waits on parent` | Fix the parent first |
| `no path` | Steps 5 and 6 |
| `in the way` | Stop: another estate owns it |
| `nothing live` | Check it really does not exist; an apply creates it |

5. If an old `terraform.tfstate` holding these instances survives, run [`choudoufu live-import`](../migrate/) on it.
6. Plan again and read the reason on anything still unbound.

A record-backed resource whose record is gone does not come back: a
`random_pet` regenerates.
[`live/RECOVERY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECOVERY.md)
has what does not come back and the worked example.
