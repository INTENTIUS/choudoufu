# Issue #313: a root-module data source feeds a local, the local feeds a
# module ARGUMENT, and the child module's own for_each and count are
# expressed over that argument. The module call carries no count and no
# for_each - which is the whole point, because that is the case
# [resolver.callerVariables] used to decline, leaving the child's var.azs
# answered by the load-time frozen closure that has never seen a data
# lookup. The read phase would classify data.aws_availability_zones.available
# readable, read it, hand the value in - and the child's expansion would
# refuse anyway, because the value could not cross the module call.
#
# terraform-aws-modules/vpc is the shape in the wild: every one of its
# subnet, route-table and association resources counts over an azs list its
# caller computes from exactly this data source.
data "aws_availability_zones" "available" {
  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, 2)
}

module "net" {
  source = "./child"

  azs = local.azs
}
