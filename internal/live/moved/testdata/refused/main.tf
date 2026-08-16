# One of each residual refusal.

resource "aws_s3_bucket" "still_here" {
  bucket = "still-here"
}

resource "aws_s3_bucket" "target" {
  bucket = "target"
}

resource "aws_s3_bucket_versioning" "other_type" {
  bucket = aws_s3_bucket.target.id
}

# The source is still declared: nothing is vacated.
moved {
  from = aws_s3_bucket.still_here
  to   = aws_s3_bucket.target
}

# Two resource types: a marker names the type of the resource it is on.
moved {
  from = aws_s3_bucket.gone
  to   = aws_s3_bucket_versioning.other_type
}
