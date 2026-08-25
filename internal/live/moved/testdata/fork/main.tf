# gauntlet:destroy-order: two moved blocks that both claim to know what
# aws_s3_bucket.ambiguous became - a configuration no honest tool would
# emit by hand, but Honourable does not refuse a duplicate `from`, and
# Newest has to say it will not guess between them rather than silently
# preferring one.

resource "aws_s3_bucket" "x" {
  bucket = "x"
}

resource "aws_s3_bucket" "y" {
  bucket = "y"
}

moved {
  from = aws_s3_bucket.ambiguous
  to   = aws_s3_bucket.x
}

moved {
  from = aws_s3_bucket.ambiguous
  to   = aws_s3_bucket.y
}
