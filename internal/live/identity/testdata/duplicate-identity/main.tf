# Two blocks claiming one bucket name. Both would bind to the same live
# bucket, which is an ambiguity to name rather than resolve.
resource "aws_s3_bucket" "one" {
  bucket = "estate-shared"
}

resource "aws_s3_bucket" "two" {
  bucket = "estate-shared"
}
