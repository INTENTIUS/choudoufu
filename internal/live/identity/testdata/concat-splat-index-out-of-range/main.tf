# concat()'s arguments provably contribute exactly one element in total, but
# the index asks for the sixth. This is provably out of range - concat()
# itself would error at apply time - and this package should refuse ahead
# of that with a specific reason, not silently resolve nothing or crash.

resource "aws_security_group" "a" {
  count  = 1
  vpc_id = "vpc-x"
}

locals {
  picked_id = concat(aws_security_group.a.*.id)[5]
}

resource "aws_security_group_rule" "ingress" {
  type              = "ingress"
  security_group_id = local.picked_id
  from_port         = 80
  to_port           = 80
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
}
