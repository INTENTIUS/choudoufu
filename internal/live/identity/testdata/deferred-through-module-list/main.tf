# GitHub issue #368's OTHER estate, reduced: corpus-rds-complete-postgres.
#
# The caller writes terraform-aws-modules/terraform-aws-rds's own
# examples/complete-postgres shape verbatim - a list of one object literal
# whose `cidr_blocks` leaf reads another module's output, which is itself a
# managed resource's attribute - and the receiving module is
# terraform-aws-modules/terraform-aws-security-group, whose universal ingress
# rule indexes that list with `count.index` and pulls the field out with
# `lookup(..., "cidr_blocks", join(",", var.ingress_cidr_blocks))`.
#
# #368 was filed on the reading that the FUNCTION is what blocks this
# estate. It is not, or not only: see deferred_through_module_list_test.go,
# which measures all four variants against this one fixture.
provider "aws" {
  region = "us-east-1"
}

module "vpc" {
  source = "./vpc"

  cidr = "10.0.0.0/16"
}

module "sg" {
  source = "./sg"

  ingress_with_cidr_blocks = [
    {
      from_port   = 5432
      to_port     = 5432
      protocol    = "tcp"
      cidr_blocks = module.vpc.vpc_cidr_block
    },
  ]

  fallback_cidr = module.vpc.other_cidr_block
}

module "sg_typed" {
  source = "./sgtyped"

  rules_object_string  = [{ from_port = 5437, cidr_blocks = module.vpc.vpc_cidr_block }]
  rules_map_string     = [{ from_port = 5438, cidr_blocks = module.vpc.vpc_cidr_block }]
  rules_object_missing = [{ from_port = 5439, cidr_blocks = module.vpc.vpc_cidr_block }]

  fallback_cidr = module.vpc.other_cidr_block
}
