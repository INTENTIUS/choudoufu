variable "rules" {
  type    = any
  default = {}
}

variable "names" {
  type    = set(string)
  default = []
}

resource "aws_iam_user" "keyed" {
  for_each = var.rules

  name = "user-${each.key}"
}

resource "aws_iam_group" "named" {
  for_each = var.names

  name = "group-${each.key}"
}
