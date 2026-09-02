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

# The group name is the identity, and this one has none: the identity
# resolver cannot name the live object, which is fatal rather than a
# create.
#
# This was aws_s3_bucket until #190 taught the table the provider's
# auto-generated-name convention. A bucket is one of the 37 types that
# convention covers, so omitting its name now defers to discovery rather
# than refusing. aws_iam_group's name carries no such promise in the
# provider's own Argument Reference, so it still refuses.
resource "aws_iam_group" "data" {
  tags = {
    tofu-estate = "unit"
  }
}
