variable "cidr" {
  type = string
}

resource "aws_vpc" "this" {
  count = 1

  cidr_block = var.cidr
}

# terraform-aws-modules/terraform-aws-vpc's own outputs.tf, in shape.
output "vpc_cidr_block" {
  value = try(aws_vpc.this[0].cidr_block, null)
}
