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

# aws_vpc's identity is server-assigned, so it needs marker discovery - and
# with one instance of it under each provider configuration, the
# needs-discovery scan itself (not just the sweep) spans two provider
# configurations. That is still refused: TestLivePlan_needsDiscoveryAcrossProvidersIsRefused
# is issue #69's regression pin for the rule statelessNeedsDiscoveryProvider's
# own doc comment states - a list against the wrong account or region would
# misreport an estate as missing, which is a hazard the sweep's own
# provider-awareness does not share and is not meant to fix.
resource "aws_vpc" "east" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_vpc" "west" {
  provider   = aws.west
  cidr_block = "10.1.0.0/16"
}
