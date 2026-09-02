# The same marked-value crash in objectConsKeys, which predates #189 - it
# arrived with #178's key-set fix and had been reachable ever since. Found
# by the audit sweep that started from forExprKeys' copy of the same three
# lines.

variable "prefix" {
  type      = string
  default   = "p"
  sensitive = true
}

resource "aws_iam_role" "team" {
  name = "team"
}

locals {
  merged = {
    "${var.prefix}-a" = aws_iam_role.team.name
  }
}

resource "aws_iam_user" "this" {
  for_each = local.merged
  name     = each.key
}
