resource "aws_ecr_repository" "r" {
  name         = "${var.sfx}-fd-repo"
  force_delete = false
}
