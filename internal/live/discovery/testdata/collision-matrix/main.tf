# GitHub issue #415's collision-outcome matrix: one resource block per
# {identity shape} x {instance shape} cell the matrix exercises against a
# manufactured marker collision. Every block here is deliberately minimal -
# nothing in this fixture is applied or read by a real provider, only
# resolved statically and discovered against a fake cloud - so each block
# carries only the arguments its identity computation needs.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

# Scalar, ServerAssigned/ARN: EC2 mints the VPC ID at create time; no
# configuration argument identifies it, so it always needs discovery.
resource "aws_vpc" "scalar_server" {
  cidr_block = "10.0.0.0/16"
}

# Scalar, config-identified: the ARN is built from the name argument plus a
# Cloud-derived account ID, so the type is config-identified end to end but
# still needs discovery - the account ID is not knowable from static
# configuration alone (see identity.Component.Cloud).
resource "aws_sns_topic" "scalar_config" {
  name = "scalar-config-topic"
}

# Count set, ServerAssigned/ARN: a fungible set of provider-minted
# identities - the #411 reproduction's own shape.
resource "aws_eip" "count_server" {
  count  = 2
  domain = "vpc"
}

# Count set, config-identified: individually config-identified members, in a
# block that is still a fungible set - count.index names a position, not an
# instance.
resource "aws_sns_topic" "count_config" {
  count = 2
  name  = "count-config-topic-${count.index}"
}

# for_each set, ServerAssigned/ARN: each instance carries its own stable,
# config-supplied key, so - unlike count - the set is never fungible: a
# for_each instance's key IS its identity within the block.
resource "aws_subnet" "foreach_server" {
  for_each          = toset(["a", "b"])
  vpc_id            = aws_vpc.scalar_server.id
  cidr_block        = "10.0.1.0/24"
  availability_zone = each.key
}

# for_each set, config-identified.
resource "aws_sns_topic" "foreach_config" {
  for_each = toset(["a", "b"])
  name     = "foreach-config-topic-${each.key}"
}
