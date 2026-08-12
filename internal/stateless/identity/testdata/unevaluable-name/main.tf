# The bucket name comes from a required variable with no value supplied, so
# the identity cannot be computed. Resolution must fail rather than guess a
# name (an empty string, or the variable's name).
variable "suffix" {
  type = string
}

resource "aws_s3_bucket" "data" {
  bucket = "estate-${var.suffix}"
}
