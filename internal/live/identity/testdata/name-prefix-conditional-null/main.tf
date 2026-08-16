# The name/name_prefix convention #190 covers is usually spelled by
# omitting one argument outright (see testdata/name-prefix-discovery), but
# terraform-aws-modules and similar module libraries commonly spell the
# identical convention through a pair of complementary conditionals
# instead: both "name" and "name_prefix" are written in every instance, and
# exactly one evaluates non-null depending on a boolean variable. Both
# arguments are syntactically PRESENT either way, so the resolver has to
# look at the VALUE, not just presence, to tell this apart from a resource
# that genuinely has no way to name itself.

variable "use_prefix_a" {
  type    = bool
  default = true
}

variable "use_prefix_b" {
  type    = bool
  default = false
}

# name evaluates to null; name_prefix evaluates to a real string. This is
# the conditional spelling of the name_prefix convention and must defer to
# discovery exactly like the omitted-argument spelling does.
resource "aws_iam_role" "prefixed" {
  name                = var.use_prefix_a ? null : "unused-a"
  name_prefix         = var.use_prefix_a ? "app-role-" : null
  assume_role_policy  = "{}"
}

# name evaluates to a real string; name_prefix evaluates to null. The
# ordinary concrete path, unaffected by the sibling's presence.
resource "aws_iam_role" "named_conditional" {
  name                = var.use_prefix_b ? null : "explicit-name-2"
  name_prefix         = var.use_prefix_b ? "app-role-2-" : null
  assume_role_policy  = "{}"
}

# name evaluates to null and there is no name_prefix sibling at all: a
# genuinely broken configuration (nothing names this role), which must keep
# refusing exactly as before this fix - a peek that finds no prefix sibling
# must never suppress the "Null identity argument" diagnostic.
resource "aws_iam_role" "broken_no_prefix" {
  name                = var.use_prefix_a ? null : "unused-c"
  assume_role_policy  = "{}"
}
