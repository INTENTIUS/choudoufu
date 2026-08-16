# A for_each key carrying "%" — outside the AWS tag-value set, so no
# escaping rule can admit it: it can never be carried as a tag value at
# all. Expansion must refuse it before it becomes an address.
#
# "." and ":" used to be the example here (this fixture predates issue
# #178, which escapes a key's own "." and ":" reversibly rather than
# excluding them - see live/MARKERS.md, "for_each key escaping").
resource "aws_subnet" "this" {
  for_each = toset(["50%full"])

  cidr_block = "10.42.1.0/24"
}
