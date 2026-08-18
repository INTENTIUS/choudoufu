variable "users" {
  type = any
}

variable "groups" {
  type = any
}

variable "enabled" {
  type = bool
}

locals {
  # A for-expression with an if clause, wrapped in the null guard the ECS
  # module writes around its own capacity_providers. Neither the caller-
  # climbing chase in staticForEachKeys nor a bare var.users reference is
  # what makes this resolve: the condition itself reads var.users, so the
  # chase cannot prove which branch is taken and the whole key set is left
  # to the tolerant retry.
  selected = var.users != null ? { for k, v in var.users : k => v if var.enabled } : {}
}

resource "aws_iam_user" "this" {
  for_each = local.selected

  name = "user-${each.key}"
}

resource "aws_iam_group" "g" {
  count = length(var.groups) > 1 ? 1 : 0

  name = "the-group"
}
