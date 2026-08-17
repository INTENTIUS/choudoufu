# The create-over-existing fixture.
#
# Two server-named resources, both needs-discovery, both carrying this
# estate's markers in real tags. They differ in exactly one property: whether
# the provider's native list resource returns the object's tag map.
#
#   aws_vpc.control  - ec2:DescribeVpcs returns TagSet, so the listed object
#                      carries its marker and discovery binds it.
#   aws_iam_role.subject - iam:ListRoles returns no tags and the provider
#                      issues no GetRole per member, so the listed object
#                      carries an empty tag map and its marker is unreadable.
#
# Neither name is in the configuration: the VPC has no name at all (its
# identity is the server-assigned vpc-id) and the role uses name_prefix, so
# the provider assigns the suffix. Both are therefore ClassNeedsDiscovery and
# both depend entirely on the marker being readable off a listed object.
#
# The control is what makes the run a measurement rather than an anecdote. If
# both resources came back proposed for creation the finding would be "this
# harness cannot discover anything"; the control passing and the subject
# failing isolates the tag-losing list path as the cause.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "create-over-e2e"
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

resource "aws_vpc" "control" {
  cidr_block = "10.77.0.0/16"

  tags = {
    tofu-estate  = "create-over-e2e"
    tofu-address = "aws_vpc.control"
  }
}

resource "aws_iam_role" "subject" {
  name_prefix = "create-over-e2e-"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })

  tags = {
    tofu-estate  = "create-over-e2e"
    tofu-address = "aws_iam_role.subject"
  }
}
