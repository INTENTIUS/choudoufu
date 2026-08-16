# The condition depends on a required root variable with no value. This
# must refuse rather than default to picking a branch - the same "error at
# use time" every other identity argument in this package already gives an
# unset required variable.

variable "unset_flag" {
  type = bool
}

resource "aws_route_table" "primary" {}
resource "aws_route_table" "secondary" {}

resource "aws_subnet" "this" {
  cidr_block = "10.0.0.0/24"
}

resource "aws_route_table_association" "assoc" {
  subnet_id      = aws_subnet.this.id
  route_table_id = var.unset_flag ? aws_route_table.primary.id : aws_route_table.secondary.id
}
