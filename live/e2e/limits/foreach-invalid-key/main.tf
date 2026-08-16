# Limits fixture: a for_each key outside the AWS tag-value set.
#
# Both resource types here are admitted. The problem is the for_each key
# itself: live/MARKERS.md's escaping rule can only carry characters AWS
# itself allows in a tag value, so a key containing anything else - "%"
# below - is outside the subset regardless of escaping. RuleForEachKey
# (internal/live/lint/foreach_key.go) rejects any for_each key outside
# Unicode letters, Unicode digits, space, and "+ - = . _ : / @", so Check()
# reports exactly one RuleForEachKey issue for the "a%b" key below. See
# live/LIMITATIONS.md.
#
# "." and ":" used to be refused here too (this fixture was named
# foreach-dotted-key before issue #178): live/MARKERS.md's escaping rule now
# escapes a key's own "." and ":" (and "@") before it reaches an address,
# reversibly, rather than excluding them, so a dotted or colon-bearing key
# is admitted. internal/live/lint/testdata/foreach-key-clean/main.tf is
# where that admission is pinned.

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
