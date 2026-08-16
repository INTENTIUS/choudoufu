# Two upgrades' worth of renames, still in the source because they came from a
# published module. An estate that has not run since before the first one must
# still bind: a to b to c.

resource "aws_s3_bucket" "c" {
  bucket = "chain"
}

moved {
  from = aws_s3_bucket.a
  to   = aws_s3_bucket.b
}

moved {
  from = aws_s3_bucket.b
  to   = aws_s3_bucket.c
}
