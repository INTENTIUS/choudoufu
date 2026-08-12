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

# The four S3 bucket children below (#19's second slice) are the same
# named-singleton-child shape as the bucket policy: at most one per bucket,
# identity is the parent bucket's name, no tags argument on any of them —
# untaggable by type, so no marker, and owned through the bucket.

resource "aws_s3_bucket_versioning" "data" {
  bucket = aws_s3_bucket.data.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "data" {
  bucket = aws_s3_bucket.data.id

  block_public_acls       = true
  block_public_policy     = false
  ignore_public_acls      = true
  restrict_public_buckets = false
}

resource "aws_s3_bucket_server_side_encryption_configuration" "data" {
  bucket = aws_s3_bucket.data.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "data" {
  bucket = aws_s3_bucket.data.id

  rule {
    id     = "expire-tmp"
    status = "Enabled"

    filter {
      prefix = "tmp/"
    }

    expiration {
      days = 7
    }
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
