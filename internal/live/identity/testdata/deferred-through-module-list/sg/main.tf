variable "ingress_with_cidr_blocks" {
  type    = list(any)
  default = []
}

variable "ingress_cidr_blocks" {
  type    = list(string)
  default = []
}

resource "aws_security_group" "this" {
  count = 1

  name = "sg-fixed"
}

# The estate's own rule, verbatim in shape: a count.index into the caller's
# list, and lookup() to pull the field out.
resource "aws_security_group_rule" "estate_shape" {
  count = length(var.ingress_with_cidr_blocks)

  security_group_id = aws_security_group.this[0].id
  type              = "ingress"
  from_port         = 5432
  to_port           = 5432
  protocol          = "tcp"

  cidr_blocks = compact(split(
    ",",
    lookup(
      var.ingress_with_cidr_blocks[count.index],
      "cidr_blocks",
      join(",", var.ingress_cidr_blocks),
    ),
  ))
}

# Variant: count.index, but no lookup() and no compact/split. Isolates the
# INDEX half of the routing question.
resource "aws_security_group_rule" "count_index_only" {
  count = length(var.ingress_with_cidr_blocks)

  security_group_id = aws_security_group.this[0].id
  type              = "ingress"
  from_port         = 5433
  to_port           = 5433
  protocol          = "tcp"

  cidr_blocks = [var.ingress_with_cidr_blocks[count.index].cidr_blocks]
}

# Variant: a literal index and the lookup()/compact/split. Isolates the
# LOOKUP half.
resource "aws_security_group_rule" "lookup_only" {
  count = length(var.ingress_with_cidr_blocks)

  security_group_id = aws_security_group.this[0].id
  type              = "ingress"
  from_port         = 5434
  to_port           = 5434
  protocol          = "tcp"

  cidr_blocks = compact(split(
    ",",
    lookup(
      var.ingress_with_cidr_blocks[0],
      "cidr_blocks",
      join(",", var.ingress_cidr_blocks),
    ),
  ))
}

# Control: a literal index, no lookup, no functions. This one resolves, and
# it is what makes the two above findings rather than "the module boundary
# is impassable".
resource "aws_security_group_rule" "literal_index" {
  count = length(var.ingress_with_cidr_blocks)

  security_group_id = aws_security_group.this[0].id
  type              = "ingress"
  from_port         = 5435
  to_port           = 5435
  protocol          = "tcp"

  cidr_blocks = [var.ingress_with_cidr_blocks[0].cidr_blocks]
}
