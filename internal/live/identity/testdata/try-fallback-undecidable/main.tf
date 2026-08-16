# The danger cases for fallback.go: shapes where which argument try()
# selects is NOT settled by resource expansion. Each must refuse rather than
# commit to an argument, because committing to the wrong one writes an
# identity that binds the configuration to a live object it does not own.

variable "create" {
  type    = bool
  default = true
}

resource "aws_subnet" "this" {
  cidr_block = "10.0.0.0/24"
}

resource "aws_subnet" "other" {
  cidr_block = "10.0.1.0/24"
}

resource "aws_route_table" "primary" {
  count = var.create ? 1 : 0

  tags = {
    Name = "primary"
  }
}

# One step further than an attribute. The instance exists, but indexing a
# map that is unknown at plan cannot raise an error at plan and CAN raise
# one at apply, once the map is known and the key turns out to be missing -
# so "the instance exists" does not prove this argument is the one selected.
resource "aws_route_table_association" "deep_index" {
  subnet_id      = aws_subnet.this.id
  route_table_id = try(aws_route_table.primary[0].tags["Name"], aws_route_table.primary[0].id)
}

# The argument is a function call over another resource. Whether it raises
# is not a question about expansion at all.
resource "aws_route_table_association" "wrapped" {
  subnet_id      = aws_subnet.other.id
  route_table_id = try(lower(aws_route_table.primary[0].id), aws_route_table.primary[0].id)
}
