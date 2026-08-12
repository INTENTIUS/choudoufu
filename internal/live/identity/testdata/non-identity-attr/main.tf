# The bucket policy names its bucket via an attribute of another resource
# that is not an identity attribute. cidr_block is known only after apply,
# so this is an error, not a formula.
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

resource "aws_s3_bucket_policy" "data" {
  bucket = aws_vpc.main.cidr_block
  policy = "{}"
}
