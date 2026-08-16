variable "org" {
  type = string
}

provider "aws" {
  region = "us-east-1"
}

resource "aws_s3_bucket" "data" {
  bucket = "local-provider-block-child-${var.org}"
}
