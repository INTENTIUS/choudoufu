---
title: Recover an estate
description: What to do when the cache or the record store is lost and the live resources remain.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/recover-an-estate/: run
  `choudoufu plan -adoption-only` and give me each instance's class.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

A lost cache or working copy costs one slower plan. Lost markers make
resources look unowned; find who untagged them. A lost record store needs the
procedure below. Most resources come back from their markers.

1. Leave records and tags as they are.
2. Restore the record store if you can. Removing a bucket record's delete
   marker restores it while the noncurrent version lasts, with the
   [recovery permissions](../bucket/#what-a-recovery-needs). If that works, stop.
3. Run `choudoufu init`, then `choudoufu plan -adoption-only`.
4. Act on each instance's class.

| Class | Action |
|---|---|
| `already marked` | None |
| `adoptable now` | Run the printed tag write |
| `waits on parent` | Fix the parent first |
| `no path` | Steps 5 and 6 |
| `in the way` | Stop: another estate owns it |
| `nothing live` | Confirm it is gone; an apply creates it |

5. If an old `terraform.tfstate` holds these instances, run [`choudoufu live-import`](../migrate/) on it.
6. Plan again and read why anything stays unbound.

A record-backed resource such as `random_pet` regenerates without its record.
[`live/RECOVERY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECOVERY.md)
has a worked example.
