# Fixture for RuleMovedBlock. The resource is an admitted type, so the moved
# block is the only rejection.

resource "aws_s3_bucket" "new" {
  bucket = "tofu-stateless-lint-data"
}

moved {
  from = aws_s3_bucket.old
  to   = aws_s3_bucket.new
}
