---
title: Identity
description: How a resource carries the estate that owns it.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/identity/ and list the
  resources here that only a marker can find again. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

A marker names the estate that owns a resource. The apply writes it on the
create call.

![How a plan binds a configuration address to a live AWS resource](../../../assets/diagram-identity.svg)

The diagram shows AWS. On Kubernetes a plan lists each kind by the label and
joins each object to its block on namespace and name.

| Platform | Where identity lives |
|---|---|
| AWS | Tags `tofu-estate` and `tofu-address`, plus `tofu-slot` on interchangeable `count` members |
| Kubernetes | One label, `tofu-estate`, with the address in an annotation |

## Two ways back

Where configuration names the resource, such as an S3 bucket, the marker
confirms ownership. Where the platform assigns the id, such as a VPC, only the
marker finds it again. Every Kubernetes object is the first kind: its kind,
namespace and name are in the configuration, so the label holds only the
estate.

## Who may change it

| Platform | The fence |
|---|---|
| AWS | IAM conditions on the `tofu-estate` tag ([gate](../../aws/gate/)) |
| Kubernetes | One admission policy reads the label, and a ClusterRole grants `use` on the estate ([gate](../../kubernetes/gate/)) |

Both bind the credential, so a plain `aws` or `kubectl` call meets the same
fence.

## Types with no marker

About half the AWS provider's resource types take no `tags`. Their names come
from configuration, as with `aws_iam_role_policy`. IAM conditions on the tag
cannot cover them. Nearly every Kubernetes type with a `metadata`
block carries the label, as does every `kubernetes_manifest` object.

## Renaming and stripping

`choudoufu live-mv` rewrites the marker ([rename a
resource](../../docs/use/rename-a-resource/)). A plan cannot see a resource
whose marker was stripped. An AWS plan proposes a second one. A Kubernetes
plan stops, because the stripped object still holds the name.
[`live/IDENTITY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/IDENTITY.md)
has the full rule.
