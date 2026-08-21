terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

# Client-named identity, same as the "live-plan" fixture: the bucket name is
# in the configuration, so the projection can read this one back with no
# memory at all. Kept deliberately alone (no server-assigned aws_vpc, unlike
# "live-plan") so a clean run needs no -target and no discovery pass - the
# outputs below are the only thing under test.
resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-unit-data"
}

# A plain resource-attribute reference - GitHub issue #348's exact repro
# shape (terraform-aws-modules/terraform-aws-lambda's examples/simple
# outputs every one of its 23 outputs this way).
output "bucket_arn" {
  value = aws_s3_bucket.data.arn
}

# An expression built from a resource attribute, not a bare reference.
output "bucket_label" {
  value = "label-${aws_s3_bucket.data.bucket}"
}

# A sensitive output, so its "before" and "after" sensitivity marks have to
# agree for the diff to read as unchanged too.
output "bucket_secret" {
  value     = "shh"
  sensitive = true
}
