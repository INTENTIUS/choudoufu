# aws_subnet.this[each.key].id: a reference into another for_each'd
# resource selected by a computed index, rather than a bare reference
# (resolveTraversal's hcl.AbsTraversalForExpr path) or a syntactic list
# construct. each.key is known per instance from the child's own for_each,
# so the addressed subnet instance - and its identity, server-assigned
# though it is - is exactly as reachable as aws_subnet.this["a"].id would be
# written by hand.

resource "aws_subnet" "this" {
  for_each   = toset(["a", "b"])
  cidr_block = "10.0.0.0/24"
}

resource "aws_route_table_association" "assoc" {
  for_each = toset(["a", "b"])

  subnet_id      = aws_subnet.this[each.key].id
  route_table_id = "rtb-fixed"
}
