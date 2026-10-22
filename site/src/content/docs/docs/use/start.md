---
title: Start a new estate
description: Install choudoufu, add one file, and apply an estate where choudoufu creates every resource.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/use/start/ in this repository.
  Install choudoufu, create `estate.chdf.hcl` with an estate name I confirm,
  remove any `backend` or `cloud` block, add `.tofu-records/` to `.gitignore`,
  then run `choudoufu init` and `choudoufu plan` and show me the plan.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

This page starts an empty estate. For one whose resources already exist, [migrate](../migrate/) instead.

## Install

Every [release](https://github.com/INTENTIUS/choudoufu/releases) carries
binaries for macOS, Linux and Windows on amd64 and arm64. Download and unpack
the one for your machine with the GitHub CLI, signed in through `gh auth login`:

```
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
gh release download -R INTENTIUS/choudoufu --pattern "*_${os}_${arch}.tar.gz"
tar xzf choudoufu_*_"${os}"_"${arch}".tar.gz
```

Then put `choudoufu` on your `PATH`.

## On AWS

Two files. `estate.chdf.hcl` is the live backend, in place of a `backend` block:

```hcl
estate = "my-estate"

record_store "s3" {
  bucket = "my-records-bucket"
  region = "us-east-2"
}
```

`main.tf`:

```hcl
provider "aws" {
  region = "us-east-2"
}

resource "aws_vpc" "main" {
  cidr_block = "10.99.0.0/16"
}
```

The bucket comes from [Set up a record store bucket](../bucket/). For records on
your machine, leave out `record_store` and gitignore `.tofu-records/`.

Apply with `choudoufu init` and `choudoufu apply`. The plan adds the two tags:

```
      + tags       = {
          + "tofu-address" = "aws_vpc.main"
          + "tofu-estate"  = "my-estate"
        }
```

## On Kubernetes

`estate.chdf.hcl`:

```hcl
estate = "my-estate"

record_store "kubernetes" {
  config_path = "~/.kube/config"
}
```

`main.tf`:

```hcl
provider "kubernetes" {
  config_path = "~/.kube/config"
}

resource "kubernetes_config_map_v1" "app" {
  metadata {
    name      = "app-config"
    namespace = "default"
  }
  data = {
    greeting = "hello"
  }
}
```

Records go to the `tofu-records-my-estate` namespace
([`examples/record-store-cluster`](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-cluster/README.md)).
Apply the same way. The plan adds the label and the address annotation:

```
          + annotations = {
              + "choudoufu.intentius.io/tofu-address" = "kubernetes_config_map_v1.app"
            }
          + labels      = {
              + "tofu-estate" = "my-estate"
            }
```

## On GCP

`estate.chdf.hcl`:

```hcl
estate = "my-estate"

record_store "gcs" {
  bucket = "my-records-bucket"
}
```

`main.tf`:

```hcl
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.6.0"
    }
  }
}

provider "google" {
  project = "my-project"
  region  = "us-central1"
}

resource "google_pubsub_topic" "events" {
  name = "events"
}
```

Create the bucket as [CONTRACT.md](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-gcs/CONTRACT.md)
describes, then apply. The labels carry the address as a hash:

```
      + labels                     = {
          + "tofu-address" = "sr3xznzcyhjfbivm2dllsqcp4d-sr3xznzcyhjfbivm2dllsqcp4d"
          + "tofu-estate"  = "my-estate"
        }
```

## On Azure

`estate.chdf.hcl`:

```hcl
estate = "my-estate"

record_store "azurerm" {
  storage_account_name = "myrecords"
  container_name       = "records"
  resource_group_name  = "records-rg"
}
```

`main.tf`:

```hcl
provider "azurerm" {
  features {}
  subscription_id = "00000000-0000-0000-0000-000000000000"
}

resource "azurerm_resource_group" "app" {
  name     = "my-estate-app"
  location = "eastus"
}
```

The storage account needs the three settings in
[CONTRACT.md](https://github.com/INTENTIUS/choudoufu/blob/main/examples/record-store-azurerm/CONTRACT.md).
The plan writes both tags on the resource group:

```
      + tags     = {
          + "tofu-address" = "azurerm_resource_group.app"
          + "tofu-estate"  = "my-estate"
        }
```

## Check that the markers carry it

Run `choudoufu plan` again. It reads the markers and reports no changes.

## Next

- [What you set up by hand](../setup/)
- [Day-2 operations](../day2/)
