variable "role" {
  type = string
}

variable "name" {
  type = string
}

resource "aws_iam_role_policy" "this" {
  role = var.role
  name = var.name
}
