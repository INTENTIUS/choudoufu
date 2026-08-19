# Both splat arguments provably expand to zero instances, so coalescelist()
# falls through to its trailing literal-list argument, and element()'s
# index [0] lands on that literal element - not on any resource's
# attribute. That is NOT identity-bearing via a marker at all, so the
# result should resolve CONCRETE from the literal, not refuse and not
# fabricate a resource reference.

resource "aws_route_table" "database" {
  count = 0
}

resource "aws_route_table" "private" {
  count = 0
}

resource "aws_route_table_association" "database" {
  subnet_id = "subnet-fake"
  route_table_id = element(
    coalescelist(aws_route_table.database[*].id, aws_route_table.private[*].id, ["rtb-fallback"]),
    0,
  )
}
