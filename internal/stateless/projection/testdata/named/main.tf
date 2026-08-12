# The client-named shapes the ownership check is about: a taggable resource
# whose name is in the configuration, and an untaggable child whose identity
# is its parent's. Both are read by an identity that came out of this file
# rather than out of a marker, which is what makes the live object's own tags
# the only evidence of who owns it.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

resource "aws_cloudwatch_log_group" "app" {
  name = "/ours/logs"
}

resource "aws_s3_bucket_policy" "data" {
  bucket = "ownership-unit-data"
  policy = "{\"Version\":\"2012-10-17\"}"
}
