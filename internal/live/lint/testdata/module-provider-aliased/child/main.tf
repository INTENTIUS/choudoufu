provider "aws" {
  alias  = "east"
  region = "us-east-1"
}

resource "aws_s3_bucket" "data" {
  provider = aws.east
  bucket   = "module-provider-aliased-child"
}
