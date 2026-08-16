# #239's second boundary: the list's length is perfectly static, but the
# KEY clause reads a managed resource's attribute. The number of instances
# is knowable and their keys are not, and a key is what becomes an address
# and then a marker - so this refuses, exactly as it would if the whole
# for_each expression had been the resource attribute.
#
# The narrower distinction #239 turns on is visible here: knowing a list's
# LENGTH is not knowing what its elements are called.

resource "aws_iam_role" "team" {
  name = "team"
}

locals {
  hosts = [
    { host = "alpha" },
    { host = "beta" },
  ]

  byidx = {
    for i, h in local.hosts : "${aws_iam_role.team.name}-${i}" => h
  }
}

resource "aws_iam_user" "this" {
  for_each = local.byidx
  name     = each.key
}
