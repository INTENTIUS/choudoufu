# GitHub issue #198, the shape where the instance alias is not enough: the
# block gained `count` in the same change that renamed it, so the live members
# carry the bare pre-count marker "aws_eip.single" rather than an indexed one.
#
# `count = var.create ? 1 : 0` is how every terraform-aws-modules resource is
# written, and a module that both renamed a resource and wrapped it in that
# idiom produces exactly this.

resource "aws_eip" "pool" {
  count = 2

  domain = "vpc"
}

moved {
  from = aws_eip.single
  to   = aws_eip.pool
}
