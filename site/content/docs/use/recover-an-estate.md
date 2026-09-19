---
title: "Recover an estate"
weight: 4
---

# Recover an estate

You have lost the cache, the record store, or both, and the live resources
are still there. Work out which you lost, then follow the steps in order.

## What you lost

| Lost | What it costs | What to do |
|---|---|---|
| The cache, `.terraform/choudoufu-cache.tfstate` | One slower plan | Nothing. Run `choudoufu plan` |
| Your whole working copy | The same | Check the configuration out again and plan |
| Markers on live resources | Those resources look unowned | Somebody untagged them. See [the ownership policy]({{< relref "/docs/use/ownership-policy" >}}) |
| The record store | Record-backed resources, and the identity of a few AWS types with no tags | The rest of this page |

Most resources come back from their markers alone.
[Claim 5](https://github.com/INTENTIUS/choudoufu/blob/main/live/smoke/claims/recovery-is-a-rerun.md)
deletes every local file and the next plan is `No changes.` Run it with
`just smoke recovery-is-a-rerun`.

## The procedure

1. Change nothing by hand. Do not edit records or strip tags to start clean.
   The markers are what still works.
2. Restore the record store if you can. In a bucket, every deleted record is
   still there as a noncurrent version until the lifecycle rule expires it.
   Removing the delete marker brings it back, and that takes
   `s3:ListBucketVersions` and `s3:DeleteObjectVersion`, which the estate's
   own role does not have
   ([IAM]({{< relref "/docs/use/iam#what-a-recovery-needs" >}})). If this
   works, you are done.
3. Run `choudoufu init`, then `choudoufu plan -adoption-only`. That prints
   which live resource each declared instance binds to, which is what you
   need. The full plan's diff is the wrong tool here.
4. Read each instance's class.

| Class | Meaning | What to do |
|---|---|---|
| `already marked` | The marker is on the live resource | Nothing |
| `adoptable now` | A live resource sits at the declared identity with no marker | Run the tag write the report prints |
| `waits on parent` | A parent it derives from did not resolve | Fix the parent first |
| `no path` | It needs a record and there is none | Steps 5 and 6 |
| `in the way` | Another estate owns the live resource | Stop |
| `nothing live` | Nothing was found, so an apply creates it | Check that it really does not exist |

5. If any `terraform.tfstate` that holds these instances still exists, an old
   backup or a colleague's copy, run
   [`choudoufu live-import`]({{< relref "/docs/use/migrate" >}}) on it. It is
   the only command that writes an identity record outside an apply.
6. Plan again and read the reason on anything still unbound.

## What does not come back

A record-backed resource whose record is gone is gone: a `random_pet`
regenerates, and everything named after it is proposed for create under the
new name. A server-minted credential such as `aws_iam_access_key` keeps
working for whoever holds it, and its secret cannot be read again.

[`live/RECOVERY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/RECOVERY.md)
has the full inventory: which types need a record and which do not, the 96
AWS types whose identity is only in the record, and the worked example.
