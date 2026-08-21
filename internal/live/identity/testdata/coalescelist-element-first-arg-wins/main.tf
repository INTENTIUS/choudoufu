# GitHub issue #324 item 1: element(coalescelist(A[*].id, B[*].id), idx) -
# terraform-aws-modules/vpc's own route_table_id accessor for
# aws_route_table_association.database (main.tf:497-500 in the pinned
# source), the real corpus shape from corpus-rds-complete-postgres. Here
# aws_route_table.database provably expands to a nonzero instance count, so
# coalescelist() selects it (its first argument) over aws_route_table.private,
# and element()'s own wraparound then picks the count.index-th instance of
# THAT splat, not the other one.

locals {
  n = 3
}

resource "aws_subnet" "database" {
  count      = local.n
  vpc_id     = "vpc-x"
  cidr_block = "10.0.${count.index}.0/24"
}

resource "aws_route_table" "database" {
  count = local.n
}

resource "aws_route_table" "private" {
  count = local.n
}

resource "aws_route_table_association" "database" {
  count = local.n

  subnet_id = element(aws_subnet.database[*].id, count.index)
  route_table_id = element(
    coalescelist(aws_route_table.database[*].id, aws_route_table.private[*].id),
    count.index,
  )
}
