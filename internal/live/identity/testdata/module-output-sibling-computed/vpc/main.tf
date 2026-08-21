variable "cidr" { type = string }

resource "aws_vpc" "this" {
  count      = 1
  cidr_block = var.cidr
}

# The example's own output, verbatim in shape: a try() around a Computed
# attribute of a count-gated managed resource.
output "vpc_cidr_block" {
  value = try(aws_vpc.this[0].cidr_block, null)
}

# The same value with nothing managed anywhere in it.
output "passthrough" { value = var.cidr }
