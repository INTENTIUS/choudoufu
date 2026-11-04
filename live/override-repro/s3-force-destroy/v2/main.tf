data "aws_caller_identity" "me" {}
resource "aws_s3_bucket" "b" {
  bucket        = "${var.sfx}-fd-${data.aws_caller_identity.me.account_id}"
  force_destroy = true
}
