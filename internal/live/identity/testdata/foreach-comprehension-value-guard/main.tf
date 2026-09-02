# The negative proof for the for-comprehension rule: the "if" filter reads
# v, the comprehension's own iterated value, not just its key. v stands for
# a live subnet whose attributes are not known until apply, so the
# resulting key set is not knowable from configuration - the same "cannot
# be determined until apply" refusal stock OpenTofu gives this shape,
# rather than a guess at which keys would survive the filter.
resource "aws_subnet" "this" {
  for_each = toset(["a", "b", "c"])

  cidr_block = "10.42.1.0/24"
}

resource "aws_route_table_association" "selected" {
  for_each = { for k, v in aws_subnet.this : k => v if v.cidr_block == "10.42.1.0/24" }

  subnet_id      = "subnet-${each.key}"
  route_table_id = "rtb-fixed"
}
