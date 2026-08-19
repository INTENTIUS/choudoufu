# Fixture for the crash TestCountIndexSurvivesAnUndeclaredVariableInAModuleArgument
# pins. It is a shape real Terraform ships: the vendored `modules/_templates`
# directory inside terraform-aws-modules/security-group is a code-generation
# TEMPLATE, so it references var.auto_* names that no variable block in that
# directory declares. `terraform init` materializes it under
# .terraform/modules/, and anything that analyzes a directory tree reaches it.
#
# What matters here is only the shape, so this fixture is the two-file
# minimum rather than a copy of that module:
#
#   1. a module-call argument that is a BINARY OPERATION,
#   2. one operand of which is an UNDECLARED input variable,
#   3. feeding a child module's `count`,
#   4. on a resource whose identity-bearing argument reads count.index.
#
# (4) is what puts checkCountIndex on the path; (3) makes it evaluate the
# count expression, which sends it back across the module-call boundary into
# (1); and (2) is what made staticScopeData.GetInputVariable answer
# cty.NilVal, which normalizeRefValue then turned into an unknown of the
# ZERO cty.Type. cty panics on any question asked of that type, and the
# binary operator in (1) asks one - convert.Convert on its left operand.
#
# The correct outcome is an "Undefined variable" refusal naming
# var.undeclared_by_design, plus the count-index refusal that follows from a
# count nobody can compute. Both are graceful; neither is a crash.

variable "declared_count" {
  type    = number
  default = 2
}

module "child" {
  source = "./child"

  rule_count = var.undeclared_by_design + var.declared_count
}
