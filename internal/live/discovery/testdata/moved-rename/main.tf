# Fixture for GitHub issue #198: a moved block over a needs-discovery type,
# whose identity the provider assigns at create time and whose ownership
# marker is therefore the only thing connecting a live ID to an address.
#
# That is the shape where the alias has to exist. A live subnet still carrying
# "aws_subnet.old" would otherwise read as an orphan - the plan proposes
# destroying it - while aws_subnet.renamed reads as absent and the plan
# proposes creating it. One cloud object, a destroy and a create.

resource "aws_subnet" "renamed" {
  cidr_block = "10.99.0.0/24"
}

resource "aws_subnet" "untouched" {
  cidr_block = "10.99.1.0/24"
}

moved {
  from = aws_subnet.old
  to   = aws_subnet.renamed
}

# A multi-instance move, for the interrupted-apply question: the marker
# rewrite is one tag change per instance through the ordinary apply, so a run
# that dies partway leaves some instances carrying the new address and some
# still carrying the old one. Both spellings have to bind on the next run, or
# resuming would not be possible.
resource "aws_subnet" "pair" {
  for_each = toset(["a", "b"])

  cidr_block = "10.99.2.0/24"
}

moved {
  from = aws_subnet.pair_old
  to   = aws_subnet.pair
}
