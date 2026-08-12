# Limits fixture: a for_each key containing ".".
#
# Both resource types here are admitted. The problem is the for_each key
# itself: stateless/MARKERS.md's escaping rule cannot unambiguously round-trip
# a key containing "." (or ":"), so the roadmap pushes such keys out of the
# subset. RA.3 enforced this: RuleForEachKey
# (internal/stateless/lint/foreach_key.go) rejects any for_each key outside
# Unicode letters, Unicode digits, space, and "+ - = _ / @", so Check()
# reports exactly one RuleForEachKey issue for the "a.b" key below. See
# stateless/LIMITATIONS.md.

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

resource "aws_subnet" "this" {
  for_each = {
    "a.b" = "10.42.1.0/24"
  }

  vpc_id     = aws_vpc.main.id
  cidr_block = each.value
}
