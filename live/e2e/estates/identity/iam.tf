# Supporting, not coverage: aws_iam_group.identity and aws_iam_user.identity
# exist only so aws_iam_group_policy.app/aws_iam_group_policy_attachment.app
# and aws_iam_user_policy.app/aws_iam_user_policy_attachment.app have a real
# group and a real user to attach to, the same "Supporting, not coverage"
# pattern live/e2e/estates/messaging/iam.tf's aws_iam_role.messaging already
# uses by hand (estate-gen's own generic pass has no cross-type alias for
# "group" or "user" the way it does for "role" - see
# tools/estate-gen/gen.go's iamRoleRefExpr comment). Both are themselves
# client-named-shaped exactly the way live/e2e/estate/'s own aws_iam_role.app
# is, and both are already covered there or by the IAM/ECR batch
# (aws_iam_group, aws_iam_user) - not repeated as coverage rows here.

resource "aws_iam_group" "identity" {
  name = "tofu-identity-cohort-group"
}

resource "aws_iam_user" "identity" {
  name = "tofu-identity-cohort-user"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_iam_user.identity"
  }
}
