# Danger case: the splat's source has a count this run cannot compute -
# var.table_count is required and nothing supplies a value. How many
# elements the splat has is therefore unknown, so whether it is the
# arity-one case is unknown too, and the answer is not "assume one".
#
# The refusal comes from [resolver.countExpansion] rather than from
# splat.go: expansionFor fails first and has already said why, and
# splatTargets deliberately adds nothing on top of it.

variable "table_count" {
  type = number
}

resource "aws_subnet" "this" {
  cidr_block = "10.0.0.0/24"
}

resource "aws_route_table" "primary" {
  count = var.table_count
}

resource "aws_route_table_association" "unknown" {
  subnet_id      = aws_subnet.this.id
  route_table_id = join("", aws_route_table.primary.*.id)
}
