# Fixture: the collapse that really is a collision. Both identity
# components select the SAME sibling instance for every index, so all three
# aws_route_table_association instances render one identity.
#
# This is the safety half of sibling_select.go, and the reason it is a
# fixture rather than a paragraph: RuleCountIndex steps aside here exactly
# as it does next door in testdata/count-index-sibling-select, and the
# configuration is still refused - by name, with the duplicated identity
# quoted - by internal/live/identity's own checkCollisions, which compares
# whole rendered identities instead of one argument at a time. A rule that
# only stopped firing would have turned this into three configuration
# addresses claiming one live route table association.
#
# TestSiblingSelectionCollapseIsCaughtByCollisionCheck asserts both halves:
# zero count-index issues from lint, and the collision error with its
# identity string.

locals {
  n = 3
}

resource "aws_route_table" "private" {
  count  = 1
  vpc_id = "vpc-0123456789abcdef0"
}

resource "aws_route_table_association" "collapsed" {
  count = local.n

  # Both indices are written with count.index, and both collapse: element()
  # wraps modulo the source's own instance count, and both sources here
  # expand to one instance, so every index picks [0].
  subnet_id      = element(aws_subnet.only[*].id, count.index)
  route_table_id = element(aws_route_table.private[*].id, count.index)
}

resource "aws_subnet" "only" {
  count      = 1
  vpc_id     = "vpc-0123456789abcdef0"
  cidr_block = "10.9.0.0/24"
}
