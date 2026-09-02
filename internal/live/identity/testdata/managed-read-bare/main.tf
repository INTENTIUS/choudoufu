# for_each over a WHOLE managed resource. The instance keys come from the
# parent block's own expansion and never from a read, so this must keep
# that route even when a caller happens to hand in a read of the parent.
resource "aws_subnet" "this" {
  for_each   = toset(["a", "b"])
  vpc_id     = "vpc-1"
  cidr_block = "10.0.0.0/24"
}

resource "aws_route_table_association" "this" {
  for_each       = aws_subnet.this
  subnet_id      = each.value.id
  route_table_id = "rtb-1"
}
