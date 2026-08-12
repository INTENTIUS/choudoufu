# Fixture for the for_each key rule (RA.3, finding F-FE).
#
# Every block below is an admitted type with nothing else wrong with it: the
# only finding is the instance key. The three shapes are the three ways a key
# reaches the rule — a literal object, a set built by a function, and a local
# — so the rule is exercised through the static evaluator rather than only
# against a literal.
#
# The ":" case is the one the audit found live: it is an AWS-legal tag
# character, so it stamps and applies, and only the NEXT run sees a malformed
# marker.

locals {
  cidrs = toset(["10.0.0.0/24", "10.0.1.0/24"])
}

resource "aws_subnet" "dotted" {
  for_each = {
    "a.b" = "10.42.1.0/24"
  }

  cidr_block = each.value
}

resource "aws_subnet" "colon" {
  for_each = toset(["2001:db8::/64"])

  cidr_block = "10.42.2.0/24"
}

resource "aws_subnet" "from_local" {
  for_each = local.cidrs

  cidr_block = each.key
}

resource "aws_s3_bucket" "punctuation" {
  for_each = toset(["ok-key", "bad%key"])

  bucket = "tofu-stateless-lint-${each.key}"
}
