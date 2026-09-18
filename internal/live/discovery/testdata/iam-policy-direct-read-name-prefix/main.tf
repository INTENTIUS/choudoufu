# Regression fixture for GitHub issue #1212: one aws_iam_policy instance
# declared with `name_prefix` instead of `name`, which is the shape
# .corpus/iam/examples/iam-policy's own iam-policy module call produces
# (use_name_prefix defaults to true) and the shape corpus-iam-policy's
# greenfield stage replans against.
#
# The whole point of the fixture is that composeIAMPolicyARN CANNOT work
# here: IAM mints the name, so configuration states no name to compose a
# candidate ARN from, and directread.go's leg has nothing to read. The
# marker on the live object is therefore the only route left, and #1125's
# per-service tag read (iam:ListPolicyTags) is the only call that carries
# it once iam:ListPolicies has dropped the tags and the Resource Groups
# Tagging API does not index iam:policy on this target.

resource "aws_iam_policy" "prefixed" {
  name_prefix = "example-"
  policy      = jsonencode({ Sid = "team" })
}
