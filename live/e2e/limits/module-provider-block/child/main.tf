# The in-module provider block is the refused construct; the resource is an
# ordinary admitted type, present so the module is a real module rather than
# an empty shell.

provider "aws" {
  region = "us-east-1"
}

resource "aws_s3_bucket" "data" {
  bucket = "estate-module-provider-block"
}
