# The other face of the same audit finding: a for_each whose expression is
# a tuple/list. OpenTofu rejects this outright - "the for_each argument must
# be a map, or set of strings, and you have provided a value of type tuple" -
# but #189's TupleConsExpr case in staticForEachKeys read the list as though
# its elements' own object keys were the block's instance keys, and produced
# aws_iam_user.this["a"] and ["b"] with no diagnostic.
#
# staticForEachKeys now admits the tuple reading only where merge()'s
# splatted final argument stands, which is the one position where a list's
# elements really are the separate objects being unioned.

resource "aws_iam_role" "team" {
  for_each = toset(["a", "b"])
  name     = "team-${each.key}"
}

locals {
  l = [
    { a = aws_iam_role.team["a"].name },
    { b = aws_iam_role.team["b"].name },
  ]
}

resource "aws_iam_user" "this" {
  for_each = local.l
  name     = each.key
}
