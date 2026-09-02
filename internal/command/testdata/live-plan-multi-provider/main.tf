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

provider "aws" {
  alias  = "west"
  region = "us-west-2"
}

# Two client-named resources under two provider configurations, the
# command-level unit-test twin of internal/live/discovery/testdata/alias-e2e:
# neither needs marker discovery, so this fixture isolates the sweep's own
# provider-awareness (issue #69) from marker-discovery's own single-provider
# rule, which TestLivePlan_needsDiscoveryAcrossProvidersIsRefused covers
# instead.
resource "aws_s3_bucket" "east" {
  bucket = "tofu-multi-provider-east"
}

resource "aws_s3_bucket" "west" {
  provider = aws.west
  bucket   = "tofu-multi-provider-west"
}
