# The key-set fix's merge() extension: for_each ranges over merge() of two
# object constructors, one of them carrying a resource-attribute value. The
# key set is the union of both, still knowable without the values.

resource "aws_iam_group" "admins" {
  name = "admins"
}

locals {
  base = {
    "alice" = [aws_iam_group.admins.name]
  }
  extra = {
    "carol" = ["static-group"]
  }
}

resource "aws_iam_user" "team" {
  for_each = merge(local.base, local.extra)
  name     = each.key
}
