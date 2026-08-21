# Both splat arguments expand to zero instances, so concat()'s provable
# index [0] lands on the trailing literal-list element rather than on any
# resource's attribute. That is NOT identity-bearing via a marker - it is
# whatever string the configuration itself wrote - so the result should
# resolve concrete from the literal alone, not refuse and not fabricate a
# resource reference.

resource "aws_security_group" "a" {
  count  = 0
  vpc_id = "vpc-x"
}

resource "aws_security_group" "b" {
  count  = 0
  vpc_id = "vpc-x"
}

locals {
  picked_id = concat(aws_security_group.a.*.id, aws_security_group.b.*.id, ["sg-fallback"])[0]
}

resource "aws_security_group_rule" "ingress" {
  type              = "ingress"
  security_group_id = local.picked_id
  from_port         = 80
  to_port           = 80
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
}
