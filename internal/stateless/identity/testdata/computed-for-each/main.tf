# for_each computed from another resource's attributes. The keys are not
# knowable from configuration, so the instance set is not knowable.
resource "aws_subnet" "this" {
  for_each = toset(["a", "b"])

  cidr_block = "10.42.1.0/24"
}

resource "aws_route_table_association" "this" {
  for_each = toset([for s in aws_subnet.this : s.id])

  subnet_id      = each.value
  route_table_id = "rtb-fixed"
}
