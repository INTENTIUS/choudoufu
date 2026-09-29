terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
    random = {
      source = "hashicorp/random"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

provider "random" {}

# corpus-eks-basic's chain, reduced: the provider-configuration data source
# reaches a managed resource whose own identity is parent-derived from a
# record-backed one, and a migration is what seeds the record store, so
# nothing on this path can materialize the parent by reading. The state file
# being migrated is the only thing that has the value.
resource "random_string" "suffix" {
  length  = 8
  special = false
}

locals {
  name = "tofu-import-unit-${random_string.suffix.result}"
}

resource "aws_s3_bucket" "data" {
  bucket = local.name
}

data "aws_s3_bucket" "config" {
  bucket = aws_s3_bucket.data.id
}

provider "aws" {
  alias  = "derived"
  region = data.aws_s3_bucket.config.region
}

resource "aws_s3_bucket" "derived_target" {
  provider = aws.derived
  bucket   = "tofu-import-unit-derived"
}
