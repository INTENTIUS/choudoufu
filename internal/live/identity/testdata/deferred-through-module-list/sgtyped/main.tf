# The declared-type half of the fold, in three variables that differ only in
# what the type says about the attribute being selected.
#
# OpenTofu converts a module call's argument to the declared type before
# anything in here reads it (prepareFinalInputVariableValue), so rendering
# the CALLER's expression is sound only where that conversion is the identity
# function on the selected leaf. The first two say it is; the third drops the
# attribute entirely, and lookup() answers a dropped attribute with its third
# argument - silently, which is the whole reason the gate exists.

variable "rules_object_string" {
  type    = list(object({ from_port = number, cidr_blocks = string }))
  default = []
}

variable "rules_map_string" {
  type    = list(map(string))
  default = []
}

variable "rules_object_missing" {
  type    = list(object({ from_port = number }))
  default = []
}

# The same uncomputable fallback the sg module carries, for the same reason.
variable "fallback_cidr" {
  type    = string
  default = ""
}

resource "aws_security_group" "this" {
  count = 1

  name = "sg-typed"
}

# The type declares cidr_blocks a string, so the conversion leaves the
# caller's own value alone and this resolves.
resource "aws_security_group_rule" "typed_object_string" {
  count = length(var.rules_object_string)

  security_group_id = aws_security_group.this[0].id
  type              = "ingress"
  from_port         = 5437
  to_port           = 5437
  protocol          = "tcp"

  cidr_blocks = compact(split(
    ",",
    lookup(var.rules_object_string[count.index], "cidr_blocks", ""),
  ))
}

# terraform-aws-modules/security-group's own declaration in shape: every
# value is a string inside the module, so the conversion is the identity
# function on this one and it resolves too.
resource "aws_security_group_rule" "typed_map_string" {
  count = length(var.rules_map_string)

  security_group_id = aws_security_group.this[0].id
  type              = "ingress"
  from_port         = 5438
  to_port           = 5438
  protocol          = "tcp"

  cidr_blocks = compact(split(
    ",",
    lookup(var.rules_map_string[count.index], "cidr_blocks", ""),
  ))
}

# The type does not declare cidr_blocks at all, so the conversion drops what
# the caller wrote before this module reads anything and lookup() answers
# with its third argument. That argument is itself uncomputable here, so the
# right verdict is a refusal - and a route that rendered the caller's own
# expression instead would write module.vpc.aws_vpc.this[0]'s cidr_block
# into a cloud tag for a rule the configuration points somewhere else.
resource "aws_security_group_rule" "typed_object_missing" {
  count = length(var.rules_object_missing)

  security_group_id = aws_security_group.this[0].id
  type              = "ingress"
  from_port         = 5439
  to_port           = 5439
  protocol          = "tcp"

  cidr_blocks = compact(split(
    ",",
    lookup(var.rules_object_missing[count.index], "cidr_blocks", var.fallback_cidr),
  ))
}
