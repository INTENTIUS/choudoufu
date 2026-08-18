variable "users" {
  type = any
}

variable "enabled" {
  type = bool
}

locals {
  selected = { for k, v in var.users : k => v if var.enabled }
}

resource "aws_iam_user" "this" {
  for_each = local.selected

  name = "user-${each.key}"
}
