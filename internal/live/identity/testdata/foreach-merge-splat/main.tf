# #189's merge(list...) half of the key-set fix: the corpus shape is
# simpleinfra's team-members-datadog/users.tf -
# "merge(local._do_not_use_all_teams...)", where the argument is a single
# LIST of objects rather than several separate object arguments. The list's
# own elements are for-comprehensions whose value clauses reach a managed
# resource - same as [resolver.staticForEachKeys]'s existing multi-argument
# merge() case, just reached through a splat instead of separate arguments.

resource "aws_iam_role" "team" {
  for_each = toset(["a", "b"])

  name = "team-${each.key}"
}

locals {
  teams = [
    { for k, v in { a = {} } : k => merge(v, { role = aws_iam_role.team["a"].name }) },
    { for k, v in { b = {} } : k => merge(v, { role = aws_iam_role.team["b"].name }) },
  ]

  merged = merge(local.teams...)
}

resource "aws_iam_user" "this" {
  for_each = local.merged

  name = each.key
}
