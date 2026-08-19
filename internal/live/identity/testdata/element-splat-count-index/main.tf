# GitHub issue #321's own shape: element(<resource>[*].id, count.index) in
# an identity-bearing argument, over a splat of TAGGED (server-assigned)
# resources - the exact pattern terraform-aws-modules/vpc's
# aws_route_table_association.private uses for both subnet_id and
# route_table_id (main.tf:348-349 in the pinned v6.6.1 source).

variable "single_nat_gateway" {
  type    = bool
  default = false
}

locals {
  n = 3
}

resource "aws_subnet" "private" {
  count      = local.n
  vpc_id     = "vpc-x"
  cidr_block = "10.0.${count.index}.0/24"
}

# route_table_id's own count collapses to 1 when single_nat_gateway is true
# (the real module's own shape) - not exercised by this fixture directly
# (single_nat_gateway defaults to false, so it stays 1:1 with the
# associations below), but the conditional index expression itself,
# var.single_nat_gateway ? 0 : count.index, is exactly what
# aws_route_table_association.private's own route_table_id argument uses.
resource "aws_route_table" "private" {
  count = var.single_nat_gateway ? 1 : local.n
}

resource "aws_route_table_association" "private" {
  count = local.n

  subnet_id      = element(aws_subnet.private[*].id, count.index)
  route_table_id = element(aws_route_table.private[*].id, var.single_nat_gateway ? 0 : count.index)
}
