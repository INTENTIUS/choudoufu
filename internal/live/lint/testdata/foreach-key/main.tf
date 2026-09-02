# Fixture for the for_each key rule (RA.3, finding F-FE).
#
# Every block below is an admitted type with nothing else wrong with it: the
# only finding is the instance key. The three shapes are the three ways a key
# reaches the rule — a literal object, a set built by a function, and a local
# — so the rule is exercised through the static evaluator rather than only
# against a literal.
#
# "." and ":" used to be here (the audit found the ":" case live: it is an
# AWS-legal tag character, so it stamped and applied, and only the NEXT run
# saw a malformed marker). Issue #178 closed that gap by escaping a key's
# own "." and ":" (and "@") before it reaches an address, reversibly, rather
# than refusing them - see live/MARKERS.md, "for_each key escaping" - so
# both characters moved to testdata/foreach-key-clean/main.tf.
#
# ";" and "!" moved out here too, for the same reason, when issue #210
# widened admission to any printable character except the six that collide
# with a rule OUTSIDE the AWS tag-value charset (markerkey.Encode's doc
# comment has the full accounting). What remains here is exactly that
# six-character exclusion - a quote, a backslash, and a literal "[" - plus
# "%", which issue #210 leaves refused unconditionally rather than only
# before "{" (see live/MARKERS.md, "for_each key escaping").

locals {
  cidrs = toset(["10.0.0.0/24[10.0.1.0/24"])
}

resource "aws_subnet" "quote" {
  for_each = {
    "a\"b" = "10.42.1.0/24"
  }

  cidr_block = each.value
}

resource "aws_subnet" "backslash" {
  for_each = toset(["a\\b"])

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
