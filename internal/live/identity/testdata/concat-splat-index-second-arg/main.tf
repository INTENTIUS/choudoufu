# concat()'s offset arithmetic across TWO non-empty splat arguments: "a"
# contributes one element at flattened position 0, "b" contributes two more
# at positions 1 and 2. Index 2 must land on b[1], not b[0] - the same
# cumulative-length bookkeeping resolveElementCall's wraparound needs, just
# without the wraparound.

resource "aws_security_group" "a" {
  count  = 1
  vpc_id = "vpc-x"
}

resource "aws_security_group" "b" {
  count  = 2
  vpc_id = "vpc-x"
}

locals {
  picked_id = concat(aws_security_group.a.*.id, aws_security_group.b.*.id, [""])[2]
}

resource "aws_security_group_rule" "ingress" {
  type              = "ingress"
  security_group_id = local.picked_id
  from_port         = 80
  to_port           = 80
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
}
