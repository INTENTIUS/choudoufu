# The index provably falls past the first splat's own one instance and into
# a second argument that is neither a splat over a managed resource nor a
# literal list - a plain variable reference. How many elements THAT
# contributes is not knowable from configuration alone, so this package
# cannot locate the index and must refuse with its own specific reason
# rather than guess or fall back to the generic "cannot follow" message.

variable "extra_ids" {
  type    = list(string)
  default = ["sg-extra-1", "sg-extra-2"]
}

resource "aws_security_group" "a" {
  count  = 1
  vpc_id = "vpc-x"
}

locals {
  picked_id = concat(aws_security_group.a.*.id, var.extra_ids)[1]
}

resource "aws_security_group_rule" "ingress" {
  type              = "ingress"
  security_group_id = local.picked_id
  from_port         = 80
  to_port           = 80
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
}
