# The same marked-value crash a third time, in
# [resolver.forEachOverComprehension] - a for_each comprehension ranging
# over a SIBLING RESOURCE's instances, whose key clause interpolates a
# sensitive variable. Predates both audited merges; found by sweeping the
# package for the three-line "convert, check known, AsString" shape after
# forExprKeys' copy of it crashed.
#
# [resolver.forEachExpansion] already refused a wholly sensitive for_each
# value with "Sensitive for_each expression"; the comprehension paths did
# not, and cty's AsString panics rather than erroring.

variable "secret" {
  type      = string
  default   = "s"
  sensitive = true
}

resource "aws_subnet" "this" {
  for_each   = toset(["a", "b"])
  cidr_block = "10.0.0.0/24"
}

resource "aws_iam_user" "this" {
  for_each = { for k, v in aws_subnet.this : "${var.secret}-${k}" => v }
  name     = each.key
}
