# The in-module provider block used to be the refused construct (issue #70);
# since issue #201 it is admitted and honoured instead, because the call to
# this module in ../main.tf names none of count, for_each, enabled or
# depends_on - see ../main.tf. The resource is an ordinary admitted type,
# present so the module is a real module rather than an empty shell.

provider "aws" {
  region = "us-east-1"
}

resource "aws_s3_bucket" "data" {
  bucket = "estate-module-provider-block"
}
