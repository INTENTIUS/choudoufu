variable "name" {
  type = string
}

resource "aws_iam_user" "this" {
  name = var.name
}
