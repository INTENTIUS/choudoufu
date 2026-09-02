# GitHub issue #324 item 2: local.this_sg_id = concat(A[*].id, B[*].id,
# [literal])[N], reached through a local value - terraform-aws-modules/
# security-group's own universal security_group_id accessor
# (module.security_group's locals.this_sg_id), which every rule resource
# that module creates reads. B (this_name_prefix) has zero instances here,
# matching the real module's own default create_sg=true /
# create_sg_name_prefix=false split, so concat's second argument
# contributes nothing and [0] must resolve through the first splat's own
# single instance.

variable "create_sg" {
  type    = bool
  default = true
}

variable "security_group_id" {
  type    = string
  default = ""
}

resource "aws_security_group" "this" {
  count  = var.create_sg ? 1 : 0
  vpc_id = "vpc-x"
}

resource "aws_security_group" "this_name_prefix" {
  count  = 0
  vpc_id = "vpc-x"
}

locals {
  this_sg_id = var.create_sg ? concat(aws_security_group.this.*.id, aws_security_group.this_name_prefix.*.id, [""])[0] : var.security_group_id
}

resource "aws_security_group_rule" "ingress" {
  type              = "ingress"
  security_group_id = local.this_sg_id
  from_port         = 80
  to_port           = 80
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
}
