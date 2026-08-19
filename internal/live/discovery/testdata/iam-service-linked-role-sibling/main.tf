# Regression fixture for GitHub issue #302: an estate that declares both an
# ordinary aws_iam_role and an aws_iam_service_linked_role. IAM has no
# separate ListServiceLinkedRoles operation, so iam:ListRoles -
# aws_iam_role's own native list call - returns the service-linked role
# right alongside the ordinary one, and that object's marker names
# aws_iam_service_linked_role rather than aws_iam_role - the same "AWS has
# no separate list call for the special case" shape #325's
# default-adopter-dup fixture exercises for aws_default_security_group.

resource "aws_iam_role" "other" {
  assume_role_policy = jsonencode({})
}

resource "aws_iam_service_linked_role" "app" {
  aws_service_name = "elasticbeanstalk.amazonaws.com"
}
