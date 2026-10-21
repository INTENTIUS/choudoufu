---
title: What you set up by hand
description: What must exist before the first plan.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/setup/ for this repository
  and tell me what is missing. Create nothing in the cloud.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

| Piece | Who creates it |
|---|---|
| Credentials and an access policy | You |
| `estate.chdf.hcl` | You, one line |
| A shared record store, a bucket or a namespace | You |
| Markers on new resources | The apply |
| Markers on existing resources | You, by [migrating](../migrate/) |

## Credentials

On AWS, set `AWS_PROFILE` and a region. On Kubernetes, the provider reads your
kubeconfig or `KUBE_CONFIG_PATH`.

## The estate declaration

```hcl
# estate.chdf.hcl
estate = "my-estate"
```

## The record store

| Store | What must exist first |
|---|---|
| `local` | Nothing. Gitignore `.tofu-records/` |
| `s3` | The bucket ([set it up](../bucket/)) |
| `kubernetes` | The namespace, and a role bound to Secrets in it ([`examples/record-store-cluster`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-cluster/README.md)) |

Create the store outside the estate that uses it.

## Permissions

On AWS, adoption needs the service's tagging action, such as `ec2:CreateTags`
([`live/IAM-REFERENCE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/IAM-REFERENCE.md)).
On Kubernetes, each writer needs `use` on its estate ([the gate](../../../kubernetes/gate/)).
