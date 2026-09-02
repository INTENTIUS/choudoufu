variable "name" {
  type = string
}

# Never set by the caller, so null: the argument coalesce() skips.
variable "override_name" {
  type    = string
  default = null
}

# Set by the caller to nothing at all. coalesce() skips the empty string as
# well as null, and only where the call's own return type is a string.
variable "blank_name" {
  type    = string
  default = ""
}

locals {
  # The estate's own shape: a conditional whose selected branch is the
  # coalesce, reached through a local. Neither the conditional nor the
  # coalesce names a managed resource anywhere in it - the record-backed
  # parent is two module boundaries away, behind var.name.
  role_name   = var.enabled ? coalesce(var.override_name, var.name, "*") : null
  policy_name = coalesce(var.override_name, local.role_name, "*")
}

variable "enabled" {
  type    = bool
  default = true
}

# The plain case: a null argument skipped, then the record-backed one.
resource "aws_iam_group" "plain" {
  name = coalesce(var.override_name, "${var.name}-plain", "*")
}

# The empty string is skipped too.
resource "aws_iam_group" "blank_skipped" {
  name = coalesce(var.blank_name, "${var.name}-blank", "*")
}

# Through a conditional and a local, twice over.
resource "aws_iam_group" "through_local" {
  name = local.policy_name
}

# The selected argument is a template with its own literal prefix, not a
# bare reference: main.tf:279's log-group shape.
resource "aws_iam_group" "prefixed" {
  name = coalesce(var.override_name, "/aws/lambda/${var.name}")
}

# A literal earlier in the chain wins outright and the record-backed
# argument is never consulted.
resource "aws_iam_group" "literal_wins" {
  name = coalesce("literal-name", var.name)
}
