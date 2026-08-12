# No bucket argument at all: nothing to build an identity from.
resource "aws_s3_bucket" "data" {
  force_destroy = true
}
