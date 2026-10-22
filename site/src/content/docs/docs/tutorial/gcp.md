---
title: "Tutorial: GCP"
description: Apply a small estate to a local GCP emulator, delete its cache, and watch the next plan find everything by its labels.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/tutorial/gcp/ in an empty
  directory. Install the choudoufu release, start the floci-gcp container,
  write each file exactly as the page shows it, and run each step against
  the emulator on 127.0.0.1:4588, showing me each output. Before each apply,
  check that provider.tf points every endpoint at http://127.0.0.1:4588 and
  uses the fake access_token, and stop if it does not.
  Apply only in the tutorial's two directories against that emulator; never
  apply or destroy anywhere else (`choudoufu apply`, `choudoufu destroy`,
  `tofu apply`), approve a saved plan, or merge; never edit or remove an
  owner tag by hand.
---

This run builds two estates in one local GCP emulator. It takes about fifteen
minutes and needs Docker and `curl`.

## Install

Install the release binary as [Start a new estate](../../use/start/#install)
shows, and put `choudoufu` on your `PATH`. Check it:

```
choudoufu version
```

## Start the emulator

```
docker run -d --name floci-gcp -p 127.0.0.1:4588:4588 \
  -e FLOCI_GCP_DEFAULT_PROJECT_ID=chdf-tutorial \
  ghcr.io/lex00/floci-gcp@sha256:97faafa3e7adecb6233f4bb2596cf4bca979126673dc8485b4b8075ca6b5a4b7
```

Wait until it answers:

```
curl -fsS http://127.0.0.1:4588/health
```

## Write the estate

Make a directory `orders` and write three files in it. `provider.tf` points
every endpoint at the emulator and carries a fake token:

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
  project      = "chdf-tutorial"
  region       = "us-central1"
  zone         = "us-central1-a"
  access_token = "chdf-tutorial-token"

  compute_custom_endpoint          = "http://127.0.0.1:4588/compute/v1/"
  storage_custom_endpoint          = "http://127.0.0.1:4588/storage/v1/"
  iam_custom_endpoint              = "http://127.0.0.1:4588/"
  iam_beta_custom_endpoint         = "http://127.0.0.1:4588/v1/"
  iam_credentials_custom_endpoint  = "http://127.0.0.1:4588/v1/"
  pubsub_custom_endpoint           = "http://127.0.0.1:4588/v1/"
  resource_manager_custom_endpoint = "http://127.0.0.1:4588/v1/"
  service_usage_custom_endpoint    = "http://127.0.0.1:4588/v1/"
  cloud_billing_custom_endpoint    = "http://127.0.0.1:4588/v1/"
  cloud_asset_custom_endpoint      = "http://127.0.0.1:4588/cloudasset/v1/"
}
```

`main.tf`:

```hcl
resource "google_pubsub_topic" "events" {
  name = "orders-events"
}

resource "google_storage_bucket" "files" {
  name          = "chdf-tutorial-orders-files"
  location      = "US"
  force_destroy = true
}
```

`estate.chdf.hcl`:

```hcl
estate = "orders"
```

Records stay on your machine in `.tofu-records/`.

## Initialize

Initialize the directory from inside `orders`.

```
choudoufu init
```

It ends with `OpenTofu has been successfully initialized!`.

## Apply

```
choudoufu apply
```

The plan adds two labels to each resource, `tofu-estate = "orders"` and a
hashed `tofu-address`. Type `yes`. The apply ends with
`Apply complete! Resources: 2 added, 0 changed, 0 destroyed.`

## See the labels

Read the topic and the bucket from the emulator:

```
curl -s -H "Authorization: Bearer chdf-tutorial-token" \
  http://127.0.0.1:4588/v1/projects/chdf-tutorial/topics/orders-events
curl -s -H "Authorization: Bearer chdf-tutorial-token" \
  http://127.0.0.1:4588/storage/v1/b/chdf-tutorial-orders-files
```

Each one's `labels` carry `"tofu-estate": "orders"`. The
[labels](../../../how-it-works/identity/) are the ownership record.

## Delete the cache

```
rm .terraform/choudoufu-cache.tfstate
choudoufu plan
```

The plan finds both resources by their labels and shows `No changes.`

## A second estate

Make a directory `billing` beside `orders`. Copy `provider.tf` into it, then
write a `main.tf` with one topic and an `estate.chdf.hcl` with its own name:

```hcl
resource "google_pubsub_topic" "events" {
  name = "billing-events"
}
```

```hcl
estate = "billing"
```

Apply it and drop its cache from inside `billing`.

```
choudoufu init
choudoufu apply
rm .terraform/choudoufu-cache.tfstate
choudoufu plan
```

The apply adds one topic, and the plan shows `No changes.` List what the
estate holds:

```
choudoufu live-ls -estate=billing .
```

It lists `billing-events` alone. Both estates share one project, and each
plan reads only its own labels.

When you are done, `docker rm -f floci-gcp` removes the emulator.

## Next

- [Start a new estate](../../use/start/)
- [Migrate an existing estate](../../use/migrate/)
- [GCP](../../../gcp/)
