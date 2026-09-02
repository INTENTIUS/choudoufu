# #178's key-set fix: for_each ranges over an object constructor (through a
# local) whose keys are static literals but whose values are another
# resource's attribute. The child resource here reads only each.key, so its
# identity never needs the values at all - simpleinfra/team-members-access's
# aws_iam_user.users, generalized past its resource names.

resource "aws_iam_group" "admins" {
  name = "admins"
}

resource "aws_iam_group" "readers" {
  name = "readers"
}

locals {
  members = {
    "alice" = [aws_iam_group.admins.name],
    "bob"   = [aws_iam_group.readers.name],
  }
}

resource "aws_iam_user" "team" {
  for_each = local.members
  name     = each.key
}
