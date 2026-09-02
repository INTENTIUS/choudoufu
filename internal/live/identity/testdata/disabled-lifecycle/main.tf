# lifecycle.enabled = false expands to zero instances; the sibling stays.
resource "aws_s3_bucket" "off" {
  bucket = "estate-off"

  lifecycle {
    enabled = false
  }
}

resource "aws_s3_bucket" "on" {
  bucket = "estate-on"

  lifecycle {
    enabled = true
  }
}
