---
title: "Operate"
weight: 3
description: "Rename, remove, scale, review a plan before applying it, and run the whole thing from CI."
deeper:
  - "[Day-2 operations]({{< relref \"/docs/use/day2\" >}}) indexes every task; [rename]({{< relref \"/docs/use/rename-a-resource\" >}}) and [remove]({{< relref \"/docs/use/remove-a-resource\" >}}) have their own pages."
  - "[Running an estate from CI]({{< relref \"/docs/use/cicd\" >}}): the five jobs, per forge, and what the governance policies lock."
  - "[Two runs at once]({{< relref \"/docs/model/concurrency\" >}}): why there is no lock to manage or force open."
  - "Claims [2]({{< relref \"/docs/claims/no-self-managed-locks\" >}}), [4]({{< relref \"/docs/claims/backend-sets-itself-up\" >}}), [8]({{< relref \"/docs/claims/stock-when-you-need-it\" >}}) and [15]({{< relref \"/docs/claims/apply-what-was-approved\" >}})."
---

# Operate

Day two looks like stock, with the state moves replaced by tag writes.

## Rename

A `moved` block works as it does in stock. So does `choudoufu live-mv <old>
<new>`, which rewrites the `tofu-address` tag in place. Both produce a plan
with no destroy and no create; the gauntlet's rename stage measures that on
every estate.

## Remove

Delete the block and the object is destroyed under the default policy, in an
order the cloud accepts, including untaggable children whose parents stay.
To stop managing something without destroying it, the ownership policy has a
verb for each of the four quadrants (declared or not, tagged or not), and the
`threshold` argument refuses a run that would delete more than you said.

## Scale

Scaling a `count` block down and back up destroys and creates only the
instances stock would, and every survivor keeps its identity. A `count` pool
the configuration does not tell apart carries a third tag, `tofu-slot`, so
the members stay bound as the pool reorders.

## Plan, review, apply

`plan -out=FILE` writes stock's own plan file, and `apply FILE` reads it as an
approval rather than an instruction. The apply re-reads the live system,
plans against what is there now, and compares that fresh plan against the
approved one down to the values each change writes. Where they agree it
applies without asking again. Where they differ, nothing changes: it prints
`The approved plan no longer matches the live system` with the rows that
moved and exits 3, a status a pipeline can route back to review instead of
paging somebody.

## From CI

A choudoufu pipeline is five jobs: `live-check` and `live-plan` on a pull
request, `live-apply` behind an approval on a push to `main`, `live-adopt`
behind an approval on a push to `staging`, and `live-discover` on a cron that
reports what carries this estate's marker that nobody declared. Three of them
read and nothing else; the two that write stop at an approval first.

## Two runs at once

There is no lock. Two concurrent applies settle at the API: each create
carries its tags, the second one to arrive reads the first one's object on
its next plan, and nothing needs forcing open afterwards.
