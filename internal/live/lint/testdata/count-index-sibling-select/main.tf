# Fixture: count.index in a position that SELECTS A SIBLING RESOURCE
# INSTANCE rather than computing a value from the index. See
# sibling_select.go. Every resource here is admitted, and
# TestSiblingSelectionRendersDistinctIdentities resolves them through
# internal/live/identity and asserts the rendered identity of each instance
# BY VALUE - an admission that only proved "the refusal went away" would
# prove nothing, since a wrong identity converges silently.
#
# The shape is terraform-aws-modules/vpc v6.6.1's own, verbatim from
# main.tf:200-201 and 348-352, which is what corpus-eks-basic hits: the
# module is pulled in by terraform-aws-eks's "basic" example and its four
# aws_route_table_association arguments were the whole of that estate's
# count-index wall.
#
# single_nat_gateway = true here on purpose, which is what the eks example
# itself passes. It makes route_table_id COLLAPSE - every association
# instance points at aws_route_table.private[0] - so per-argument reasoning
# must refuse it and the pairs are nonetheless all distinct, because
# subnet_id still varies. That is the case sibling_select.go's doc comment
# calls the open question, and the answer is the whole-identity collision
# check next door in testdata/count-index-sibling-select-collision.
#
# The other spelling of the same selection, R[<idx>].attr, is in
# testdata/count-index-sibling-select-indexed - a separate directory because
# it renders the IDENTICAL identity, which is the claim, and two resources
# rendering one identity in one configuration is what checkCollisions
# correctly refuses.

variable "single_nat_gateway" {
  type    = bool
  default = true
}

locals {
  n = 3
}

resource "aws_subnet" "private" {
  count      = local.n
  vpc_id     = "vpc-0123456789abcdef0"
  cidr_block = "10.0.${count.index}.0/24"
}

resource "aws_route_table" "private" {
  count  = var.single_nat_gateway ? 1 : local.n
  vpc_id = "vpc-0123456789abcdef0"
}

# element(R[*].attr, <idx>): resolveElementCall's own shape.
resource "aws_route_table_association" "private" {
  count = local.n

  subnet_id = element(aws_subnet.private[*].id, count.index)
  route_table_id = element(
    aws_route_table.private[*].id,
    var.single_nat_gateway ? 0 : count.index,
  )
}
