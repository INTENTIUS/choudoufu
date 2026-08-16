# foreach-value-proven with exactly one edit: bob's value is a string rather
# than aws_iam_group.admins.name. Everything else - the group, the local, the
# for_each, the identity argument - is byte-for-byte the same.
#
# It exists so that "bob refuses" over there is shown to be caused by the
# managed-resource reference and not by anything incidental to the shape. A
# refusal that survived this edit would be a refusal the sibling test is
# attributing to the wrong cause.
resource "aws_iam_group" "admins" {
  name = "admins"
}

locals {
  members = {
    "alice" = "alice-from-config"
    "bob"   = "bob-from-config"
  }
}

resource "aws_iam_user" "team" {
  for_each = local.members

  name = each.value
}
