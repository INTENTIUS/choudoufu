# Danger case: the splat's source expands to TWO instances, so the join is
# two live route tables' IDs run together with a separator between them.
# That string names neither of them and must refuse - the separator being
# non-empty is not what makes it wrong, and neither is it what makes the
# arity-one case right.

resource "aws_subnet" "this" {
  cidr_block = "10.0.0.0/24"
}

resource "aws_route_table" "pair" {
  count = 2
}

resource "aws_route_table_association" "multi" {
  subnet_id      = aws_subnet.this.id
  route_table_id = join("-", aws_route_table.pair.*.id)
}

# The same two instances with an empty separator: still two objects, still
# refused. An empty separator is not permission to concatenate.
resource "aws_route_table_association" "multi_empty_separator" {
  subnet_id      = aws_subnet.this.id
  route_table_id = join("", aws_route_table.pair[*].id)
}
