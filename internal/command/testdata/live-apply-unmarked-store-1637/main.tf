# GitHub issue #1637, claim 3's shape at the command tier: an inline IAM
# group policy with no `name`, so AWS assigns one at create time. The type
# carries no tags, so no marker can find it, and its identity needs a value
# only the apply learns. With no record store open that is #950's refusal.
# With a writable store the apply records group and name, and later runs
# find the object by that record, so the refusal steps aside.
terraform {
  live {
    estate = "unmarked-store-1637"

    record_store "local" {}
  }

  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

resource "aws_iam_group_policy" "app" {
  group  = "smoke-recordonly-group"
  policy = "{}"
}
