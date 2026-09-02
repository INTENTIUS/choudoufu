# The danger case: a conditional's CONDITION itself references a managed
# resource, so which branch applies is not known until apply. This must
# still refuse - reusing [resolver.isSymbolic], the same check a bare
# resource-attribute reference already goes through - not resolve to
# whichever branch happens to be lexically first.

resource "aws_subnet" "flag" {
  cidr_block = "10.0.0.0/24"
}

resource "aws_route_table" "primary" {}
resource "aws_route_table" "secondary" {}

resource "aws_route_table_association" "assoc" {
  subnet_id      = aws_subnet.flag.id
  route_table_id = aws_subnet.flag.id == "sn-primary" ? aws_route_table.primary.id : aws_route_table.secondary.id
}
