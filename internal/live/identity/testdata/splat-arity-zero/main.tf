# Danger case: the splat's source expands to NO instances, so join() of it
# is the empty string. OpenTofu is perfectly happy with that; an identity is
# not. An empty component claims ownership of nothing while looking exactly
# like an ordinary answer, so this refuses rather than resolving to "".

variable "create_primary" {
  type    = bool
  default = false
}

resource "aws_subnet" "this" {
  cidr_block = "10.0.0.0/24"
}

resource "aws_route_table" "primary" {
  count = var.create_primary ? 1 : 0
}

resource "aws_route_table_association" "empty" {
  subnet_id      = aws_subnet.this.id
  route_table_id = join("", aws_route_table.primary.*.id)
}
