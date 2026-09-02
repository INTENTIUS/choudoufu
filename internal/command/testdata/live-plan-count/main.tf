terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

# A fungible set whose cardinality this fixture's tests move with -var. No
# argument distinguishes one instance from another, so which live resource is
# which is answered by the tofu-slot marker or by nothing at all.
variable "pool_size" {
  type    = number
  default = 3
}

resource "aws_eip" "pool" {
  count = var.pool_size

  domain = "vpc"
}
