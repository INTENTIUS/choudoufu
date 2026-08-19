# element()'s own wraparound (modulo the list length): aws_subnet.small has
# 2 instances and aws_route_table_association.wrap has 5, so indices 2, 3
# and 4 must resolve against aws_subnet.small[0], [1] and [0] - the same
# instances element(aws_subnet.small[*].id, count.index) would pick at apply
# time. route_table_id is a plain per-instance literal (not identity-bearing
# on its own resource) so the composite identity stays distinct across all
# 5 associations despite subnet_id repeating - this fixture is about the
# wraparound arithmetic, not about GitHub issue #196's collision detector.
resource "aws_subnet" "small" {
  count      = 2
  vpc_id     = "vpc-x"
  cidr_block = "10.1.${count.index}.0/24"
}

resource "aws_route_table_association" "wrap" {
  count = 5

  subnet_id      = element(aws_subnet.small[*].id, count.index)
  route_table_id = "rtb-${count.index}"
}
