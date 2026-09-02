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

# A SECOND vpc, so a control can name a fallback this package cannot compute
# and still tell the two apart: without the declared-type gate the wrong
# answer is `this`'s cidr_block, and the right answer - the one OpenTofu
# computes - is `other`'s.
resource "aws_vpc" "other" {
  count = 1

  cidr_block = "10.55.0.0/16"
}

output "other_cidr_block" {
  value = try(aws_vpc.other[0].cidr_block, null)
}
