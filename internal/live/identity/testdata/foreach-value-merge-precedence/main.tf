# merge()'s own precedence, applied to the VALUES a keyOnly expansion
# carries: a key supplied by two arguments takes the LATER argument's value.
#
# The key union has always been order-insensitive - it is sorted before it
# becomes instance keys - so nothing before this fixture could tell first-wins
# from last-wins. A value can, and getting it backwards would bind each.value
# to a value the configuration overrode, producing a marker for a live object
# the run does not own.
#
# "dyn" is here so the whole expression does not evaluate as one value; with
# only the two "shared" objects, merge() succeeds outright and this never
# reaches the key-set chase at all.
resource "aws_iam_group" "admins" {
  name = "admins"
}

locals {
  base = {
    "shared" = "from-base"
    "dyn"    = aws_iam_group.admins.name
  }
  override = {
    "shared" = "from-override"
  }
}

resource "aws_iam_user" "team" {
  for_each = merge(local.base, local.override)

  name = each.value
}
