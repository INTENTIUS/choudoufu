# Limits fixture: RuleMovedBlock.
#
# A moved block rewrites which state entry belongs to which address; there is
# no state to rewrite. Ownership lives on the resource itself, in its
# tofu-address marker. See stateless/LIMITATIONS.md.

resource "aws_s3_bucket" "new" {
  bucket = "tofu-stateless-limits-moved"
}

moved {
  from = aws_s3_bucket.old
  to   = aws_s3_bucket.new
}
