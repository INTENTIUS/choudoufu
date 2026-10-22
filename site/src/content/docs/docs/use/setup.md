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
| `estate.chdf.hcl`, the live backend: the estate name and the `record_store` block | You |
| The record store: a bucket, a namespace or a storage container | You |
| Markers on new resources | The apply |
| Markers on existing resources | You, by [migrating](../migrate/) |

## Credentials

| Platform | Credentials |
|---|---|
| AWS | `AWS_PROFILE` and a region |
| Kubernetes | `config_path` in `record_store`, or `KUBECONFIG` or `KUBE_CONFIG_PATH` |
| GCP | `gcloud auth application-default login` |
| Azure | `az login`, and `subscription_id` in the `azurerm` provider block |

## The estate declaration

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "s3" {
  bucket = "my-records-bucket"
  region = "us-east-2"
}
```

## The record store

| Store | What must exist first |
|---|---|
| `local` | Nothing. Gitignore `.tofu-records/` |
| `s3` | The bucket ([set it up](../bucket/)) |
| `kubernetes` | The namespace, a role bound to Secrets in it ([`examples/record-store-cluster`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-cluster/README.md)), [the admission policy](../../../kubernetes/gate/) and encryption at rest ([Records](../../../kubernetes/operate/#records)) |
| `gcs` | The bucket ([CONTRACT.md](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-gcs/CONTRACT.md)) |
| `azurerm` | The storage account and container ([CONTRACT.md](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-azurerm/CONTRACT.md)) |

Create the store outside the estate that uses it.

## Permissions

| Platform | What each writer holds |
|---|---|
| AWS | The service's tagging action, such as `ec2:CreateTags` ([`live/IAM-REFERENCE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/IAM-REFERENCE.md)) |
| Kubernetes | `use` on its estate ([the gate](../../../kubernetes/gate/)) |
| GCP | The roles for the estate's types, on its project ([the gate](../../../gcp/gate/)) |
| Azure | A role on the estate's resource groups ([the gate](../../../azure/gate/)) |
