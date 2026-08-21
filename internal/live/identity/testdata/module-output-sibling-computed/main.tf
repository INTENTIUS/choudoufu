# GitHub issue #346, reduced from terraform-aws-modules/terraform-aws-vpc's
# "complete" example (its own examples/complete/main.tf:94-99 verbatim in
# shape): a module call argument whose skeleton is a literal map and one of
# whose leaves is a ONE-ELEMENT LIST holding another module call's output,
# which itself reads a managed sibling's Optional+Computed attribute. The
# receiving module binds each.value to that element and builds a
# Component.SoleElement identity argument out of it.
#
# Three separate walls stand between that shape and a resolution, and each
# module call below isolates one of them. Every call passes a different port so
# no two instances can collide on one identity; the ports are otherwise
# meaningless.
locals {
  vpc_cidr = "10.99.0.0/16"
}

module "vpc" {
  source = "./vpc"
  cidr   = local.vpc_cidr
}

# A managed resource in the root module, used only to make an element
# expression unevaluable as a VALUE, which is what binds each.value as an
# EXPRESSION - the route the whole issue travels. Without one of these the
# for_each map evaluates whole and none of this is exercised.
resource "aws_vpc" "root" {
  cidr_block = "10.96.0.0/16"
}

# The issue itself: a one-element list holding a module output that reads a
# sibling's Computed attribute.
module "endpoints" {
  source = "./endpoints"
  port   = 443

  security_group_rules = {
    ingress_https = {
      description = "HTTPS from VPC"
      cidr_blocks = [module.vpc.vpc_cidr_block]
    }
  }
}

# Wall one on its own: the list construct. The value is a plain literal, so
# nothing about the module boundary or the sibling is involved; only the
# one-element narrowing is.
module "endpoints_literal_list" {
  source = "./endpoints"
  port   = 444

  security_group_rules = {
    ingress_https = {
      description = aws_vpc.root.id
      cidr_blocks = ["10.97.0.0/16"]
    }
  }
}

# Wall one again, this time over a module output that reads nothing managed at
# all: an ordinary configuration value that crossed a module boundary.
module "endpoints_output_list" {
  source = "./endpoints"
  port   = 445

  security_group_rules = {
    ingress_https = {
      description = aws_vpc.root.id
      cidr_blocks = [module.vpc.passthrough]
    }
  }
}

# Wall two on its own: the same sibling Computed attribute with no list
# construct around it, so only the deferred parent read is exercised.
module "endpoints_bare_computed" {
  source = "./endpoints"
  port   = 446

  security_group_rules = {
    ingress_https = {
      description = aws_vpc.root.id
      cidr_blocks = module.vpc.vpc_cidr_block
    }
  }
}

# The control that must keep refusing: TWO elements. The AWS API, not this
# list's order, decides how more than one composes.
module "endpoints_two" {
  source = "./endpoints"
  port   = 447

  security_group_rules = {
    ingress_https = {
      description = aws_vpc.root.id
      cidr_blocks = [module.vpc.passthrough, "10.95.0.0/16"]
    }
  }
}
