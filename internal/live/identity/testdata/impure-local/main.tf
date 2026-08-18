# The same thing one hop away: the identity argument is a plain reference to
# a local, and the local is impure. The syntactic check cannot see through
# the reference, so this fixture is the one that proves the pure static scope
# is doing the work rather than the message.
#
# aws_iam_group, not aws_s3_bucket as this fixture read before GitHub issue
# #289 - see testdata/impure-name/main.tf for why.
locals {
  suffix = uuid()
}

resource "aws_iam_group" "data" {
  name = "estate-${local.suffix}"
}
