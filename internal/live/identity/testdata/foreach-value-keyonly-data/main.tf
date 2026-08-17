# The half of foreach-value-keyonly that still refuses after #260, and the
# one that matches the real corpus site: govuk-infrastructure's
# govuk-publishing-infrastructure aws_security_group_rule.postgres_from_eks_workers,
# whose for_each value was a data source read (data.tfe_outputs...), not a
# managed resource's attribute.
#
# #260 taught each.value to carry the element's own EXPRESSION, so an
# element that is a managed resource's identity attribute now resolves the
# way a direct reference to it always has. A data source's attribute does
# not: it is knowable at plan time and not before, so this must still refuse
# cleanly rather than answer with the key or with anything else in reach.
data "aws_iam_group" "admins" {
  group_name = "admins"
}

locals {
  members = {
    "alice" = data.aws_iam_group.admins.group_name
  }
}

resource "aws_iam_user" "team" {
  for_each = local.members

  name = each.value
}
