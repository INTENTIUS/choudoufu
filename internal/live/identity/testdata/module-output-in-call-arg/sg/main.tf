variable "rules_a" { type = any }
variable "rules_b" { type = any }
variable "rules_c" { type = any }
variable "rules_d" { type = any }
variable "rules_e" { type = any }
variable "rules_f" { type = any }

resource "aws_security_group_rule" "a" {
  count             = length(var.rules_a)
  security_group_id = "sg-fixed"
  type              = "ingress"
  protocol          = "tcp"
  from_port         = lookup(var.rules_a[count.index], "from_port", 0)
  to_port           = lookup(var.rules_a[count.index], "from_port", 0)
  cidr_blocks       = compact(split(",", lookup(var.rules_a[count.index], "cidr_blocks", "")))
}

resource "aws_security_group_rule" "b" {
  count             = length(var.rules_b)
  security_group_id = "sg-fixed"
  type              = "ingress"
  protocol          = "tcp"
  from_port         = lookup(var.rules_b[count.index], "from_port", 0)
  to_port           = lookup(var.rules_b[count.index], "from_port", 0)
  cidr_blocks       = compact(split(",", lookup(var.rules_b[count.index], "cidr_blocks", "")))
}

resource "aws_security_group_rule" "c" {
  count             = length(var.rules_c)
  security_group_id = "sg-fixed"
  type              = "ingress"
  protocol          = "tcp"
  from_port         = lookup(var.rules_c[count.index], "from_port", 0)
  to_port           = lookup(var.rules_c[count.index], "from_port", 0)
  cidr_blocks       = compact(split(",", lookup(var.rules_c[count.index], "cidr_blocks", "")))
}

resource "aws_security_group_rule" "d" {
  count             = length(var.rules_d)
  security_group_id = "sg-fixed"
  type              = "ingress"
  protocol          = "tcp"
  from_port         = lookup(var.rules_d[count.index], "from_port", 0)
  to_port           = lookup(var.rules_d[count.index], "from_port", 0)
  cidr_blocks       = compact(split(",", lookup(var.rules_d[count.index], "cidr_blocks", "")))
}

resource "aws_security_group_rule" "e" {
  count             = length(var.rules_e)
  security_group_id = "sg-fixed"
  type              = "ingress"
  protocol          = "tcp"
  from_port         = lookup(var.rules_e[count.index], "from_port", 0)
  to_port           = lookup(var.rules_e[count.index], "from_port", 0)
  cidr_blocks       = compact(split(",", lookup(var.rules_e[count.index], "cidr_blocks", "")))
}

resource "aws_security_group_rule" "f" {
  count             = length(var.rules_f)
  security_group_id = "sg-fixed"
  type              = "ingress"
  protocol          = "tcp"
  from_port         = lookup(var.rules_f[count.index], "from_port", 0)
  to_port           = lookup(var.rules_f[count.index], "from_port", 0)
  cidr_blocks       = compact(split(",", lookup(var.rules_f[count.index], "cidr_blocks", "")))
}
