# Fixture: R[<idx>].attr - the same sibling-instance selection as
# testdata/count-index-sibling-select spells with element(), written as a
# direct indexed traversal instead. hclsyntax builds an IndexExpr the moment
# the index is not a constant, so this shape is refused through
# analyzeCountIndexSafety's IndexExpr case rather than its FunctionCallExpr
# one; both have to move together or the rule is about syntax rather than
# about what the expression selects.
#
# It is a separate directory rather than a second resource next door because
# the two spellings render the IDENTICAL identity, which is the claim - and
# two resources rendering one identity in one configuration is exactly what
# internal/live/identity's checkCollisions refuses, correctly.

locals {
  n = 3
}

resource "aws_subnet" "private" {
  count      = local.n
  vpc_id     = "vpc-0123456789abcdef0"
  cidr_block = "10.0.${count.index}.0/24"
}

resource "aws_route_table" "private" {
  count  = 1
  vpc_id = "vpc-0123456789abcdef0"
}

# R[<idx>].attr: the same selection spelled as a direct indexed traversal,
# which resolveIndexedTraversal answers and which analyzeCountIndexSafety
# refuses through its IndexExpr case rather than its FunctionCallExpr one.
# Both spellings have to move together or the rule is about syntax.
resource "aws_route_table_association" "indexed" {
  count = local.n

  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[0].id
}
