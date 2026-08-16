# The same marked-value crash in #189's forExprKeys: cty.Value.AsString
# panics on a marked value, and a key clause interpolating a sensitive
# variable produces one. Refusing is the answer; crashing is not.

variable "prefix" {
  type      = string
  default   = "p"
  sensitive = true
}

resource "aws_iam_role" "team" {
  name = "team"
}

locals {
  users  = { a = { login = "alice" } }
  merged = { for k, v in local.users : "${var.prefix}-${k}" => merge(v, { role = aws_iam_role.team.name }) }
}

resource "aws_iam_user" "this" {
  for_each = local.merged
  name     = each.key
}
