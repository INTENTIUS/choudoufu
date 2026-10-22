---
title: Recover an estate
description: What to do when the cache or the record store is lost and the live resources remain.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/recover-an-estate/: run
  `choudoufu plan -adoption-only` and give me each instance's class.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

| Lost | Action |
|---|---|
| The cache | Run `choudoufu plan` |
| Your working copy | Check it out and plan |
| The record store | The steps below |

1. Restore the record store if you can. On S3, remove a record's delete marker
   ([recovery permissions](../bucket/#what-a-recovery-needs)).
2. Run `choudoufu init`, then `choudoufu plan -adoption-only`.
3. Act on each instance's class.

| Class | Action |
|---|---|
| `already marked` | None |
| `adoptable now` | Run the printed tag write |
| `waits on parent` | Fix the parent first |
| `no path` | Run [`choudoufu live-import`](../migrate/) on an old state file |
| `in the way` | Another estate owns it |
| `nothing live` | An apply creates it |
| `not read` | Fix the read error printed under it |

[`live/RECOVERY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECOVERY.md)
has a worked example.
