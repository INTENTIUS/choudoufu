---
title: What it is
description: How choudoufu keeps ownership on the resources.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/ and explain what choudoufu
  would change for the configuration in this repository. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

OpenTofu's state file records which resource each block created. Whoever can
write that file can change anything in it. choudoufu is OpenTofu with the live
backend. Ownership goes on the resources and the rest in small records.

![What choudoufu keeps, and where](../../../assets/diagram-pieces.svg)

## Three words

An estate is one configuration and everything it owns, named once in
`estate.chdf.hcl`. The name is the unit of ownership and access control.

A marker is a tag or label holding the estate name and block address. [Identity](identity/) has the rules.

A record holds what the live resource cannot return, such as a generated
password. [Records](values/) has the kinds and stores.

## The marker says what you own

A plan finds resources by marker and reads each one live. The policy engine
you already run decides who may change each resource: IAM tag conditions on
AWS, one admission policy on the label on Kubernetes. Any tool can list an
estate:

```
aws resourcegroupstaggingapi get-resources \
  --tag-filters Key=tofu-estate,Values=prod-networking

kubectl get configmaps,services -A -l tofu-estate=prod-networking
```

## The state file becomes a cache

Every run writes a local state file. Remove the `live` block and stock
OpenTofu carries on with it ([the cache](cache/)).

## What changes from a state file

![What a state file holds, and where each part goes](../../../assets/diagram-split.svg)

| | `terraform.tfstate` | choudoufu |
|---|---|---|
| Permission unit | one file | one resource |
| To narrow access | split the state | write a policy |
| Two applies at once | a lock a crash can orphan | a conditional write per record |
| Losing it | you no longer know what you own | a slower plan |

## What it costs

Reading the live system makes a plan slower
([`live/PLAN-CONE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/PLAN-CONE.md)). A marker must be knowable before the resource exists.
Compatibility on [AWS](../aws/compatibility/) and
[Kubernetes](../kubernetes/compatibility/) lists what that rules out.

## Upstream

choudoufu forks and tracks [opentofu/opentofu](https://github.com/opentofu/opentofu)
under MPL-2.0. It is not affiliated with OpenTofu or the Linux Foundation,
owner of the OpenTofu trademark.
