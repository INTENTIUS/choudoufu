# #239's first boundary: a list whose LENGTH is not knowable from
# configuration. The source is split() over a managed resource's attribute,
# so neither route to a key set reaches one - the collection does not
# evaluate (the resource attribute is only known once the cloud is read),
# and its syntax is a function call that is not merge, whose result length
# nothing here can count.
#
# There is no honest answer to "how many instances", so this must refuse.
# Answering "one per element of something" would be the #178 defect again
# with a different arithmetic.

resource "aws_iam_role" "team" {
  name = "team"
}

locals {
  hosts = split(",", aws_iam_role.team.name)

  byidx = {
    for i, h in local.hosts : "item-${i}" => h
  }
}

resource "aws_iam_user" "this" {
  for_each = local.byidx
  name     = each.key
}
