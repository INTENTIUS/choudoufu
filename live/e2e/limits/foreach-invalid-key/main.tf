# Limits fixture: a for_each key one of the six characters
# markerkey.Excluded still refuses.
#
# Both resource types here are admitted. The problem is the for_each key
# itself, but not - since issue #210 - because it sits outside the AWS
# tag-value charset: markerkey.Encode now carries almost any printable
# character into a marker reversibly (an Introducer-led hex escape), so a
# key like "a (b)" or "a;b" is admitted today, encoded rather than refused.
# What remains refused is a narrower, six-character set that collides with a
# DIFFERENT escaping rule this package does not own - a literal quote,
# backslash, "[" or "]" (delimiters or escape sequences
# addrs.toHCLQuotedString or internal/live/markers' own address-level
# scanning already claim) - plus "$" and "%", which that same function
# doubles when followed by "{" and issue #210 leaves refused unconditionally
# rather than only in that one shape. "%" below is one of those six.
# RuleForEachKey (internal/live/lint/foreach_key.go,
# markerkey.Excluded) rejects it, so Check() reports exactly one
# RuleForEachKey issue for the "a%b" key below. See live/LIMITATIONS.md.
#
# "." and ":" used to be refused here too (this fixture was named
# foreach-dotted-key before issue #178): live/MARKERS.md's escaping rule now
# escapes a key's own "." and ":" (and "@") before it reaches an address,
# reversibly, rather than excluding them, so a dotted or colon-bearing key
# is admitted. internal/live/lint/testdata/foreach-key-clean/main.tf is
# where that admission - and issue #210's much wider one, covering almost
# every other printable character - is pinned.

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

resource "aws_subnet" "this" {
  for_each = {
    "a%b" = "10.42.1.0/24"
  }

  vpc_id     = aws_vpc.main.id
  cidr_block = each.value
}
