locals {
  estate = "estate-child"
}

resource "aws_vpc" "child" {
  cidr_block = "10.1.0.0/16"

  tags = {
    tofu-estate = local.estate
  }
}

module "grandchild" {
  source = "./grandchild"
}
