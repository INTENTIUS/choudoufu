# aws_route_table.database provably expands to ZERO instances here
# (mirroring terraform-aws-modules/vpc's own
# create_database_subnet_route_table = false branch), so coalescelist()
# skips it and selects aws_route_table.private (its second argument)
# instead - and because private has fewer instances (2) than this
# resource's own count (5), element()'s own wraparound must apply to
# PRIVATE's length, not database's (which contributes nothing) and not the
# association's own count.

resource "aws_subnet" "database" {
  count      = 5
  vpc_id     = "vpc-x"
  cidr_block = "10.0.${count.index}.0/24"
}

resource "aws_route_table" "database" {
  count = 0
}

resource "aws_route_table" "private" {
  count = 2
}

resource "aws_route_table_association" "database" {
  count = 5

  subnet_id = element(aws_subnet.database[*].id, count.index)
  route_table_id = element(
    coalescelist(aws_route_table.database[*].id, aws_route_table.private[*].id),
    count.index,
  )
}
