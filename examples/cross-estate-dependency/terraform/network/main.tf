# The producer: one VPC, and no `output` block at all.
#
# The absence of the output block is the point (live/OUTPUTS.md). A stock
# split-root layout would publish `output "vpc_id"` here and read it back
# through `terraform_remote_state` in the consumer, which means the consumer
# reads a *state file* this root writes. A live root writes none: ownership
# is the marker tags on the resources themselves. So there is nothing for a
# remote-state data source to read, and nothing to publish either - the
# consumer reads the live VPC instead, in terraform/service/main.tf.
#
# Nothing below is choudoufu-specific. Stock OpenTofu runs this root
# unchanged; what makes it live is estate.chdf.hcl beside it.

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source = "hashicorp/aws"
      # The provider version choudoufu's admission evidence was surveyed
      # against (live/survey.json's provider_version). The identity a marker
      # carries is derived from the provider's identity schema, so the
      # provider version is part of what a plan means: pin it.
      version = "~> 6.59.0"
    }
  }
}

variable "aws_region" {
  description = "Region both estates live in. The pipelines pass this as TF_VAR_aws_region."
  type        = string
  default     = "us-east-1"
}

variable "vpc_cidr" {
  description = "The producer VPC's CIDR. The service estate's subnet CIDR must sit inside it."
  type        = string
  default     = "10.90.0.0/16"
}

provider "aws" {
  region = var.aws_region
}

# choudoufu stamps `tofu-estate = "cross-estate-network"` and
# `tofu-address = "aws_vpc.main"` onto this VPC on apply. Those two tags are
# what the service estate filters on; nothing here has to be arranged for the
# consumer's benefit, because every managed resource in a live root already
# carries the pair.
resource "aws_vpc" "main" {
  cidr_block = var.vpc_cidr

  tags = {
    Example = "cross-estate-dependency"
  }
}
