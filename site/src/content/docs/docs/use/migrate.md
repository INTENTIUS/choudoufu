---
title: Migrate an existing estate
description: Move a Terraform or OpenTofu configuration with a state file onto markers, and back.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/migrate/ for this repository,
  using the section for my platform. Install choudoufu, keep the `backend`
  block, create `estate.chdf.hcl` (empty, or with an estate name and
  `record_store` I confirm), then run `choudoufu init`, `choudoufu live-check`
  and the dry `choudoufu live-import`, and show me each output, including the
  "Records:" line init prints. Do not pass `-approve`.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Run these steps in the directory that holds your configuration, before any
`choudoufu apply`.

Keep your `backend` block. choudoufu ignores it for state, reads the old
state from it on `live-import`, publishes outputs to it, and, with no
`record_store`, keeps records in its bucket. The estate name defaults to the
backend key (`prod/network/terraform.tfstate` gives `prod-network`), else the
root's path. `init` and `plan` print both:

```
Records: s3 bucket "acme-tfstate" under tofu-records/prod-network/ (derived from backend "s3" at backend.tf:2); estate "prod-network" (from the backend key)
```

## Install

With the GitHub CLI, signed in through `gh auth login`:

```
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
gh release download -R INTENTIUS/choudoufu --pattern "*_${os}_${arch}.tar.gz"
tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz
```

With curl:

```
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
v=$(curl -fsSLo /dev/null -w '%{url_effective}' https://github.com/INTENTIUS/choudoufu/releases/latest | sed 's|.*/||')
curl -fsSLO "https://github.com/INTENTIUS/choudoufu/releases/download/${v}/choudoufu_${v}_${os}_${arch}.tar.gz"
tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz
```

For Windows, download `choudoufu_<version>_windows_amd64.zip` (or `_arm64.zip`)
from the [latest release](https://github.com/INTENTIUS/choudoufu/releases/latest)
and unzip it. Then put `choudoufu` on your `PATH`.

## Steps

1. [Install choudoufu](#install).
2. Create `estate.chdf.hcl`. It can be empty on AWS, GCP and Azure. To name
   the estate or the store yourself, see the examples below.
3. Migrate:

   ```
   choudoufu init                    # prints where records go
   choudoufu live-check
   choudoufu live-import             # preview; reads the old state from the backend
   choudoufu live-import -approve    # write the markers
   choudoufu plan                    # no changes
   ```

4. [Archive the old state](#archive-the-old-state).

## Naming the store

On AWS ([the bucket](../bucket/)):

```hcl
estate = "my-estate"

record_store "s3" {
  bucket = "my-records-bucket"
  region = "us-east-2"
}
```

On GCP ([the bucket](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-gcs/CONTRACT.md)),
`record_store "gcs" { bucket = "my-records-bucket" }`. On Azure
([the storage account](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-azurerm/CONTRACT.md)),
`record_store "azurerm"` with `storage_account_name`, `container_name` and
`resource_group_name`; the derived store needs the backend block's
`resource_group_name` too.

On Kubernetes the `kubernetes` backend has no record store counterpart, so
name one ([the namespace](../setup/#the-record-store)):

```hcl
estate = "my-estate"

record_store "kubernetes" {
  config_path = "~/.kube/config"
}
```

A backend set only through `-backend-config` cannot be read from the block:
run `terraform state pull > stock.tfstate`, pass `-state=stock.tfstate` to
`live-import`, and name the store, since `init` prints that it fell back to
the local one.

## Archive the old state

The old state document still lists every resource, and an apply never
overwrites a document that holds resources, so `terraform_remote_state`
readers keep reading the pre-migration outputs until it is archived. Once
`choudoufu plan` is empty:

```
tofu state pull > pre-live.tfstate        # or terraform; keep this copy
aws s3 mv s3://acme-tfstate/prod/network/terraform.tfstate \
          s3://acme-tfstate/archive/prod/network/terraform.tfstate
```

On GCP use `gcloud storage mv`; on Azure, copy the blob and delete the
original. The next `choudoufu apply` writes an outputs-only document in its
place.

## Undo

1. Copy the cache: `cp .terraform/choudoufu-cache.tfstate terraform.tfstate`.
2. Delete `estate.chdf.hcl`, then run `choudoufu init`.
3. Remove the markers: `CHOUDOUFU_UNMIGRATE=my-estate choudoufu apply`.
4. Push the state; the `backend` block is still in place:

   ```
   terraform init -reconfigure                     # or tofu
   terraform state push -force terraform.tfstate
   ```

5. `terraform plan` reports no changes.
