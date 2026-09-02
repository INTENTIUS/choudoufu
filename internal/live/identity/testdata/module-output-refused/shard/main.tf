variable "label" {
  type = string
}

resource "aws_iam_role" "this" {
  name = var.label
}

output "name" {
  value = aws_iam_role.this.name
}

# A literal output: it does not read var.label, so nothing downstream of the
# module hop can notice a bogus instance key. This is what isolates the
# expansion-key check in [resolver.resolveModuleOutput] - without it, a
# reference to a nonexistent instance resolves to this literal and the
# resource gets a confident, wrong identity.
output "constant" {
  value = "fixed"
}
