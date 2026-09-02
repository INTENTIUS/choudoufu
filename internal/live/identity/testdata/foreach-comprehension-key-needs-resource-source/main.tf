# A second safety boundary [resolver.forExprKeys] must never cross: the
# for-comprehension's SOURCE collection is a managed resource directly
# (aws_iam_role.team, not a local/var), so its own key set is not reached
# through [resolver.staticForEachKeys]'s local/var/object/merge/tuple/for
# chase at all - the recursive call on fe.CollExpr falls through to its
# final "not applicable" case and forExprKeys refuses, exactly as
# evaluating the whole for_each expression already does.

resource "aws_iam_role" "team" {
  for_each = toset(["a", "b"])

  name = "team-${each.key}"
}

locals {
  merged = {
    for k, v in aws_iam_role.team : k => v.arn
  }
}

resource "aws_iam_user" "this" {
  for_each = local.merged

  name = each.key
}
