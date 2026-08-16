# The corpus shape this reached first: terraform-aws-modules/iam's
# iam-role-for-service-accounts, whose aws_iam_role_policy_attachment
# additional has
#
#   for_each = { for k, v in var.policies : k => v if var.create }
#
# and whose examples/iam-role-for-service-accounts passes
#
#   policies = {
#     AmazonEKS_CNI_Policy = "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy"
#     additional           = aws_iam_policy.additional.arn
#   }
#
# One key's value is a literal ARN and the other's is a managed resource's
# attribute. The comprehension's value clause is the bare loop variable, so
# each.value for a key is the source element beside it - proven for the
# first, not for the second.
#
# Written with a local rather than a module variable so the fixture is one
# file; the chase through [resolver.namedDef] is the same either way, and
# local-attr-module-var already covers the module-variable hop on its own.
resource "aws_iam_group" "admins" {
  name = "admins"
}

variable "create" {
  type    = bool
  default = true
}

locals {
  members = {
    "alice" = "alice-from-config"
    "bob"   = aws_iam_group.admins.name
  }
}

resource "aws_iam_user" "team" {
  for_each = { for k, v in local.members : k => v if var.create }

  name = each.value
}
