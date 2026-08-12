# The same thing one hop away: the identity argument is a plain reference to
# a local, and the local is impure. The syntactic check cannot see through
# the reference, so this fixture is the one that proves the pure static scope
# is doing the work rather than the message.
locals {
  suffix = uuid()
}

resource "aws_s3_bucket" "data" {
  bucket = "estate-${local.suffix}"
}
