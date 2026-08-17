# The boundary of the concrete branch: the parent is concrete, but the
# attribute being read is one the provider's schema does not declare at all.
# Nothing would ever be found on the parent's live object under that name,
# so the refusal has to stand.

resource "aws_iam_role" "assumed" {
  name               = "release-assumed"
  assume_role_policy = "{}"
}

resource "aws_eks_access_entry" "assumed" {
  cluster_name  = "govuk"
  principal_arn = aws_iam_role.assumed.no_such_attribute
}
