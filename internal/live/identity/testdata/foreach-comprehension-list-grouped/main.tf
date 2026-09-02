# Grouping mode is the one place a repeated key legitimately means one
# entry: `k => v...` collects every element sharing a key into a list under
# it, so `i % 2` over three elements really is TWO instances, item-0 and
# item-1, and OpenTofu creates two.
#
# The pairing with foreach-comprehension-list-key-collides is the point.
# The same key clause over the same list refuses without the ellipsis and
# resolves with it, because the ellipsis is what makes the fold the
# configuration's own intent rather than this package losing an instance.

resource "aws_iam_role" "team" {
  name = "team"
}

locals {
  hosts = [
    { host = "alpha" },
    { host = "beta" },
    { host = "gamma" },
  ]

  grouped = {
    for i, h in local.hosts : "item-${i % 2}" => merge(h, { role = aws_iam_role.team.name })...
  }
}

resource "aws_iam_user" "this" {
  for_each = local.grouped
  name     = each.key
}
