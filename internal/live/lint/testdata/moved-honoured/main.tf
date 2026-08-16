# Fixture for the moved blocks the live path carries (GitHub issue #198).
#
# Each of these is a shape internal/live/discovery indexes as an alias on the
# destination's declared entry, so a live resource still carrying the old
# address binds to the instance that declares it now and the plan rewrites its
# tofu-address tag in place. Lint reports none of them.
#
# The count-expanded destination is here deliberately: `count = var.x ? 1 : 0`
# is how every terraform-aws-modules resource is written, so it is what every
# moved block shipped inside a published module lands on.

variable "create" {
  type    = bool
  default = true
}

resource "aws_s3_bucket" "new" {
  bucket = "tofu-stateless-lint-data"
}

resource "aws_s3_bucket_versioning" "this" {
  count = var.create ? 1 : 0

  bucket = aws_s3_bucket.new.id
}

moved {
  from = aws_s3_bucket.old
  to   = aws_s3_bucket.new
}

moved {
  from = aws_s3_bucket_versioning.legacy
  to   = aws_s3_bucket_versioning.this
}
