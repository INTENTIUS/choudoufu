# gauntlet:sweep-moved-alias fixture: an untaggable, record-located type
# (aws_iam_role_policy - identity.LookupType's own Components: role, then
# name) renamed with a plain `moved` block. The declared block's own
# address never carried the identity record migrate first wrote it under -
# only a `moved` block says the two are the same instance, exactly the
# giantswarm/sg/ec2/rds/autoscaling/dynamodb/s3/overture shape
# 619ea617ac's merge message named.

resource "aws_iam_role_policy" "inline" {
  name   = "deploy"
  role   = "app"
  policy = "{}"
}

moved {
  from = aws_iam_role_policy.inline_old
  to   = aws_iam_role_policy.inline
}
