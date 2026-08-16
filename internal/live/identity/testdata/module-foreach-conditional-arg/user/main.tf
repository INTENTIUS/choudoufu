variable "role_name" {
  type = string
}

resource "aws_iam_user" "this" {
  name = var.role_name
}
