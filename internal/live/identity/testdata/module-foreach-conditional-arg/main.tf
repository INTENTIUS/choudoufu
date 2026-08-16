# The seam between the two audited merges: wall/localvalue (#189) taught
# namedDef to evaluate a module CALL's argument expression in the call's own
# repetition scope, and wall/conditional (#196) taught resolveExpr to
# decompose a conditional. They landed on independent branches and merged
# cleanly in text, which proves nothing about whether they compose.
#
# Here they meet in one expression: a conditional inside a for_each'd module
# call's argument, whose condition is a root variable and whose branches
# index two for_each'd sibling resources by the CALL's own each.key. Getting
# this right needs the conditional to be evaluated against the module call's
# repetition data (not the caller's, not none), and needs each module
# instance to see its OWN each.key rather than another instance's - a leak
# would show as a wrong identity, not a refusal.

variable "use_primary" {
  type    = bool
  default = true
}

resource "aws_iam_role" "primary" {
  for_each = toset(["alice", "bob"])
  name     = "primary-${each.key}"
}

resource "aws_iam_role" "secondary" {
  for_each = toset(["alice", "bob"])
  name     = "secondary-${each.key}"
}

locals {
  users = {
    alice = { role = "admin" }
    bob   = { role = "reader" }
  }
}

module "user" {
  source   = "./user"
  for_each = local.users

  # A conditional inside a module-call argument, evaluated in the CALL's own
  # repetition scope (each.key of the module block), selecting between two
  # for_each'd sibling resources indexed by that same each.key.
  role_name = var.use_primary ? aws_iam_role.primary[each.key].name : aws_iam_role.secondary[each.key].name
}
