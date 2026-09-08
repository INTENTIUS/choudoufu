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

# aws_vpc's real identity is server-assigned (EC2 assigns the VPC ID), so
# leaving cidr_block as the only argument makes this instance
# ClassNeedsDiscovery, findable only by its ownership marker - exactly like
# every other aws_vpc fixture in this package (see
# twoRegionNeedsDiscoveryCloud). GitHub issue #950's own point is that the
# test schema behind THIS fixture (statelessTestSchemasWithout("aws_vpc"),
# also reused by internal/live/check's
# nodestamp_recordbacked_test.go - see its own doc comment for why it
# points here instead of a second copy under its own testdata) has no
# "tags" attribute at all: a type the hand-curated markerless table has
# never heard of (aws_vpc is not in
# internal/live/identity/markerless_generated.go), whose real schema still
# has nowhere to write a marker. Nothing here declares a live block or an
# -estate flag with markers already stamped, so this is a brand-new
# instance with no cloud object and no record: a plain CREATE.
resource "aws_vpc" "unmarkable" {
  cidr_block = "10.0.0.0/16"
}
