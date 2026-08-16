variable "users" {
  type = map(object({
    name   = string
    suffix = optional(string, "std")
  }))
}

resource "aws_iam_user" "u" {
  for_each = var.users
  name     = "${each.value.name}-${each.value.suffix}"
}
