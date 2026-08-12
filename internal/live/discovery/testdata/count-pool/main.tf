# A count block on its own, with a cardinality this package's tests can move.
# The estate fixture's aws_eip.pool is fixed at three instances and may not be
# edited, and every claim about set matching is a claim about what happens when
# the count changes.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

variable "pool_size" {
  type    = number
  default = 3
}

# The fungible set. Nothing here distinguishes one instance from another,
# which is the shape the set matcher exists for: no argument reads
# count.index except the tofu-address marker, whose specified job is to carry
# the instance address.
resource "aws_eip" "pool" {
  count = var.pool_size

  domain = "vpc"

  tags = {
    tofu-estate  = "count-e2e"
    tofu-address = "aws_eip.pool:${count.index}"
  }
}
