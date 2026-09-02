variable "name" {
  type = string
}

variable "override" {
  type    = string
  default = null
}

# Bare var.name. [resolver.resolveExpr] hands this straight to
# resolveTraversal, which reaches [resolver.namedDef], which chases the
# reference to the module call's own argument expression and threads the
# call's repetition data itself. Resolved since #189 slice 1.
resource "aws_iam_user" "direct" {
  name = var.name
}

# The same reference inside a string template. Also resolved before #252,
# because resolveExpr decomposes a TemplateExpr into its parts and recurses,
# so the bare var.name part reaches namedDef exactly as above. Kept here to
# pin that the template is not the obstacle - #252's own diagnosis said a
# template was enough, and it is not.
resource "aws_iam_user" "templated" {
  name = "t-${var.name}"
}

# The same reference inside a function call. resolveExpr does NOT decompose
# a FunctionCallExpr, so nothing here ever reaches namedDef: the whole
# expression goes to the static evaluator, which answers var.* through the
# module call's frozen variables closure - built at load time against a
# parent evaluator with no repetition data at all. This is the shape #252
# measures.
resource "aws_iam_user" "wrapped" {
  name = coalesce(var.override, "u-${var.name}")
}
