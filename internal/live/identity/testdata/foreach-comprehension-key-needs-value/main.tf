# The safety boundary [resolver.forExprKeys] must never cross: the key
# clause here is "v.login", not the loop's own key variable - it reads the
# for-comprehension's VALUE side, so the key set is NOT knowable without
# the same value data the whole fix exists to avoid needing. login comes
# from a managed resource's attribute, so this must still refuse exactly
# as evaluating the whole for_each expression already does, not silently
# answer as though the key clause had been "k".

resource "aws_iam_role" "team" {
  for_each = toset(["a", "b"])

  name = "team-${each.key}"
}

locals {
  users = {
    a = { login = aws_iam_role.team["a"].name }
    b = { login = aws_iam_role.team["b"].name }
  }

  merged = {
    for k, v in local.users : v.login => v
  }
}

resource "aws_iam_user" "this" {
  for_each = local.merged

  name = each.key
}
