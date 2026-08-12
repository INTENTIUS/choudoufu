# Fixture for the overlong-address rule: escaped tofu-address values past
# the 256-character AWS tag-value cap (stateless/MARKERS.md).
#
# Every block is an admitted type with nothing else wrong with it: the only
# finding is address length. The three hot shapes are the three ways an
# instance address is measured (plain, for_each key, count index), and the
# two quiet blocks pin the boundaries: a short address, and a long label
# whose for_each the static scope cannot evaluate, which the rule skips
# rather than guesses at.

# "aws_s3_bucket." is 14 characters, so this 250-character label escapes to
# a 264-character address with no instance key involved.
resource "aws_s3_bucket" "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" {
  bucket = "overlong-plain"
}

# The base address fits, but the 250-character for_each key pushes one
# instance to 266 escaped characters ("aws_subnet.wide:" is 16). The other
# key stays comfortably inside the cap.
resource "aws_subnet" "wide" {
  for_each = toset(["ok", "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"])

  cidr_block = "10.42.1.0/24"
}

# "aws_vpc." plus this 250-character label is already 258 characters, and
# the highest count index makes the longest instance address, 260.
resource "aws_vpc" "yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy" {
  count = 2

  cidr_block = "10.42.0.0/16"
}

# Inside the cap: nothing to report.
resource "aws_s3_bucket" "short" {
  bucket = "short-enough"
}

# The label alone is past the cap, but the for_each keys are not statically
# evaluable, so the rule declines to guess, the same can-see boundary
# checkForEachKeys draws. Identity resolution refuses a non-static for_each
# outright, before any marker is minted.
resource "aws_cloudwatch_log_group" "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" {
  for_each = toset(data.aws_availability_zones.available.names)

  name = "/tofu/${each.key}"
}

data "aws_availability_zones" "available" {}
