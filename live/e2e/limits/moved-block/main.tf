# Limits fixture: RuleMovedBlock's residual refusal.
#
# Most moved blocks are carried, not refused: ownership lives in the live
# resource's tofu-address tag, so a moved block reads as "a resource carrying
# the old address is the object the new address names", and discovery binds it
# under both. See live/LIMITATIONS.md, "moved-block".
#
# This one cannot be carried. The address it moves from is still declared, so
# nothing is vacated: the live resource carrying it stays bound to it and the
# destination is created fresh, which is the opposite assignment to the one
# stock OpenTofu makes over two objects a later change could tell apart. Stock
# refuses the same shape, as "Moved object still exists".

resource "aws_s3_bucket" "old" {
  bucket = "tofu-stateless-limits-moved-old"
}

resource "aws_s3_bucket" "new" {
  bucket = "tofu-stateless-limits-moved"
}

moved {
  from = aws_s3_bucket.old
  to   = aws_s3_bucket.new
}
