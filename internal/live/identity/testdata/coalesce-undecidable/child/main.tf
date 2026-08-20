variable "name" {
  type = string
}

variable "bare_ref" {
  type = string
}

variable "secret" {
  type      = string
  sensitive = true
}

variable "override_name" {
  type    = string
  default = null
}

# The selected argument resolves, but to a formula that is nothing but a
# parent reference. A parent attribute that came back empty would make
# coalesce() skip it at apply while this package had already built the
# marker from it, so it is refused rather than guessed.
resource "aws_iam_group" "bare_parent_ref" {
  name = coalesce(var.override_name, var.bare_ref, "*")
}

# An argument BEFORE the record-backed one that this package cannot prove
# empty. Selecting the later one would be a fall-through past a value that
# may well be set.
resource "aws_iam_group" "undecidable_first" {
  name = coalesce(var.bare_ref, var.name, "*")
}

# A sensitive argument's contents are not readable here, so whether
# coalesce() would skip it is not decidable either.
resource "aws_iam_group" "sensitive_first" {
  name = coalesce(var.secret, var.name, "*")
}
