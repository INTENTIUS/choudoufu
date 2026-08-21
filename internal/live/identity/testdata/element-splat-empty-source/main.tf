# The source resource expands to zero instances: element() itself errors on
# an empty list at apply time, and this package should refuse before ever
# reaching that, the same way the arity-collapse rule refuses an empty
# splat (splat-arity-zero).
resource "aws_subnet" "empty" {
  count      = 0
  vpc_id     = "vpc-x"
  cidr_block = "10.2.0.0/24"
}

resource "aws_route_table_association" "empty_source" {
  subnet_id      = element(aws_subnet.empty[*].id, 0)
  route_table_id = "rtb-fixed"
}
