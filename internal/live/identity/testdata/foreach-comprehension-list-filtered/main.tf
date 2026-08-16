# An "if" clause decides membership, so it decides the key set. Where it
# evaluates from what is bound - here from the loop's own index and the
# element the statically-readable source supplied - the key set is still
# provable, and it is provable to be SMALLER than the source: two instances
# out of three, item-0 and item-1, exactly what OpenTofu expands this to.
#
# Before #239 an if clause refused the whole comprehension unconditionally,
# on the grounds that it might read a value side nothing had read. That is
# the right answer only when the clause cannot be evaluated; see
# foreach-comprehension-filter-unreadable.

resource "aws_iam_role" "team" {
  name = "team"
}

locals {
  hosts = [
    { host = "alpha", keep = true },
    { host = "beta", keep = true },
    { host = "gamma", keep = false },
  ]

  byidx = {
    for i, h in local.hosts : "item-${i}" => merge(h, { role = aws_iam_role.team.name }) if h.keep
  }
}

resource "aws_iam_user" "this" {
  for_each = local.byidx
  name     = each.key
}
