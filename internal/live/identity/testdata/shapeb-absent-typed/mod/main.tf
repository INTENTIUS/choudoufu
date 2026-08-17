variable "users" {
  type = map(object({
    description = string
    account     = string
    name        = optional(string, "from-the-declared-type")
  }))
}

resource "aws_iam_user" "this" {
  for_each = var.users

  name = try(each.value.name, "fallback")
}
