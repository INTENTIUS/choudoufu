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

# A keyed set. The key is the identity: a live subnet's tofu-address marker
# carries it, so binding is exact and a key that changes in this map is a
# rename rather than a create beside a delete. The tests move the map with
# -var to make that happen.
variable "subnets" {
  type = map(string)
  default = {
    a = "10.42.1.0/24"
    b = "10.42.2.0/24"
  }
}

resource "aws_subnet" "this" {
  for_each = var.subnets

  cidr_block = each.value
}
