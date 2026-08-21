# The shape terraform-aws-modules/terraform-aws-rds's complete-postgres
# example writes verbatim (its main.tf:224): a module CALL argument whose
# skeleton is a literal list-of-objects and one of whose leaves reads
# another module call's output.
locals {
  vpc_cidr = "10.77.0.0/16"
}

module "vpc" {
  source = "./vpc"
  cidr   = local.vpc_cidr
}

module "sg" {
  source = "./sg"

  # The output is an ordinary configuration value: a variable the caller set
  # from a literal. Nothing live anywhere in the chain.
  rules_a = [{ from_port = 5432, cidr_blocks = module.vpc.passthrough }]

  # The output reads a managed resource's Optional+Computed attribute. The
  # provider has its own path to a different value, so this must stay
  # refused.
  rules_b = [{ from_port = 5433, cidr_blocks = module.vpc.via_optcomp }]

  # The output reads a managed resource's plain-Optional attribute. Refused
  # too, for a narrower reason: the child module's own static evaluator has
  # no answer for a managed reference at all.
  rules_c = [{ from_port = 5434, cidr_blocks = module.vpc.via_plain }]

  # An output whose expression mints a different value on every call.
  rules_d = [{ from_port = 5435, cidr_blocks = module.vpc.via_impure }]

  # An output the child declared sensitive.
  rules_e = [{ from_port = 5436, cidr_blocks = module.vpc.via_sensitive }]

  # An output that is two CIDRs, not one: the AWS API decides how more than
  # one composes, so this package must not pick.
  rules_f = [{ from_port = 5437, cidr_blocks = module.vpc.via_two }]
}
