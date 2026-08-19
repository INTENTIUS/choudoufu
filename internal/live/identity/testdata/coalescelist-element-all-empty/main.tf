# Both coalescelist() arguments provably expand to zero instances, and there
# is no trailing literal-list argument to fall back to. coalescelist()
# itself errors at apply time in exactly this case ("no non-empty lists"),
# so this package should refuse ahead of that with its own specific reason,
# not crash and not silently resolve nothing useful.

resource "aws_route_table" "database" {
  count = 0
}

resource "aws_route_table" "private" {
  count = 0
}

resource "aws_route_table_association" "empty_branches" {
  subnet_id = "subnet-fake"
  route_table_id = element(
    coalescelist(aws_route_table.database[*].id, aws_route_table.private[*].id),
    0,
  )
}
