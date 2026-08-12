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
resource "aws_iam_role_policy" "app" {
  name = "tofu-stateless-e2e-app-inline"
  role = aws_iam_role.app.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid      = "AllowListDataBucket"
      Effect   = "Allow"
      Action   = ["s3:ListBucket"]
      Resource = aws_s3_bucket.data.arn
    }]
  })
}
