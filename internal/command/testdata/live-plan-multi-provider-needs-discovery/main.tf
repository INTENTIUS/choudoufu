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
# configurations. That is the shape of a CloudFront estate, which needs an
# aliased provider in the one region WAFv2-for-CloudFront and ACM-for-
# CloudFront can live in while the rest of it sits somewhere else.
#
# Issue #283: this used to be refused outright. It is now one scoped
# discovery pass per provider configuration, so each VPC is looked for in the
# region its own resource block names - and only there. The tests over this
# fixture are TestLivePlan_needsDiscoveryBindsThroughItsOwnProvider (each
# resource is swept through its own configuration) and
# TestLivePlan_needsDiscoveryDoesNotBindAcrossProviders (an object one
# configuration can see is never bound to a resource belonging to another).
resource "aws_vpc" "east" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_vpc" "west" {
  provider   = aws.west
  cidr_block = "10.1.0.0/16"
}
