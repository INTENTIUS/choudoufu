# Coverage: client-named path (aws_iam_role — identity is the role name in
# config) and attachment composite (aws_iam_role_policy_attachment — identity
# is the composite of role name + policy ARN, both already in config).

resource "aws_iam_role" "app" {
  name = "tofu-stateless-e2e-app"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_iam_role.app"
  }
}

# No tags argument on this resource type — untaggable by type. Identity is
# the (role, policy_arn) pair, both client-named already.
resource "aws_iam_role_policy_attachment" "app" {
  role       = aws_iam_role.app.name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}

# Coverage: concrete composite with a client-named name half
# (aws_iam_role_policy — the import ID is ROLENAME:POLICYNAME, both halves
# client-chosen strings already in config). Untaggable by type, like the
# attachment. #19's second slice.
#
# The policy names the data bucket's ARN as a literal, not as
# aws_s3_bucket.data.arn. The bucket's name is fixed in this fixture, so
# the two spell the same string — but under floci's S3 tag-replacement gap
# (mv_test.go's flociS3TagGap) a renamed bucket loses its estate marker and
# plans as a create, and an interpolated ARN would drag this untaggable
# child into that plan the way aws_s3_bucket_policy.data already is. The
# literal keeps this block out of the emulator's boundary; on real AWS both
# spellings plan identically.
resource "aws_iam_role_policy" "app" {
  name = "tofu-stateless-e2e-app-inline"
  role = aws_iam_role.app.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid      = "AllowListDataBucket"
      Effect   = "Allow"
      Action   = ["s3:ListBucket"]
      Resource = "arn:aws:s3:::tofu-stateless-e2e-data"
    }]
  })
}
