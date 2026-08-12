# Coverage: client-named path (aws_s3_bucket — identity is the bucket name in
# config, nothing to recover) and named singleton child (aws_s3_bucket_policy
# — its own identity is the parent bucket's name; exactly one per bucket).

resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-e2e-data"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_s3_bucket.data"
  }
}

# No tags argument on this resource type — untaggable by type. Its identity
# is the bucket's own name, so it needs no marker to be admitted.
resource "aws_s3_bucket_policy" "data" {
  bucket = aws_s3_bucket.data.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AllowAppRoleReadWrite"
      Effect    = "Allow"
      Principal = { AWS = aws_iam_role.app.arn }
      Action    = ["s3:GetObject", "s3:PutObject"]
      Resource  = "${aws_s3_bucket.data.arn}/*"
    }]
  })
}
