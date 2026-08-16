# A for_each source whose keys are all knowable and whose VALUES are
# knowable one at a time: "alice" is a plain string, "bob" is a managed
# resource's attribute. Before values were carried onto a keyOnly expansion
# both instances refused on each.value, because the expansion recorded only
# the key set; the string beside "alice" was proven and then thrown away.
#
# The rule the fixture pins is per-key, not per-block: alice binds
# each.value because this resolver evaluated THAT expression itself, and bob
# stays unbound because aws_iam_group.admins.name is not evaluable before
# the cloud is read. foreach-value-proven-mutated is this same configuration
# with bob's obstacle removed and nothing else changed.
resource "aws_iam_group" "admins" {
  name = "admins"
}

locals {
  members = {
    "alice" = "alice-from-config"
    "bob"   = aws_iam_group.admins.name
  }
}

resource "aws_iam_user" "team" {
  for_each = local.members

  name = each.value
}
