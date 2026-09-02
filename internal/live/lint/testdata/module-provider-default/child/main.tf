provider "aws" {
  region = "us-west-2"
}

resource "aws_s3_bucket" "data" {
  bucket = "module-provider-default-child"
}
