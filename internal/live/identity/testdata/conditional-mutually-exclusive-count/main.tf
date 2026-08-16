# The corpus idiom #196 exists for, reduced: terraform-aws-modules/s3-bucket
# declares aws_s3_bucket.this with `count = ... && !var.is_directory_bucket
# ? 1 : 0` and aws_s3_directory_bucket.this with the complement, then writes
#
#   bucket = var.is_directory_bucket ? aws_s3_directory_bucket.this[0].bucket
#                                    : aws_s3_bucket.this[0].id
#
# Exactly one of the two exists in any given run, so BOTH branches index a
# resource that has no instance [0] on the runs where the other applies.
# [resolver.resolveConditional] never consulting the unselected branch is
# not an optimization here: consulting it would refuse every use of this
# idiom, and the idiom is pervasive.
#
# The bare consumer is the control the audit used against the real corpus:
# main.tf:210 (aws_s3_bucket_versioning, a bare aws_s3_bucket.this[0].id)
# against main.tf:301 and :612 (the wrapped form). Whatever the bare form
# resolves to, the wrapped form must resolve to the same thing.

variable "is_directory" {
  type    = bool
  default = false
}

resource "aws_s3_bucket" "this" {
  count  = var.is_directory ? 0 : 1
  bucket = "plain-bucket"
}

resource "aws_s3_directory_bucket" "this" {
  count  = var.is_directory ? 1 : 0
  bucket = "dir-bucket--use1-az1--x-s3"

  location {
    name = "use1-az1"
  }
}

resource "aws_s3_bucket_policy" "wrapped" {
  bucket = var.is_directory ? aws_s3_directory_bucket.this[0].bucket : aws_s3_bucket.this[0].id
  policy = "{}"
}
