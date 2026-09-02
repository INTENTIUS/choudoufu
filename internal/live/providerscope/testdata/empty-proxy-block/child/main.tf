provider "aws" {
}

resource "aws_s3_bucket" "data" {
  bucket = "empty-proxy-block-child"
}
