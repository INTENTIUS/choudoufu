variable "label" {
  type = string
}

resource "aws_iam_role" "this" {
  name = var.label
}

output "name" {
  value = aws_iam_role.this.name
}
