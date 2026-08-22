# GitHub issue #368, reduced from two real estates: a pure function applied to
# a value that is only known once a parent has been read.
#
# The ECS half (module.cluster / module.svc below) is
# terraform-aws-modules/terraform-aws-ecs's examples/fargate in shape:
# `cluster_arn = module.ecs_cluster.arn` at the call, and inside the module
# `local.cluster_name = try(element(split("/", var.cluster_arn), 1), "")`
# feeding aws_appautoscaling_target's resource_id. The deferred read itself
# already resolved before #368 - aws_ecs_service.this below proves it, with
# the same arn as a whole reference - so what was missing was only the ability
# to say "split this, take element 1".
#
# The security-group half (aws_security_group_rule.ingress) is
# terraform-aws-modules/terraform-aws-security-group's own universal ingress
# rule, `compact(split(",", <cidr blocks>))`, over a [Component.SoleElement]
# component: a collection whose LENGTH is a function of the live value, which
# neither of the two existing sole-element narrowings can count.
#
# The three resources whose names end in a control are negative: each isolates
# one boundary of the render contract and must NOT resolve.
provider "aws" {
  region = "us-east-1"
}

module "cluster" {
  source = "./cluster"

  name = "ex-fargate"
}

module "svc" {
  source = "./svc"

  name        = "ex-fargate"
  cluster_arn = module.cluster.arn
}

resource "aws_vpc" "this" {
  count = 1

  cidr_block = "10.0.0.0/16"
}

resource "aws_security_group" "this" {
  count = 1

  name = "ex-fargate"
}

resource "aws_security_group_rule" "ingress" {
  count = 1

  security_group_id = aws_security_group.this[0].id
  type              = "ingress"
  from_port         = 5432
  to_port           = 5432
  protocol          = "tcp"

  cidr_blocks = compact(split(",", aws_vpc.this[0].cidr_block))
}

# Control 1: the source of a pipeline has to be ONE deferred read. Here it is
# a template - a literal and a read concatenated - and one value in / one
# value out is the whole of what [applyOps] can carry.
resource "aws_security_group_rule" "multipart_control" {
  count = 1

  security_group_id = aws_security_group.this[0].id
  type              = "ingress"
  from_port         = 5433
  to_port           = 5433
  protocol          = "tcp"

  cidr_blocks = compact(split(",", "${aws_vpc.this[0].cidr_block},10.1.0.0/16"))
}

# Control 2: every parameter of a transform other than the value itself has to
# be readable HERE. element()'s own error for a negative index is not modelled,
# so a negative index is declined rather than rendered.
resource "aws_security_group_rule" "negative_index_control" {
  count = 1

  security_group_id = aws_security_group.this[0].id
  type              = "ingress"
  from_port         = 5434
  to_port           = 5434
  protocol          = "tcp"

  cidr_blocks = [element(split(",", aws_vpc.this[0].cidr_block), -1)]
}

# Control 3: a pipeline that still holds a LIST where a string belongs is not
# an identity component. Nothing narrows this one - protocol is an ordinary
# scalar component - so the pipeline would have to render a list into a marker.
resource "aws_security_group_rule" "list_valued_control" {
  count = 1

  security_group_id = aws_security_group.this[0].id
  type              = "ingress"
  from_port         = 5435
  to_port           = 5435
  protocol          = split(",", aws_vpc.this[0].cidr_block)
  cidr_blocks       = ["10.2.0.0/16"]
}
