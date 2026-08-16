# #189's for-comprehension half of the key-set fix (forExprKeys,
# localvalue.go): the corpus shape is simpleinfra's
# team-members-datadog/users.tf - a for_each source built by a
# for-comprehension whose VALUE clause reaches a managed resource's
# attribute (datadog_role.<x>.name in the real corpus, aws_iam_role.team
# here), but whose KEY clause is the bare loop key and needs nothing from
# the value at all.

resource "aws_iam_role" "team" {
  for_each = toset(["a", "b"])

  name = "team-${each.key}"
}

locals {
  users = {
    a = { login = "alice" }
    b = { login = "bob" }
  }

  # The value clause reads aws_iam_role.team[k].name - a managed resource's
  # attribute - so evaluating merged AS A WHOLE fails. The key clause, "k",
  # needs none of that.
  merged = {
    for k, v in local.users : k => merge(v, { role = aws_iam_role.team[k].name })
  }
}

resource "aws_iam_user" "this" {
  for_each = local.merged

  name = each.key
}
