# GitHub issue #1002: an estate where declared_tagged = "untag" reaches some
# instances and not others, so the plan's "Policy untag" section can be
# asserted by value rather than by whether it rendered at all.
terraform {
  live {
    estate = "stateless-unit"

    policy {
      declared_tagged = "untag"
    }
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

# Two instances of one block. The test's cloud holds pool["owned"] live and
# marked for this estate, so it is declared_tagged and the verb releases its
# tofu-estate. pool["fresh"] does not exist yet: it is a create, no quadrant
# of the policy governs it, and it is stamped in full.
resource "aws_s3_bucket" "pool" {
  for_each = toset(["owned", "fresh"])

  bucket = "tofu-untag-1002-${each.key}"
}

# Live and marked like pool["owned"], so the verb governs it too, but its
# configuration writes the key by hand. The writer never overwrites a
# hand-written marker value, so the release does not happen and the report
# must not claim it did.
resource "aws_s3_bucket" "pinned" {
  bucket = "tofu-untag-1002-pinned"

  tags = {
    "tofu-estate" = "stateless-unit"
  }
}
