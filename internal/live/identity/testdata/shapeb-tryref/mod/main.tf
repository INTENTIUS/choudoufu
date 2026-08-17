variable "users" {
  type = any
}

resource "aws_iam_user" "this" {
  for_each = var.users

  name = try(each.value.name, "fallback")
}
