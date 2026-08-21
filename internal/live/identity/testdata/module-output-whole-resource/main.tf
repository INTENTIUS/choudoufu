# GitHub issue #354, reduced from terraform-aws-modules/terraform-aws-autoscaling's
# examples/complete (its own main.tf:50-55 verbatim in shape): a module call
# argument whose skeleton is a literal map and one of whose leaves reads
# another module's output; that output publishes a WHOLE managed resource, so
# the leaf indexes into it and selects an attribute; and the receiving module
# declares the argument as map(object({...})), iterates it behind a guard
# conditional, and builds an identity out of each.value.
#
# Three things stood between that shape and a resolution, and this fixture
# exercises all three at once:
#
#  1. The for_each source is `var.create && var.attachments != null ?
#     var.attachments : {}`. The structural key-set chase has no arm for a
#     conditional, so the whole source falls through to the tolerant retry,
#     which answers with a VALUE and carries no element expressions at all.
#     [resolver.elementExprBindings] collects them beside that value.
#  2. The declared type drops the element expression at the hop, because a
#     conversion is not the identity function on an element as a whole.
#     [preservedExpr]'s object case carries it across with the type it must be
#     read under.
#  3. The leaf is `module.alb.target_groups["ex_asg"].arn`, and the chase
#     lands inside module.alb on `aws_lb_target_group.this` with `["ex_asg"]`
#     and `.arn` still owed. [resolver.selectStatic] had no arm for a managed
#     resource reference with steps left, so it declined at the last hop.
provider "aws" {
  region = "us-east-1"
}

module "alb" {
  source = "./alb"
  groups = ["ex_asg"]
}

module "asg" {
  source = "./asg"

  attachments = {
    ex-alb = {
      identifier = module.alb.target_groups["ex_asg"].arn
    }
  }

  # The negative control for the declared-type gate: the same leaf, selected
  # through an attribute the module declares a number.
  numeric = {
    n = {
      identifier = module.alb.target_groups["ex_asg"].arn
    }
  }

  # The negative control for what may be READ off the parent: port is not an
  # identity attribute of aws_lb_target_group, so the deferred selection ends
  # at the same refusal a direct reference to it ends at.
  other = {
    o = {
      identifier = module.alb.target_groups["ex_asg"].port
    }
  }

  # The negative control for the ordering: `name` is not written here at all,
  # so the module's own declared type supplies null and coalesce() takes the
  # key. `target` is what makes the element unreadable as a value.
  policies = {
    p1 = {
      target = module.alb.target_groups["ex_asg"].arn
    }
  }
}
