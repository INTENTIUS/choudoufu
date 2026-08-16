# An impure call in a for_each source's value, beside an ordinary string.
#
# uuid() returns a different value on every evaluation, so a name derived
# from it is a name nothing can compute again: the first apply would create a
# user under it and every plan afterwards would propose creating another one,
# with nothing to detect the divergence. [resolver.evalStatic] already
# refuses an identity argument that calls one; the value chase has to refuse
# the same call in the same position, or the refusal is bypassed by routing
# the value through for_each instead of writing it in the argument.
#
# "carol" is an ordinary string in the same object, so the refusal is shown
# to be about the call rather than about the block.
resource "aws_iam_group" "admins" {
  name = "admins"
}

locals {
  members = {
    "alice" = uuid()
    "bob"   = aws_iam_group.admins.name
    "carol" = "carol-from-config"
  }
}

resource "aws_iam_user" "team" {
  for_each = local.members

  name = each.value
}
