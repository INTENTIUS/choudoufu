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
#
# The block is aws_iam_group, not aws_iam_user as this fixture read before
# GitHub issue #289: aws_iam_user is taggable and enumerable, so its own
# marker fallback would now answer "Identity derived from an impure
# function" for it too - correctly, since a discovered instance never
# renders an import ID from the call - but that is not what THIS fixture
# pins. It pins the call never reaching the identity in the first place,
# which is what "alice" not resolving at all, still, keeps proving.
# aws_iam_group carries no tags argument and stays outside that gate.
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

resource "aws_iam_group" "team" {
  for_each = local.members

  name = each.value
}
