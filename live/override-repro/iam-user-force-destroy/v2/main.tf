resource "aws_iam_user" "u" {
  name          = "${var.sfx}-fd-user"
  force_destroy = true
}
