---
title: "Tutorial: AWS"
description: Apply a small estate to a local AWS emulator, delete its cache, and watch the plan find everything again by its markers.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/tutorial/aws/ step by step
  against the local floci emulator on http://localhost:4566 only. Before
  each choudoufu command, check that AWS_ENDPOINT_URL is
  http://localhost:4566 and that every endpoint in the provider block names
  localhost:4566, and show me each check's output.
  Never run anything against a real AWS account: stop and ask if a step would reach one. Never apply or destroy outside this tutorial's two directories, approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

You apply two small estates to floci, an AWS emulator on your machine.
Every call goes to `localhost:4566`, with test credentials.

## Install

Install choudoufu from a release, as [Start a new estate](../../use/start/#install)
shows, and start Docker. Check both:

```
choudoufu version
docker info
```

## Start the emulator

```
docker run -d --name floci -p 4566:4566 \
  ghcr.io/lex00/floci@sha256:5ec92ae8046445d62c0009590dd09f04560886e5a5ef31248cb3fcc09b2961ec
```

Point every AWS client in this shell at it:

```
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
```

Check that it answers. The reply lists its services, `ec2` among them:

```
curl -s http://localhost:4566/_localstack/health
```

## Write the estate

```
mkdir tutorial-a && cd tutorial-a
```

`estate.chdf.hcl`:

```hcl
estate = "tutorial-a"
```

`main.tf`:

```hcl
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.58.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "3.7.2"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true

  endpoints {
    ec2 = "http://localhost:4566"
    iam = "http://localhost:4566"
    sts = "http://localhost:4566"
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.10.0.0/16"
}

resource "aws_iam_role" "app" {
  name = "tutorial-a-app"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })
}

resource "random_pet" "release" {}
```

The VPC gets an id from AWS, the role has the name you gave it, and the pet
lives only in the estate's records ([identity](../../../how-it-works/identity/)).

## Init

```
choudoufu init
```

It installs the `aws` and `random` providers and reports success.

## Apply

```
choudoufu apply
```

The plan adds all three, and the VPC and the role each get two tags:

```
      + tags       = {
          + "tofu-address" = "aws_vpc.main"
          + "tofu-estate"  = "tutorial-a"
        }
```

Type `yes`. The pet's record is now in `.tofu-records/`.

## See the markers

```
choudoufu live-ls -estate=tutorial-a .
```

The listing shows the VPC and the role, each with its `tofu-address`.

## Delete the cache

```
rm .terraform/choudoufu-cache.tfstate
```

## Plan from the markers

```
choudoufu plan
```

The plan reads the markers and the record and reports:

```
No changes. Your infrastructure matches the configuration.
```

## A second estate

In a new directory beside the first:

```
cd .. && mkdir tutorial-b && cd tutorial-b
```

`estate.chdf.hcl`:

```hcl
estate = "tutorial-b"
```

`main.tf` is the same `terraform` and `provider` blocks, with one VPC:

```hcl
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.58.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true

  endpoints {
    ec2 = "http://localhost:4566"
    iam = "http://localhost:4566"
    sts = "http://localhost:4566"
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.20.0.0/16"
}
```

```
choudoufu init
choudoufu apply
choudoufu live-ls -estate=tutorial-b .
```

The listing shows one VPC. Now the emulator holds two VPCs at `aws_vpc.main`,
one per estate. Plan the first estate again:

```
cd ../tutorial-a
choudoufu plan
```

It reports `No changes.` and leaves tutorial-b's VPC to tutorial-b.

## Next

- [Start a new estate](../../use/start/)
- [Migrate an existing estate](../../use/migrate/)
- [AWS](../../../aws/)
