# The boundary of the concrete branch: the parent is concrete, but the
# attribute being read is one the provider's schema does not declare at all.
# Nothing would ever be found on the parent's live object under that name,
# so the refusal has to stand.
#
# The child is aws_iam_group, not a taggable/enumerable type like
# aws_eks_access_entry this fixture used before GitHub issue #289: that
# type's marker fallback would now answer a made-up attribute reference the
# same way it answers a genuinely unresolvable one, which is not what this
# fixture is for - it is pinning the schema rule itself, ungated.

resource "aws_iam_role" "assumed" {
  name               = "release-assumed"
  assume_role_policy = "{}"
}

resource "aws_iam_group" "assumed" {
  name = aws_iam_role.assumed.no_such_attribute
}
