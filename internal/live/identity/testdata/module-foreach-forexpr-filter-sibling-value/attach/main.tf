variable "role_name" {
  type = string
}

variable "policies" {
  type = any
}

resource "aws_iam_role_policy_attachment" "this" {
  for_each = { for k, v in var.policies : k => v if lookup(v, "create", true) }

  role       = var.role_name
  policy_arn = each.value.arn
}

# The corpus-alb-complete shape this fixture also reproduces: a ternary whose
# CONDITION reads a plain-literal sibling attribute (kind) of the same
# for_each element that carries the poisoned leaf (arn) - "aws_lb_target_group_attachment.this"'s
# own `port = try(each.value.target_type, null) == "lambda" ? null :
# try(each.value.port, var.default_port)`, reduced to a string identity
# argument so it can be asserted by value.
resource "aws_iam_user" "tag" {
  for_each = { for k, v in var.policies : k => v if lookup(v, "create", true) }

  name = try(each.value.kind, null) == "special" ? "special-${each.key}" : "plain-${each.key}"
}

# The third shape: an indexed reference into a DIFFERENT resource, where the
# index is a plain-literal sibling (target_key) of the same poisoned element -
# terraform-aws-modules/alb's own
# `aws_lb_target_group.this[each.value.target_group_key].arn`.
resource "aws_iam_role" "target" {
  for_each = { t1 = "role-one", t2 = "role-two" }
  name     = each.value
}

resource "aws_iam_role_policy_attachment" "byindex" {
  for_each = { for k, v in var.policies : k => v if lookup(v, "create", true) }

  role       = aws_iam_role.target[each.value.target_key].name
  policy_arn = each.value.arn
}
