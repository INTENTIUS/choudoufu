# The per-element identity fixture.
#
# aws_iam_user_group_membership is the first admitted type whose identity has
# a set-valued tail: the provider imports it as USERNAME/GROUP1/GROUP2...,
# one segment per group, and the number of segments is a property of the
# configuration rather than of the row. Component.PerElement renders that.
#
# The type is untaggable. IAM group memberships carry no tag map at all, so
# no ownership marker can be written on one - and it does not need one. The
# identity re-derives from the declaration on every run, which is the whole
# point of the client-named path.
#
# The three memberships are chosen so the run discriminates the rendering
# rule rather than merely exercising it:
#
#   solo   one group. The degenerate case: one tail segment.
#   pair   two groups, DECLARED OUT OF ORDER. The configuration lists
#          [pe-e2e-zulu, pe-e2e-alpha]; the identity must render
#          pe-e2e-alpha/pe-e2e-zulu. If the rendering followed declaration
#          order this instance would read "pair/pe-e2e-zulu/pe-e2e-alpha",
#          and run.sh asserts that string never appears.
#   trio   three groups, also declared out of order, so the sort is exercised
#          on a permutation two elements cannot produce.
#
# The declared-order strings are what makes this a measurement. A set has no
# order in the provider's own schema, so a wrong order still round-trips
# through a live read: the provider splits the ID on "/" and puts the tail in
# a set. Nothing on the wire would complain. The only thing that catches a
# wrong order is reading the rendered string itself, which run.sh does.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "per-element-e2e"
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

locals {
  groups = ["pe-e2e-alpha", "pe-e2e-mike", "pe-e2e-zulu"]

  # Declaration order is deliberately not sorted order for pair and trio.
  members = {
    "pe-e2e-solo" = ["pe-e2e-mike"]
    "pe-e2e-pair" = ["pe-e2e-zulu", "pe-e2e-alpha"]
    "pe-e2e-trio" = ["pe-e2e-zulu", "pe-e2e-alpha", "pe-e2e-mike"]
  }
}

resource "aws_iam_group" "groups" {
  for_each = toset(local.groups)
  name     = each.value
}

resource "aws_iam_user" "users" {
  for_each = local.members
  name     = each.key

  # aws_iam_user IS taggable, so these three carry the estate's markers and
  # give the run something to read back through the tagging index. The
  # memberships below carry nothing, by design.
  tags = {
    tofu-estate  = "per-element-e2e"
    tofu-address = "aws_iam_user.users:${each.key}"
  }
}

resource "aws_iam_user_group_membership" "members" {
  for_each = local.members
  user     = each.key
  groups   = each.value

  depends_on = [aws_iam_user.users, aws_iam_group.groups]
}
