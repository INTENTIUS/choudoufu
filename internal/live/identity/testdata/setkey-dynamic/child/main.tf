variable "s" {
  type = set(string)
}

resource "aws_iam_user" "u" {
  for_each = { for k, v in var.s : "n-${k}" => v }
  name     = each.key
}

resource "aws_iam_group" "g" {
  for_each = { for k, v in var.s : "n-${k}" => v }
  name     = "g-${each.value}"
}
