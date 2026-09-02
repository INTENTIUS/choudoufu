# Fixture for RuleMovedBlock's residual refusal (GitHub issue #198). A moved
# block whose endpoints describe a move markers can follow is carried by
# internal/live/discovery and reported by nobody - see testdata/moved-honoured.
#
# This one cannot be carried: the address it moves from is still declared, so
# there is no vacated address for the live resource to move into and the
# assignment of two interchangeable objects to two addresses would be a guess.
# Stock OpenTofu refuses the same shape, as "Moved object still exists".

resource "aws_s3_bucket" "old" {
  bucket = "tofu-stateless-lint-old"
}

resource "aws_s3_bucket" "new" {
  bucket = "tofu-stateless-lint-data"
}

moved {
  from = aws_s3_bucket.old
  to   = aws_s3_bucket.new
}
