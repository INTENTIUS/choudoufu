# Danger case: the splat's source is keyed by strings, not integers. Its
# expansion has exactly one instance here, so an arity check alone would let
# it through - but OpenTofu does not splat a map of instances, and this
# package must not invent an order (or an "of course there is only one") for
# a collection the language never gives it. splatTargets refuses the shape
# as not-applicable and the caller's generic refusal stands.

resource "aws_subnet" "this" {
  cidr_block = "10.0.0.0/24"
}

resource "aws_route_table" "keyed" {
  for_each = toset(["only"])
}

resource "aws_route_table_association" "keyed" {
  subnet_id      = aws_subnet.this.id
  route_table_id = join("", aws_route_table.keyed[*].id)
}
