variable "security_group_rules" { type = any }
variable "port" { type = number }

# The lookup() spelling, which is what the module publishes.
resource "aws_security_group_rule" "this" {
  for_each = { for k, v in var.security_group_rules : k => v }

  security_group_id = "sg-fixed"
  protocol          = try(each.value.protocol, "tcp")
  from_port         = var.port
  to_port           = var.port
  type              = try(each.value.type, "ingress")

  description = try(each.value.description, null)
  cidr_blocks = lookup(each.value, "cidr_blocks", null)
}

# The dotted spelling of the identical selection, which #346 measured refuses
# identically. Ported one apart so the two cannot collide.
resource "aws_security_group_rule" "dotted" {
  for_each = { for k, v in var.security_group_rules : k => v }

  security_group_id = "sg-fixed"
  protocol          = "tcp"
  from_port         = var.port + 1000
  to_port           = var.port + 1000
  type              = "ingress"

  cidr_blocks = each.value.cidr_blocks
}

# lookup()'s fallback arm: a key the element provably does not have, so the
# language takes the third argument. Nothing may fall back over a key that IS
# there and merely could not be resolved.
resource "aws_security_group_rule" "absent" {
  for_each = { for k, v in var.security_group_rules : k => v }

  security_group_id = "sg-fixed"
  protocol          = "tcp"
  from_port         = var.port + 2000
  to_port           = var.port + 2000
  type              = "ingress"

  cidr_blocks = lookup(each.value, "no_such_key", "10.94.0.0/16")
}
