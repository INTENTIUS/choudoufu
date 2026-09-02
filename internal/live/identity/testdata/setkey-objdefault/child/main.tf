variable "s" {
  type = object({
    a = string
    b = optional(string, "beta")
    c = string
  })
}

resource "aws_iam_user" "u" {
  for_each = var.s
  name     = each.key
}
