provider "aws" {
}

resource "aws_s3_bucket" "data" {
  bucket = "module-provider-empty-proxy-child"
}
