# Limits fixture: two resources claiming one identity.
#
# Both blocks are the admitted type aws_s3_bucket, and neither is individually
# wrong; the problem is that both resolve to the same client-assigned
# identity ("estate-shared"), which is a resolve-time ambiguity
# (internal/live/identity, see checkCollisions in resolve.go), not a lint
# rule. Check() reports nothing for this directory: lint has no notion of
# identity at all, only construct and type shape. See
# live/LIMITATIONS.md.

resource "aws_s3_bucket" "one" {
  bucket = "estate-shared"
}

resource "aws_s3_bucket" "two" {
  bucket = "estate-shared"
}
