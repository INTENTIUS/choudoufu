variable "role" {
  type = string
}

resource "aws_iam_role_policy" "this" {
  role = var.role
  name = "p"
}
