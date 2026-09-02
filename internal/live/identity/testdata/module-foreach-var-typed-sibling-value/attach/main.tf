variable "role_name" {
  type = string
}

# The declared type is what makes this fixture #301 rather than
# module-foreach-var (whose module variable is untyped and so already
# carried the raw expression across the hop): a concrete map(string) is
# exactly the hop #251's varConvertedElems used to convert wholesale and
# strip the expression from.
variable "policies" {
  type = map(string)
}

resource "aws_iam_role_policy_attachment" "this" {
  for_each = var.policies

  role       = var.role_name
  policy_arn = each.value
}
