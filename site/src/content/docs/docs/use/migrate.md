---
title: Migrate an existing estate
description: Move a Terraform or OpenTofu configuration with a state file onto markers, and back.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/migrate/ for this repository,
  using the section for my platform. Install choudoufu, run
  `terraform state pull > stock.tfstate` (or `tofu state pull`), remove the
  `backend` block, create `estate.chdf.hcl` with an estate name and
  `record_store` I confirm, then run `choudoufu init`, `choudoufu live-check`
  and the dry `choudoufu live-import -state=stock.tfstate -estate=<estate>`,
  and show me each output. Do not pass `-approve`.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

Run these steps in the directory that holds your configuration, before any
`choudoufu apply`. Follow the section for your platform, from step 1. Steps 3
and 4 swap your `backend` block for the live backend, `estate.chdf.hcl`.

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

## On AWS

1. [Install choudoufu](#install).
2. Pull the state:

   ```
   terraform state pull > stock.tfstate   # or tofu state pull
   ```

3. Delete the `backend "s3"` block.
4. Create `estate.chdf.hcl` ([the bucket](../bucket/)):

   ```hcl
   estate = "my-estate"

   record_store "s3" {
     bucket = "my-records-bucket"
     region = "us-east-2"
   }
   ```

5. Migrate:

   ```
   choudoufu init
   choudoufu live-check
   choudoufu live-import -state=stock.tfstate -estate=my-estate            # preview
   choudoufu live-import -state=stock.tfstate -estate=my-estate -approve   # write the tags
   choudoufu plan                                                          # no changes
   ```

## On Kubernetes

1. [Install choudoufu](#install).
2. Pull the state:

   ```
   terraform state pull > stock.tfstate   # or tofu state pull
   ```

3. Delete the `backend "kubernetes"` block.
4. Create `estate.chdf.hcl` ([the namespace](../setup/#the-record-store)):

   ```hcl
   estate = "my-estate"

   record_store "kubernetes" {
     config_path = "~/.kube/config"
   }
   ```

5. Migrate:

   ```
   choudoufu init
   choudoufu live-check
   choudoufu live-import -state=stock.tfstate -estate=my-estate            # preview
   choudoufu live-import -state=stock.tfstate -estate=my-estate -approve   # write the labels
   choudoufu plan                                                          # no changes
   ```

## On GCP

1. [Install choudoufu](#install).
2. Pull the state:

   ```
   terraform state pull > stock.tfstate   # or tofu state pull
   ```

3. Delete the `backend "gcs"` block.
4. Create `estate.chdf.hcl` ([the bucket](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-gcs/CONTRACT.md)):

   ```hcl
   estate = "my-estate"

   record_store "gcs" {
     bucket = "my-records-bucket"
   }
   ```

5. Migrate:

   ```
   choudoufu init
   choudoufu live-check
   choudoufu live-import -state=stock.tfstate -estate=my-estate            # preview
   choudoufu live-import -state=stock.tfstate -estate=my-estate -approve   # write the labels
   choudoufu plan                                                          # no changes
   ```

## On Azure

1. [Install choudoufu](#install).
2. Pull the state:

   ```
   terraform state pull > stock.tfstate   # or tofu state pull
   ```

3. Delete the `backend "azurerm"` block.
4. Create `estate.chdf.hcl` ([the storage account](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-azurerm/CONTRACT.md)):

   ```hcl
   estate = "my-estate"

   record_store "azurerm" {
     storage_account_name = "myrecords"
     container_name       = "records"
     resource_group_name  = "records-rg"
   }
   ```

5. Migrate:

   ```
   choudoufu init
   choudoufu live-check
   choudoufu live-import -state=stock.tfstate -estate=my-estate            # preview
   choudoufu live-import -state=stock.tfstate -estate=my-estate -approve   # write the tags
   choudoufu plan                                                          # no changes
   ```

## Undo

1. Copy the cache: `cp .terraform/choudoufu-cache.tfstate terraform.tfstate`.
2. Delete `estate.chdf.hcl`, then run `choudoufu init`.
3. Remove the markers: `CHOUDOUFU_UNMIGRATE=my-estate choudoufu apply`.
4. Put the `backend` block back.
5. Push the state:

   ```
   terraform init -reconfigure                     # or tofu
   terraform state push -force terraform.tfstate
   ```

6. `terraform plan` reports no changes.
