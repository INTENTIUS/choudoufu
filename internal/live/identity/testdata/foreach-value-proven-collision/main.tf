# Two keys whose proven values are the same string, so two instances resolve
# to one identity and one live object.
#
# Newly-bound values create newly-comparable identities, and a collision check
# that only ever saw values from a whole-expression evaluation would go quiet
# exactly where the new bindings arrive. This is the shape that has to stay
# noisy: aws_iam_user.team["a"] and aws_iam_user.team["b"] would both claim
# the user named "shared-name".
#
# "dyn" keeps the object from evaluating whole, which is what routes the two
# colliding keys through the key-set chase rather than the unchanged path.
resource "aws_iam_group" "admins" {
  name = "admins"
}

locals {
  members = {
    "a"   = "shared-name"
    "b"   = "shared-name"
    "dyn" = aws_iam_group.admins.name
  }
}

resource "aws_iam_user" "team" {
  for_each = local.members

  name = each.value
}
